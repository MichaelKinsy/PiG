package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Pi keeps every member of settings.json and of a models.json compat object, modeled or not. These tests drive the members PiG does not
// model through the callers that read them after a load: the settings layers merged by deepMergeSettings (settings-manager.ts:63-80,141-145),
// a save that patches only modified fields (settings-manager.ts:700-732), migrateSettings (settings-manager.ts:395-454), and a models.json
// provider composed by mergeCompat (provider-composer.ts:122-143,247).

func settingsJSONObject(t *testing.T, settings Settings) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	return object
}

func TestUnmodeledSettingsMergeProjectOverGlobalAsDeepMergeObjectsDoes(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	writeConfigSchemaFile(t, filepath.Join(agentDir, "settings.json"), `{"extensionSetting":{"enabled":true,"mode":"a"},"other":1,"globalOnly":"g"}`)
	if err := os.MkdirAll(ProjectConfigDir(cwd), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfigSchemaFile(t, filepath.Join(ProjectConfigDir(cwd), "settings.json"), `{"extensionSetting":{"mode":"b"},"other":[2]}`)

	got := settingsJSONObject(t, NewSettingsManager(cwd, agentDir).Get())
	for name, want := range map[string]any{
		"extensionSetting": map[string]any{"enabled": true, "mode": "b"},
		"other":            []any{float64(2)},
		"globalOnly":       "g",
	} {
		if !reflect.DeepEqual(got[name], want) {
			t.Errorf("getSettings()[%q] = %#v, want %#v", name, got[name], want)
		}
	}
}

func TestUnmodeledSettingsSurviveASave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeConfigSchemaFile(t, path, `{"$schema":"`+configSchemaURL("settings")+`","theme":"dark","extensionSetting":{"enabled":true}}`)
	manager := NewSettingsManager(dir, dir)
	if err := manager.SetTheme("light"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]any
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"$schema": configSchemaURL("settings"), "theme": "light", "extensionSetting": map[string]any{"enabled": true}}
	if !reflect.DeepEqual(file, want) {
		t.Fatalf("settings.json after setTheme = %s, want %v", data, want)
	}
	if got := settingsJSONObject(t, manager.GetGlobalSettings()); !reflect.DeepEqual(got, want) {
		t.Fatalf("getGlobalSettings() after setTheme = %v, want %v", got, want)
	}
}

func TestUnmodeledSettingsLeaveOutOnlyTheLegacyMembersMigrateSettingsRewrote(t *testing.T) {
	for _, tc := range []struct {
		name, file string
		want       map[string]any
	}{
		{"queueMode migrated", `{"queueMode":"all"}`, map[string]any{"steeringMode": "all"}},
		{"queueMode beside steeringMode", `{"queueMode":"all","steeringMode":"one-at-a-time"}`, map[string]any{"queueMode": "all", "steeringMode": "one-at-a-time"}},
		{"websockets migrated", `{"websockets":true}`, map[string]any{"transport": "websocket"}},
		{"websockets beside transport", `{"websockets":false,"transport":"auto"}`, map[string]any{"websockets": false, "transport": "auto"}},
		{"websockets not a boolean", `{"websockets":"yes"}`, map[string]any{"websockets": "yes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfigSchemaFile(t, filepath.Join(dir, "settings.json"), tc.file)
			if got := settingsJSONObject(t, NewSettingsManager(dir, dir).GetGlobalSettings()); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("getGlobalSettings() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUnmodeledCompatMembersReachTheComposedModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	writeConfigSchemaFile(t, path, `{"providers":{"local":{"baseUrl":"http://localhost:8080/v1","api":"openai-completions","apiKey":"key",`+
		`"compat":{"customOption":"provider","shared":"provider","supportsStore":false},`+
		`"models":[{"id":"m","compat":{"shared":"model","nested":{"a":1}}}]}}}`)
	registry := NewModelRegistryWithModelsPath(path)
	if got := registry.LoadError(); got != "" {
		t.Fatalf("LoadError = %q, want none", got)
	}
	models := registry.GetProviderModelData("local")
	if len(models) != 1 || models[0].ID != "m" || models[0].ProviderMeta.Compat == nil {
		t.Fatalf("provider local models = %+v, want model m with compat", models)
	}
	encoded, err := json.Marshal(models[0].ProviderMeta.Compat)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	// { ...providerConfig.compat, ...definition.compat }
	want := map[string]any{"customOption": "provider", "shared": "model", "supportsStore": false, "nested": map[string]any{"a": float64(1)}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("model m compat = %s, want %v", encoded, want)
	}
}
