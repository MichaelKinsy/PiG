package configschema_test

// pi: packages/coding-agent/src/core/key-id-schema.ts
// pi: packages/coding-agent/src/core/keybindings-schema.ts
// pi: packages/coding-agent/src/core/settings-defaults.ts

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/configschema"
)

// Ports packages/coding-agent/test/config-schemas.test.ts, "generated configuration schemas". The cases that Pi runs through TypeBox's
// Compile(schema).Check run here through the pinned Pi's typebox over the schemas Pig renders, so the artifact is judged by Pi's validator.

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func renderedSchemas(t *testing.T) map[string]string {
	t.Helper()
	rendered, err := configschema.Render(codingagent.KeybindingsSchemaArtifact())
	if err != nil {
		t.Fatal(err)
	}
	return rendered
}

func renderedJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(renderedSchemas(t)[path]), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func at(t *testing.T, value any, path ...string) any {
	t.Helper()
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("%v: not an object at %q", path, key)
		}
		if value, ok = object[key]; !ok {
			t.Fatalf("%v: missing %q", path, key)
		}
	}
	return value
}

func contains(t *testing.T, value any, want string, path ...string) {
	t.Helper()
	got, _ := at(t, value, path...).(string)
	if !strings.Contains(got, want) {
		t.Errorf("%v = %q, want it to contain %q", path, got, want)
	}
}

type validationCase struct {
	Schema    any   `json:"schema"`
	Documents []any `json:"documents"`
}

// check compiles each schema with Pi's typebox and returns Check() for every document.
func check(t *testing.T, cases ...validationCase) [][]bool {
	t.Helper()
	data, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "cases.json")
	if err := os.WriteFile(input, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/validate_schema.mjs", pigversion.UpstreamPackageVersion, input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var results [][]bool
	if err := json.Unmarshal(output, &results); err != nil {
		t.Fatal(err)
	}
	return results
}

