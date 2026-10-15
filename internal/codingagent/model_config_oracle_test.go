package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// model-config.ts ModelConfig.load: the text a models.json that fails the schema reports (typebox's localized errors,
// formatted by formatValidationPath), against the pinned Pi. Documents are curated mistakes plus seeded corruptions of
// a valid document; a document Pi accepts must load in Pig too.
func TestModelsJSONSchemaErrorsMatchPi(t *testing.T) {
	base := `{"providers":{"custom":{"name":"Custom","baseUrl":"https://example.test/v1","apiKey":"k","api":"openai-completions","headers":{"x-a":"1"},"compat":{"supportsStore":true,"maxTokensField":"max_tokens","openRouterRouting":{"order":["a"],"sort":"price"}},"authHeader":true,"models":[{"id":"m1","name":"M1","reasoning":true,"thinkingLevelMap":{"off":null,"high":"hi"},"input":["text","image"],"cost":{"input":1,"output":2,"cacheRead":0.5,"cacheWrite":0.1,"tiers":[{"inputTokensAbove":1000,"input":1,"output":2,"cacheRead":0,"cacheWrite":0}]},"promptCache":{"short":5,"long":60},"contextWindow":128000,"maxTokens":4096,"inputLimits":{"maxRequestBytes":100,"images":{"resize":{"maxWidth":10,"jpegQuality":80},"maxPerMessage":2}},"samplingParams":{"temperature":0.2},"samplingParamsByThinkingLevel":{"high":{"top_p":1}},"headers":{"h":"v"},"compat":{"supportsDeveloperRole":false}}],"modelOverrides":{"m1":{"name":"N","cost":{"input":3},"compat":{"supportsStrictMode":true}}}}}}`
	docs := []string{
		base, `{}`, `[]`, `5`, `null`, `"s"`, `{"providers":5}`, `{"providers":null}`, `{"providers":[]}`, `{"providers":{"x":5}}`, `{"providers":{"x":null}}`, `{"providers":{"x":[]}}`,
		`{"providers":{"x":{"baseUrl":5,"apiKey":7,"models":[{},{"id":3}]}}}`, `{"providers":{"custom":{"models":[{}]}}}`, `{"providers":{"x":{"models":[{"id":"","name":""}]}}}`,
		`{"providers":{"x":{"compat":5}}}`, `{"providers":{"x":{"compat":{"supportsStore":"yes"}}}}`, `{"providers":{"x":{"models":[{"id":"a","input":["audio"]}]}}}`,
		`{"providers":{"x":{"models":[{"id":"a","cost":{"input":1}}]}}}`, `{"providers":{"x":{"oauth":"other"}}}`, `{"providers":{"x":{"models":"nope"}}}`, `{"providers":{"x":{"headers":{"a":1}}}}`,
		`{"providers":{"x":{"models":[{"id":"a","inputLimits":{"images":{"resize":{"jpegQuality":101}}}}]}}}`, `{"providers":{"x":{"models":[{"id":"a","promptCache":{"short":0}}]}}}`,
		// JSON.parse reads an integer written with a fraction or exponent as that integer; ModelDefinitionSchema's Type.Number() accepts it.
		strings.Replace(strings.Replace(base, `"contextWindow":128000`, `"contextWindow":128000.0`, 1), `"maxTokens":4096`, `"maxTokens":4.096e3`, 1),
		`{"providers":{"x":{"baseUrl":"https://example.test","apiKey":"k","api":"openai-completions","models":[{"id":"a","contextWindow":1.28E5,"maxTokens":40960e-1,"promptCache":{"short":5.0}}],"modelOverrides":{"b":{"contextWindow":2e3,"maxTokens":100.00}}}}}`,
		`{"providers":{"x":{"models":[{"id":"a","maxTokens":null}]}}}`, `{"providers":{"x":{"compat":{"allowedFallbackModels":[1,2,3,4]}}}}`, `{"providers":{"a/b":{"baseUrl":1},"c~d":{"baseUrl":2},"1":{"baseUrl":3},"0":{"baseUrl":4}}}`,
		`{"providers":{"x":{"models":[{"id":"a","inputLimits":{"maxRequestBytes":1.5}}]}}}`, `{"providers":{"x":{"models":[{"id":"a","thinkingLevelMap":{"off":5,"max":true}}]}}}`,
		`{"providers":{"x":{"models":[{"id":"a","input":["text","image","audio","video"]}]}}}`,
		`{"providers":{"x":{"compat":{"supportsLongCacheRetention":"x","maxTokensField":"bad","allowedFallbackModels":[{"provider":"p","model":"m","cost":{"input":1,"output":1,"cacheRead":1,"cacheWrite":1}},{"provider":"p","model":"m","cost":{"input":1,"output":1,"cacheRead":1,"cacheWrite":1}},{"provider":"p","model":"m","cost":{"input":1,"output":1,"cacheRead":1,"cacheWrite":1}},{"provider":"","model":"m"}]}}}}`,
		`{"providers":{"x":{"compat":{"supportsLongCacheRetention":"x","allowedFallbackModels":[{"provider":"p","model":"m","cost":{"input":1,"output":1,"cacheRead":1,"cacheWrite":1}},{"provider":"p","model":"m","cost":{"input":1,"output":1,"cacheRead":1,"cacheWrite":1}},{"provider":"p","model":"m","cost":{"input":1,"output":1,"cacheRead":1,"cacheWrite":1}},{"provider":"p","model":"m","cost":{"input":1,"output":1,"cacheRead":1,"cacheWrite":1}}]}}}}`,
		`{"providers":{"x":{"compat":{"supportsLongCacheRetention":"x","maxTokensField":"bad","openRouterRouting":{"max_price":{"prompt":true,"request":null},"preferred_min_throughput":{"p50":"x"},"sort":{"by":1,"partition":2},"data_collection":"maybe"}}}}}`,
		`{"providers":{"x":{"compat":{"supportsLongCacheRetention":"x","maxTokensField":"bad","chatTemplateKwargs":{"a":{"$var":"other"},"b":[1]},"vercelGatewayRouting":{"only":[1]},"sessionAffinityFormat":"x","thinkingFormat":"y","cacheControlFormat":"openai"}}}}`,
		`{"providers":{"x":{"models":[{"id":"a","compat":{"supportsLongCacheRetention":"x","maxTokensField":"bad","vllmPriority":"p"},"samplingParamsByThinkingLevel":{"off":5,"high":[]},"thinkingLevelMap":{"minimal":1}}]}}}`,
		`{"providers":{"x":{"compat":{"supportsLongCacheRetention":"x","openRouterRouting":{"max_price":{"prompt":true,"request":null},"preferred_min_throughput":{"p50":"x"},"sort":{"by":1,"partition":2}}}}}}`,
		`{"providers":{"x":{"compat":{"supportsLongCacheRetention":"x","vercelGatewayRouting":{"only":[1],"order":[2]},"vllmPriority":"p"}}}}`,
		`{"providers":{"x":{"compat":{"supportsLongCacheRetention":"x","cacheControlFormat":"openai","chatTemplateArgs":{"a":{"$var":"x"}}}}}}`,
		// 42a3497d: one merged compat schema, $schema, positive token counts, Type.Enum input modalities.
		`{"$schema":42,"providers":{}}`, `{"$schema":"https://example.test/models.schema.json","providers":{}}`, `{"$schema":null}`,
		`{"providers":{"x":{"models":[{"id":"a","contextWindow":0,"maxTokens":-1}],"modelOverrides":{"m":{"contextWindow":0,"maxTokens":0}}}}}`,
		`{"providers":{"x":{"models":[{"id":"a","contextWindow":1,"maxTokens":1}],"modelOverrides":{"m":{"contextWindow":1,"maxTokens":1}}}}}`,
		`{"providers":{"x":{"compat":{"zaiToolStream":"x","thinkingTokenBudgetField":"bad","supportsMidConvoSystemMessages":1,"supportsAdditionalTools":"x","forceAdaptiveThinking":1}}}}`,
		`{"providers":{"x":{"compat":{"thinkingTokenBudgetField":5,"supportsMidConvoToolChanges":"y","supportsThinkingTokenBudget":null,"supportsExplicitPromptCacheMode":[]}}}}`,
		`{"providers":{"x":{"compat":{"thinkingTokenBudgetField":"thinking_budget","chatTemplateKwargs":{"a":{"$var":"thinking.budget","omitWhenOff":true}},"customOption":"value","supportsAdditionalTools":true}}}}`,
		`{"providers":{"x":{"models":[{"id":"a","input":[5,null],"compat":{"sessionAffinityFormat":"openrouter","maxTokensField":7}}]}}}`,
		`{"providers":{"x":{"modelOverrides":{"m":{"cost":{"input":"1"},"name":""},"n":5}}}}`, `{"providers":{"x":{"models":[{"id":"😀😀","name":"é"}]}}}`,
		// minimum, maximum and key order of a record: errors follow the order JavaScript enumerates the keys in.
		`{"providers":{"x":{"models":[{"id":"a","inputLimits":{"maxRequestBytes":0,"images":{"resize":{"maxWidth":0,"jpegQuality":0},"maxPerMessage":-2}}}]}}}`,
		`{"providers":{"b":{"baseUrl":1},"a":{"baseUrl":2},"10":{"baseUrl":3},"9":{"baseUrl":4}}}`,
		`{"providers":{"x":{"headers":{"z":1,"y":2,"2":3,"1":4}}}}`,
		// Keyword boundaries: exactly maxItems fallback models, jpegQuality at its minimum and maximum, a one-character id.
		`{"providers":{"x":{"compat":{"allowedFallbackModels":[{"provider":"p","model":"m","cost":{"input":1,"output":1,"cacheRead":1,"cacheWrite":1}},{"provider":"p","model":"m","cost":{"input":1,"output":1,"cacheRead":1,"cacheWrite":1}},{"provider":"p","model":"m","cost":{"input":1,"output":1,"cacheRead":1,"cacheWrite":1}}]}}}}`,
		`{"providers":{"x":{"models":[{"id":"a","inputLimits":{"images":{"resize":{"jpegQuality":100,"maxWidth":1}}}},{"id":"b","inputLimits":{"images":{"resize":{"jpegQuality":1}}}},{"id":"c","inputLimits":{"images":{"resize":{"jpegQuality":0}}}}]}}}`,
	}
	rng := rand.New(rand.NewSource(7))
	corruptions := []string{`null`, `"str"`, `5`, `1.5`, `-1`, `0`, `true`, `[]`, `{}`, `""`, `["text"]`, `{"a":1}`, `101`}
	var corrupt func(value any, depth int) any
	corrupt = func(value any, depth int) any {
		switch v := value.(type) {
		case map[string]any:
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			if len(keys) == 0 {
				return v
			}
			sort.Strings(keys)
			key := keys[rng.Intn(len(keys))]
			out := map[string]any{}
			maps.Copy(out, v)
			switch {
			case depth > 0 && rng.Intn(3) > 0:
				out[key] = corrupt(v[key], depth-1)
			case rng.Intn(5) == 0:
				delete(out, key)
			default:
				var replacement any
				_ = json.Unmarshal([]byte(corruptions[rng.Intn(len(corruptions))]), &replacement)
				out[key] = replacement
			}
			return out
		case []any:
			if len(v) == 0 {
				return v
			}
			out := append([]any(nil), v...)
			i := rng.Intn(len(v))
			if depth > 0 && rng.Intn(2) == 0 {
				out[i] = corrupt(v[i], depth-1)
			} else {
				var replacement any
				_ = json.Unmarshal([]byte(corruptions[rng.Intn(len(corruptions))]), &replacement)
				out[i] = replacement
			}
			return out
		}
		return value
	}
	var baseValue any
	if err := json.Unmarshal([]byte(base), &baseValue); err != nil {
		t.Fatal(err)
	}
	for range 400 {
		value := baseValue
		for range 1 + rng.Intn(3) {
			value = corrupt(value, 8)
		}
		encoded, _ := json.Marshal(value)
		docs = append(docs, string(encoded))
	}

	dir := t.TempDir()
	for i, doc := range docs {
		if err := os.MkdirAll(filepath.Join(dir, fmt.Sprint(i)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprint(i), "models.json"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/model_config.mjs", pigversion.UpstreamPackageVersion, dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, doc := range docs {
		registry := NewModelRegistryWithModelsPath(filepath.Join(dir, fmt.Sprint(i), "models.json"))
		got := registry.LoadError()
		want := expected[i]
		if want == "" && got != "" && !strings.HasPrefix(got, "Provider ") {
			// A provider that fails composition is reported by the composer (applyModelsJson), not by ModelConfig.
			if failures++; failures <= 8 {
				t.Errorf("doc %d %s\n  Pig %q\n  Pi accepts the document", i, doc, got)
			}
			continue
		}
		if want != "" && got != want {
			if failures++; failures <= 8 {
				t.Errorf("doc %d %s\n  Pig %q\n  Pi  %q", i, doc, got, want)
			}
		}
	}
	if failures > 8 {
		t.Errorf("%d of %d documents differ from Pi", failures, len(docs))
	}
}
