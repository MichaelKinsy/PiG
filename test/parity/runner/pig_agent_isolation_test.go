//go:build parity

package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestPigAgentDirectoryChild(t *testing.T) {
	if os.Getenv("PIG_TEST_AGENT_DIRECTORY_CHILD") != "1" {
		return
	}
	fmt.Print(codingagent.AgentDir())
	os.Exit(0)
}

func TestResolvePigBinOverridesInheritedAgentDirectory(t *testing.T) {
	inherited := t.TempDir()
	if err := os.WriteFile(filepath.Join(inherited, "settings.json"), []byte(`{"outputPad":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_CODING_AGENT_DIR", inherited)
	t.Setenv("PIG_PARITY_REAL_AUTH", "")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_PARITY_PIG_BIN", executable)
	ref := ResolvePigBin(t)
	cmd := exec.CommandContext(t.Context(), ref.Path, "-test.run=^TestPigAgentDirectoryChild$")
	cmd.Env = append(os.Environ(), ref.Env...)
	cmd.Env = append(cmd.Env, "PIG_TEST_AGENT_DIRECTORY_CHILD=1")
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	dir := string(output)
	var home string
	for _, entry := range ref.Env {
		if value, ok := strings.CutPrefix(entry, "PIG_HOME="); ok {
			home = value
		}
	}
	if want := filepath.Join(home, "agent"); dir != want || dir == inherited {
		t.Fatalf("resolved agent directory=%q; want %q", dir, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("operator settings leaked: %v", err)
	}
}

// A scenario's snapshotted PIG_HOME must outrank the default sandbox without reviving an inherited agent-directory override.
func TestResolvePigBinPreservesHomeOnlyScenarioOverride(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_PARITY_PIG_BIN", executable)
	t.Setenv("PIG_PARITY_REAL_AUTH", "")
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	fixture := t.TempDir()
	if err := os.Mkdir(filepath.Join(fixture, "agent"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := []byte(`{"outputPad":3}`)
	if err := os.WriteFile(filepath.Join(fixture, "agent", "settings.json"), settings, 0o600); err != nil {
		t.Fatal(err)
	}
	scenarioPath := filepath.Join(t.TempDir(), "scenario.toml")
	ref := ResolvePigBin(t)
	defaults := snapshotBinaryEnv(t, scenarioPath, ref.Env, false, false)
	overrides, _ := snapshotAgentDirs(t, scenarioPath, []string{"PIG_HOME=" + fixture}, nil, false, false)
	want := filepath.Join(strings.TrimPrefix(overrides[0], "PIG_HOME="), "agent")
	cmd := exec.CommandContext(t.Context(), ref.Path, "-test.run=^TestPigAgentDirectoryChild$")
	cmd.Env = append(os.Environ(), defaults...)
	cmd.Env = append(cmd.Env, overrides...)
	cmd.Env = append(cmd.Env, "PIG_TEST_AGENT_DIRECTORY_CHILD=1")
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != want {
		t.Fatalf("resolved directory=%q; want snapshotted fixture %q", output, want)
	}
	data, err := os.ReadFile(filepath.Join(string(output), "settings.json"))
	if err != nil || string(data) != string(settings) {
		t.Fatalf("settings=%q error=%v", data, err)
	}
	data, err = os.ReadFile(filepath.Join(fixture, "agent", "settings.json"))
	if err != nil || string(data) != string(settings) {
		t.Fatalf("source fixture changed: %q %v", data, err)
	}
}
