package codingagent

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

func writeSettingsFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSettingsExplicitZeroKeepsItsValue mirrors upstream settings-manager.ts
// getters, which use `?? default`: an explicit 0 is a value, not a missing
// setting, and a project 0 overrides a global value.
func TestSettingsExplicitZeroKeepsItsValue(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	writeSettingsFixture(t, filepath.Join(agentDir, "settings.json"), `{
		"retry": {"maxRetries": 5, "baseDelayMs": 0, "provider": {"timeoutMs": 0, "maxRetries": 0}},
		"compaction": {"reserveTokens": 0, "keepRecentTokens": 0},
		"branchSummary": {"reserveTokens": 0}
	}`)
	writeSettingsFixture(t, filepath.Join(cwd, CONFIG_DIR_NAME, "settings.json"), `{"retry": {"maxRetries": 0}}`)
	sm := NewSettingsManagerWithProjectTrust(cwd, agentDir, true)

	retry := sm.GetRetrySettings()
	if retry.MaxRetries != 0 || retry.BaseDelayMs != 0 {
		t.Fatalf("retry = %+v, want maxRetries 0 (project) and baseDelayMs 0", retry)
	}
	if got := sm.GetCompactionSettings(); got.ReserveTokens != 0 || got.KeepRecentTokens != 0 {
		t.Fatalf("compaction = %+v, want zero tokens", got)
	}
	if got := sm.GetBranchSummarySettings().ReserveTokens; got != 0 {
		t.Fatalf("branch summary reserve = %d, want 0", got)
	}
	if got := sm.GetProviderRetrySettings(); got.MaxRetries != 0 || got.TimeoutMs != 0 {
		t.Fatalf("provider retry = %+v", got)
	}
	if got := mustTimeout(t)(sm.GetProviderRequestTimeoutMs()); got != 0 {
		t.Fatalf("provider request timeout = %d, want the explicit 0", got)
	}

	if err := sm.SetRetryEnabled(false); err != nil {
		t.Fatal(err)
	}
	reloaded := NewSettingsManagerWithProjectTrust(t.TempDir(), agentDir, false)
	if got := reloaded.GetRetrySettings(); got.MaxRetries != 5 || got.BaseDelayMs != 0 || got.Enabled {
		t.Fatalf("reloaded global retry = %+v, want maxRetries 5, baseDelayMs 0, disabled", got)
	}
	if got := reloaded.GetCompactionSettings(); got.ReserveTokens != 0 {
		t.Fatalf("reloaded compaction = %+v, want the explicit 0 kept on save", got)
	}
}

// TestSettingsAbsentValuesUseDefaults pins the upstream defaults.
func TestSettingsAbsentValuesUseDefaults(t *testing.T) {
	sm := NewSettingsManagerWithProjectTrust(t.TempDir(), t.TempDir(), false)
	if got := sm.GetRetrySettings(); got.MaxRetries != 3 || got.BaseDelayMs != 2000 || got.MaxDelayMs != 60000 || !got.Enabled {
		t.Fatalf("retry defaults = %+v", got)
	}
	if got := sm.GetCompactionSettings(); got != defaultCompactionConfig {
		t.Fatalf("compaction defaults = %+v", got)
	}
	if got := sm.GetBranchSummarySettings().ReserveTokens; got != 16384 {
		t.Fatalf("branch summary default = %d", got)
	}
}

// TestSettingsLoadToleratesUpstreamValidValues covers CFG-04: upstream
// accepts "disabled" and numeric strings for httpIdleTimeoutMs and never drops
// a whole settings file for one field's type, so the other settings load and
// writes still work. The raw value stays on disk.
func TestSettingsLoadToleratesUpstreamValidValues(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		wantIdle int
	}{
		{name: "disabled", body: `{"theme":"dark","defaultModel":"m","httpIdleTimeoutMs":"disabled"}`, wantIdle: 0},
		{name: "numeric string", body: `{"theme":"dark","defaultModel":"m","httpIdleTimeoutMs":" 1500.7 "}`, wantIdle: 1500},
		{name: "float", body: `{"theme":"dark","defaultModel":"m","httpIdleTimeoutMs":2500.9}`, wantIdle: 2500},
		{name: "mistyped unrelated field", body: `{"theme":"dark","defaultModel":"m","quietStartup":"yes","retry":{"maxRetries":"many","baseDelayMs":7}}`, wantIdle: defaultHTTPIdleTimeoutMs},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agentDir := t.TempDir()
			path := filepath.Join(agentDir, "settings.json")
			writeSettingsFixture(t, path, tc.body)
			sm := NewSettingsManagerWithProjectTrust(t.TempDir(), agentDir, false)
			if errs := sm.DrainErrors(); len(errs) != 0 {
				t.Fatalf("load errors = %+v", errs)
			}
			if got := sm.GetTheme(); got != "dark" {
				t.Fatalf("theme = %q, want dark", got)
			}
			if got := mustTimeout(t)(sm.GetHttpIdleTimeoutMs()); got != tc.wantIdle {
				t.Fatalf("idle timeout = %d, want %d", got, tc.wantIdle)
			}
			if err := sm.SetTheme("light"); err != nil {
				t.Fatalf("SetTheme: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), `"light"`) || !strings.Contains(string(data), `"m"`) {
				t.Fatalf("saved settings lost values:\n%s", data)
			}
			if tc.name == "disabled" && !strings.Contains(string(data), `"disabled"`) {
				t.Fatalf("saved settings rewrote the untouched httpIdleTimeoutMs:\n%s", data)
			}
		})
	}
}

