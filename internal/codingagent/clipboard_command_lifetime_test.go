package codingagent

// pi: packages/coding-agent/src/utils/clipboard-command.ts

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// clipboardDescendantEnv names the test's loopback address. This test binary, run with it set, connects and reports "child", starts a grandchild that inherits its stdin and stdout, reports the grandchild's PID, waits for the test's acknowledgement of that report, and exits without closing the connection, so the test reads the connection's end as the child's exit. The acknowledgement matters on Windows, where a process that exits with an open socket resets the connection and the reset discards report bytes the test has not read yet.
const clipboardDescendantEnv = "PIG_TEST_CLIPBOARD_DESCENDANT"

// clipboardDescendantHoldEnv names the test's loopback address. This test binary, run with it set, connects, reports "grandchild" and holds its inherited stdin and stdout until the connection ends. It neither reads stdin nor writes stdout unless the test sends "write", which writes clipboardDescendantPayload bytes to stdout and reports "wrote".
const clipboardDescendantHoldEnv = "PIG_TEST_CLIPBOARD_DESCENDANT_HOLD"

const clipboardDescendantFailureEnv = "PIG_TEST_CLIPBOARD_DESCENDANT_FAILURE"

// clipboardDescendantPayload is larger than any pipe buffer, so the grandchild's write completes only while the reader drains stdout.
const clipboardDescendantPayload = 1 << 20

// clipboardDescendantConn keeps the child's connection reachable until the process exits; only the exit closes it.
var clipboardDescendantConn net.Conn

