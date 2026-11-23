package testenv

import (
	"testing"
	"time"
)

// SessionExitWait bounds how long AwaitSessionExit waits for a terminal session's processes to exit after tmux hangs them up. Pig exits within seconds of a hangup, so a process still running after this is a hung shutdown.
const SessionExitWait = 30 * time.Second

// AwaitSessionExit returns once every process of terminal session sid has exited, as live lists them (LiveSessionProcesses, or a test's stand-in). tmux starts each pane's program as the leader of a terminal session, so sid is the process ID of the pane's program, and session names the tmux session in a failure. A test calls it after it ends the tmux session or its server, and before its temporary directories are removed: tmux kill-session and kill-server return when the server has dropped the session; they hang the pane's terminal up and do not wait for anything the pane started. Pig handles the hangup as a termination signal, and before it exits it writes its exit record into the agent directory, which it makes again when it is gone, so a test that removes the agent directory before the pane's program exits races the shutdown and can leave an empty agent directory behind when it cleans up.
func AwaitSessionExit(t testing.TB, session string, sid int, live func(sid int) ([]int, error)) {
	t.Helper()
	deadline := time.Now().Add(SessionExitWait)
	for {
		alive, err := live(sid)
		if err != nil {
			t.Errorf("list processes of tmux session %s: %v", session, err)
			return
		}
		if len(alive) == 0 {
			return
		}
		if !time.Now().Before(deadline) {
			t.Errorf("processes %v of tmux session %s still running %s after tmux hung their terminal up", alive, session, SessionExitWait)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
