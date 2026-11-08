package models

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

func loadConfig(t *testing.T, content string) *ModelConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadModelConfig(path)
}

func TestModelConfigLoad(t *testing.T) {
	if config := LoadModelConfig(filepath.Join(t.TempDir(), "missing.json")); config.Error() != "" || len(config.ProviderIDs()) != 0 {
		t.Fatalf("a missing file is empty: %q", config.Error())
	}
	config := loadConfig(t, "\uFEFF// comment\n{\"providers\":{\"b\":{\"baseUrl\":\"https://b\"},\"a\":{\"headers\":{\"X\":\"1\"}}}}")
	if config.Error() != "" || strings.Join(config.ProviderIDs(), ",") != "b,a" {
		t.Fatalf("providers = %v, error %q", config.ProviderIDs(), config.Error())
	}
	if config.Provider("b") == nil || config.Provider("nope") != nil {
		t.Fatal("provider lookup")
	}
	if config := loadConfig(t, `{`); !strings.HasPrefix(config.Error(), "Invalid models.json syntax. File: ") {
		t.Fatalf("syntax error: %q", config.Error())
	}
	config = loadConfig(t, `{"providers":{"p":{"models":[{"id":""}],"authHeader":1}}}`)
	for _, want := range []string{"Invalid models.json schema:\n", "  - providers.p.models.0.id: ", "  - providers.p.authHeader: ", "\n\nFile: "} {
		if !strings.Contains(config.Error(), want) {
			t.Errorf("schema error lacks %q: %q", want, config.Error())
		}
	}
	if config := loadConfig(t, `[]`); !strings.Contains(config.Error(), "  - root: ") {
		t.Fatalf("root error: %q", config.Error())
	}
}

func composed(t *testing.T, content string, base *ai.Models) *ai.Models {
	t.Helper()
	config := loadConfig(t, content)
	if config.Error() != "" {
		t.Fatal(config.Error())
	}
	if base == nil {
		base = ai.CreateModels(ai.CreateModelsOptions{})
	}
	if err := ApplyModelConfig(base, config); err != nil {
		t.Fatal(err)
	}
	return base
}

func TestApplyModelConfigDefinesCustomProvider(t *testing.T) {
	t.Setenv("P2_TEST_PROVIDER_KEY", "sk-test")
	models := composed(t, `{"providers":{"local":{
		"name":"Local","baseUrl":"https://llm.example/v1","api":"openai-completions","apiKey":"$P2_TEST_PROVIDER_KEY",
		"headers":{"X-Provider":"p"},"authHeader":true,
		"models":[{"id":"small","name":"Small","contextWindow":4096,"headers":{"X-Model":"$P2_TEST_PROVIDER_KEY"}},{"id":"big","reasoning":true,"maxTokens":2000}],
		"modelOverrides":{"small":{"name":"Smaller","headers":{"X-Override":"o"}}}}}}`, nil)
	provider := models.GetProvider("local")
	if provider == nil || provider.Name != "Local" {
		t.Fatalf("provider = %+v", provider)
	}
	listed := models.GetModels("local")
	if len(listed) != 2 || listed[0].ID != "small" || listed[0].DisplayName != "Smaller" || listed[1].DisplayName != "big" {
		t.Fatalf("models = %+v", listed)
	}
	if listed[0].Capabilities.ContextWindow != 4096 || listed[1].Capabilities.MaxOutputTokens != 2000 || listed[1].Capabilities.ContextWindow != 128000 {
		t.Fatalf("defaults: %+v %+v", listed[0].Capabilities, listed[1].Capabilities)
	}
	if listed[0].ProviderMeta.BaseURL != "https://llm.example/v1" || listed[0].ProviderMeta.API != "openai-completions" {
		t.Fatalf("meta = %+v", listed[0].ProviderMeta)
	}
	// The key is configured, so auth resolves without a credential; headers resolve against the environment.
	auth, err := models.GetAuth(context.Background(), "local")
	if err != nil || auth == nil || auth.Auth.APIKey != "sk-test" || auth.Source != "configured API key" {
		t.Fatalf("auth = %+v, %v", auth, err)
	}
	headers := map[string]string{}
	for name, value := range auth.Auth.Headers {
		headers[name] = *value
	}
	if headers["X-Provider"] != "p" || headers["Authorization"] != "Bearer sk-test" {
		t.Fatalf("provider headers = %v", headers)
	}
	modelAuth, err := models.GetModelAuth(context.Background(), listed[0])
	if err != nil || modelAuth == nil {
		t.Fatalf("model auth = %+v, %v", modelAuth, err)
	}
	headers = map[string]string{}
	for name, value := range modelAuth.Auth.Headers {
		headers[name] = *value
	}
	if headers["X-Model"] != "sk-test" || headers["X-Override"] != "o" || headers["X-Provider"] != "p" {
		t.Fatalf("model headers = %v", headers)
	}
}

