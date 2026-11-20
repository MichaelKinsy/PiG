package env

// Ports packages/env/test/remote-abort.test.ts (Pi main c96e368e2): aborts and timeouts against a remote host that does not answer; the call settles on
// time, whatever the host does. The two "daemons" of the test are this test binary in the modes TestMain gives it.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/env/daemon"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// blockUntilKilled parks the fake daemon until the connection kills it, as `ssh` to a host that is down stays until it is killed. A bare `select {}` is
// a deadlock to Go's detector in a binary built without cgo (the macOS runners'): it ends the process with "all goroutines are asleep" and exit code 2.
// A process that waits for a signal has a goroutine in the signal queue, which the detector counts as running.
func blockUntilKilled() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, os.Interrupt)
	<-signals
	os.Exit(1)
}

// runSilentAfterHello is SILENT_AFTER_HELLO: a daemon that answers `hello` and then nothing, like one behind a connection that died unnoticed (a rebooting
// host): it neither answers requests nor exits.
func runSilentAfterHello(input io.Reader, output io.Writer, token string) int {
	if _, err := fmt.Fprintf(output, "PI-ENV %s\n", token); err != nil {
		return 1
	}
	answered := false
	for {
		request, err := daemon.ReadFrame(input)
		if err != nil || request == nil {
			blockUntilKilled()
			return 1
		}
		if answered || request.Kind != daemon.FrameRequest {
			continue
		}
		answered = true
		reply := &daemon.Frame{Kind: daemon.FrameResult, ID: request.ID, JSON: daemon.Object{
			"protocol": 1, "version": "test", "os": "linux", "arch": "x86_64", "home": "/", "tmpdir": "/tmp", "separator": "/",
			"cwd": "/", "driveCwds": daemon.Object{}, "pid": os.Getpid(),
		}}
		if err := daemon.WriteFrame(output, reply); err != nil {
			return 1
		}
	}
}

func abortedConnection(t *testing.T, mode string) *Connection {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake daemons are this test binary started over a POSIX shell")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	connection := NewConnection(ConnectionOptions{Command: []string{self, mode}})
	t.Cleanup(connection.Close)
	return connection
}

func abortedEnvironment(connection *Connection) *RemoteExecutionEnv {
	return NewRemoteExecutionEnv(RemoteExecutionEnvOptions{Connection: connection, ID: "pi-env:test", Cwd: "/"})
}

// abortDelay is how long after it starts Pi's tests abort a call (abortAfter(100) in remote-abort.test.ts).
const abortDelay = 100 * time.Millisecond

// abortTimer is a context that a timer ends after d, and the instant the timer did. Pi bounds how long a call takes from its start, which holds the
// abort timer's own delay; a timer fires when the machine runs it, and on a loaded host that was 1.3 s late (a 1.43 s "took" for a 100 ms timer, with the
// call returning 20 us after the context ended), none of it in the code under test. A call ends "at once" when it ends soon after the abort, so the span
// starts at firedAt, which the timer callback stamps just before it ends the context.
type abortTimer struct {
	ctx     context.Context
	firedAt atomic.Pointer[time.Time]
}

func abortAfter(t *testing.T, d time.Duration) *abortTimer {
	t.Helper()
	ctx, cancel := context.WithCancel(background)
	abort := &abortTimer{ctx: ctx}
	timer := time.AfterFunc(d, func() {
		now := time.Now()
		abort.firedAt.Store(&now)
		cancel()
	})
	t.Cleanup(func() {
		timer.Stop()
		cancel()
	})
	return abort
}

// run calls call with the context and returns how long after the abort it returned, and its error; zero when the abort had not fired, which the caller's
// check of the error catches. A call that began after the abort fired is measured from its beginning.
func (a *abortTimer) run(call func(ctx context.Context) error) (time.Duration, error) {
	began := time.Now()
	err := call(a.ctx)
	settled := time.Now()
	fired := a.firedAt.Load()
	if fired == nil {
		return 0, err
	}
	from := *fired
	if began.After(from) {
		from = began
	}
	return settled.Sub(from), err
}

// timed is how long call takes to settle and its error.
func timed(call func() error) (time.Duration, error) {
	started := time.Now()
	err := call()
	return time.Since(started), err
}

// wantUnder is one of Pi's bars on a duration. A wall-clock bar measures the machine as much as the call: on a loaded host a thread that was made runnable
// waits for a CPU for as long as the host likes (a call returned 182 ms after its context ended, with the other threads of the process running), so the bar is
// checked only where nothing else runs, as the other wall-clock budgets of the repository are: serially, by make test-timing (PIG_SERIAL_TIMING=1), as a
// timing gate (testenv.TimingGate, automation/perf/timing-gates.json). Every other run asserts the code and the order (wantStartPending, abortGrace).
// Pi measures its bars from the start of the call; the bars on the span after the abort are Pi's minus abortDelay.
func wantUnder(t *testing.T, what string, took, bar time.Duration) {
	t.Helper()
	if os.Getenv("PIG_SERIAL_TIMING") == "" {
		return
	}
	testenv.TimingGate(t, took >= bar, "%s took %v, want under %v", what, took, bar)
}

// wantStartPending is the order of Pi's "at once": the daemon of this connection never gets ready, and the start gives up only at startTimeout, so a call
// that returned while the start still goes on was ended by the abort or the call's own timeout, not by waiting for the connection.
func wantStartPending(t *testing.T, connection *Connection) {
	t.Helper()
	connection.mu.Lock()
	ready := connection.ready
	connection.mu.Unlock()
	if ready == nil {
		t.Fatal("the start of the connection had failed when the call returned: the call waited for the connection")
	}
	select {
	case <-ready.done:
		t.Fatal("the start of the connection had ended when the call returned: the call waited for the connection")
	default:
	}
}