// TestSettingsInvalidIdleTimeoutIsAnError covers CFG-10: a negative value is
// an error the caller reports, not a crash, and the rest of the file loads.
func TestSettingsInvalidIdleTimeoutIsAnError(t *testing.T) {
	agentDir := t.TempDir()
	writeSettingsFixture(t, filepath.Join(agentDir, "settings.json"), `{"theme":"dark","httpIdleTimeoutMs":-5}`)
	sm := NewSettingsManagerWithProjectTrust(t.TempDir(), agentDir, false)
	if got := sm.GetTheme(); got != "dark" {
		t.Fatalf("theme = %q, want dark", got)
	}
	if _, err := sm.GetHttpIdleTimeoutMs(); err == nil || err.Error() != "Invalid httpIdleTimeoutMs setting: -5" {
		t.Fatalf("GetHttpIdleTimeoutMs error = %v", err)
	}
	if _, err := sm.GetProviderRequestTimeoutMs(); err == nil {
		t.Fatal("GetProviderRequestTimeoutMs accepted the invalid idle timeout")
	}
}

// TestSettingsSaveAcceptsBOM covers CFG-05: upstream saves through
// JSON.parse(stripBom(current)), so a settings.json written with a BOM
// stays writable.
func TestSettingsSaveAcceptsBOM(t *testing.T) {
	agentDir := t.TempDir()
	path := filepath.Join(agentDir, "settings.json")
	writeSettingsFixture(t, path, "\xef\xbb\xbf"+`{"theme":"dark"}`)
	sm := NewSettingsManagerWithProjectTrust(t.TempDir(), agentDir, false)
	if err := sm.SetDefaultModel("claude-opus-4-8"); err != nil {
		t.Fatalf("SetDefaultModel: %v", err)
	}
	reloaded := NewSettingsManagerWithProjectTrust(t.TempDir(), agentDir, false)
	if got := reloaded.GetDefaultModel(); got != "claude-opus-4-8" {
		t.Fatalf("default model = %q", got)
	}
	if got := reloaded.GetTheme(); got != "dark" {
		t.Fatalf("theme = %q, want dark", got)
	}
}

// fileURLCase is a file:// URL that Node's fileURLToPath converts on this
// platform: on Windows it needs a drive (a drive-less file URL throws there).
func fileURLCase() struct{ raw, want string } {
	if runtime.GOOS == "windows" {
		return struct{ raw, want string }{"file:///C:/tools/zsh.exe", `C:\tools\zsh.exe`}
	}
	return struct{ raw, want string }{"file:///usr/local/bin/zsh", "/usr/local/bin/zsh"}
}

// On win32 upstream normalizePath also converts MSYS drive paths, expands
// "~\", and gives a file URL's host as a UNC share.
func TestSettingsPathNormalizesWindowsFormsLikeUpstream(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("upstream applies these conversions only on win32")
	}
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	for raw, want := range map[string]string{
		"/c/tools/bash.exe":        `C:\tools\bash.exe`,
		`~\bin\bash.exe`:           filepath.Join(home, "bin", "bash.exe"),
		"file://server/share/zsh":  `\\server\share\zsh`,
		"file:///D:/a%20b/zsh.exe": `D:\a b\zsh.exe`,
		// Node's fileURLToPath gives an IDNA host in Unicode, lowercased.
		"file://xn--mnich-kva/share/zsh.exe": `\\münich\share\zsh.exe`,
		"file://SERVER/share/zsh":            `\\server\share\zsh`,
	} {
		if got, err := normalizeSettingsPath(raw); err != nil || got != want {
			t.Errorf("normalizeSettingsPath(%q) = %q, %v, want %q", raw, got, err, want)
		}
	}
}