// dialLoopback connects to the listener whose address the parent test put in this helper's environment. It refuses an address
// that is not loopback, so the helper can only reach the test that started it.
func dialLoopback(addr string) (net.Conn, error) {
	target, err := netip.ParseAddrPort(addr)
	if err != nil {
		return nil, err
	}
	if !target.Addr().IsLoopback() {
		return nil, fmt.Errorf("%s is not a loopback address", addr)
	}
	conn, err := net.DialTCP("tcp", nil, net.TCPAddrFromAddrPort(target))
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func runClipboardDescendantHelper(addr string) int {
	conn, err := dialLoopback(addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	clipboardDescendantConn = conn
	env := []string{clipboardDescendantHoldEnv + "=" + addr}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, clipboardDescendantEnv+"=") {
			env = append(env, entry)
		}
	}
	grandchild := exec.Command(os.Args[0])
	grandchild.Env = env
	grandchild.Stdin, grandchild.Stdout = os.Stdin, os.Stdout
	if err = grandchild.Start(); err == nil {
		_, err = fmt.Fprintf(conn, "child %d\n", grandchild.Process.Pid)
		if err == nil {
			// The test acknowledges once it has read the report; a closed connection ends the wait too.
			_, _ = conn.Read(make([]byte, 1))
		}
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

func holdClipboardDescendantPipes(addr string) {
	conn, err := dialLoopback(addr)
	if err != nil {
		os.Exit(1)
	}
	if _, err := io.WriteString(conn, "grandchild\n"); err != nil {
		os.Exit(1)
	}
	commands := bufio.NewReader(conn)
	for {
		command, err := commands.ReadString('\n')
		if err != nil {
			os.Exit(0)
		}
		if command != "write\n" {
			os.Exit(1)
		}
		reply := "wrote\n"
		if _, err := os.Stdout.Write(make([]byte, clipboardDescendantPayload)); err != nil {
			reply = "error " + err.Error() + "\n"
		}
		if _, err := io.WriteString(conn, reply); err != nil {
			os.Exit(1)
		}
	}
}

// clipboardDescendantFixture makes the next clipboard command, this test binary, leave a running grandchild holding the command's stdin and stdout. Both report over loopback connections, so the test observes the child's exit and drives the grandchild without waiting on the clock.
type clipboardDescendantFixture struct {
	reports       chan clipboardDescendantReport
	grandchild    net.Conn
	grandchildIn  *bufio.Reader
	grandchildPID int
}

// clipboardDescendantReport is the first line one helper process sent over its connection: its role, or the error that kept it from arriving.
type clipboardDescendantReport struct {
	conn net.Conn
	in   *bufio.Reader
	role string
	err  error
}

func startClipboardDescendantFixture(t *testing.T, failure bool) *clipboardDescendantFixture {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(clipboardDescendantEnv, listener.Addr().String())
	if failure {
		t.Setenv(clipboardDescendantFailureEnv, "1")
	}
	f := &clipboardDescendantFixture{reports: make(chan clipboardDescendantReport, 2)}
	var (
		accepting sync.WaitGroup
		mu        sync.Mutex
		accepted  []net.Conn
	)
	accepting.Go(func() {
		for range 2 {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			accepted = append(accepted, conn)
			mu.Unlock()
			// Read each report as soon as it connects, not when the test next looks: the child exits right after reporting, and on Windows the reset its exit sends discards report bytes still unread.
			accepting.Go(func() {
				in := bufio.NewReader(conn)
				role, err := in.ReadString('\n')
				if err == nil && role != "grandchild\n" {
					// Release the child, which waits for this before it exits.
					_, err = io.WriteString(conn, "\n")
				}
				f.reports <- clipboardDescendantReport{conn, in, role, err}
			})
		}
	})
	t.Cleanup(func() {
		_ = listener.Close()
		// Closing a connection ends the grandchild, and unblocks a report that never arrived; the kill covers a grandchild that never connected.
		mu.Lock()
		for _, conn := range accepted {
			_ = conn.Close()
		}
		mu.Unlock()
		accepting.Wait()
		if f.grandchildPID != 0 {
			if process, err := os.FindProcess(f.grandchildPID); err == nil {
				_ = process.Kill()
				_ = process.Release()
			}
		}
	})
	return f
}

// awaitChildExit returns once the child has started the grandchild and exited and the grandchild has connected. A result on done first fails the test: the command returned while the grandchild still held its stdout.
func (f *clipboardDescendantFixture) awaitChildExit(t *testing.T, done <-chan clipboardCommandResult) {
	t.Helper()
	childExited := make(chan struct{})
	for childExited != nil || f.grandchild == nil {
		select {
		case report := <-f.reports:
			conn, in, role := report.conn, report.in, report.role
			switch {
			case report.err != nil:
				t.Fatalf("descendant connection: %q %v", role, report.err)
			case role == "grandchild\n":
				f.grandchild, f.grandchildIn = conn, in
			default:
				if _, err := fmt.Sscanf(role, "child %d\n", &f.grandchildPID); err != nil {
					t.Fatalf("child report %q: %v", role, err)
				}
				// The child never closes its connection, so its end is the child's exit.
				go func(exited chan<- struct{}) {
					_, _ = io.Copy(io.Discard, in)
					_ = conn.Close()
					close(exited)
				}(childExited)
			}
		case <-childExited:
			childExited = nil
		case result := <-done:
			t.Fatalf("clipboard command returned output=%q ok=%v before its child exited and left a grandchild holding stdout", result.output, result.ok)
		}
	}
}

// grandchildWrites has the grandchild write clipboardDescendantPayload bytes to the stdout it inherited. The write completes only while the reader still drains that stdout.
func (f *clipboardDescendantFixture) grandchildWrites(t *testing.T, done <-chan clipboardCommandResult) {
	t.Helper()
	if _, err := io.WriteString(f.grandchild, "write\n"); err != nil {
		t.Fatal(err)
	}
	reply := make(chan string, 1)
	go func() {
		line, err := f.grandchildIn.ReadString('\n')
		if err != nil {
			line = "grandchild ended: " + err.Error()
		}
		reply <- line
	}()
	select {
	case line := <-reply:
		if line != "wrote\n" {
			t.Fatalf("the reader stopped draining stdout after its child exited: %s", line)
		}
	case result := <-done:
		t.Fatalf("clipboard command returned output=%q ok=%v while the grandchild was writing its stdout", result.output, result.ok)
	}
}

// controlClipboardCommandDeadline replaces runClipboardCommand's timer: every command must request want, and the first command's deadline fires only when the test calls fire.
func controlClipboardCommandDeadline(t *testing.T, want time.Duration) (fire func()) {
	t.Helper()
	previous := clipboardCommandDeadline
	t.Cleanup(func() { clipboardCommandDeadline = previous })
	first := make(chan context.CancelFunc, 1)
	clipboardCommandDeadline = func(timeout time.Duration) (context.Context, context.CancelFunc) {
		if timeout != want {
			t.Errorf("clipboard command deadline %v, want %v", timeout, want)
		}
		ctx, cancel := context.WithCancel(context.Background())
		select {
		case first <- cancel:
		default:
		}
		return ctx, cancel
	}
	return func() { (<-first)() }
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
	f := startClipboardDescendantFixture(t, false)
	// Pi's default runClipboardCommand timeout; the test fires it once the child has exited, however long the host takes to start it.
	fire := controlClipboardCommandDeadline(t, 3*time.Second)
	done := runClipboardCommandAsync(os.Args[0], clipboardCommandOptions{})
	f.awaitChildExit(t, done)
	f.grandchildWrites(t, done)
	fire()
	// The grandchild holds stdout until cleanup, so only the deadline can end the command.
	if result := <-done; result.ok || len(result.output) != 0 {
		t.Fatalf("reader with a descendant holding stdout: output=%q ok=%v; want failure at the deadline", result.output, result.ok)
	}
}

// Pi finishes on 'close', not 'exit', even for a nonzero exit status. A failed reader with an inherited stdout keeps draining it until EOF or the clipboard deadline.
func TestClipboardFailedReaderWaitsForDescendantStdout(t *testing.T) {
	f := startClipboardDescendantFixture(t, true)
	// The deadline never fires: the grandchild's exit closes stdout first.
	controlClipboardCommandDeadline(t, 3*time.Second)
	done := runClipboardCommandAsync(os.Args[0], clipboardCommandOptions{})
	f.awaitChildExit(t, done)
	f.grandchildWrites(t, done)
	select {
	case result := <-done:
		t.Fatalf("failed reader returned output=%q ok=%v while a grandchild held its stdout", result.output, result.ok)
	default:
	}
	_ = f.grandchild.Close()
	if result := <-done; result.ok || len(result.output) != 0 {
		t.Fatalf("failed reader output=%q ok=%v; want failure after stdout closed", result.output, result.ok)
	}
}

// Pi clipboard-command.ts:11-14,31-35 gives a writer no output pipe, so 'close' follows the child's exit; a descendant holding unread stdin neither delays nor fails the copy.
func TestClipboardWriterDoesNotWaitForDescendantStdin(t *testing.T) {
	f := startClipboardDescendantFixture(t, false)
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
	f.awaitChildExit(t, nil)
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
