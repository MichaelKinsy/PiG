package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestPackageManagementSurfaceIsListAndValidateOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "must-not-exist")
	for _, command := range []string{"create", "add", "remove", "edit"} {
		stdout, stderr, code := captureStdoutStderr(t, func() int {
			return runPackageCommand([]string{"package", command, root})
		})
		if code != 2 || stdout != "" || !strings.Contains(stderr, "unknown command") || !strings.Contains(stderr, "use list or validate") {
			t.Fatalf("command=%s code=%d stdout=%q stderr=%q", command, code, stdout, stderr)
		}
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("removed Package CRUD mutated filesystem: %v", err)
	}
}

func TestPackageManagementHelpListsOnlyListAndValidate(t *testing.T) {
	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runPackageCommand([]string{"package", "--help"})
	})
	if code != 0 || stderr != "" || !strings.Contains(stdout, "package list") || !strings.Contains(stdout, "package validate") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, removed := range []string{"package create", "package add", "package remove", "package edit"} {
		if strings.Contains(stdout, removed) {
			t.Fatalf("help retained %q: %s", removed, stdout)
		}
	}
}

func TestPackageListJSONUsesConfiguredAgentDirectory(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Chdir(cwd)
	root := filepath.Join(t.TempDir(), "pkg")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"pkg"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: root}}); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runPackageCommand([]string{"package", "list", "--json", "--no-input"})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var output packageListOutput
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatal(err)
	}
	assertNoTopLevelVersionJSON(t, stdout)
	if len(output.Packages) != 1 || output.Packages[0].Source != root || output.Packages[0].InstalledPath != root {
		t.Fatalf("output = %#v", output)
	}
}

func TestPackageValidateReportsResourcesWithoutMutation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"pkg","pi":{"skills":["skills/review"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(root, "skills", "review")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: review\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runPackageCommand([]string{"package", "validate", root, "--json", "--no-input"})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var output packageValidationOutput
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatal(err)
	}
	if !output.Valid || output.Resources["skills"] != 1 {
		t.Fatalf("output = %#v", output)
	}
	after, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("validation mutated manifest: after=%q err=%v", after, err)
	}
}