func wantRemoteCode(t *testing.T, err error, code string) {
	t.Helper()
	remote, ok := errors.AsType[*RemoteError](err)
	if !ok || remote.Code != code {
		t.Fatalf("error = %v, want a RemoteError with code %s", err, code)
	}
}

// remote-abort.test.ts "Connection aborts": "stops waiting for a start that does not finish, at once".
func TestConnectionAbortStopsWaitingForAStartThatDoesNotFinish(t *testing.T) {
	t.Parallel()
	connection := abortedConnection(t, "never-ready")
	after, err := abortAfter(t, abortDelay).run(func(ctx context.Context) error {
		_, err := connection.Request(ctx, "lstat", Json{"path": "/"}, RequestOptions{})
		return err
	})
	wantRemoteCode(t, err, "aborted")
	wantStartPending(t, connection)
	wantUnder(t, "request", after, time.Second-abortDelay)
	after, err = abortAfter(t, abortDelay).run(func(ctx context.Context) error {
		_, err := connection.Info(ctx)
		return err
	})
	wantRemoteCode(t, err, "aborted")
	wantStartPending(t, connection)
	wantUnder(t, "info", after, time.Second-abortDelay)
}

// remote-abort.test.ts "Connection aborts": "settles a sent request the daemon does not answer within a second of the abort".
func TestConnectionAbortSettlesASentRequestTheDaemonDoesNotAnswer(t *testing.T) {
	t.Parallel()
	connection := abortedConnection(t, "silent-after-hello")
	if _, err := connection.Info(background); err != nil {
		t.Fatal(err)
	}
	after, err := abortAfter(t, abortDelay).run(func(ctx context.Context) error {
		_, err := connection.Request(ctx, "exec", Json{"command": "true"}, RequestOptions{})
		return err
	})
	wantRemoteCode(t, err, "aborted")
	// The daemon never answers the cancel, so only the grace timer, started after the abort, can settle the call: it cannot do so sooner than abortGrace
	// after the abort. Pi's "ms >= 1000" is this bound from the call's start.
	if after < abortGrace {
		t.Fatalf("request settled %v after the abort, before the %v the daemon has to answer the cancel", after, abortGrace)
	}
	wantUnder(t, "request", after, 2500*time.Millisecond-abortDelay)
}

// remote-abort.test.ts "RemoteExecutionEnv.exec on a host that does not answer": ends at the timeout and when aborted, for a connection that does not start and
// a daemon that stopped answering.
func TestRemoteExecOnAHostThatDoesNotAnswer(t *testing.T) {
	t.Parallel()
	for _, daemonMode := range []struct{ name, mode string }{
		{"connection that does not start", "never-ready"},
		{"daemon that stopped answering", "silent-after-hello"},
	} {
		t.Run("ends at the timeout: "+daemonMode.name, func(t *testing.T) {
			t.Parallel()
			connection := abortedConnection(t, daemonMode.mode)
			if daemonMode.mode == "silent-after-hello" {
				if _, err := connection.Info(background); err != nil {
					t.Fatal(err)
				}
			}
			timeout := 1.0
			// The timer that ends this call is the product's own (RemoteExecutionEnv.Exec), which a test cannot stamp: the span is the call's, Pi's bar included.
			took, err := timed(func() error {
				_, err := abortedEnvironment(connection).Exec(background, "uptime", &durableenv.ShellExecOptions{Timeout: &timeout})
				return err
			})
			if failure, ok := errors.AsType[*durableenv.ExecutionError](err); !ok || failure.Code != durableenv.ExecutionErrorTimeout {
				t.Fatalf("error = %v, want code timeout", err)
			}
			if daemonMode.mode == "never-ready" {
				wantStartPending(t, connection)
			}
			wantUnder(t, "exec", took, 3*time.Second)
		})
		t.Run("ends when aborted: "+daemonMode.name, func(t *testing.T) {
			t.Parallel()
			connection := abortedConnection(t, daemonMode.mode)
			if daemonMode.mode == "silent-after-hello" {
				if _, err := connection.Info(background); err != nil {
					t.Fatal(err)
				}
			}
			timeout := 30.0
			after, err := abortAfter(t, abortDelay).run(func(ctx context.Context) error {
				_, err := abortedEnvironment(connection).Exec(ctx, "uptime", &durableenv.ShellExecOptions{Timeout: &timeout})
				return err
			})
			if failure, ok := errors.AsType[*durableenv.ExecutionError](err); !ok || failure.Code != durableenv.ExecutionErrorAborted {
				t.Fatalf("error = %v, want code aborted", err)
			}
			if daemonMode.mode == "never-ready" {
				wantStartPending(t, connection)
			}
			wantUnder(t, "exec", after, 2500*time.Millisecond-abortDelay)
		})
	}
}

// remote-abort.test.ts "ends file operations waiting for the connection when aborted". The connection never gets ready, so the call can only end by the
// abort: it must return while the start still goes on, within Pi's bar of the abort.
func TestRemoteFileOperationsStopWaitingForTheConnectionWhenAborted(t *testing.T) {
	t.Parallel()
	connection := abortedConnection(t, "never-ready")
	after, err := abortAfter(t, abortDelay).run(func(ctx context.Context) error {
		_, err := abortedEnvironment(connection).ReadTextFile(ctx, "/etc/hostname")
		return err
	})
	if failure, ok := errors.AsType[*durableenv.FileError](err); !ok || failure.Code != durableenv.FileErrorAborted {
		t.Fatalf("error = %v, want code aborted", err)
	}
	wantStartPending(t, connection)
	wantUnder(t, "readTextFile", after, time.Second-abortDelay)
}
