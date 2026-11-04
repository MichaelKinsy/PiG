//go:build linux

package codingagent

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const interruptInitChildEnv = "PIG_TEST_INTERRUPT_DURING_INIT_CHILD"

// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:974-977. init() registers the signal handlers before it builds anything, so a SIGINT that arrives while the screen is still being
// built, or right after it drew, finds the handler. Installed late, the signal killed the process by Go's default action: no exit.log line, no exit 130.
func TestInitTakesSIGINTBeforeBuildingTheScreen(t *testing.T) {
	captured, report, waitErr := runInitPTYChildRaw(t, "TestInitTakesSIGINTBeforeBuildingTheScreenChild", interruptInitChildEnv)
	exit, ok := errors.AsType[*exec.ExitError](waitErr)
	if !ok || exit.ExitCode() != 130 {
		t.Fatalf("child wait = %v, want exit status 130\n%q", waitErr, tailBytes(captured, 400))
	}
	if data, _ := os.ReadFile(ExitLogPath(filepath.Dir(report))); !strings.Contains(string(data), "received SIGINT (exit 130)") {
		t.Fatalf("exit.log = %q", data)
	}
}

func TestInitTakesSIGINTBeforeBuildingTheScreenChild(t *testing.T) {
	report := os.Getenv(interruptInitChildEnv)
	if report == "" {
		t.Skip("runs only as the pty child of TestInitTakesSIGINTBeforeBuildingTheScreen")
	}
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), AgentDir: filepath.Dir(report), NoThemes: true})
	m.opts.StartupMark = func(label string) {
		if label != "theme-detected" {
			return
		}
		if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
			t.Fatal(err)
		}
		// The handler ends the process. Without one the signal has already killed it.
		select {}
	}
	_ = m.Init(t.Context())
	t.Fatal("Init returned: the SIGINT handler did not end the process")
}
