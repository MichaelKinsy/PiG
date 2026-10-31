//go:build !pig_strip_self_update

package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func releaseManifestServer(t *testing.T, manifest string) *httptest.Server {
	t.Helper()
	return signedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(manifest)) }))
}

// Pi package-command-paths.test.ts "runs PI_SELF_UPDATE_COMMAND instead of the managed update and the selfUpdateCommand
// setting": the container tier (Pi's managed install) is replaced, as is the setting, and the pig on PATH confirms the version.
func TestConfiguredSelfUpdateCommandEnvReplacesTheTierAndTheSetting(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_INSTALL_TIER", "container")
	wrapper, record := prepareSelfUpdateCommand(t, "9.9.9", 0)
	agentDir := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	settingRecord := filepath.Join(t.TempDir(), "setting-record.json")
	settings, _ := json.Marshal(map[string]any{"selfUpdateCommand": []string{wrapper, settingRecord}})
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), settings, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_SELF_UPDATE_COMMAND", "pi-wrapper upgrade {installSpec} {version}")
	server := releaseManifestServer(t, `{"version":"9.9.9","packageName":"@new-scope/pi","notes":"Read me","binaries":{}}`)
	defer server.Close()
	t.Setenv("PIG_UPDATE_URL", server.URL)

	stdout, stderr, code := captureStdoutStderr(t, func() int { return runSelfUpdate(false) })

	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q, want 0 and no stderr; stdout=%q", code, stderr, stdout)
	}
	if got := readSelfUpdateRecord(t, record); strings.Join(got, "|") != "upgrade|@new-scope/pi@9.9.9|9.9.9" {
		t.Fatalf("PI_SELF_UPDATE_COMMAND ran with %q", got)
	}
	if _, err := os.Stat(settingRecord); err == nil {
		t.Fatal("the selfUpdateCommand setting ran although PI_SELF_UPDATE_COMMAND takes precedence")
	}
	if !strings.Contains(stdout, "Read me") {
		t.Fatalf("the release note did not print first:\n%s", stdout)
	}
	// Like mise, the command leaves this installation untouched; the pig on PATH reports the new version.
	if want := "Updated pig from " + selfUpdateVersion() + " to 9.9.9"; !strings.Contains(stdout, want) {
		t.Fatalf("stdout lacks %q:\n%s", want, stdout)
	}
}

// Pi "runs the global selfUpdateCommand setting only when a newer release exists": the project's setting is never read.
func TestConfiguredSelfUpdateSettingRunsOnlyForANewerRelease(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	wrapper, record := prepareSelfUpdateCommand(t, "9.9.9", 0)
	agentDir, project := t.TempDir(), t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Chdir(project)
	t.Setenv("PI_SELF_UPDATE_COMMAND", "")
	projectRecord := filepath.Join(t.TempDir(), "project-record.json")
	for dir, argv := range map[string][]string{agentDir: {wrapper, "{packageName}"}, filepath.Join(project, ".pig"): {wrapper, projectRecord}} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(map[string]any{"selfUpdateCommand": argv})
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	current := upToDateManifest(t)
	defer current.Close()
	t.Setenv("PIG_UPDATE_URL", current.URL)
	_, stderr, code := captureStdoutStderr(t, func() int { return runSelfUpdate(false) })
	if _, err := os.Stat(record); err == nil || code != 0 || !strings.Contains(stderr, "up to date") {
		t.Fatalf("an up-to-date release ran the command or failed: code=%d stderr=%q", code, stderr)
	}

	newer := releaseManifestServer(t, fmt.Sprintf(`{"version":"9.9.9","packageName":%q,"binaries":{}}`, codingagent.PackageName))
	defer newer.Close()
	t.Setenv("PIG_UPDATE_URL", newer.URL)
	stdout, stderr, code := captureStdoutStderr(t, func() int { return runSelfUpdate(false) })
	if code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if got := readSelfUpdateRecord(t, record); strings.Join(got, "|") != codingagent.PackageName {
		t.Fatalf("the setting ran with %q", got)
	}
	if _, err := os.Stat(projectRecord); err == nil {
		t.Fatal("a project's selfUpdateCommand ran")
	}
}

// Pi "warns when the pi on PATH does not report the new version".
func TestConfiguredSelfUpdateWarnsWhenThePigOnPathDoesNotReportTheNewVersion(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	_, _ = prepareSelfUpdateCommand(t, selfUpdateVersion(), 0)
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_SELF_UPDATE_COMMAND", "pi-wrapper upgrade")
	server := releaseManifestServer(t, fmt.Sprintf(`{"version":"9.9.9","packageName":%q,"binaries":{}}`, codingagent.PackageName))
	defer server.Close()
	t.Setenv("PIG_UPDATE_URL", server.URL)

	stdout, _, code := captureStdoutStderr(t, func() int { return runSelfUpdate(false) })

	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	if want := "Ran pi-wrapper upgrade, but `pig --version` reports " + selfUpdateVersion() + ", not 9.9.9."; !strings.Contains(stdout, want) {
		t.Fatalf("stdout lacks %q:\n%s", want, stdout)
	}
	if strings.Contains(stdout, "Updated pig from") {
		t.Fatalf("a failed update was reported as done:\n%s", stdout)
	}
}

// Pi "fails when the configured self-update command fails".
func TestConfiguredSelfUpdateFailsWhenTheCommandFails(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	_, _ = prepareSelfUpdateCommand(t, selfUpdateVersion(), 23)
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_SELF_UPDATE_COMMAND", "pi-wrapper upgrade")
	server := releaseManifestServer(t, fmt.Sprintf(`{"version":"9.9.9","packageName":%q,"binaries":{}}`, codingagent.PackageName))
	defer server.Close()
	t.Setenv("PIG_UPDATE_URL", server.URL)

	stdout, stderr, code := captureStdoutStderr(t, func() int { return runSelfUpdate(false) })

	if code != 1 || !strings.Contains(stderr, "pi-wrapper upgrade exited with code 23") {
		t.Fatalf("code=%d stderr=%q, want 1 and the exit code", code, stderr)
	}
	if strings.Contains(stdout, "Ran pi-wrapper") {
		t.Fatalf("a failed command was followed by the version report:\n%s", stdout)
	}
}

// pig divergence (D39): a configured command needs the release it fills its placeholders from. With no update source
// there is none, and the command does not run on a guess.
func TestConfiguredSelfUpdateWithoutAnUpdateSourceDoesNotRun(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_UPDATE_URL", "")
	_, record := prepareSelfUpdateCommand(t, selfUpdateVersion(), 0)
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_SELF_UPDATE_COMMAND", "pi-wrapper upgrade")

	_, stderr, code := captureStdoutStderr(t, func() int { return runSelfUpdate(false) })

	if _, err := os.Stat(record); err == nil || code != 1 || !strings.Contains(stderr, "no update source configured") {
		t.Fatalf("code=%d stderr=%q, want 1, no update source and no command run", code, stderr)
	}
}
