package configschema

import "testing"

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
		"ProviderCompatSchema":                ProviderCompatSchema,
	}
	for name, build := range builders {
		want, ok := pi[name]
		if !ok {
			t.Errorf("Pi has no export %s", name)
			continue
		}
		if got := schemaText(build()); got != want {
			t.Errorf("%s differs from Pi's:\n got %s\nwant %s", name, got, want)
		}
	}
	if len(pi) != len(builders) {
		t.Errorf("Pi compat-schema.js exports %d values, the test covers %d: a new export needs a Go counterpart", len(pi), len(builders))
	}
}
