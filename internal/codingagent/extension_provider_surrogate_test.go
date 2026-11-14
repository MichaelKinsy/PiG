package codingagent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// pi: provider-composer.ts extensionModelFromDefinition spreads the model definition an extension passed to registerProvider into the model it
// registers: the object the extension built, so a name or a compat value with a lone UTF-16 surrogate is that string in the model. An SDK extension
// sends the configuration as JSON, where the unit is its escape: the registration keeps the unit in the model's name and compat.
func TestExtensionProviderRegistrationKeepsALoneSurrogate(t *testing.T) {
	const unit = "\xed\xa0\xbd"
	var config extension.ProviderConfig
	if err := json.Unmarshal([]byte(`{"baseUrl":"http://127.0.0.1:9/v1","apiKey":"k","api":"openai-completions","models":[{"id":"xm","name":"XN[\ud83d]","reasoning":false,"input":["text"],`+
		`"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096,"compat":{"openRouterRouting":{"sort":"XS[\ud83d]"}}}]}`), &config); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistryWithModelsPath(filepath.Join(t.TempDir(), "models.json"))
	if err := registry.RegisterExtensionProvider("extp", config); err != nil {
		t.Fatal(err)
	}
	model := registry.GetProviderModelDataByID("extp", "xm")
	if model == nil {
		t.Fatal("the registered model is not in the registry")
	}
	if want := "XN[" + unit + "]"; model.DisplayName != want {
		t.Errorf("name = %q, want %q", model.DisplayName, want)
	}
	compat := model.ProviderMeta.Compat
	if compat == nil {
		t.Fatal("the model has no compat")
	}
	if routing := fmt.Sprint(compat.OpenRouterRouting); !strings.Contains(routing, "XS["+unit+"]") {
		t.Errorf("openRouterRouting = %q, want its sort to hold the unit", routing)
	}
}
