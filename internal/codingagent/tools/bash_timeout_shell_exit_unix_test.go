//go:build !windows

package tools

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/childwait"
)

// .upstream/main 06aa6563e packages/coding-agent/test/suite/regressions/11613-bash-timeout-after-shell-exit.test.ts
func TestBashTimeoutAfterShellExit11613(t *testing.T) {
	t.Run("reports the shell's exit code when background processes kept writing until the timeout", func(t *testing.T) {
		// Pi's test runs the real timer and the real 100 ms post-exit grace against a loop that forks `sleep 0.02` between writes. A host that
		// is slow to fork leaves the loop silent for more than the grace, and the pipes are released before the timeout: on a loaded macOS runner
		// the shell exited after 0.2 s, the loop had written six lines at 0.5 s, and the result was "Command exited with code 3" (nightly 42368266926).
		// The grace never elapses here (the loop holds the pipe, which is the case under test) and the timeout fires once the shell has exited, so
		// the loop's timing does not matter: the shell's exit, which the command's own `sleep 0.2` puts after the loop started, is what the timeout waits for.
		testHookStdioGraceTimer = func() childwait.IdleTimer {
			return childwait.IdleTimer{Expired: make(chan time.Time), Rearm: func() {}, Stop: func() {}}
		}
		timeout := make(chan time.Time, 1)
		testHookTimeoutTimer = func(time.Duration) (<-chan time.Time, func()) { return timeout, func() {} }
		testHookShellExited = func() { timeout <- time.Now() }
		t.Cleanup(func() { testHookStdioGraceTimer, testHookTimeoutTimer, testHookShellExited = nil, nil, nil })
		result := runFileTool(t, &BashTool{CWD: t.TempDir()}, t.Context(), map[string]any{
			"command": "(while true; do echo tick; sleep 0.02; done) & sleep 0.2; echo boom >&2; exit 3",
			"timeout": 1,
		})
		assertBashPortError(t, result, `boom[\s\S]*Command timed out after 1 seconds\. The shell exited with code 3 after 0\.\d seconds, but background processes it started kept writing output until the timeout\.$`)
	})
	t.Run("keeps the plain timeout message when the shell was still running at the deadline", func(t *testing.T) {
		result := runFileTool(t, &BashTool{CWD: t.TempDir()}, t.Context(), map[string]any{"command": "echo started; sleep 30", "timeout": 0.3})
		assertBashPortError(t, result, `started\s+Command timed out after 0\.3 seconds$`)
	})
}
