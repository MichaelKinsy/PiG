package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestStatusJSONReportsHealthyEmptyState(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	cwd := filepath.Join(root, "workspace")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_HOME", root)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("HOME", root)
	t.Chdir(cwd)
	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runStatusCommand([]string{"status", "--json", "--no-input"})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var status statusOutput
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatal(err)
	}
	assertNoTopLevelVersionJSON(t, stdout)
	if !status.Healthy || status.Packages.Total != 0 || status.Resources.Total != 0 || status.Piglets.Total != 0 || len(status.Errors) != 0 {
		t.Fatalf("status = %#v", status)
	}
	if status.Paths.Home != canonicalStatusPath(root) || status.Paths.Agent != canonicalStatusPath(agentDir) || status.Paths.Project != canonicalStatusPath(filepath.Join(cwd, ".pig")) {
		t.Fatalf("paths = %#v", status.Paths)
	}
}

func TestStatusFailsOnDuplicateResourceIdentity(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	cwd := filepath.Join(root, "workspace")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_HOME", root)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Chdir(cwd)
	for _, directory := range []string{"one", "two"} {
		writeResourceFixture(t, filepath.Join(agentDir, "skills", directory, "SKILL.md"), "---\nname: duplicate\n---\n")
	}
	stdout, stderr, code := captureStdoutStderr(t, func() int { return runStatusCommand([]string{"status", "--json"}) })
	if code != 1 || stderr != "" || !strings.Contains(stdout, "duplicate skills resource") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestStatusJSONFailsOnUnmaterializedPackage(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	cwd := filepath.Join(root, "workspace")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_HOME", root)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Chdir(cwd)
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	if err := settings.SetPackages([]codingagent.PackageSource{{Source: filepath.Join(root, "missing")}}); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runStatusCommand([]string{"status", "--json"})
	})
	if code != 1 || stderr != "" {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var status statusOutput
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatal(err)
	}
	if status.Healthy || len(status.Errors) == 0 || !strings.Contains(strings.Join(status.Errors, "\n"), "not materialized") {
		t.Fatalf("status = %#v", status)
	}
}

func TestStatusHelpAndUnknownOption(t *testing.T) {
	stdout, stderr, code := captureStdoutStderr(t, func() int { return runStatusCommand([]string{"status", "--help"}) })
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Usage:") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	_, stderr, code = captureStdoutStderr(t, func() int { return runStatusCommand([]string{"status", "--unknown"}) })
	if code != 2 || !strings.Contains(stderr, "Usage:") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
}

func TestStatusAtHomeDoesNotReportGlobalRootAsProject(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, ".pig", "agent")
	t.Setenv("HOME", home)
	t.Setenv("PIG_HOME", filepath.Join(home, ".pig"))
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Chdir(home)

	status := collectStatus()
	if status.Paths.Project != "" {
		t.Fatalf("global root reported as project path: %#v", status.Paths)
	}
}

func TestStatusAcceptsDisabledMissingPackageMember(t *testing.T) {
	cwd, agentDir, packageRoot := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Chdir(cwd)
	writeResourceFixture(t, filepath.Join(packageRoot, "package.json"), `{"name":"pkg","pi":{"extensions":["extensions/removed"]}}`)
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	if err := settings.SetPackages([]codingagent.PackageSource{{Source: packageRoot, Extensions: []string{"-extensions/removed"}}}); err != nil {
		t.Fatal(err)
	}

	status := collectStatus()
	if !status.Healthy || len(status.Errors) != 0 {
		t.Fatalf("disabled missing member made status unhealthy: %#v", status)
	}
	var missing *statusResourceItem
	for i := range status.Resources.Items {
		if status.Resources.Items[i].Name == "removed" {
			missing = &status.Resources.Items[i]
		}
	}
	if missing == nil || missing.Health != "missing" || missing.Enabled {
		t.Fatalf("missing resource item = %#v in %#v", missing, status.Resources.Items)
	}
}

func writeResourceFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
