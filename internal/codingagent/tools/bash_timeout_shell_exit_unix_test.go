//go:build !windows

package tools

import "testing"

// .upstream/main 06aa6563e packages/coding-agent/test/suite/regressions/11613-bash-timeout-after-shell-exit.test.ts
func TestBashTimeoutAfterShellExit11613(t *testing.T) {
	t.Run("reports the shell's exit code when background processes kept writing until the timeout", func(t *testing.T) {
		result := runFileTool(t, &BashTool{CWD: t.TempDir()}, t.Context(), map[string]any{
			"command": "(while true; do echo tick; sleep 0.02; done) & sleep 0.2; echo boom >&2; exit 3",
			"timeout": 3,
		})
		assertBashPortError(t, result, `boom[\s\S]*Command timed out after 3 seconds\. The shell exited with code 3 after \d+(?:\.\d)? seconds, but background processes it started kept writing output until the timeout\.$`)
	})
	t.Run("keeps the plain timeout message when the shell was still running at the deadline", func(t *testing.T) {
		result := runFileTool(t, &BashTool{CWD: t.TempDir()}, t.Context(), map[string]any{"command": "echo started; sleep 30", "timeout": 2})
		assertBashPortError(t, result, `started\s+Command timed out after 2 seconds$`)
	})
}
