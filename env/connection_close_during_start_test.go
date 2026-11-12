//go:build unix

package env

import (
	"os/exec"
	"syscall"
	"testing"
	"testing/synctest"
)

// Close marks the connection closed and tears down the session it finds. A start that has spawned its daemon and not yet
// kept its session is past its own closed check, so that Close finds none: the start then sees the connection closed, signals
// the daemon it just spawned and returns. Nothing else is started for that daemon, so the start is also what has it waited
// for: a process nobody waits for stays in the process table until this one exits, with its output pipes open. Node reaps a
// child by itself, and connection.ts #start has no such window: it spawns and keeps the session in one synchronous step
// (connection.ts:341-347).
func TestConnectionWaitsForADaemonItStartedWhileItWasClosed(t *testing.T) {
	binary := daemonBinary(t)
	synctest.Test(t, func(t *testing.T) {
		connection := NewConnection(ConnectionOptions{Command: []string{binary}})
		var pid int
		closed := make(chan struct{})
		connection.afterDaemonStart = func(cmd *exec.Cmd) {
			pid = cmd.Process.Pid
			go func() {
				connection.Close()
				close(closed)
			}()
			// Close has marked the connection closed and found no session once it cancels the lifetime; it then waits for this start.
			<-connection.lifetime.Done()
		}
		if _, err := connection.Info(t.Context()); err == nil {
			t.Fatal("Info succeeded on a connection closed during its start")
		}
		<-closed
		if pid == 0 {
			t.Fatal("the start spawned no daemon")
		}
		// A goroutine that waits for the daemon is in a system call until the daemon ends at the SIGTERM of the teardown, which
		// is no durable block: Wait returns once that goroutine has reaped the daemon and ended. Without one, Wait returns as
		// soon as the start has ended, and signal 0 still finds the process.
		synctest.Wait()
		if err := syscall.Kill(pid, 0); err == nil {
			t.Fatalf("daemon process %d, started while the connection closed, is still in the process table after Close returned and every goroutine of the connection ended: nothing waits for it", pid)
		}
	})
}
