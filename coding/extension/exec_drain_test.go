package extension_test

import (
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/childwait"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// A child that exits before the call's readers have taken a byte, by more than the post-exit grace (a machine too busy to schedule them), still
// delivers everything it wrote on both pipes: no descendant holds them, so the grace (exec.ts waitForChildProcess's idle timer) has measured
// a slow reader, not a quiet writer.
func TestExecCommandKeepsTheOutputOfAChildThatExitedBeforeItsReadersRan(t *testing.T) {
	graceExpired := make(chan struct{})
	var once sync.Once
	ctx := extension.WithExecDrainHooks(testbudget.Context(t), childwait.Hooks{
		BeforeRead:   func() { <-graceExpired },
		GraceExpired: func() { once.Do(func() { close(graceExpired) }) },
	})
	command, args := "sh", []string{"-c", "printf out; printf err >&2"}
	if runtime.GOOS == "windows" {
		command, args = "cmd", []string{"/c", "echo out& echo err 1>&2"}
	}
	result, err := extension.ExecCommand(ctx, t.TempDir(), command, args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out, errText := strings.TrimSpace(result.Stdout), strings.TrimSpace(result.Stderr); out != "out" || errText != "err" || result.Code != 0 {
		t.Fatalf("result = %+v, want stdout %q, stderr %q and code 0", result, "out", "err")
	}
}
