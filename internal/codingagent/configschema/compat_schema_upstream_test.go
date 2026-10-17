package configschema

import "testing"

// Ports packages/ai/test/compat-schema.test.ts. Compile(schema).Default({}) fills each property that carries a `default` keyword, so the
// defaults a schema assigns are the properties with that keyword. "preserves property types in the provider superset" is a TypeScript
// type-level assertion (expectTypeOf); the property types of the Go compat structs are checked by the compiler.

func defaultsOf(t *testing.T, s *Schema) map[string]any {
	t.Helper()
	out := map[string]any{}
	for _, p := range propsOf(s) {
		if v, ok := p.Schema.get("default"); ok {
			out[p.Name] = v
		}
	}
	return out
}

func TestCompatSchemasPreserveAPISpecificDefaults(t *testing.T) {
	completions := defaultsOf(t, OpenAICompletionsCompatSchema())
	for _, name := range []string{"thinkingFormat", "supportsLongCacheRetention"} {
		if _, ok := completions[name]; ok {
			t.Errorf("completions default assigns %s", name)
		}
	}
	if got := defaultsOf(t, OpenAIResponsesCompatSchema())["supportsDeveloperRole"]; got != true {
		t.Errorf("responses supportsDeveloperRole default = %v, want true", got)
	}
	anthropic := defaultsOf(t, AnthropicMessagesCompatSchema())
	if _, ok := anthropic["sendSessionAffinityHeaders"]; ok {
		t.Error("anthropic default assigns sendSessionAffinityHeaders")
	}
	if anthropic["supportsLongCacheRetention"] != true {
		t.Errorf("anthropic supportsLongCacheRetention default = %v, want true", anthropic["supportsLongCacheRetention"])
	}
	if got := defaultsOf(t, BedrockCompatSchema())["supportsStrictMode"]; got != false {
		t.Errorf("bedrock supportsStrictMode default = %v, want false", got)
	}
}

func TestProviderCompatSchemaAssignsNoAPISpecificDefaults(t *testing.T) {
	defaults := defaultsOf(t, ProviderCompatSchema())
	for _, name := range []string{"supportsDeveloperRole", "sendSessionAffinityHeaders", "supportsLongCacheRetention", "supportsStrictMode"} {
		if _, ok := defaults[name]; ok {
			t.Errorf("the provider superset assigns a default to %s", name)
		}
	}
}
