package extension_test

import (
	"bufio"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi's execCommand (core/exec.ts) waits with waitForChildProcess
// (utils/child-process.ts): after the child exits it finishes when both output
// pipes end or when no data has arrived for 100 ms, and it signals only the
// child, with SIGTERM, never its descendants. These tests drive that through a
// descendant that outlives the child and keeps the child's output pipes open.
//
// Every process is this test binary (see TestExecHelperProcess), so the tests
// run alike on Linux, macOS and Windows. A descendant dials the test over TCP
// and echoes what it reads: the connection is its readiness signal and, after
// ExecCommand returns, a ping that comes back proves that the descendant is
// still running. A descendant stops when the test closes the connection, and
// its safety deadline bounds a test that never does.

// holderLifetime bounds a descendant that the test never reaches. It is longer
// than any successful call takes, so a call that waits for the descendant to
// exit sees a dead descendant and fails.
const holderLifetime = 15 * time.Second

// treeHelpers are the child modes of TestExecHelperProcess that this file
// adds. Each takes the arguments after the mode.
var treeHelpers = map[string]func(args []string){
	// holder ADDR: keep the inherited output pipes open and echo ADDR.
	"holder": func(args []string) { echoUntilClosed(dial(args[0])) },
	// bg-echo PIDFILE ADDR: `sleep 12 & echo ok`.
	"bg-echo": func(args []string) {
		startHolder(args[0], args[1], nil)
		_, _ = os.Stdout.WriteString("ok\n")
	},
	// bg-wait PIDFILE ADDR: `sleep 12 & wait`.
	"bg-wait": func(args []string) {
		_ = startHolder(args[0], args[1], nil).Wait()
	},
	// bg-trap PIDFILE ADDR: `trap 'echo cleanup; exit 0' TERM; sleep 12 & wait`.
	"bg-trap": func(args []string) {
		terminated := make(chan os.Signal, 1)
		signal.Notify(terminated, syscall.SIGTERM)
		holder := startHolder(args[0], args[1], nil)
		go func() { _ = holder.Wait() }()
		<-terminated
		_, _ = os.Stdout.WriteString("cleanup\n")
	},
	// bg-late PIDFILE ADDR: print HEAD and exit while a descendant writes after
	// the exit, as in earendil-works/pi#5303.
	"bg-late": func(args []string) {
		r, w, err := os.Pipe()
		if err != nil {
			panic(err)
		}
		startHolderMode(args[0], "late-writer", args[1], r)
		_ = r.Close()
		_, _ = os.Stdout.WriteString("HEAD\n")
		// The descendant reads the end of w as the leader's exit: the process
		// ends with it and the operating system closes w.
		defer runtime.KeepAlive(w)
	},
	// late-writer ADDR: after stdin ends, which is the leader's exit, write
	// output for longer than the idle grace, one chunk shorter than the grace
	// apart, then hold the pipes.
	"late-writer": func(args []string) {
		conn := dial(args[0])
		_, _ = io.Copy(io.Discard, os.Stdin)
		for i := 1; i <= 15; i++ {
			_, _ = os.Stdout.WriteString("TICK" + strconv.Itoa(i) + "\n")
			time.Sleep(20 * time.Millisecond)
		}
		echoUntilClosed(conn)
	},
	// stubborn ADDR: report SIGTERM over the connection and keep running until
	// told to exit with code 7.
	"stubborn": func(args []string) {
		terminated := make(chan os.Signal, 1)
		signal.Notify(terminated, syscall.SIGTERM)
		conn := dial(args[0])
		go func() {
			<-terminated
			_, _ = conn.Write([]byte("term\n"))
		}()
		_, _ = bufio.NewReader(conn).ReadString('\n')
		os.Exit(7)
	},
}

func dial(addr string) net.Conn {
	conn, err := new(net.Dialer).DialContext(context.Background(), "tcp", addr)
	if err != nil {
		panic(err)
	}
	_ = conn.SetDeadline(time.Now().Add(holderLifetime))
	return conn
}

func echoUntilClosed(conn net.Conn) {
	_, _ = io.Copy(conn, conn)
	os.Exit(0)
}

// startHolder starts a descendant with the leader's output pipes and records
// its process ID in pidfile.
func startHolder(pidfile, addr string, stdin *os.File) *exec.Cmd {
	return startHolderMode(pidfile, "holder", addr, stdin)
}

func startHolderMode(pidfile, mode, addr string, stdin *os.File) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestExecHelperProcess$", "--", mode, addr)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if stdin != nil {
		cmd.Stdin = stdin
	}
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	if err := os.WriteFile(pidfile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
		panic(err)
	}
	return cmd
}

// treeFixture is the listener a descendant dials and the file that records its
// process ID.
type treeFixture struct {
	listener net.Listener
	pidfile  string
}

func newTreeFixture(t *testing.T) *treeFixture {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &treeFixture{listener: listener, pidfile: t.TempDir() + string(os.PathSeparator) + "pid"}
	t.Cleanup(func() {
		_ = listener.Close()
		// The descendant outlives the call by design: stop it.
		if data, err := os.ReadFile(f.pidfile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				if process, err := os.FindProcess(pid); err == nil {
					_ = process.Kill()
					_, _ = process.Wait()
				}
			}
		}
	})
	return f
}

func (f *treeFixture) args(t *testing.T, mode string) (string, []string) {
	command, args := helper(t, mode, f.pidfile, f.listener.Addr().String())
	return command, args
}

