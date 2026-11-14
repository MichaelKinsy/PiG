package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pi: packages/coding-agent/src/core/model-registry.ts reads models.json with JSON.parse, which keeps a lone UTF-16 surrogate of a string. A
// model whose id holds one is selected by that id, and its name and compat hold the unit too: the request names the model and carries the
// compat as Pi's does (parity scenario tools/35 compares the bytes of both CLIs).
func TestModelsJSONKeepsALoneSurrogate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	content := `{"providers":{"local":{"api":"openai-completions","baseUrl":"http://127.0.0.1:9/v1","apiKey":"k","models":[` +
		`{"id":"MX[\ud83d]","name":"NM[\ud83d]","reasoning":false,"input":["text"],"compat":{"openRouterRouting":{"sort":"RS[\ud83d]"},"X[\ud83d]":"V[\ud83d]"}}]},` +
		`"PV[\ud83d]":{"api":"openai-completions","baseUrl":"http://127.0.0.1:9/v1","apiKey":"k","compat":{"openRouterRouting":{"sort":"PS[\ud83d]"}},"models":[{"id":"m2","reasoning":false,"input":["text"]}]}}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	const unit = "\xed\xa0\xbd"
	registry := NewModelRegistryWithModelsPath(path)
	model := registry.GetProviderModelDataByID("local", "MX["+unit+"]")
	if model == nil {
		var ids []string
		for _, listed := range registry.GetProviderModelData("local") {
			ids = append(ids, fmt.Sprintf("%q", listed.ID))
		}
		t.Fatalf("no model with the id models.json gives; the provider lists %s", strings.Join(ids, ", "))
	}
	if want := "NM[" + unit + "]"; model.DisplayName != want {
		t.Errorf("name = %q, want %q", model.DisplayName, want)
	}
	compat := model.ProviderMeta.Compat
	if compat == nil {
		t.Fatal("the model has no compat")
	}
	if routing := fmt.Sprint(compat.OpenRouterRouting); !strings.Contains(routing, "RS["+unit+"]") {
		t.Errorf("openRouterRouting = %q, want its sort to hold the unit", routing)
	}
	// A provider is named by the member name models.json gives it, and its compat reaches its models.
	second := registry.GetProviderModelDataByID("PV["+unit+"]", "m2")
	if second == nil {
		t.Fatal("no model under the provider name models.json gives")
	}
	if second.ProviderMeta.Compat == nil || !strings.Contains(fmt.Sprint(second.ProviderMeta.Compat.OpenRouterRouting), "PS["+unit+"]") {
		t.Errorf("the provider's compat = %+v, want its sort to hold the unit", second.ProviderMeta.Compat)
	}
	extra, ok := compat.Extra["X["+unit+"]"]
	if !ok || string(extra) != `"V[\ud83d]"` {
		t.Errorf("the unmodeled member = %s (present %v), want the member named with the unit and its value as models.json writes it; members: %v", extra, ok, compat.Extra)
	}
}

// BenchmarkModelsJSONLoad loads a models.json of forty models and finds the last one: the decodes that keep a lone surrogate scan the
// document for an escape and read it as before when there is none.
func BenchmarkModelsJSONLoad(b *testing.B) {
	models := make([]string, 0, 40)
	for i := range 40 {
		models = append(models, fmt.Sprintf(`{"id":"model-%d","name":"Model %d","reasoning":false,"input":["text"],"contextWindow":128000,"maxTokens":4096,"compat":{"supportsDeveloperRole":false}}`, i, i))
	}
	path := filepath.Join(b.TempDir(), "models.json")
	content := `{"providers":{"local":{"api":"openai-completions","baseUrl":"http://127.0.0.1:9/v1","apiKey":"k","models":[` + strings.Join(models, ",") + `]}}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if NewModelRegistryWithModelsPath(path).GetProviderModelDataByID("local", "model-39") == nil {
			b.Fatal("the model was not loaded")
		}
	}
}
