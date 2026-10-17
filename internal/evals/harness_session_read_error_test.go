//go:build unix

package evals

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"testing"
)

// packages/evals/src/harness.ts:420-430: the finally reports "Pi eval produced no session file." only when the session path is unset or does not exist (existsSync); a session file that exists but cannot be read pushes readFile's own error as the cleanup error.
// mutation-checked: reporting every read failure as a missing session file fails it.
func TestRunPiCodingAgentReportsAnUnreadableSessionFileByItsReadError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a file whatever its mode")
	}
	options := harnessFixture(t, newScriptedModel(t))
	options.NoTools = "all"
	var sessionFile string
	options.Output = func(_ context.Context, run AgentRun) (any, error) {
		sessionFile = run.SessionStatistic.SessionFile
		return run.Response, os.Chmod(sessionFile, 0)
	}
	run, err := RunPiCodingAgent(t.Context(), PromptInput("What is the capital of France?"), options)
	if sessionFile == "" {
		t.Fatalf("the run reported no session file (run = %v, err = %v)", run, err)
	}
	if run != nil || !errors.Is(err, fs.ErrPermission) || err.Error() == "Pi eval produced no session file." {
		t.Fatalf("RunPiCodingAgent = %v, %v; want the session file's read error", run, err)
	}
}
