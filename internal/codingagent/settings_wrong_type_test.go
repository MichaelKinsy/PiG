package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pig divergence (D158): a setting of the wrong JSON type is dropped and the rest of the file loads. Pi 1.1.0 exits 1 with
// `TypeError: entries is not iterable` for {"skills":5} and {"extensions":{}}, and `pattern.includes is not a function`
// for {"enabledModels":[5]}; for {"enabledModels":"ab"} it iterates the string and warns once per character.
func TestSettingsDropWrongTypedValuesAndKeepTheRest(t *testing.T) {
	for _, raw := range []string{
		`{"skills":5,"theme":"dark"}`,
		`{"extensions":{},"theme":"dark"}`,
		`{"enabledModels":[5],"theme":"dark"}`,
		`{"enabledModels":"ab","theme":"dark"}`,
	} {
		settings, err := parseSettingsJSON([]byte(raw))
		if err != nil {
			t.Errorf("%s: %v", raw, err)
			continue
		}
		if settings.Theme != "dark" || len(settings.Skills) != 0 || len(settings.Extensions) != 0 || len(settings.EnabledModels) != 0 {
			t.Errorf("%s: theme %q, skills %v, extensions %v, enabledModels %v; want only the theme kept", raw, settings.Theme, settings.Skills, settings.Extensions, settings.EnabledModels)
		}
	}
}

// pig divergence (D158), owner-approved addition: a setting that is ignored for its wrong JSON type is reported once per settings file, naming the
// setting and the type it expects, so the user is not left wondering why it had no effect.
func TestSettingsWarnOnceNamingTheIgnoredSettingAndItsExpectedType(t *testing.T) {
	agentDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"skills":5,"enabledModels":"ab","theme":"dark","hideThinkingBlock":"yes"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sm := NewSettingsManager(t.TempDir(), agentDir)
	errs := sm.DrainErrors()
	if len(errs) != 1 {
		t.Fatalf("%d settings warnings, want one: %v", len(errs), errs)
	}
	message := errs[0].Error.Error()
	for _, want := range []string{`"skills"`, "an array of strings", `"enabledModels"`, `"hideThinkingBlock"`, "a boolean"} {
		if !strings.Contains(message, want) {
			t.Errorf("warning %q does not contain %q", message, want)
		}
	}
	if errs[0].Scope != "global" || !strings.HasSuffix(errs[0].Path, "settings.json") {
		t.Errorf("warning scope %q path %q", errs[0].Scope, errs[0].Path)
	}
	if got := sm.GetTheme(); got != "dark" {
		t.Errorf("theme = %q: the rest of the file must still load", got)
	}

	clean := t.TempDir()
	if err := os.WriteFile(filepath.Join(clean, "settings.json"), []byte(`{"theme":"dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if errs := NewSettingsManager(t.TempDir(), clean).DrainErrors(); len(errs) != 0 {
		t.Fatalf("a well-typed file warned: %v", errs)
	}
}

// A trusted project's settings file warns the same way, both when the manager loads it at construction and when SetProjectTrusted loads it after the
// startup trust prompt.
func TestSettingsWarnOnceForAProjectFileOfTheWrongType(t *testing.T) {
	cwd := t.TempDir()
	projectPath := filepath.Join(ProjectConfigDir(cwd), "settings.json")
	if err := os.MkdirAll(filepath.Dir(projectPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPath, []byte(`{"skills":5,"theme":"dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	check := func(t *testing.T, sm *SettingsManager) {
		t.Helper()
		errs := sm.DrainErrors()
		if len(errs) != 1 || errs[0].Scope != "project" || errs[0].Path != projectPath || !strings.Contains(errs[0].Error.Error(), `"skills" (expected an array of strings)`) {
			t.Fatalf("project warnings = %v, want one naming \"skills\" for %s", errs, projectPath)
		}
		if got := sm.GetTheme(); got != "dark" {
			t.Errorf("theme = %q: the rest of the project file must still load", got)
		}
	}
	t.Run("trusted at construction", func(t *testing.T) {
		check(t, NewSettingsManagerWithOptions(cwd, t.TempDir(), SettingsManagerCreateOptions{ProjectTrusted: new(true)}))
	})
	t.Run("trusted after the prompt", func(t *testing.T) {
		sm := NewSettingsManagerWithOptions(cwd, t.TempDir(), SettingsManagerCreateOptions{ProjectTrusted: new(false)})
		if errs := sm.DrainErrors(); len(errs) != 0 {
			t.Fatalf("an untrusted project warned: %v", errs)
		}
		sm.SetProjectTrusted(true)
		check(t, sm)
	})
}
