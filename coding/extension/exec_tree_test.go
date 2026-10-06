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
	"sync/atomic"
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

// lateWriterTicks is the number of ticks the late writer emits after the leader
// exits: 15 x 20 ms is three times the idle grace.
const lateWriterTicks = 15

// treeHelpers are the child modes of TestExecHelperProcess that this file
// adds. Each takes the arguments after the mode.
var treeHelpers = map[string]func(args []string){
	// holder ADDR: keep the inherited output pipes open and echo ADDR.
	"holder": func(args []string) { echoUntilClosed(dial(args[0])) },
	// bg-echo PIDFILE ADDR: `sleep 12 & echo ok`.
	"bg-echo": func(args []string) {
		startHolder(args[0], args[1])
		_, _ = os.Stdout.WriteString("ok\n")
	},
	// bg-wait PIDFILE ADDR: `sleep 12 & wait`.
	"bg-wait": func(args []string) {
		_ = startHolder(args[0], args[1]).Wait()
	},
	// bg-trap PIDFILE ADDR: `trap 'echo cleanup; exit 0' TERM; sleep 12 & wait`.
	"bg-trap": func(args []string) {
		terminated := make(chan os.Signal, 1)
		signal.Notify(terminated, syscall.SIGTERM)
		holder := startHolder(args[0], args[1])
		go func() { _ = holder.Wait() }()
		<-terminated
		_, _ = os.Stdout.WriteString("cleanup\n")
	},
	// bg-late PIDFILE ADDR: start a descendant that writes after this process
	// exits, as in earendil-works/pi#5303, and print HEAD and exit only once the
	// descendant has connected back, so that it is already running its ticks
	// when the exit happens, however long the system takes to start it.
	"bg-late": func(args []string) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			panic(err)
		}
		_ = listener.(*net.TCPListener).SetDeadline(time.Now().Add(holderLifetime))
		startHolderMode(args[0], "late-writer", args[1], listener.Addr().String())
		leader, err := listener.Accept()
		if err != nil {
			panic(err)
		}
		_, _ = os.Stdout.WriteString("HEAD\n")
		// The descendant reads the end of leader as this process's exit: the
		// process ends with it and the operating system closes leader.
		defer runtime.KeepAlive(leader)
	},
	// late-writer ADDR LEADER: connect to the leader and write a PRE tick every
	// 20 ms, so the output never idles for the grace however late the
	// scheduler delivers the leader's exit. Once the leader's connection ends,
	// which is its exit, write EOF and then 15 POST ticks, one chunk shorter
	// than the grace apart and longer than the grace in total, then hold the
	// pipes.
	"late-writer": func(args []string) {
		conn := dial(args[0])
		leader := dial(args[1])
		var exited atomic.Bool
		go func() {
			_, _ = io.Copy(io.Discard, leader)
			exited.Store(true)
		}()
		for !exited.Load() {
			_, _ = os.Stdout.WriteString("PRE\n")
			time.Sleep(20 * time.Millisecond)
		}
		_, _ = os.Stdout.WriteString("EOF\n")
		for i := 1; i <= lateWriterTicks; i++ {
			_, _ = os.Stdout.WriteString("POST" + strconv.Itoa(i) + "\n")
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
func startHolder(pidfile, addr string) *exec.Cmd {
	return startHolderMode(pidfile, "holder", addr)
}

func startHolderMode(pidfile, mode string, args ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestExecHelperProcess$", "--", mode}, args...)...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
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
	if result.Stderr != "" || result.Code != 0 || result.Killed {
		t.Fatalf("result = %+v", result)
	}
	// The descendant ticks before and across the leader's exit, so the order of
	// HEAD among the PRE ticks is the scheduler's. EOF is written only after the
	// leader exited: it and every POST tick after it, which span more than the
	// grace, must have arrived.
	head, tail, found := strings.Cut(result.Stdout, "EOF\n")
	if !found {
		t.Fatalf("output written after the leader exited was lost: %q", result.Stdout)
	}
	if strings.ReplaceAll(strings.ReplaceAll(head, "PRE\n", ""), "HEAD\n", "") != "" || strings.Count(head, "HEAD\n") != 1 {
		t.Fatalf("output before the leader exited = %q, want HEAD among PRE ticks", head)
	}
	var want strings.Builder
	for i := 1; i <= lateWriterTicks; i++ {
		want.WriteString("POST" + strconv.Itoa(i) + "\n")
	}
	if tail != want.String() {
		t.Fatalf("output after the leader exited = %q, want %q", tail, want.String())
	}
	assertAlive(t, f.accept(t))
}
