package env

// Ports packages/env/test/remote-abort.test.ts (Pi main c96e368e2, D304): aborts and timeouts against a remote host that does not answer; the call settles on
// time, whatever the host does. The two "daemons" of the test are this test binary in the modes TestMain gives it.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/env/daemon"
)

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
			select {}
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

// abortAfter is a context that ends after d.
func abortAfter(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(background, d)
	t.Cleanup(cancel)
	return ctx
}

// timed is how long call takes to settle and its error.
func timed(call func() error) (time.Duration, error) {
	started := time.Now()
	err := call()
	return time.Since(started), err
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
	took, err := timed(func() error {
		_, err := connection.Request(abortAfter(t, 100*time.Millisecond), "lstat", Json{"path": "/"}, RequestOptions{})
		return err
	})
	wantRemoteCode(t, err, "aborted")
	if took >= time.Second {
		t.Fatalf("request took %v, want under 1s", took)
	}
	took, err = timed(func() error {
		_, err := connection.Info(abortAfter(t, 100*time.Millisecond))
		return err
	})
	wantRemoteCode(t, err, "aborted")
	if took >= time.Second {
		t.Fatalf("info took %v, want under 1s", took)
	}
}

// remote-abort.test.ts "Connection aborts": "settles a sent request the daemon does not answer within a second of the abort".
func TestConnectionAbortSettlesASentRequestTheDaemonDoesNotAnswer(t *testing.T) {
	t.Parallel()
	connection := abortedConnection(t, "silent-after-hello")
	if _, err := connection.Info(background); err != nil {
		t.Fatal(err)
	}
	took, err := timed(func() error {
		_, err := connection.Request(abortAfter(t, 100*time.Millisecond), "exec", Json{"command": "true"}, RequestOptions{})
		return err
	})
	wantRemoteCode(t, err, "aborted")
	if took < time.Second || took >= 2500*time.Millisecond {
		t.Fatalf("request took %v, want between 1s and 2.5s", took)
	}
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
			took, err := timed(func() error {
				_, err := abortedEnvironment(connection).Exec(background, "uptime", &durableenv.ShellExecOptions{Timeout: &timeout})
				return err
			})
			if failure, ok := errors.AsType[*durableenv.ExecutionError](err); !ok || failure.Code != durableenv.ExecutionErrorTimeout {
				t.Fatalf("error = %v, want code timeout", err)
			}
			if took >= 3*time.Second {
				t.Fatalf("exec took %v, want under 3s", took)
			}
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
			took, err := timed(func() error {
				_, err := abortedEnvironment(connection).Exec(abortAfter(t, 100*time.Millisecond), "uptime", &durableenv.ShellExecOptions{Timeout: &timeout})
				return err
			})
			if failure, ok := errors.AsType[*durableenv.ExecutionError](err); !ok || failure.Code != durableenv.ExecutionErrorAborted {
				t.Fatalf("error = %v, want code aborted", err)
			}
			if took >= 2500*time.Millisecond {
				t.Fatalf("exec took %v, want under 2.5s", took)
			}
		})
	}
}

// remote-abort.test.ts "ends file operations waiting for the connection when aborted".
func TestRemoteFileOperationsStopWaitingForTheConnectionWhenAborted(t *testing.T) {
	t.Parallel()
	connection := abortedConnection(t, "never-ready")
	took, err := timed(func() error {
		_, err := abortedEnvironment(connection).ReadTextFile(abortAfter(t, 100*time.Millisecond), "/etc/hostname")
		return err
	})
	if failure, ok := errors.AsType[*durableenv.FileError](err); !ok || failure.Code != durableenv.FileErrorAborted {
		t.Fatalf("error = %v, want code aborted", err)
	}
	if took >= time.Second {
		t.Fatalf("readTextFile took %v, want under 1s", took)
	}
}
