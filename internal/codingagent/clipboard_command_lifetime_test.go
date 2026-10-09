package codingagent

// pi: packages/coding-agent/src/utils/clipboard-command.ts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// clipboardDescendantEnv names a PID file. This test binary, run with it set, starts a grandchild that inherits its stdin and stdout, waits until the grandchild runs, records the grandchild's PID, and exits successfully while the grandchild keeps both pipes open.
const clipboardDescendantEnv = "PIG_TEST_CLIPBOARD_DESCENDANT"

// clipboardDescendantHoldEnv makes this test binary signal readiness on stderr and then hold its inherited stdin and stdout without reading or writing until the test kills it.
const clipboardDescendantHoldEnv = "PIG_TEST_CLIPBOARD_DESCENDANT_HOLD"

const clipboardDescendantFailureEnv = "PIG_TEST_CLIPBOARD_DESCENDANT_FAILURE"

func runClipboardDescendantHelper(pidFile string) int {
	env := []string{clipboardDescendantHoldEnv + "=1"}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, clipboardDescendantEnv+"=") {
			env = append(env, entry)
		}
	}
	grandchild := exec.Command(os.Args[0])
	grandchild.Env = env
	grandchild.Stdin, grandchild.Stdout = os.Stdin, os.Stdout
	ready, err := grandchild.StderrPipe()
	if err == nil {
		err = grandchild.Start()
	}
	if err == nil {
		_, err = io.ReadFull(ready, make([]byte, 1))
	}
	if err == nil {
		err = os.WriteFile(pidFile, []byte(strconv.Itoa(grandchild.Process.Pid)), 0o600)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if os.Getenv(clipboardDescendantFailureEnv) == "1" {
		return 1
	}
	return 0
}

func holdClipboardDescendantPipes() {
	_, _ = os.Stderr.Write([]byte{'\n'})
	// The test kills this process during cleanup; the bound only limits a leak if the test process dies first.
	time.Sleep(2 * time.Minute)
	os.Exit(0)
}

// startClipboardDescendantFixture makes the next clipboard command, this test binary, leave a running grandchild holding the command's stdin and stdout. It returns the PID file path, and cleanup kills the grandchild.
func startClipboardDescendantFixture(t *testing.T) string {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	t.Setenv(clipboardDescendantEnv, pidFile)
	t.Cleanup(func() {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(string(data))
		if err != nil {
			t.Errorf("grandchild pid %q: %v", data, err)
			return
		}
		if process, err := os.FindProcess(pid); err == nil {
			_ = process.Kill()
			_ = process.Release()
		}
	})
	return pidFile
}

type clipboardCommandResult struct {
	output []byte
	ok     bool
}

func runClipboardCommandAsync(name string, options clipboardCommandOptions) <-chan clipboardCommandResult {
	done := make(chan clipboardCommandResult, 1)
	go func() {
		output, ok := runClipboardCommand(name, nil, options)
		done <- clipboardCommandResult{output, ok}
	}()
	return done
}

// Pi clipboard-command.ts:18-28,31-33 resolves undefined when its timer fires although the child already exited: 'close' waits for every holder of stdout, and abort destroys the stream instead of joining that holder.
func TestClipboardReaderDeadlineDoesNotWaitForDescendantStdout(t *testing.T) {
	pidFile := startClipboardDescendantFixture(t)
	// Pi's default runClipboardCommand timeout; the child and grandchild start well within it.
	const timeout = 3 * time.Second
	select {
	case result := <-runClipboardCommandAsync(os.Args[0], clipboardCommandOptions{timeout: timeout}):
		if result.ok || len(result.output) != 0 {
			t.Fatalf("reader with a descendant holding stdout: output=%q ok=%v; want failure at the deadline", result.output, result.ok)
		}
	case <-time.After(timeout + 30*time.Second):
		t.Fatal("clipboard reader waited past its deadline for a descendant holding its stdout")
	}
	if _, err := os.Stat(pidFile); err != nil {
		t.Fatalf("the child did not leave a grandchild holding stdout before the deadline: %v", err)
	}
}

// Pi finishes on 'close', not 'exit', even for a nonzero exit status. A failed reader with an inherited stdout still waits until EOF or the clipboard deadline.
func TestClipboardFailedReaderWaitsForDescendantStdout(t *testing.T) {
	pidFile := startClipboardDescendantFixture(t)
	t.Setenv(clipboardDescendantFailureEnv, "1")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	output, err := runClipboardCommandContext(ctx, os.Args[0], nil, clipboardCommandOptions{})
	if err == nil || len(output) != 0 {
		t.Fatalf("failed reader output=%q error=%v", output, err)
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("failed reader returned on exit before inherited stdout closed or the deadline fired")
	}
	if _, err := os.Stat(pidFile); err != nil {
		t.Fatalf("the child did not leave a grandchild holding stdout: %v", err)
	}
}

// Pi clipboard-command.ts:11-14,31-35 gives a writer no output pipe, so 'close' follows the child's exit; a descendant holding unread stdin neither delays nor fails the copy.
func TestClipboardWriterDoesNotWaitForDescendantStdin(t *testing.T) {
	pidFile := startClipboardDescendantFixture(t)
	// More input than any pipe buffer holds, so writing blocks while the descendant keeps stdin open without reading.
	input := strings.Repeat("x", 8<<20)
	const timeout = time.Minute
	select {
	case result := <-runClipboardCommandAsync(os.Args[0], clipboardCommandOptions{input: &input, timeout: timeout}):
		if !result.ok || len(result.output) != 0 {
			t.Fatalf("writer after a successful child exit: output=%q ok=%v; want success", result.output, result.ok)
		}
	case <-time.After(timeout / 2):
		t.Fatal("clipboard writer waited after the child exited for a descendant holding its stdin")
	}
	if _, err := os.Stat(pidFile); err != nil {
		t.Fatalf("the child did not leave a grandchild holding stdin: %v", err)
	}
}

// Pi clipboard-command.ts:24-28,37 aborts the child immediately on overflow instead of waiting for its timeout or natural exit.
func TestClipboardOutputOverflowCancelsCommand(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var out bytes.Buffer
	writer := &limitedWriter{w: &out, remaining: 3, cancel: cancel}
	if n, err := writer.Write([]byte{0, 255, 10}); err != nil || n != 3 || ctx.Err() != nil {
		t.Fatalf("at limit: n=%d err=%v context=%v", n, err, ctx.Err())
	}
	if n, err := writer.Write([]byte{1}); err == nil || n != 0 || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("overflow: n=%d err=%v context=%v", n, err, ctx.Err())
	}
	if !bytes.Equal(out.Bytes(), []byte{0, 255, 10}) {
		t.Fatalf("retained output=%v", out.Bytes())
	}
}

func BenchmarkClipboardImageCommand(b *testing.B) {
	node, err := exec.LookPath("node")
	if err != nil {
		b.Fatal(err)
	}
	const size = 1024 * 1024
	b.SetBytes(size)
	b.ReportAllocs()
	for b.Loop() {
		out, err := defaultClipboardRunner(b.Context(), node, "-e", "process.stdout.write(Buffer.alloc(1024 * 1024))")
		if err != nil || len(out) != size {
			b.Fatalf("image command output=%d err=%v", len(out), err)
		}
	}
}

func TestClipboardImageCommandHonorsParentCancellation(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out, err := defaultClipboardRunner(ctx, node, "-e", "process.stdout.write('unexpected')")
	if err == nil || len(out) != 0 {
		t.Fatalf("cancelled command output=%q err=%v", out, err)
	}
}
