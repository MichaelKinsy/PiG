package node

import (
	"fmt"
	"os"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// upstream: packages/durable/src/env/node.ts:814 spawns the shell with `detached: process.platform !== "win32"`, which makes
// it the leader of a new session (setsid), not only of a process group; Pi's daemon (daemon/src/sys/unix.rs:configure) calls
// setsid for the same reason.
// The shell reads its session through the test binary (testenv.ReportSessionIfRequested), not through ps: `ps -o sid` is
// Linux's, and macOS's ps has no keyword for it.
// mutation-checked: Setpgid instead of Setsid in process_unix.go detachedProcessAttributes fails it
func TestShellCommandRunsAsTheLeaderOfANewSession(t *testing.T) {
	skipOnWindows(t)
	env, _ := newTestEnv(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	options := &durableenv.ShellExecOptions{Env: map[string]string{testenv.SessionHelper: "1", "PIG_TEST_SESSION_BINARY": self}}
	_, output, err := collectShellOutput(env, `printf '%s %s' "$("$PIG_TEST_SESSION_BINARY")" "$$"`, options, background)
	mustDo(t, err)
	var session, pid string
	if _, scanErr := fmt.Sscan(output, &session, &pid); scanErr != nil || session != pid {
		t.Fatalf("session %q of process %q: the shell must lead its own session", session, pid)
	}
}
