package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

// settings-manager.ts:715-732 persists `{ ...currentFileSettings }` with the modified fields assigned onto it, then
// JSON.stringify(merged, null, 2): the file's keys keep their order, at the top level and inside a patched object.
func TestSaveSettingsPatchKeepsTheFileKeyOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	const file = `{"zeta":1,"defaultProvider":"x","alpha":{"z":1,"a":2},"mid":true}`
	if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	after := Settings{Theme: "dark"}
	if err := saveSettingsPatch(path, Settings{}, after); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const want = "{\n  \"zeta\": 1,\n  \"defaultProvider\": \"x\",\n  \"alpha\": {\n    \"z\": 1,\n    \"a\": 2\n  },\n  \"mid\": true,\n  \"theme\": \"dark\"\n}"
	if string(got) != want {
		t.Fatalf("settings.json = %q\nwant %q", got, want)
	}
}

// A replaced key keeps its position, as assigning to an existing property of a JS object does.
func TestSaveSettingsPatchReplacesInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"theme":"light","zeta":1,"defaultProvider":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveSettingsPatch(path, Settings{Theme: "light"}, Settings{Theme: "dark"}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	const want = "{\n  \"theme\": \"dark\",\n  \"zeta\": 1,\n  \"defaultProvider\": \"x\"\n}"
	if string(got) != want {
		t.Fatalf("settings.json = %q\nwant %q", got, want)
	}
}

// A patched nested object keeps the file's member order: settings-manager.ts:722-726 spreads the file's nested object
// and assigns each modified nested key onto it.
func TestSaveSettingsPatchKeepsNestedKeyOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"retry":{"zeta":1,"maxRetries":2,"enabled":true},"theme":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	enabled, two, five := true, 2, 5
	before := Settings{Retry: &RetrySettingsJSON{Enabled: &enabled, MaxRetries: &two}}
	after := Settings{Retry: &RetrySettingsJSON{Enabled: &enabled, MaxRetries: &five}}
	if err := saveSettingsPatch(path, before, after); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	const want = "{\n  \"retry\": {\n    \"zeta\": 1,\n    \"maxRetries\": 5,\n    \"enabled\": true\n  },\n  \"theme\": \"x\"\n}"
	if string(got) != want {
		t.Fatalf("settings.json = %q\nwant %q", got, want)
	}
}

// JSON.stringify(mergedSettings, null, 2) (settings-manager.ts:731) re-serializes the whole parsed file, untouched
// members included: <, > and & stay unescaped, numbers and strings take JSON.stringify's form, and array-index keys
// come first. Node: JSON.stringify({...JSON.parse(file), shellCommandPrefix: "source x && "}, null, 2).
func TestSaveSettingsPatchWritesJSONStringifyText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"zeta":"a<b>","n":1.0,"e":"\u00e9","7":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveSettingsPatch(path, Settings{}, Settings{CommandPrefix: "source x && "}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	const want = "{\n  \"7\": true,\n  \"zeta\": \"a<b>\",\n  \"n\": 1,\n  \"e\": \"é\",\n  \"shellCommandPrefix\": \"source x && \"\n}"
	if string(got) != want {
		t.Fatalf("settings.json = %q\nwant %q", got, want)
	}
}
