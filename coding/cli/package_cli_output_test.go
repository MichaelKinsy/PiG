package cli

// pi: packages/coding-agent/src/package-manager-cli.ts

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi package-manager.ts:1034-1055 emits progress before removal and persists [] (settings-manager.ts:1105-1115).
func TestPackageRemovalProgressAndEmptySettings(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(fmt.Sprint(local), func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			agentDir := filepath.Join(root, "agent")
			t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
			sm := codingagent.NewSettingsManager(root, agentDir)
			const source = "./fixture"
			if _, err := packageManagerFor(root, sm, nil).AddSourceToSettings(source, local); err != nil {
				t.Fatal(err)
			}
			args := []string{"remove", source, "--approve"}
			settingsPath := sm.GlobalSettingsPath()
			if local {
				args = append(args, "-l")
				settingsPath = filepath.Join(root, ".pig", "settings.json")
			}
			stdout, stderr, code := captureStdoutStderr(t, func() int { return runPackageCommand(args) })
			if code != 0 || stdout != "Removing ./fixture...\nRemoved ./fixture\n" || stderr != "" {
				t.Errorf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			data, err := os.ReadFile(settingsPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "{\n  \"packages\": []\n}" {
				t.Errorf("settings = %q", data)
			}
		})
	}
}

// Pi package-manager-cli.ts:1096-1100 prints the caught error only after inherited child output.
func TestPackageChildOutputAndFailure(t *testing.T) {
	for _, exit := range []int{0, 17} {
		t.Run(fmt.Sprint(exit), func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			agentDir := filepath.Join(root, "agent")
			t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
			stub := writeStubScript(t, filepath.Join(root, "npm"), fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = --version ]; then echo 12.1.0; exit; fi\nprintf 'child stdout\\n'\nprintf 'child stderr\\n' >&2\nexit %d\n", exit))
			sm := codingagent.NewSettingsManager(root, agentDir)
			if err := sm.SetNpmCommand([]string{stub}); err != nil {
				t.Fatal(err)
			}
			stdout, stderr, code := captureStdoutStderr(t, func() int { return runPackageCommand([]string{"install", "npm:fixture", "--no-approve"}) })
			wantOut, wantErr, wantCode := "Installing npm:fixture...\nchild stdout\n", "child stderr\n", 0
			if exit == 0 {
				wantOut += "Installed npm:fixture\n"
			} else {
				wantCode = 1
				wantErr += fmt.Sprintf("Error: %s install fixture --prefix %s --legacy-peer-deps failed with code %d\n", stub, filepath.Join(agentDir, "npm"), exit)
			}
			if code != wantCode || stdout != wantOut || stderr != wantErr {
				t.Fatalf("code=%d stdout=%q stderr=%q; want %d %q %q", code, stdout, stderr, wantCode, wantOut, wantErr)
			}
		})
	}
}

func TestPackageUpdateErrorPrefix(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	stdout, stderr, code := captureStdoutStderr(t, func() int { return runPackageCommand([]string{"update", "npm:missing", "--no-approve"}) })
	if code != 1 || stdout != "" || stderr != "Error: No matching package found for npm:missing\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
