package codingagent

import (
	"encoding/json"
	"strings"
	"testing"
)

// Pi writes an explicit [] or {} back as it read it; only an undefined setting is left out.
func TestSettingsKeepExplicitEmptyArrayAndMap(t *testing.T) {
	for name, value := range map[string]any{
		"Settings":     Settings{SelfUpdateCommand: []string{}, ExtensionChangelogVersions: map[string]string{}},
		"settingsWire": settingsWire{ExtensionChangelogVersions: map[string]string{}},
	} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if name == "Settings" && !strings.Contains(string(data), `"selfUpdateCommand":[]`) {
			t.Errorf("%s dropped the empty selfUpdateCommand: %s", name, data)
		}
		if !strings.Contains(string(data), `"extensionChangelogVersions":{}`) {
			t.Errorf("%s dropped the empty extensionChangelogVersions: %s", name, data)
		}
	}
	data, err := json.Marshal(Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "selfUpdateCommand") || strings.Contains(string(data), "extensionChangelogVersions") {
		t.Errorf("an unset setting was written: %s", data)
	}
}
