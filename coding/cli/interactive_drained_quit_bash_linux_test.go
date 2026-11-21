//go:build linux

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInteractiveDrainedQuitKillsRunningUserBash verifies that when the user
// quits interactive mode (Ctrl+D) while a `!` command is running and an
// extension's session_shutdown handler never settles (so the quit ends through
// exitAtDrainedQuit on the drained Node event loop), both the shell and its
// descendant are killed before pig exits 0.
func TestInteractiveDrainedQuitKillsRunningUserBash(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not on PATH:", err)
	}
	binary := buildPigBinaryForSignalTest(t)
	root := t.TempDir()
	agent, project, sessions := filepath.Join(root, "agent"), filepath.Join(root, "project"), filepath.Join(root, "sessions")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	seedFirstRunDone(t, agent)
	models := `{"providers":{"localhold":{"baseUrl":"http://127.0.0.1:1/v1","api":"openai-completions","apiKey":"dummy","models":[{"id":"hold-model"}]}}}`
	if err := os.WriteFile(filepath.Join(agent, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	silent := filepath.Join(root, "shutdown-never.mjs")
	if err := os.WriteFile(silent, []byte(quitProbeShutdownNever), 0o600); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(root, "pids.txt")
	argv := []string{binary, "--no-extensions", "--model", "localhold/hold-model", "--session-dir", sessions, "-e", silent}
	env := []string{
		"HOME=" + root,
		"PIG_HOME=" + root,
		"PI_HOME=" + root,
		"PIG_CODING_AGENT_DIR=" + agent,
		"PI_CODING_AGENT_DIR=" + agent,
		"PIG_OFFLINE=1",
		"PI_OFFLINE=1",
	}
	p := startQuitPane(t, project, filepath.Join(root, "stderr.log"), env, argv...)
	p.await("the model in the footer", func() bool { return strings.Contains(p.screen(), "hold-model") })

	p.text("!" + longBashCommandWithChild(pidFile))
	p.await("the typed command", func() bool { return strings.Contains(p.screen(), "sleep 300") })
	p.keys("Enter")
	shellPID, childPID := readTwoPIDs(t, pidFile)

	p.keys("C-d")
	status := 0
	p.await("pig's drained-quit exit while the user's command runs", func() bool {
		code, exited := p.status()
		status = code
		return exited
	})
	if status != 0 {
		t.Errorf("pig exited with status %d, want 0", status)
	}
	if strings.Contains(p.screen(), "To resume this session") {
		t.Errorf("pig printed the resume hint; the test did not hit exitAtDrainedQuit:\n%s", p.screen())
	}
	assertProcessesGone(t, "interactive drained quit", shellPID, childPID)
}
