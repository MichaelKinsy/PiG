//go:build !pig_strip_self_update

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// pig divergence (D39): "Only the global settings are read, so a project cannot inject a command." A trusted project's
// settings.json, under either project directory name, that sets selfUpdateCommand must not run a command, while the
// same value in the global settings does. Without a global command or an environment command the update takes the
// ordinary tier, here the container tier's refusal.
func TestSelfUpdateNeverRunsAProjectsSelfUpdateCommand(t *testing.T) {
	for _, mode := range []struct {
		name     string
		usePi    string
		agentEnv string
	}{
		{"PiG directories", "", "PIG_CODING_AGENT_DIR"},
		{"Pi directories", "1", "PI_CODING_AGENT_DIR"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			t.Setenv("PIG_HOME", t.TempDir())
			t.Setenv("PIG_INSTALL_TIER", "container")
			t.Setenv("PIG_USE_PI_DIRS", mode.usePi)
			t.Setenv("PI_SELF_UPDATE_COMMAND", "")
			wrapper, marker := prepareSelfUpdateCommand(t, "9.9.9", 0)
			agentDir, project := t.TempDir(), t.TempDir()
			t.Setenv(mode.agentEnv, agentDir)
			t.Chdir(project)
			// Both project directory names, so the one the mode selects is certain to be read if anything reads it.
			for _, dir := range []string{".pig", ".pi"} {
				writeSelfUpdateCommandSetting(t, filepath.Join(project, dir), []string{wrapper, "project-" + dir})
			}
			server := releaseManifestServer(t, fmt.Sprintf(`{"version":"9.9.9","packageName":%q,"binaries":{}}`, codingagent.PackageName))
			defer server.Close()
			t.Setenv("PIG_UPDATE_URL", server.URL)

			_, stderr, code := captureStdoutStderr(t, func() int { return runSelfUpdate(false) })

			if _, err := os.Stat(marker); err == nil {
				t.Fatalf("a project's selfUpdateCommand ran: %q", readSelfUpdateRecord(t, marker))
			}
			if code != 1 || !strings.Contains(stderr, "container or deployment image") {
				t.Fatalf("code=%d stderr=%q, want the ordinary container tier's refusal", code, stderr)
			}

			writeSelfUpdateCommandSetting(t, agentDir, []string{wrapper, "global"})
			_, stderr, code = captureStdoutStderr(t, func() int { return runSelfUpdate(false) })
			if code != 0 {
				t.Fatalf("the global selfUpdateCommand: code=%d stderr=%q", code, stderr)
			}
			if got := readSelfUpdateRecord(t, marker); strings.Join(got, "|") != "global" {
				t.Fatalf("the global selfUpdateCommand ran with %q, want only the global one", got)
			}
		})
	}
}

func writeSelfUpdateCommandSetting(t *testing.T, dir string, argv []string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"selfUpdateCommand": argv})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