// Upstream getShellPath and getSessionDir call Node's fileURLToPath, which
// throws for a file URL without a drive letter or host on win32 and for an
// encoded separator. The getters return that error instead of a path.
func TestSettingsPathGettersReturnInvalidFileURLErrors(t *testing.T) {
	raw := "file://server/share/a%2Fb"
	if runtime.GOOS == "windows" {
		raw = "file:///no/drive"
	}
	settings := Settings{ShellPath: raw, SessionDir: raw}
	sm := &SettingsManager{merged: settings}
	for name, get := range map[string]func() (string, error){
		"SettingsManager.GetShellPath": sm.GetShellPath,
		"Settings.GetShellPath":        settings.GetShellPath,
		"GetSessionDir":                sm.GetSessionDir,
	} {
		if got, err := get(); err == nil {
			t.Errorf("%s(%q) = %q, want Node's fileURLToPath error", name, raw, got)
		}
	}
	if _, err := tools.GetShellConfig(settings); err == nil {
		t.Errorf("GetShellConfig with shellPath %q succeeded, want the fileURLToPath error", raw)
	}
}

// TestSettingsShellPathNormalizesLikeUpstream covers CFG-07: upstream
// getShellPath and getSessionDir return normalizePath(value), expanding a
// leading "~" and file:// URLs.
func TestSettingsShellPathNormalizesLikeUpstream(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, tc := range []struct{ raw, want string }{
		{"~/bin/bash", filepath.Join(home, "bin", "bash")},
		{"~", home},
		fileURLCase(),
		{"/bin/bash", "/bin/bash"},
		{"", ""},
	} {
		settings := Settings{ShellPath: tc.raw, SessionDir: tc.raw}
		sm := &SettingsManager{merged: settings}
		if got, err := sm.GetShellPath(); err != nil || got != tc.want {
			t.Errorf("SettingsManager.GetShellPath(%q) = %q, %v, want %q", tc.raw, got, err, tc.want)
		}
		if got, err := settings.GetShellPath(); err != nil || got != tc.want {
			t.Errorf("Settings.GetShellPath(%q) = %q, %v, want %q", tc.raw, got, err, tc.want)
		}
		if got, err := sm.GetSessionDir(); err != nil || got != tc.want {
			t.Errorf("GetSessionDir(%q) = %q, %v, want %q", tc.raw, got, err, tc.want)
		}
	}
}

// TestSettingsReadsUpstreamOnlyKeys covers CFG-08: defaultTools,
// modelThinkingLevels (deep-merged with the project layer) and
// retry.maxAgentDelayMs, which caps the agent retry delay.
func TestSettingsReadsUpstreamOnlyKeys(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	writeSettingsFixture(t, filepath.Join(agentDir, "settings.json"), `{
		"defaultTools": ["read", "grep"],
		"modelThinkingLevels": {"anthropic/claude-sonnet-4-5": "high", "openai/gpt-5.5": "low"},
		"retry": {"maxAgentDelayMs": 5000}
	}`)
	writeSettingsFixture(t, filepath.Join(cwd, CONFIG_DIR_NAME, "settings.json"), `{"modelThinkingLevels": {"openai/gpt-5.5": "xhigh"}}`)
	sm := NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
	if got := sm.GetDefaultTools(); len(got) != 2 || got[0] != "read" || got[1] != "grep" {
		t.Fatalf("GetDefaultTools = %v", got)
	}
	if got := sm.GetModelThinkingLevel("anthropic", "claude-sonnet-4-5"); got != "high" {
		t.Fatalf("global per-model level = %q", got)
	}
	if got := sm.GetModelThinkingLevel("openai", "gpt-5.5"); got != "xhigh" {
		t.Fatalf("project per-model level = %q, want the project override", got)
	}
	if got := sm.GetRetrySettings().MaxDelayMs; got != 5000 {
		t.Fatalf("retry MaxDelayMs = %d, want maxAgentDelayMs 5000", got)
	}
	if err := sm.SetTheme("dark"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(agentDir, "settings.json"))
	if err != nil || !strings.Contains(string(data), `"maxAgentDelayMs": 5000`) || !strings.Contains(string(data), `"defaultTools"`) {
		t.Fatalf("saved settings dropped keys: %s, %v", data, err)
	}
}

