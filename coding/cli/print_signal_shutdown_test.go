//go:build unix

package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// TestPrintModeSignalDeliversSessionShutdown pins that a termination signal
// reaches extensions as session_shutdown before print mode exits. Pi 1.0.3
// print-mode.ts registerSignalHandlers awaits disposeRuntime(), which emits
// session_shutdown (agent-session-runtime.ts dispose), and only then calls
// process.exit(143|129). Pig's root signal handler cancelled the process
// context, which owns the extension subprocesses, while print mode was still
// disposing, so extensions were killed before the event reached them.
func TestPrintModeSignalDeliversSessionShutdown(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping signal shutdown test in short mode")
	}
	bin := buildPigBinaryForSignalTest(t)
	fixture, err := filepath.Abs(filepath.Join("testdata", "event-order.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		signal syscall.Signal
		want   int
	}{
		{syscall.SIGTERM, 143},
		{syscall.SIGHUP, 129},
	} {
		t.Run(tc.signal.String(), func(t *testing.T) {
			workDir := t.TempDir()
			eventLog := filepath.Join(t.TempDir(), "events.log")
			cmd := exec.Command(bin, "--model", "test-faux/faux-1", "--no-session", "-e", fixture, "--print", "Run: sleep for a while")
			cmd.Dir = workDir
			cmd.Env = append(os.Environ(),
				"PIG_HOME="+t.TempDir(),
				"PIG_TEST_FAUX=1",
				"PIG_TEST_FAUX_SCENARIO=parity-basic",
				"PIG_TEST_EVENT_LOG="+eventLog,
			)
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := cmd.Start(); err != nil {
				t.Fatalf("start pig: %v", err)
			}
			defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }()

			marker := filepath.Join(workDir, ai.TestFauxToolStartedMarker)
			deadline := time.Now().Add(testbudget.Wait(t))
			for {
				if _, err := os.Stat(marker); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("faux tool never started; cannot signal a run that is not in flight")
				}
				time.Sleep(20 * time.Millisecond)
			}
			if err := cmd.Process.Signal(tc.signal); err != nil {
				t.Fatalf("signal %s: %v", tc.signal, err)
			}
			err := cmd.Wait()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != tc.want {
				t.Fatalf("wait = %v, want exit status %d", err, tc.want)
			}
			data, err := os.ReadFile(eventLog)
			if err != nil {
				t.Fatalf("no events recorded: %v", err)
			}
			if got := strings.Fields(string(data)); !slices.Contains(got, "session_shutdown") {
				t.Fatalf("events = %v, want session_shutdown before exit", got)
			}
		})
	}
}
