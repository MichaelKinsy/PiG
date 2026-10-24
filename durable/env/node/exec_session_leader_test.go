package node

import (
	"fmt"
	"testing"
)

// upstream: packages/durable/src/env/node.ts:814 spawns the shell with `detached: process.platform !== "win32"`, which makes
// it the leader of a new session (setsid), not only of a process group; Pi's daemon (daemon/src/sys/unix.rs:configure) calls
// setsid for the same reason.
// mutation-checked: Setpgid instead of Setsid in process_unix.go detachedProcessAttributes fails it
func TestShellCommandRunsAsTheLeaderOfANewSession(t *testing.T) {
	skipOnWindows(t)
	env, _ := newTestEnv(t)
	_, output, err := collectShellOutput(env, `printf '%s %s' "$(ps -o sid= -p $$ | tr -d ' ')" "$$"`, nil, background)
	mustDo(t, err)
	var session, pid string
	if _, scanErr := fmt.Sscan(output, &session, &pid); scanErr != nil || session != pid {
		t.Fatalf("session %q of process %q: the shell must lead its own session", session, pid)
	}
}
