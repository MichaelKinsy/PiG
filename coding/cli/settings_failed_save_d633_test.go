package cli

// pi: packages/coding-agent/src/core/settings-manager.ts

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// holdSettingsLock takes the lock directory of the settings.json in dir, as another process that holds proper-lockfile's lock does: every save of the file fails with ELOCKED and the file stays readable. The lock's time is ahead of the clock, so no attempt finds it stale.
func holdSettingsLock(t *testing.T, dir string) {
	t.Helper()
	lock := filepath.Join(dir, "settings.json.lock")
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	ahead := time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(lock, ahead, ahead); err != nil {
		t.Fatal(err)
	}
}

// D633. RPC set_steering_mode and set_follow_up_mode answer an error when the mode cannot be saved, where Pi answers success (rpc-mode.ts:540-548). The running agent's queue mode has changed, as in Pi, so get_state reports the new modes.
func TestRPCQueueModeSettersAnswerAFailedSave(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	cwd := filepath.Join(home, "cwd")
	for _, dir := range []string{agentDir, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	input := strings.Join([]string{
		`{"id":"steering","type":"set_steering_mode","mode":"all"}`,
		`{"id":"followup","type":"set_follow_up_mode","mode":"all"}`,
		`{"id":"after","type":"get_state"}`,
	}, "\n") + "\n"

	binary := buildPigBinaryForSignalTest(t)
	holdSettingsLock(t, agentDir)
	cmd := exec.Command(binary, "--mode", "rpc", "--model", "test-faux/faux-1", "--no-extensions", "--offline", "--no-session")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+home, "PI_HOME="+home, "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1")
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("RPC process: %v\n%s", err, stderr.String())
	}

	responses := map[string]map[string]any{}
	scanner := bufio.NewScanner(bytes.NewReader(stdout.Bytes()))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode RPC output: %v", err)
		}
		if id, _ := record["id"].(string); record["type"] == "response" && id != "" {
			responses[id] = record
		}
	}
	for _, id := range []string{"steering", "followup"} {
		response := responses[id]
		if response == nil || response["success"] != false || response["error"] != pilock.ErrLocked.Error() {
			t.Errorf("%s response = %v, want an error response with %q\nstderr:\n%s", id, response, pilock.ErrLocked, stderr.String())
		}
	}
	after, _ := responses["after"]["data"].(map[string]any)
	if after["steeringMode"] != "all" || after["followUpMode"] != "all" {
		t.Errorf("state after the setters = %v, want both queue modes all: the running agent's mode changes, as in Pi", after)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "settings.json")); !os.IsNotExist(err) {
		t.Errorf("settings.json after two refused saves: %v, want no file", err)
	}
}

// D633. `pig install` prints the error of a package list that could not be saved and exits 1 after the package was installed, where Pi prints Installed and exits 0 (package-manager-cli.ts:1061-1063 over core/package-manager.ts:902). `pig remove` reports what RemoveAndPersist returns in the same way, and RemoveAndPersist returns the failed save (:933). A lock held when the command starts also refuses the read of the list, so the command finds no package to remove; the removal is therefore made on a manager that read the list before the lock was taken.
func TestPackageInstallAndRemoveReportAFailedSave(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	agentDir := filepath.Join(home, "agent")
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Chdir(cwd)
	first, second := filepath.Join(cwd, "first-pkg"), filepath.Join(cwd, "second-pkg")
	for _, pkg := range []string{first, second} {
		writePackageResource(t, filepath.Join(pkg, "extensions", "index.ts"), "export default function() {}")
	}
	if stdout, stderr, code := captureStdoutStderr(t, func() int { return runPackageCommand([]string{"install", first}) }); code != 0 {
		t.Fatalf("install before the lock is held: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	loaded := codingagent.NewSettingsManager(cwd, agentDir)
	if packages := loaded.GetGlobalSettings().Packages; len(packages) != 1 {
		t.Fatalf("packages after the first install = %+v, want the first package", packages)
	}
	holdSettingsLock(t, agentDir)
	stdout, stderr, code := captureStdoutStderr(t, func() int { return runPackageCommand([]string{"install", second}) })
	if code != 1 || !strings.Contains(stderr, pilock.ErrLocked.Error()) || strings.Contains(stdout, "Installed") {
		t.Errorf("install with the settings lock held: code=%d stdout=%q stderr=%q, want exit 1, %q and no Installed line", code, stdout, stderr, pilock.ErrLocked)
	}
	if removed, err := packageManagerFor(cwd, loaded, nil).RemoveAndPersist(first, false); err == nil || err.Error() != pilock.ErrLocked.Error() {
		t.Errorf("RemoveAndPersist with the settings lock held = %t, %v; want %q", removed, err, pilock.ErrLocked)
	}
	data, err := os.ReadFile(filepath.Join(agentDir, "settings.json"))
	if err != nil || !strings.Contains(string(data), "first-pkg") || strings.Contains(string(data), "second-pkg") {
		t.Errorf("settings.json = %s (%v), want the first package alone", data, err)
	}
}

// D633. The config selector reports a toggle that could not be saved and shows the entry in its previous state, where Pi's selector keeps the toggle (config-selector.ts:552-564, :626-628).
func TestConfigSelectorReportsAToggleThatCouldNotBeSaved(t *testing.T) {
	toggle := func(t *testing.T, selector interface {
		Render(int) []string
		HandleInput(string)
	}, settingsDir string) {
		t.Helper()
		before := strings.Join(selector.Render(80), "\n")
		holdSettingsLock(t, settingsDir)
		_, stderr, _ := captureStdoutStderr(t, func() int { selector.HandleInput(" "); return 0 })
		if !strings.HasPrefix(stderr, "config toggle: ") || !strings.Contains(stderr, pilock.ErrLocked.Error()) {
			t.Errorf("stderr = %q, want config toggle: and %q", stderr, pilock.ErrLocked)
		}
		if after := strings.Join(selector.Render(80), "\n"); after != before {
			t.Errorf("the selector shows the toggle that was not saved:\n%s\nbefore:\n%s", after, before)
		}
	}
	t.Run("global scope", func(t *testing.T) {
		_, cwd, agentDir := resourceExtensionFixture(t)
		sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
		selector, err := newScopedConfigSelector(cwd, agentDir, sm, sm, false, true, []string{"llama.cpp", "mcp"})
		if err != nil {
			t.Fatal(err)
		}
		toggle(t, selector, agentDir)
	})
	t.Run("project scope", func(t *testing.T) {
		_, cwd, agentDir := resourceExtensionFixture(t)
		global := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
		sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
		selector, err := newScopedConfigSelector(cwd, agentDir, global, sm, true, true, []string{"mcp"})
		if err != nil {
			t.Fatal(err)
		}
		projectDir := codingagent.ProjectConfigDir(cwd)
		if err := os.MkdirAll(projectDir, 0o755); err != nil {
			t.Fatal(err)
		}
		toggle(t, selector, projectDir)
	})
}
