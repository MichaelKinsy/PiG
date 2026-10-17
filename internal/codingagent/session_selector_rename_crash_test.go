package codingagent

import (
	"errors"
	"strings"
	"testing"
)

// Pi's unhandled rename rejection reaches uncaughtCrash (interactive-mode.ts:4199-4220): console.error prints the Error as "Error: <message>" before its stack, the crash is recorded with the bare message, and the /bug instructions follow.
func TestUncaughtCrashPrintsRenameRejectionAsError(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	agentDir := t.TempDir()
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: "/work", AgentDir: agentDir, AppVersion: "9.9.9"})
	var stderr strings.Builder
	m.uncaughtCrash(uncaughtError{errors.New("rename denied")}, []byte("goroutine 1 [running]:\n"), &stderr)
	out := stderr.String()
	if want := "pig exiting due to uncaughtException:\nError: rename denied\ngoroutine 1 [running]:\n"; !strings.HasPrefix(out, want) {
		t.Fatalf("stderr = %q, want prefix %q", out, want)
	}
	if !strings.HasSuffix(out, "start pig and run /bug. The crash details are attached automatically.\n") {
		t.Fatalf("stderr = %q lacks the /bug instructions", out)
	}
	records := ReadCrashLog(CrashLogPath(agentDir))
	if len(records) != 1 || records[0].Message != "rename denied" || records[0].Kind != "uncaught_exception" {
		t.Fatalf("records = %+v", records)
	}
}
