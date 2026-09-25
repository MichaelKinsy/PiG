//go:build windows

package subprocess

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// nodePipeClient connects to a named pipe the way Node extensions do and
// prints the connect latency in milliseconds. In "fail" mode it destroys the
// connection at once, as cell.mjs signalLoadFailure reports a member whose
// factory failed; in "ok" mode it writes "ping" and waits for the host to
// close the connection.
const nodePipeClient = `const net = require("node:net");
const [pipe, mode] = process.argv.slice(2);
const start = process.hrtime.bigint();
const socket = net.createConnection(pipe, () => {
  const ms = Number(process.hrtime.bigint() - start) / 1e6;
  process.stdout.write(JSON.stringify({ ms }) + "\n");
  if (mode === "fail") {
    socket.destroy();
    return;
  }
  socket.write("ping");
});
socket.on("error", (err) => {
  process.stdout.write(JSON.stringify({ error: String(err) }) + "\n");
  process.exitCode = 1;
});
`

// A Node extension connects to its pipe before the host accepts it; a packed
// cell's failed member does so while the host is still accepting an earlier
// member. The connect must complete at once, because an instance with its
// connect already pending exists before the pipe's name is handed out. When no
// instance was waiting, libuv's connect took the ERROR_PIPE_BUSY path and
// waited for one for up to 30 seconds, which hid the race or became a hang.
//
// PIG_PIPE_CONNECT_RUNS sets the number of connects (default 20).
func TestNodeConnectsToExtensionPipeWithoutWaiting(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the pipe client: %v", err)
	}
	runs := 20
	if value := os.Getenv("PIG_PIPE_CONNECT_RUNS"); value != "" {
		if runs, err = strconv.Atoi(value); err != nil || runs < 1 {
			t.Fatalf("PIG_PIPE_CONNECT_RUNS = %q", value)
		}
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "client.cjs")
	if err := os.WriteFile(script, []byte(nodePipeClient), 0o600); err != nil {
		t.Fatal(err)
	}
	latencies := make([]float64, 0, runs)
	for i := range runs {
		mode := "ok"
		if i%2 == 0 {
			mode = "fail"
		}
		latencies = append(latencies, connectNodeClient(t, node, script, filepath.Join(dir, "e.sock"), mode))
	}
	slices.Sort(latencies)
	percentile := func(p float64) float64 { return latencies[int(p*float64(len(latencies)-1))] }
	t.Logf("node pipe connect latency over %d connects (half failing members): min %.2f ms, median %.2f ms, p99 %.2f ms, max %.2f ms",
		runs, latencies[0], percentile(0.5), percentile(0.99), latencies[len(latencies)-1])
	if slowest := latencies[len(latencies)-1]; slowest >= 1000 {
		t.Fatalf("a Node connect took %.0f ms: libuv waited for a pipe instance instead of connecting at once", slowest)
	}
}

// The interval review WHF-001 names: a client connects to a pipe instance and
// closes before the server issues ConnectNamedPipe. The listener issues each
// connect before the pipe's name is handed out, so ListenExtension cannot
// produce the interval; the test forces it on a raw instance.
// ConnectNamedPipe then reports ERROR_NO_DATA, which go-winio's listener
// discards before waiting for another client. This listener must report the
// closed client, as a failed member's load failure, and then serve the next
// client, as a healthy sibling.
func TestPipeListenerReportsAClientThatClosedBeforeConnect(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	attrs, err := pipeSecurityAttributes("D:P(A;;GA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	path := `\\.\pipe\pig-ext-test-` + rand.Text()
	h, err := createPipeHandle(path, attrs, true)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		_ = windows.CloseHandle(h)
		t.Fatal(err)
	}
	_ = failed.Close()
	instance, err := connectPipeInstance(path, h)
	if err != nil {
		t.Fatalf("connect after the client closed: %v", err)
	}
	ln := &pipeListener{path: path, attrs: attrs, next: instance}
	defer func() { _ = ln.Close() }()

	accept := func() net.Conn {
		t.Helper()
		type result struct {
			conn net.Conn
			err  error
		}
		accepted := make(chan result, 1)
		go func() {
			conn, err := ln.Accept()
			accepted <- result{conn, err}
		}()
		select {
		case got := <-accepted:
			if got.err != nil {
				t.Fatal(got.err)
			}
			return got.conn
		case <-time.After(10 * time.Second):
			t.Fatal("Accept is waiting for another client")
			return nil
		}
	}
	conn := accept()
	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("first read from the closed client's connection = %v, want end of file", err)
	}
	_ = conn.Close()

	healthy, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("the next client cannot connect: %v", err)
	}
	defer func() { _ = healthy.Close() }()
	if _, err := healthy.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	conn = accept()
	defer func() { _ = conn.Close() }()
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("read %q, %v from the next client, want ping", buf, err)
	}
}

// connectNodeClient runs the Node client against a fresh extension pipe that
// the host has not accepted yet, then accepts it, and returns the client's
// connect latency in milliseconds.
func connectNodeClient(t *testing.T, node, script, sockPath, mode string) float64 {
	t.Helper()
	ln, address, err := ListenExtension(sockPath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	cmd := exec.Command(node, script, address, mode)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Wait() }()
	lines := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	var report struct {
		MS    *float64 `json:"ms"`
		Error string   `json:"error"`
	}
	select {
	case line, ok := <-lines:
		if !ok {
			t.Fatalf("%s client exited without reporting a connect", mode)
		}
		if err := json.Unmarshal([]byte(line), &report); err != nil || report.MS == nil {
			t.Fatalf("%s client: %s (%v)", mode, line, err)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("%s client did not connect within 10 s: it is waiting for a pipe instance", mode)
	}
	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	buf := make([]byte, 4)
	if mode == "fail" {
		if _, err := conn.Read(buf); !errors.Is(err, io.EOF) {
			t.Fatalf("read from the failed member's connection = %v, want end of file", err)
		}
	} else if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("read %q, %v from the healthy client, want ping", buf, err)
	}
	return *report.MS
}
