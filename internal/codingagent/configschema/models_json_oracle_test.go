package configschema_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/codingagent/configschema"
)

// pi: packages/coding-agent/src/core/model-config.ts ModelConfig.load. models.json validates against the schema trees of this package
// (Schema.Errors); Pi validates against TypeBox schemas that wrap pi-ai's pi-schema-built ones as Type.Unsafe (pi-ai-schemas.ts) and
// override cost with PartialModelCostSchema. The documents below, valid and invalid, are loaded by Pi's ModelConfig.load in the installed
// oracle: the errors it lists ("  - path: message", at most eight, in TypeBox's order) must be the ones Schema.Errors lists.

func modelsDocument(provider string) string {
	return `{"providers":{"p":` + provider + `}}`
}

func piModelsJSONDocuments() map[string]string {
	costField := `"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0}`
	return map[string]string{
		"valid minimal":                       modelsDocument(`{}`),
		"valid custom provider":               modelsDocument(`{"baseUrl":"http://x","api":"openai-completions","apiKey":"k","models":[{"id":"m",` + costField + `,"input":["text","image"],"contextWindow":1000,"maxTokens":10}]}`),
		"valid compat with unknown fields":    modelsDocument(`{"compat":{"supportsStore":true,"someFutureFlag":[1,2],"chatTemplateKwargs":{"a":"b","c":1,"d":false,"e":null,"f":{"$var":"thinking.effort","omitWhenOff":true,"extra":1}}}}`),
		"valid openrouter routing":            modelsDocument(`{"compat":{"openRouterRouting":{"order":["a"],"sort":{"by":"price","partition":null},"max_price":{"prompt":1,"image":"2"},"preferred_min_throughput":{"p50":3},"preferred_max_latency":4,"unknown":true}}}`),
		"valid fallback models":               modelsDocument(`{"compat":{"allowedFallbackModels":[{"provider":"a","model":"b","cost":{"input":1,"output":2,"cacheRead":3,"cacheWrite":4,"tiers":[{"inputTokensAbove":5,"input":1,"output":2,"cacheRead":3,"cacheWrite":4}]},"extra":1}]}}`),
		"valid override with partial cost":    modelsDocument(`{"modelOverrides":{"m":{"cost":{"input":3},"promptCache":{"short":60},"thinkingLevelMap":{"off":null,"high":"hi"},"inputLimits":{"maxRequestBytes":9,"images":{"resize":{"maxWidth":1,"jpegQuality":100},"maxPerMessage":2}}}}}`),
		"valid partial cost with tiers":       modelsDocument(`{"modelOverrides":{"m":{"cost":{"tiers":[{"inputTokensAbove":5,"input":1,"output":2,"cacheRead":3,"cacheWrite":4}]}}}}`),
		"missing providers":                   `{}`,
		"root is an array":                    `[]`,
		"model without id":                    modelsDocument(`{"models":[{"name":"n"}]}`),
		"model cost misses a rate":            modelsDocument(`{"models":[{"id":"m","cost":{"input":1}}]}`),
		"override cost is not an object":      modelsDocument(`{"modelOverrides":{"m":{"cost":3}}}`),
		"override cost rate is a string":      modelsDocument(`{"modelOverrides":{"m":{"cost":{"input":"1"}}}}`),
		"override cost tier misses a rate":    modelsDocument(`{"modelOverrides":{"m":{"cost":{"tiers":[{"inputTokensAbove":5}]}}}}`),
		"override cost tiers is not an array": modelsDocument(`{"modelOverrides":{"m":{"cost":{"tiers":{}}}}}`),
		"fallback model misses cost":          modelsDocument(`{"compat":{"allowedFallbackModels":[{"provider":"a","model":"b"}]}}`),
		"fallback model has empty names":      modelsDocument(`{"compat":{"allowedFallbackModels":[{"provider":"","model":"","cost":{"input":1,"output":2,"cacheRead":3,"cacheWrite":4}}]}}`),
		"four fallback models":                modelsDocument(`{"compat":{"allowedFallbackModels":[{},{},{},{}]}}`),
		"compat flag has the wrong type":      modelsDocument(`{"compat":{"supportsStore":"yes","supportsStrictMode":1}}`),
		"compat enum is not a listed value":   modelsDocument(`{"compat":{"thinkingFormat":"nope","maxTokensField":7,"cacheControlFormat":"other","sessionAffinityFormat":"x"}}`),
		"compat kwarg var is unknown":         modelsDocument(`{"compat":{"chatTemplateKwargs":{"a":{"$var":"thinking.nope"},"b":{},"c":[]}}}`),
		"compat chat template args":           modelsDocument(`{"compat":{"chatTemplateArgs":{"a":{"omitWhenOff":"x","$var":"thinking.budget"}}}}`),
		"routing fields have the wrong type":  modelsDocument(`{"compat":{"openRouterRouting":{"order":"a","only":[1],"sort":3,"max_price":{"prompt":true},"data_collection":"maybe","preferred_max_latency":"fast"}}}`),
		"vercel routing":                      modelsDocument(`{"compat":{"vercelGatewayRouting":{"only":"a","order":[1]}}}`),
		"thinking level map values":           modelsDocument(`{"modelOverrides":{"m":{"thinkingLevelMap":{"off":1,"max":true,"low":{}}}}}`),
		"prompt cache lifetime is not > 0":    modelsDocument(`{"modelOverrides":{"m":{"promptCache":{"short":0,"long":-1}}}}`),
		"input limits are out of range":       modelsDocument(`{"modelOverrides":{"m":{"inputLimits":{"maxRequestBytes":0,"images":{"maxPerMessage":1.5,"maxPerRequest":"2","resize":{"maxWidth":0,"jpegQuality":101,"maxBytes":0.5}}}}}}`),
		"input modality is not listed":        modelsDocument(`{"modelOverrides":{"m":{"input":["text","audio"]}}}`),
		"model input limits":                  modelsDocument(`{"models":[{"id":"m","inputLimits":[],"thinkingLevelMap":[],"promptCache":3,"compat":[]}]}`),
		"more than eight errors":              modelsDocument(`{"compat":{"supportsStore":1,"supportsDeveloperRole":1,"supportsReasoningEffort":1,"supportsUsageInStreaming":1,"supportsFinishReason":1,"requiresToolResultName":1,"requiresAssistantAfterToolResult":1,"requiresThinkingAsText":1,"requiresReasoningContentOnAssistantMessages":1,"zaiToolStream":1}}`),
		"required and property errors":        modelsDocument(`{"models":[{"cost":{"input":"x"},"compat":{"supportsStore":2}}]}`),
		"system one compat":                   modelsDocument(`{"compat":{"imageInput":"url","sendImageOptInHeader":"true"}}`),
		"unresolved types":                    modelsDocument(`{"compat":{"vllmPriority":"high","thinkingTokenBudgetField":"x","supportsThinkingTokenBudget":null}}`),
	}
}

func TestModelsJSONValidationMatchesPiModelConfigLoad(t *testing.T) {
	documents := piModelsJSONDocuments()
	names := make([]string, 0, len(documents))
	texts := make([]string, 0, len(documents))
	for name, text := range documents {
		names = append(names, name)
		texts = append(texts, text)
	}
	data, err := json.Marshal(texts)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "documents.json")
	if err := os.WriteFile(input, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/model_config_errors.mjs", pigversion.UpstreamPackageVersion, input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var pi []*string
	if err := json.Unmarshal(output, &pi); err != nil || len(pi) != len(texts) {
		t.Fatalf("Pi oracle output %s: %v", output, err)
	}
	schema := configschema.ModelsConfigSchema()
	rejected := 0
	for i, name := range names {
		lines, err := schema.Errors([]byte(texts[i]))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := strings.Join(lines, "\n")
		want := ""
		if pi[i] != nil {
			want = *pi[i]
			rejected++
		}
		if got != want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, want)
		}
	}
	if rejected < 20 || rejected == len(texts) {
		t.Errorf("Pi rejected %d of %d documents: the set must hold valid and invalid documents", rejected, len(texts))
	}
}