func TestApplyModelConfigRefusesWhatCannotBeBuilt(t *testing.T) {
	for content, want := range map[string]string{
		`{"providers":{"p":{"models":[{"id":"m"}]}}}`:                                                                    `Provider p, model m: no "api" specified. Set at provider or model level.`,
		`{"providers":{"p":{"api":"openai-completions","models":[{"id":"m"}]}}}`:                                         `Provider p: "baseUrl" is required when defining custom models.`,
		`{"providers":{"p":{"api":"openai-completions","baseUrl":"https://x","models":[{"id":"m","contextWindow":0}]}}}`: "Provider p, model m: invalid contextWindow",
		`{"providers":{"p":{"api":"openai-completions","baseUrl":"https://x","models":[{"id":"m","maxTokens":-1}]}}}`:    "Provider p, model m: invalid maxTokens",
		`{"providers":{"p":{"api":"nonsense","baseUrl":"https://x","models":[{"id":"m"}]}}}`:                             "Unsupported custom model API",
		`{"providers":{"p":{"oauth":"radius","baseUrl":"https://x"}}}`:                                                   "Custom OAuth catalogs are not supported; use a built-in OAuth provider",
		`{"providers":{"p":{"name":"only a name"}}}`:                                                                     `Provider p: must specify "baseUrl", "headers", "compat", "modelOverrides", or "models".`,
	} {
		err := ApplyModelConfig(ai.CreateModels(ai.CreateModelsOptions{}), loadConfig(t, content))
		if err == nil || err.Error() != want {
			t.Errorf("%s:\n got %v\nwant %s", content, err, want)
		}
	}
}

func TestApplyModelConfigOverridesBuiltInProvider(t *testing.T) {
	base := CreateP2Models(true, ai.CreateModelsOptions{})
	before := base.GetModels("faux")
	if len(before) == 0 {
		t.Fatal("no faux models")
	}
	id := before[0].ID
	models := composed(t, `{"providers":{"faux":{"modelOverrides":{"`+id+`":{"name":"Renamed","contextWindow":777,"cost":{"input":3},"compat":{"supportsStore":false}}}}}}`, base)
	after := models.GetModels("faux")
	if len(after) != len(before) || after[0].DisplayName != "Renamed" || after[0].Capabilities.ContextWindow != 777 || after[0].Capabilities.InputCostPer1M != 3 {
		t.Fatalf("override: %+v", after[0])
	}
	if models.GetProvider("faux").Name != base.GetProvider("faux").Name {
		t.Fatal("the provider keeps its name")
	}
}

func TestMergeCompatAndInputLimits(t *testing.T) {
	parse := func(text string) *object {
		value, err := chordjson.Decode([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		return value.(*object)
	}
	merged, _ := chordjson.Marshal(mergeCompat(
		parse(`{"supportsStore":true,"openRouterRouting":{"only":["a"],"zdr":true}}`),
		parse(`{"supportsStore":false,"openRouterRouting":{"only":["b"]},"vercelGatewayRouting":{"order":["x"]}}`),
	))
	if string(merged) != `{"supportsStore":false,"openRouterRouting":{"only":["b"],"zdr":true},"vercelGatewayRouting":{"order":["x"]}}` {
		t.Fatalf("compat = %s", merged)
	}
	if mergeCompat(nil, nil) != nil {
		t.Fatal("no compat stays absent")
	}
	limits, _ := chordjson.Marshal(mergeInputLimits(
		parse(`{"maxRequestBytes":1,"images":{"maxPerRequest":9,"resize":{"maxWidth":10,"maxHeight":20}}}`),
		parse(`{"images":{"maxPerMessage":3,"resize":{"maxWidth":5}}}`),
	))
	if string(limits) != `{"maxRequestBytes":1,"images":{"maxPerRequest":9,"resize":{"maxWidth":5,"maxHeight":20},"maxPerMessage":3}}` {
		t.Fatalf("input limits = %s", limits)
	}
}
