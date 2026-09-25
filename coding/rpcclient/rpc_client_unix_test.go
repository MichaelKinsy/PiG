//go:build !windows

package rpcclient

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

func TestRPCSpawnPipeFailureClosesEarlierDescriptors(t *testing.T) {
	const mode = "PIG_RPC_PIPE_EXHAUSTION"
	if os.Getenv(mode) != "1" {
		child := exec.Command(os.Args[0], "-test.run=^TestRPCSpawnPipeFailureClosesEarlierDescriptors$")
		child.Env = append(os.Environ(), mode+"=1")
		if output, err := child.CombinedOutput(); err != nil {
			t.Fatalf("descriptor exhaustion child: %v\n%s", err, output)
		}
		return
	}
	// Exhaust descriptors only in this owned subprocess. Disable finalizers so
	// a leaked descriptor cannot disappear before the resource count is read.
	previousGC := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(previousGC)
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = read.Close()
	_ = write.Close()
	var oldLimit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &oldLimit); err != nil {
		t.Fatal(err)
	}
	limit := oldLimit
	limit.Cur = min(limit.Cur, 128)
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Setrlimit(unix.RLIMIT_NOFILE, &oldLimit) }()
	for _, available := range []int{2, 4} {
		assertRPCPipeFailureNoLeak(t, available)
	}
}

func assertRPCPipeFailureNoLeak(t *testing.T, available int) {
	t.Helper()
	var held []*os.File
	defer func() {
		for _, file := range held {
			_ = file.Close()
		}
	}()
	for {
		file, err := os.Open(os.DevNull)
		if err != nil {
			break
		}
		held = append(held, file)
	}
	if len(held) < available {
		t.Fatal("descriptor fixture has insufficient capacity")
	}
	for range available {
		_ = held[len(held)-1].Close()
		held = held[:len(held)-1]
	}
	client := NewRpcClient(RpcClientOptions{CliPath: os.Args[0]})
	if _, _, _, err := client.spawn(); err == nil {
		t.Fatal("spawn should fail while opening pipes")
	}
	remaining := 0
	for {
		file, err := os.Open(os.DevNull)
		if err != nil {
			break
		}
		remaining++
		held = append(held, file)
	}
	if remaining != available {
		t.Fatalf("pipe-open failure leaked descriptors: available=%d remaining=%d", available, remaining)
	}
}

func TestRpcStopClosesInheritedOutputDescriptors(t *testing.T) {
	script := filepath.Join(t.TempDir(), "agent.sh")
	// The background sleep inherits the child's stdout and stderr. The child
	// reports the holder's pid as an RPC record once it runs, so the test
	// waits for that record instead of polling a file against a deadline.
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 60 &\nprintf '{\"type\":\"holder\",\"pid\":%s}\\n' \"$!\"\nwait\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	client := NewRpcClient(RpcClientOptions{CliPath: script})
	t.Cleanup(client.Stop)
	holders := make(chan int, 1)
	client.OnEvent(func(event JsonAgentSessionEvent) {
		var record struct {
			Pid int `json:"pid"`
		}
		if event.Type == "holder" && json.Unmarshal(event.Raw, &record) == nil {
			holders <- record.Pid
		}
	})
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	var holder int
	select {
	case holder = <-holders:
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("holder did not start: %s", client.GetStderr())
	}
	t.Cleanup(func() { _ = syscall.Kill(holder, syscall.SIGKILL) })
	client.Stop()
	waitForRPCReaders(t, client)
	deadline := time.Now().Add(2 * time.Second)
	for {
		stack := make([]byte, 1<<20)
		n := runtime.Stack(stack, true)
		if !strings.Contains(string(stack[:n]), "rpcclient.(*RpcClient).readStdout") && !strings.Contains(string(stack[:n]), "rpcclient.(*RpcClient).collectStderr") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Stop left readers blocked by inherited pipe descriptors")
		}
		runtime.Gosched()
	}
}

