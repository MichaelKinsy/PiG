//go:build unix

package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// print-mode.ts:44-52, 66-69: a SIGTERM runs the same disposeRuntime, which unsubscribes the JSON writer before runtimeHost.dispose() emits session_shutdown. The
// signal path already detached first; this keeps it equal to the normal teardown: what the shutdown handler appends, names and sends is not written.
func TestJSONModeShutdownHandlerOutputIsNotWrittenOnSIGTERM(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping signal test in short mode")
	}
	root, binary := shutdownWriterRoot(t)
	workDir := t.TempDir()
	cmd := exec.Command(binary, "--mode", "json", "--model", "test-faux/faux-1", "--no-session",
		"-e", filepath.Join(root, "test", "parity", "testdata", "shutdown-writer", "node"), "Run: sleep for a while")
	cmd.Dir = workDir
	home := t.TempDir()
	cmd.Env = append(os.Environ(), "PIG_HOME="+home, "PI_HOME="+home, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pig: %v", err)
	}
	t.Cleanup(func() { killProcessesIn(workDir) })
	marker := filepath.Join(workDir, ai.TestFauxToolStartedMarker)
	deadline := time.Now().Add(testbudget.Wait(t))
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatal("faux tool never started; cannot signal a run that is not in flight")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	var exitErr *exec.ExitError
	if err := cmd.Wait(); !errors.As(err, &exitErr) || exitErr.ExitCode() != 143 {
		t.Fatalf("wait = %v, want exit 143\nstderr: %s", err, stderr.String())
	}
	for _, word := range []string{"shutdown-entry", "shutdown-name", "shutdown-text", "shutdown-label", "entry_appended", "session_info_changed"} {
		if bytes.Contains(stdout.Bytes(), []byte(word)) {
			t.Errorf("stdout carries %q written by the shutdown handler after SIGTERM:\n%s", word, stdout.String())
		}
	}
}