func parse(t *testing.T, document string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(document), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func schemaOf(t *testing.T, path string) any { return renderedJSON(t, path) }

// "matches the four committed artifacts"
func TestCommittedSchemasMatchTheRenderedArtifacts(t *testing.T) {
	want := []string{"schemas/models.schema.json", "schemas/settings.schema.json", "schemas/keybindings.schema.json", "schemas/theme.schema.json"}
	if got := configschema.ArtifactPaths(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("artifact paths = %v, want %v", got, want)
	}
	for path, expected := range renderedSchemas(t) {
		committed, err := os.ReadFile(filepath.Join(repoRoot(t), path))
		if err != nil {
			t.Fatal(err)
		}
		if string(committed) != expected {
			t.Errorf("%s is stale; run make generate-schemas", path)
		}
	}
}

// "preserves editor guidance in the generated theme schema"
func TestThemeSchemaPreservesEditorGuidance(t *testing.T) {
	schema := renderedJSON(t, "schemas/theme.schema.json")
	if at(t, schema, "title") != "Pi Coding Agent Theme" || at(t, schema, "description") != "Theme schema for Pi coding agent" {
		t.Errorf("title or description differ: %v %v", schema["title"], schema["description"])
	}
	contains(t, schema, "use compatible fallbacks", "properties", "colors", "description")
	contains(t, schema, "falls back to muted", "properties", "colors", "properties", "scrollbarTrack", "description")
	if got := at(t, schema, "properties", "colors", "properties", "accent", "description"); got != "Primary accent color (logo, selected items, cursor)" {
		t.Errorf("accent description = %v", got)
	}
	contains(t, schema, "defaults derived from userMessageBg", "properties", "export", "description")
	accent := at(t, schema, "properties", "colors", "properties", "accent").(map[string]any)
	colorValue := any(accent)
	if _, ref := accent["$ref"]; ref {
		colorValue = at(t, schema, "$defs", "ColorValue")
	}
	anyOf := at(t, colorValue, "anyOf").([]any)
	contains(t, anyOf[0], "Hex color", "description")
	if _, ok := anyOf[1].(map[string]any); !ok {
		t.Errorf("second alternative is not an object: %v", anyOf[1])
	}
}

// "preserves moved model and settings guidance"
func TestModelAndSettingsSchemasPreserveMovedGuidance(t *testing.T) {
	models := renderedJSON(t, "schemas/models.schema.json")
	contains(t, models, "USD per million tokens", "$defs", "ModelCost", "properties", "input", "description")
	contains(t, models, "highest matching input threshold", "$defs", "ModelCost", "properties", "tiers", "description")
	contains(t, models, "serialized provider request size", "$defs", "ModelInputLimits", "properties", "maxRequestBytes", "description")
	settings := renderedJSON(t, "schemas/settings.schema.json")
	contains(t, settings, "hide all startup output", "properties", "quietStartup", "description")
	contains(t, settings, "+name and -name", "properties", "defaultTools", "description")
	contains(t, settings, "1 to 100", "properties", "fullscreenWheelScrollLines", "description")
}

// "validates representative $name documents"
func TestRepresentativeDocumentsValidateAgainstTheGeneratedSchemas(t *testing.T) {
	type pair struct{ artifact, valid, invalid string }
	pairs := []pair{
		{"schemas/models.schema.json", `{"providers":{"local":{"compat":{"zaiToolStream":true,"thinkingTokenBudgetField":"thinking_budget","chatTemplateKwargs":{"budget":{"$var":"thinking.budget"}},"supportsMidConvoSystemMessages":true},"models":[{"id":"model","inputLimits":{"maxRequestBytes":1024,"images":{"maxPerRequest":2}},"promptCache":{"short":300}}]}}}`, `{"providers":{"local":{"models":[{"id":""}]}}}`},
		{"schemas/settings.schema.json", `{"theme":"dark","cacheWarming":"idle","extensionSetting":{"enabled":true},"queueMode":"all","websockets":true,"skills":{"enableSkillCommands":true,"customDirectories":["./skills"]},"retry":{"maxDelayMs":1000}}`, `{"cacheWarming":"always"}`},
		{"schemas/keybindings.schema.json", `{"app.session.new":"ctrl+n","extension.action":["alt+x"]}`, `{"app.session.new":42}`},
	}
	var cases []validationCase
	for _, p := range pairs {
		cases = append(cases, validationCase{Schema: schemaOf(t, p.artifact), Documents: []any{parse(t, p.valid), parse(t, p.invalid)}})
	}
	for i, got := range check(t, cases...) {
		if !got[0] || got[1] {
			t.Errorf("%s: valid=%v invalid=%v, want true false", pairs[i].artifact, got[0], got[1])
		}
	}
}

// "rejects non-positive model token limits"
func TestModelsSchemaRejectsNonPositiveTokenLimits(t *testing.T) {
	var docs []any
	var names []string
	for _, field := range []string{"contextWindow", "maxTokens"} {
		for _, value := range []float64{0, -1} {
			docs = append(docs,
				map[string]any{"providers": map[string]any{"local": map[string]any{"models": []any{map[string]any{"id": "model", field: value}}}}},
				map[string]any{"providers": map[string]any{"local": map[string]any{"modelOverrides": map[string]any{"model": map[string]any{field: value}}}}})
			names = append(names, "models[]."+field, "modelOverrides."+field)
		}
	}
	valid := parse(t, `{"providers":{"local":{"models":[{"id":"model","contextWindow":1,"maxTokens":1}],"modelOverrides":{"model":{"contextWindow":1,"maxTokens":1}}}}}`)
	got := check(t, validationCase{Schema: schemaOf(t, "schemas/models.schema.json"), Documents: append(docs, valid)})[0]
	for i, name := range names {
		if got[i] {
			t.Errorf("%s accepted a non-positive value", name)
		}
	}
	if !got[len(docs)] {
		t.Error("positive token limits were rejected")
	}
}

// "validates keybinding syntax"
func TestKeybindingsSchemaValidatesKeySyntax(t *testing.T) {
	valid := []string{"a", "9", "pageUp", "+", "ctrl+shift+x", "alt+ctrl+?", "ctrl+shift+alt+super+f12"}
	invalid := []string{"", "Ctrl+x", "control+x", "ctrl+not-a-key", "ctrl+ctrl+x", "ctrl+shift+alt+super+ctrl+x"}
	var docs []any
	for _, binding := range append(append([]string(nil), valid...), invalid...) {
		docs = append(docs, map[string]any{"extension.action": binding})
	}
	got := check(t, validationCase{Schema: schemaOf(t, "schemas/keybindings.schema.json"), Documents: docs})[0]
	for i, binding := range append(append([]string(nil), valid...), invalid...) {
		if want := i < len(valid); got[i] != want {
			t.Errorf("%q: Check = %v, want %v", binding, got[i], want)
		}
	}
}

// "rejects settings values that runtime accessors reject"
func TestSettingsSchemaRejectsWhatRuntimeAccessorsReject(t *testing.T) {
	invalid := []string{
		`{"compaction":{"reserveTokens":-1}}`, `{"compaction":{"keepRecentTokens":1.5}}`,
		`{"compaction":{"modelOverrides":{"provider/model":{"reserveTokens":9007199254740992}}}}`,
		`{"httpIdleTimeoutMs":-1}`, `{"websocketConnectTimeoutMs":-1}`, `{"httpIdleTimeoutMs":"bogus"}`, `{"websocketConnectTimeoutMs":"30000"}`,
		`{"codemode":{"inlineBudget":-1}}`,
	}
	valid := `{"compaction":{"reserveTokens":0,"keepRecentTokens":9007199254740991,"modelOverrides":{"provider/model":{"reserveTokens":0}}},"httpIdleTimeoutMs":0,"websocketConnectTimeoutMs":"disabled","codemode":{"inlineBudget":0}}`
	var docs []any
	for _, d := range invalid {
		docs = append(docs, parse(t, d))
	}
	got := check(t, validationCase{Schema: schemaOf(t, "schemas/settings.schema.json"), Documents: append(docs, parse(t, valid))})[0]
	for i, d := range invalid {
		if got[i] {
			t.Errorf("%s was accepted", d)
		}
	}
	if !got[len(invalid)] {
		t.Error("the boundary settings document was rejected")
	}
}

// "describes legacy settings migrations accurately"
func TestSettingsSchemaDescribesLegacyMigrations(t *testing.T) {
	schema := renderedJSON(t, "schemas/settings.schema.json")
	contains(t, schema, "provider.maxRetryDelayMs", "properties", "retry", "properties", "maxDelayMs", "description")
	if got := at(t, schema, "properties", "queueMode", "description"); got != "Legacy setting migrated to steeringMode." {
		t.Errorf("queueMode description = %v", got)
	}
}