// TestSettingsReloadKeepsUndrainedErrors covers CFG-12: upstream reload()
// records new load errors without discarding ones nobody has drained yet.
func TestSettingsReloadKeepsUndrainedErrors(t *testing.T) {
	agentDir := t.TempDir()
	path := filepath.Join(agentDir, "settings.json")
	writeSettingsFixture(t, path, "{")
	sm := NewSettingsManagerWithProjectTrust(t.TempDir(), agentDir, false)
	writeSettingsFixture(t, path, `{"theme":"dark"}`)
	sm.Reload()
	errs := sm.DrainErrors()
	if len(errs) != 1 || errs[0].Scope != "global" {
		t.Fatalf("DrainErrors = %+v, want the undrained global parse error", errs)
	}
	if got := sm.GetTheme(); got != "dark" {
		t.Fatalf("theme = %q after reload", got)
	}
}

// TestAnalyticsSettings ports upstream first-time-setup.test.ts "analytics
// settings".
func TestAnalyticsSettings(t *testing.T) {
	newManager := func(t *testing.T) *SettingsManager {
		return NewSettingsManagerWithProjectTrust(t.TempDir(), t.TempDir(), false)
	}
	uuidPattern := regexp.MustCompile(`^[0-9a-f-]{36}$`)
	t.Run("defaults to disabled with no tracking identifier", func(t *testing.T) {
		manager := newManager(t)
		if manager.GetEnableAnalytics() || manager.GetTrackingID() != "" {
			t.Fatalf("analytics = %v, trackingId = %q", manager.GetEnableAnalytics(), manager.GetTrackingID())
		}
	})
	t.Run("generates a tracking identifier on opt-in", func(t *testing.T) {
		manager := newManager(t)
		if err := manager.SetEnableAnalytics(true); err != nil {
			t.Fatal(err)
		}
		if !manager.GetEnableAnalytics() || !uuidPattern.MatchString(manager.GetTrackingID()) {
			t.Fatalf("analytics = %v, trackingId = %q", manager.GetEnableAnalytics(), manager.GetTrackingID())
		}
	})
	t.Run("does not generate a tracking identifier on opt-out", func(t *testing.T) {
		manager := newManager(t)
		if err := manager.SetEnableAnalytics(false); err != nil {
			t.Fatal(err)
		}
		if manager.GetEnableAnalytics() || manager.GetTrackingID() != "" {
			t.Fatalf("analytics = %v, trackingId = %q", manager.GetEnableAnalytics(), manager.GetTrackingID())
		}
	})
	t.Run("keeps the tracking identifier when toggling analytics", func(t *testing.T) {
		manager := newManager(t)
		for _, enabled := range []bool{true, false, true} {
			if err := manager.SetEnableAnalytics(enabled); err != nil {
				t.Fatal(err)
			}
		}
		trackingID := manager.GetTrackingID()
		if err := manager.SetEnableAnalytics(false); err != nil {
			t.Fatal(err)
		}
		if err := manager.SetEnableAnalytics(true); err != nil {
			t.Fatal(err)
		}
		if trackingID == "" || manager.GetTrackingID() != trackingID {
			t.Fatalf("trackingId = %q, want %q kept", manager.GetTrackingID(), trackingID)
		}
	})
}

// TestSettingsDropsOnlyTheBadArrayElement covers CH-019: a mistyped element
// inside an array (here a package object with a numeric source) drops that
// element, not the whole settings file.
func TestSettingsDropsOnlyTheBadArrayElement(t *testing.T) {
	agentDir := t.TempDir()
	writeSettingsFixture(t, filepath.Join(agentDir, "settings.json"), `{"theme":"dark","packages":["npm:a",{"source":5},{"source":"npm:b","skills":["x"]}]}`)
	sm := NewSettingsManagerWithProjectTrust(t.TempDir(), agentDir, false)
	if errs := sm.DrainErrors(); len(errs) != 0 {
		t.Fatalf("load errors = %+v", errs)
	}
	if got := sm.GetTheme(); got != "dark" {
		t.Fatalf("theme = %q, want dark", got)
	}
	packages := sm.GetPackages()
	if len(packages) != 2 || packages[0].Source != "npm:a" || packages[1].Source != "npm:b" || len(packages[1].Skills) != 1 {
		t.Fatalf("packages = %+v, want npm:a and npm:b", packages)
	}
}