// accept returns the connection of the descendant.
func (f *treeFixture) accept(t *testing.T) net.Conn {
	t.Helper()
	_ = f.listener.(*net.TCPListener).SetDeadline(time.Now().Add(testbudget.Wait(t)))
	conn, err := f.listener.Accept()
	if err != nil {
		t.Fatalf("the descendant did not connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(testbudget.Wait(t)))
	return conn
}

// assertAlive pings the descendant over conn.
func assertAlive(t *testing.T, conn net.Conn) {
	t.Helper()
	if _, err := conn.Write([]byte("ping\n")); err != nil {
		t.Fatalf("the descendant is gone: %v", err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || line != "ping\n" {
		t.Fatalf("the descendant did not answer a ping: %q, %v; ExecCommand waited for it to exit", line, err)
	}
}

type execOutcome struct {
	result extension.ExecResult
	err    error
}

// The issue's first row: `sh -c "sleep 12 & echo ok"` with no timeout returns
// when the pipes fall idle, with code 0, not when the descendant exits.
func TestExecCommandDoesNotWaitForADescendantHoldingTheOutput(t *testing.T) {
	f := newTreeFixture(t)
	command, args := f.args(t, "bg-echo")
	result, err := extension.ExecCommand(t.Context(), t.TempDir(), command, args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := (extension.ExecResult{Stdout: "ok\n"}); result != want {
		t.Fatalf("result = %+v, want %+v", result, want)
	}
	assertAlive(t, f.accept(t))
}

// The issue's second row: `sleep 12 & wait` with a timeout. Pi sends SIGTERM to
// the child alone, resolves with code 0 (the signal gives no code) and
// killed=true, and leaves the descendant running. On Windows Node's kill
// terminates the child with status 1, but libuv reports the signal it sent
// and no code, so the code is 0 there too.
func TestExecCommandTimeoutSignalsOnlyTheLeader(t *testing.T) {
	f := newTreeFixture(t)
	command, args := f.args(t, "bg-wait")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan execOutcome, 1)
	go func() {
		result, err := extension.ExecCommand(ctx, t.TempDir(), command, args, nil)
		done <- execOutcome{result, err}
	}()
	conn := f.accept(t)
	// The descendant runs, so the leader is waiting for it: expire the call.
	cancel()
	outcome := <-done
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if want := (extension.ExecResult{Killed: true}); outcome.result != want {
		t.Fatalf("result = %+v, want %+v", outcome.result, want)
	}
	assertAlive(t, conn)
}

// The issue's third row: a SIGTERM handler runs and its exit code and output
// reach the caller. Windows has no signals to handle.
func TestExecCommandTermHandlerRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Node's kill ends a Windows child without a handler")
	}
	f := newTreeFixture(t)
	command, args := f.args(t, "bg-trap")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan execOutcome, 1)
	go func() {
		result, err := extension.ExecCommand(ctx, t.TempDir(), command, args, nil)
		done <- execOutcome{result, err}
	}()
	conn := f.accept(t)
	cancel()
	outcome := <-done
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if want := (extension.ExecResult{Stdout: "cleanup\n", Killed: true}); outcome.result != want {
		t.Fatalf("result = %+v, want %+v", outcome.result, want)
	}
	assertAlive(t, conn)
}

// Pi sends SIGTERM once. Node sets proc.killed when the signal is delivered, so
// the SIGKILL that its timer sends `if (!proc.killed)` never follows: a child
// that ignores SIGTERM keeps the call waiting. Windows has no signals.
func TestExecCommandDoesNotEscalateAfterSIGTERM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Node's kill ends a Windows child without a handler")
	}
	f := newTreeFixture(t)
	command, args := helper(t, "stubborn", f.listener.Addr().String())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan execOutcome, 1)
	go func() {
		result, err := extension.ExecCommand(ctx, t.TempDir(), command, args, nil)
		done <- execOutcome{result, err}
	}()
	conn := f.accept(t)
	cancel()
	// The child reports the delivery of SIGTERM; a SIGKILL ends it instead.
	reader := bufio.NewReader(conn)
	if line, err := reader.ReadString('\n'); err != nil || line != "term\n" {
		t.Fatalf("the child did not report SIGTERM: %q, %v", line, err)
	}
	select {
	case outcome := <-done:
		t.Fatalf("ExecCommand returned while the child ignores SIGTERM: %+v", outcome)
	default:
	}
	if _, err := conn.Write([]byte("exit\n")); err != nil {
		t.Fatal(err)
	}
	outcome := <-done
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if want := (extension.ExecResult{Code: 7, Killed: true}); outcome.result != want {
		t.Fatalf("result = %+v, want %+v", outcome.result, want)
	}
}

// earendil-works/pi#5303: output that a descendant writes after the child exits,
// each chunk arriving within the grace of the last, is not truncated.
func TestExecCommandKeepsOutputWrittenAfterTheLeaderExits(t *testing.T) {
	f := newTreeFixture(t)
	command, args := f.args(t, "bg-late")
	result, err := extension.ExecCommand(t.Context(), t.TempDir(), command, args, nil)
	if err != nil {
		t.Fatal(err)
	}
	var want strings.Builder
	want.WriteString("HEAD\n")
	for i := 1; i <= 15; i++ {
		want.WriteString("TICK" + strconv.Itoa(i) + "\n")
	}
	if result.Stdout != want.String() || result.Stderr != "" || result.Code != 0 || result.Killed {
		t.Fatalf("result = %+v, want stdout %q", result, want.String())
	}
	assertAlive(t, f.accept(t))
}
