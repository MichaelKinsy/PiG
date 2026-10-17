package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/configschema"
)

// Ports the runtime cases of packages/coding-agent/test/config-schemas.test.ts (Pi 1.2.0) "generated configuration schemas" that load a
// models.json, settings.json or keybindings.json carrying `$schema` (the theme case is in tui/theme_schema_runtime_upstream_test.go).
// Pi's ModelConfig.load is ModelRegistry's load here, and its getError() is LoadError().

const configSchemaBaseURL = "https://pi.dev/schemas"

func configSchemaURL(name string) string { return configSchemaBaseURL + "/" + name + ".schema.json" }

func writeConfigSchemaFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// "accepts custom compatibility settings and rejects malformed known fields"
func TestConfigSchemasAcceptCustomCompatibilitySettingsAndRejectMalformedKnownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	compat := `{"customOption":"value","supportsStore":true,"supportsAdditionalTools":true}`
	writeConfigSchemaFile(t, path, `{"providers":{"demo":{"api":"custom-api","compat":`+compat+`}}}`)
	registry := NewModelRegistryWithModelsPath(path)
	if got := registry.LoadError(); got != "" {
		t.Fatalf("custom compat settings: LoadError = %q, want none", got)
	}
	// config.getProvider("demo")?.compat toEqual(compat): the loaded provider keeps the compat object as written, customOption included.
	registry.mu.RLock()
	demo, ok := registry.config.Providers["demo"]
	registry.mu.RUnlock()
	if !ok || demo.Compat == nil {
		t.Fatalf("provider demo = %+v (present %v), want its compat", demo, ok)
	}
	encoded, err := json.Marshal(ai.OpenAICompat(*demo.Compat))
	if err != nil {
		t.Fatal(err)
	}
	var got, want map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(compat), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("provider demo compat = %v, want %v", got, want)
	}

	writeConfigSchemaFile(t, path, `{"providers":{"demo":{"compat":{"supportsLongCacheRetention":"yes"}}}}`)
	if got := NewModelRegistryWithModelsPath(path).LoadError(); !strings.Contains(got, "Invalid models.json schema") {
		t.Fatalf("malformed known compat field: LoadError = %q, want it to contain %q", got, "Invalid models.json schema")
	}
}

// "validates $schema in models.json at runtime"
func TestConfigSchemasValidateSchemaInModelsJSONAtRuntime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	writeConfigSchemaFile(t, path, `{"$schema":"`+configSchemaURL("models")+`","providers":{"demo":{"baseUrl":"http://localhost:8080/v1","models":[{"id":"demo"}]}}}`)
	// Pi's ModelConfig.load validates the document and keeps every provider; composing a provider without an api fails later, in the
	// runtime. Pig's registry load does both, so the ModelConfig layer is the schema check and the decoded config that readConfig runs
	// before it drops providers that cannot compose.
	if got := NewModelRegistryWithModelsPath(path).LoadError(); strings.Contains(got, "Invalid models.json schema") || strings.Contains(got, "$schema") {
		t.Fatalf("LoadError = %q, want no schema error", got)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if errs, err := configschema.ModelsConfigSchema().Errors(content); err != nil || len(errs) != 0 {
		t.Fatalf("schema errors = %v, %v; want none", errs, err)
	}
	var config modelsConfig
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	if demo := config.Providers["demo"]; len(demo.Models) == 0 || demo.Models[0].ID != "demo" {
		t.Fatalf("provider demo = %+v, want its first model demo", demo)
	}

	writeConfigSchemaFile(t, path, `{"$schema":"`+configSchemaURL("models")+`","providers":{"demo":{"models":[{"id":""}]}}}`)
	if got := NewModelRegistryWithModelsPath(path).LoadError(); !strings.Contains(got, "Invalid models.json schema") {
		t.Fatalf("empty model id: LoadError = %q, want it to contain %q", got, "Invalid models.json schema")
	}

	writeConfigSchemaFile(t, path, `{"$schema":42,"providers":{}}`)
	if got := NewModelRegistryWithModelsPath(path).LoadError(); !strings.Contains(got, "$schema") {
		t.Fatalf("$schema 42: LoadError = %q, want it to contain %q", got, "$schema")
	}
}

// "accepts $schema in settings.json and preserves unknown settings"
func TestConfigSchemasAcceptSchemaInSettingsJSON(t *testing.T) {
	dir := t.TempDir()
	writeConfigSchemaFile(t, filepath.Join(dir, "settings.json"), `{"$schema":"`+configSchemaURL("settings")+`","theme":"light","extensionSetting":{"enabled":true}}`)

	manager := NewSettingsManager(dir, dir)
	if got := manager.GetTheme(); got != "light" {
		t.Fatalf("GetTheme = %q, want light", got)
	}
	// getGlobalSettings() toMatchObject({ $schema, extensionSetting: { enabled: true } }): Settings' JSON form is Pi's settings object.
	encoded, err := json.Marshal(manager.GetGlobalSettings())
	if err != nil {
		t.Fatal(err)
	}
	var global map[string]any
	if err := json.Unmarshal(encoded, &global); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]any{"$schema": configSchemaURL("settings"), "extensionSetting": map[string]any{"enabled": true}} {
		if got := global[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("getGlobalSettings()[%q] = %#v, want %#v", name, got, want)
		}
	}
}

// "accepts $schema in keybindings.json without treating it as an action"
func TestConfigSchemasAcceptSchemaInKeybindingsJSONWithoutTreatingItAsAnAction(t *testing.T) {
	dir := t.TempDir()
	writeConfigSchemaFile(t, filepath.Join(dir, "keybindings.json"), `{"$schema":"`+configSchemaURL("keybindings")+`","app.session.new":"ctrl+n","extension.invalid":"ctrl+not-a-key"}`)

	manager := NewKeybindingsManager(dir)
	if got, want := manager.GetUserBindings(), map[string][]KeyID{"app.session.new": {"ctrl+n"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("GetUserBindings = %v, want %v", got, want)
	}
	if got := manager.GetEffectiveConfig()["app.session.new"]; got.Key != "ctrl+n" || got.Keys != nil {
		t.Fatalf("effective app.session.new = %+v, want ctrl+n", got)
	}
}
