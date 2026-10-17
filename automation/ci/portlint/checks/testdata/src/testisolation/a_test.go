package testisolation

import (
	"os"
	"os/exec"
	"testing"
)

func TestBad(t *testing.T) {
	_ = os.Setenv("HOME", "/x") // want `os.Setenv\("HOME"\) leaks past the test`
	_, _ = os.UserHomeDir()     // want `os.UserHomeDir reads the real home directory`
}

func TestPartial(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // want `test sets 1 of the 5 home and agent-directory variables`
	_ = exec.Command("pig").Run()
}

func TestOnlyHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
}

func TestFull(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PI_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	_ = exec.Command("pig").Run()
}
