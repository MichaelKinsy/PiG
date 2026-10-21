package configschema

import (
	"strings"
	"testing"
)

// Compares every export of packages/ai/src/providers/compat-schema.ts with its Go counterpart in compat_a.go and compat_b.go, as the JSON text
// TypeBox builds (JSON.stringify(schema, null, 2)). The generated models.schema.json is byte-equal to Pi's artifact too, but it folds the
// shared shapes into $defs; this compares each schema as Pi declares it.
func TestCompatSchemaExportsMatchPi(t *testing.T) {
	pi := piSchemaExports(t, "compat-schema")
	builders := map[string]func() *Schema{
		"SessionAffinityFormatSchema":         SessionAffinityFormatSchema,
		"ThinkingTokenBudgetFieldSchema":      ThinkingTokenBudgetFieldSchema,
		"ChatTemplateKwargValueSchema":        ChatTemplateKwargValueSchema,
		"OpenRouterRoutingSchema":             OpenRouterRoutingSchema,
		"VercelGatewayRoutingSchema":          VercelGatewayRoutingSchema,
		"AnthropicAllowedFallbackModelSchema": AnthropicAllowedFallbackModelSchema,
		"OpenAICompletionsCompatSchema":       OpenAICompletionsCompatSchema,
		"OpenAIResponsesCompatSchema":         OpenAIResponsesCompatSchema,
		"AnthropicMessagesCompatSchema":       AnthropicMessagesCompatSchema,
		"BedrockCompatSchema":                 BedrockCompatSchema,
		"MistralConversationsCompatSchema":    MistralConversationsCompatSchema,
		"SystemOneCompatSchema":               SystemOneCompatSchema,
		"ProviderCompatSchema":                ProviderCompatSchema,
	}
	for name, build := range builders {
		if build == nil {
			continue
		}
		want, ok := pi[name]
		if !ok {
			t.Errorf("Pi has no export %s", name)
			continue
		}
		if got := schemaText(build()); got != want {
			t.Errorf("%s differs from Pi's:\n got %s\nwant %s", name, got, want)
		}
	}
	// CompatSchemasByApi (Pi 4954d760) is an object of API name to schema: its JSON text is each schema's under its key.
	byAPI, ok := pi["CompatSchemasByApi"]
	if !ok {
		t.Error("Pi has no export CompatSchemasByApi")
	} else {
		var parts []string
		for _, entry := range CompatSchemasByApi() {
			text := strings.ReplaceAll(schemaText(compatSchemaNamed(entry.Schema)), "\n", "\n  ")
			parts = append(parts, "  \""+entry.API+"\": "+text)
		}
		if got := "{\n" + strings.Join(parts, ",\n") + "\n}"; got != byAPI {
			t.Errorf("CompatSchemasByApi differs from Pi's (first 400 bytes):\n got %.400s\nwant %.400s", got, byAPI)
		}
		builders["CompatSchemasByApi"] = nil
	}
	if len(pi) != len(builders) {
		t.Errorf("Pi compat-schema.js exports %d values, the test covers %d: a new export needs a Go counterpart", len(pi), len(builders))
	}
}