// Stop's SIGTERM ends the child while a request is in flight; the exit handler
// rejects it with Node's (code=null signal=SIGTERM) rendering.
func TestRpcClientStopReportsSignalExitToPendingRequests(t *testing.T) {
	client, _ := childClient(t, "emit")
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := client.GetState(); done <- err }()
	// The emit child answers nothing; wait until the request is in flight.
	deadline := time.Now().Add(10 * time.Second)
	for {
		client.mu.Lock()
		n := len(client.pendingRequests)
		client.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("request never became pending")
		}
		time.Sleep(10 * time.Millisecond)
	}
	client.Stop()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "Agent process exited (code=null signal=SIGTERM)") {
			t.Fatalf("pending request error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("pending request not rejected by exit")
	}
}

func TestRpcClientStopDoesNotCloseStdoutBeforeChildExits(t *testing.T) {
	// rpc-client.ts detaches JSONL callbacks before SIGTERM but leaves stdout open until exit.
	script := filepath.Join(t.TempDir(), "shutdown-output.mjs")
	const source = `#!/usr/bin/env node
process.on('SIGTERM', () => {
  setInterval(() => process.stdout.write('{"type":"stopping"}\n'), 10);
});
process.stdout.on('error', (error) => {
  process.stderr.write('stdout-error:' + error.code + '\n');
  process.exit(77);
});
process.stdout.write('{"type":"ready"}\n');
setInterval(() => process.stdout.write('{"type":"tick"}\n'), 10);
`
	if err := os.WriteFile(script, []byte(source), 0o700); err != nil {
		t.Fatal(err)
	}
	client := NewRpcClient(RpcClientOptions{CliPath: script})
	t.Cleanup(func() { client.Stop(); client.Wait() })
	ready := make(chan struct{})
	var shutdownEvents []string
	client.OnEvent(func(event JsonAgentSessionEvent) {
		switch event.Type {
		case "ready":
			close(ready)
		case "stopping":
			shutdownEvents = append(shutdownEvents, string(event.Raw))
		}
	})
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ready:
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("child did not install signal handler: %s", client.GetStderr())
	}
	client.mu.Lock()
	proc := client.process
	client.mu.Unlock()
	client.Stop()
	waitForRPCReaders(t, client)
	if err := proc.exitErr; err == nil || !strings.Contains(err.Error(), "code=null signal=SIGKILL") {
		t.Fatalf("stdout must stay open during stop grace: exit=%v; stderr=%q", err, client.GetStderr())
	}
	if stderr := client.GetStderr(); stderr != "" {
		t.Fatalf("unexpected child error: %q", stderr)
	}
	if len(shutdownEvents) != 0 {
		t.Fatalf("callbacks continued during Stop: %v", shutdownEvents)
	}
}

func TestRPCStopReapsForcedKillAndRejectsPending(t *testing.T) {
	script := filepath.Join(t.TempDir(), "ignore-term.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntrap '' TERM\nprintf '{\"type\":\"ready\"}\\n'\nwhile read line; do :; done\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	client := NewRpcClient(RpcClientOptions{CliPath: script})
	t.Cleanup(func() { client.Stop(); client.Wait() })
	ready := make(chan struct{})
	client.OnEvent(func(event JsonAgentSessionEvent) {
		if event.Type == "ready" {
			close(ready)
		}
	})
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	// ready is the child's own record, printed after its trap is installed;
	// the wait only bounds a genuine hang.
	select {
	case <-ready:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("child did not install signal handler")
	}
	request, _, err := client.registerRequest()
	if err != nil {
		t.Fatal(err)
	}
	proc := request.process
	client.Stop()
	select {
	case <-proc.exited:
	default:
		t.Fatal("Stop returned before reaping SIGKILL child")
	}
	select {
	case err := <-request.reject:
		if !strings.Contains(err.Error(), "signal=SIGKILL") {
			t.Fatalf("pending request error = %v", err)
		}
	default:
		t.Fatal("forced shutdown forgot pending request before rejecting it")
	}
	waitForRPCReaders(t, client)
}
