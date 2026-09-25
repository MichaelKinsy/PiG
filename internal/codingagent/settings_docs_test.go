package codingagent

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The settings page is a table of keys the wire struct already declares. A key
// added without a row is a key no reader can discover, and a row for a key that
// was removed sends a reader to edit a file Pig ignores.
//
// This lives in this package because settingsWire is unexported.

const settingsDocPath = "../../internal/pigdocs/content/settings.md"

var settingsRowRE = regexp.MustCompile("(?m)^\\| `([a-zA-Z]+)` \\|")

func declaredSettingKeys() []string {
	var keys []string
	t := reflect.TypeFor[settingsWire]()
	for field := range t.Fields() {
		tag := field.Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name != "" && name != "-" {
			keys = append(keys, name)
		}
	}
	slices.Sort(keys)
	return keys
}

func documentedSettingKeys(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(settingsDocPath))
	if err != nil {
		t.Fatalf("read the settings page: %v", err)
	}
	var keys []string
	for _, m := range settingsRowRE.FindAllStringSubmatch(string(data), -1) {
		if !slices.Contains(keys, m[1]) {
			keys = append(keys, m[1])
		}
	}
	slices.Sort(keys)
	return keys
}

func TestEverySettingKeyIsDocumented(t *testing.T) {
	documented := documentedSettingKeys(t)
	for _, key := range declaredSettingKeys() {
		if !slices.Contains(documented, key) {
			t.Errorf("setting %q has no row on the settings page, so no reader can find it", key)
		}
	}
}

func TestNoDocumentedSettingKeyHasBeenRemoved(t *testing.T) {
	declared := declaredSettingKeys()
	for _, key := range documentedSettingKeys(t) {
		if !slices.Contains(declared, key) {
			t.Errorf("the settings page documents %q, which Pig no longer reads", key)
		}
	}
}

// The page tells a reader that an untrusted project contributes nothing. That is
// a security claim, so bind it to the loader rather than leaving it as prose.
func TestAnUntrustedProjectContributesNoSettings(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "project")
	if err := os.MkdirAll(filepath.Join(project, CONFIG_DIR_NAME), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(project, CONFIG_DIR_NAME, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"shellCommandPrefix":"attacker-controlled"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	sm := NewSettingsManagerWithProjectTrust(project, filepath.Join(dir, "agent"), false)
	if got := sm.Get().CommandPrefix; got != "" {
		t.Fatalf("an untrusted project set the shell command prefix to %q; opening a repository must not change what Pig runs", got)
	}

	sm.SetProjectTrusted(true)
	if got := sm.Get().CommandPrefix; got != "attacker-controlled" {
		t.Fatalf("a trusted project's setting was ignored: prefix = %q", got)
	}
}
