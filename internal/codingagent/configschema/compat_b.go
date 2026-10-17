package configschema

// Mirrors packages/ai/src/providers/compat-schema.ts (second part: Anthropic, Bedrock, Mistral and the merged provider schema).

func AnthropicMessagesCompatSchema() *Schema {
	return Object([]Prop{
		Optional("supportsEagerToolInputStreaming", Boolean(D("Whether the provider accepts per-tool eager_input_streaming. When false, the Anthropic provider omits tools[].eager_input_streaming and sends the legacy fine-grained-tool-streaming-2025-05-14 beta header for tool-enabled requests."), Default(true))),
		optionalCompatBoolean("supportsLongCacheRetention", D("Whether the provider supports Anthropic long cache retention through cache_control.ttl."), Default(true)),
		optionalCompatBoolean("sendSessionAffinityHeaders", D("Whether to send x-session-affinity from options.sessionId when caching is enabled. Required for providers like Fireworks that use session affinity for prompt cache routing; requests to the same replica maximize cache hits. Default: true for OpenRouter endpoints, false otherwise.")),
		Optional("sessionAffinityFormat", Literal("openrouter", D("Session-affinity format. openrouter sends x-session-id; when unset, sends x-session-affinity."))),
		Optional("supportsCacheControlOnTools", Boolean(D("Whether the provider supports Anthropic-style cache_control markers on tool definitions. When false, cache_control is omitted from tool parameters. Some Anthropic-compatible providers, such as Fireworks, do not support this field on tools and may reject or ignore it."), Default(true))),
		Optional("supportsTemperature", Boolean(D("Whether the model accepts the Anthropic temperature request field. Claude Opus 4.7+ rejects non-default values."), Default(true))),
		Optional("forceAdaptiveThinking", Boolean(D("Whether to force adaptive thinking (thinking.type: adaptive plus output_config.effort) regardless of model ID. Built-in models that require adaptive thinking set this in generated metadata. Custom Anthropic-compatible providers can set this to true for any model whose upstream requires the adaptive format. Set false to opt out on overridden built-in models."), Default(false))),
		Optional("allowEmptySignature", Boolean(D("Whether to replay empty thinking signatures instead of converting thinking to text."), Default(false))),
		Optional("supportsStrictTools", Boolean(D("Whether the provider supports Anthropic strict tool schemas. Generated Anthropic models enable it explicitly."), Default(false))),
		Optional("supportsMidConvoEffort", Boolean(D("Whether the exact model transport supports effort-only system messages and thinking binding controls."), Default(false))),
		optionalCompatBoolean("supportsMidConvoSystemMessages", D("Whether the exact model accepts system-role messages inside the conversation. When false, later system messages are folded into the top-level system prompt."), Default(false)),
		Optional("supportsMidConvoToolChanges", Boolean(D("Whether the exact model accepts mid-conversation tool_addition and tool_removal blocks. Requires supportsMidConvoSystemMessages."), Default(false))),
		Optional("allowedFallbackModels", Array(AnthropicAllowedFallbackModelSchema(), Opt{"maxItems", 3}, D("Models Anthropic accepts for server-side refusal fallback, with local pricing metadata for returned fallback responses. When absent or empty, callers must omit fallbacks; Anthropic rejects the field for models with no permitted fallback targets."))),
	}, D("Compatibility settings for Anthropic Messages-compatible APIs."), Opt{"additionalProperties", true})
}

func BedrockCompatSchema() *Schema {
	return Object([]Prop{
		optionalCompatBoolean("supportsStrictMode", D("Whether the model supports Bedrock strict tool schemas."), Default(false)),
	}, D("Compatibility settings for Amazon Bedrock models."), Opt{"additionalProperties", true})
}

func MistralConversationsCompatSchema() *Schema {
	return Object([]Prop{
		optionalCompatBoolean("supportsMidConvoSystemMessages", D("Whether the exact model accepts system messages after the conversation has started. When false, later system messages are folded into the leading system message."), Default(false)),
	}, D("Compatibility settings for the Mistral chat API."), Opt{"additionalProperties", true})
}

// ProviderCompatSchema is mergeCompatProperties: a property keeps the position of its first schema, and an override replaces its value.
func ProviderCompatSchema() *Schema {
	overrides := []Prop{
		optionalCompatBoolean("supportsDeveloperRole", D("Whether the provider supports the developer role instead of system. Defaults are API-specific.")),
		optionalCompatBoolean("supportsMidConvoSystemMessages", D("Whether the exact model accepts system or developer messages after the conversation has started. When false, later system messages are folded into the leading system message."), Default(false)),
		optionalSessionAffinityFormat(D("Session-affinity header format. Defaults are API-specific or auto-detected.")),
		optionalCompatBoolean("supportsMidConvoEffort", D("Whether thinking level changes are sent inside the conversation instead of changing the request's effort, so a change keeps the prompt cache. Used by the anthropic-messages and openai-responses APIs."), Default(false)),
		optionalCompatBoolean("supportsLongCacheRetention", D("Whether the provider supports long prompt cache retention. Defaults are API-specific or auto-detected.")),
		optionalCompatBoolean("supportsStrictMode", D("Whether the provider supports strict tool schemas. Defaults are API-specific.")),
		optionalCompatBoolean("supportsOpenAIGrammarTools", D("Whether the provider supports OpenAI custom tools with Lark or regex grammar formats. When false, grammar-constrained tools fall back to normal function tools."), Default(false)),
		optionalCompatBoolean("sendSessionAffinityHeaders", D("Whether to send session-affinity data from options.sessionId. Defaults are API-specific.")),
	}
	// [...new Set(Object.values(CompatSchemasByApi))]: each schema once, in first-key order.
	var groups [][]Prop
	seen := map[string]bool{}
	for _, entry := range CompatSchemasByApi() {
		if !seen[entry.Schema] {
			seen[entry.Schema] = true
			groups = append(groups, propsOf(compatSchemaNamed(entry.Schema)))
		}
	}
	var merged []Prop
	index := map[string]int{}
	duplicates := map[string]bool{}
	for _, group := range groups {
		for _, p := range group {
			if i, seen := index[p.Name]; seen {
				duplicates[p.Name] = true
				merged[i] = p
				continue
			}
			index[p.Name] = len(merged)
			merged = append(merged, p)
		}
	}
	for _, o := range overrides {
		if !duplicates[o.Name] {
			panic("compatibility schema override does not resolve a duplicate property: " + o.Name)
		}
		merged[index[o.Name]] = o
	}
	for name := range duplicates {
		if _, ok := overrideNames(overrides)[name]; !ok {
			panic("duplicate compatibility schema property requires an override: " + name)
		}
	}
	return Object(merged, D("Provider and model compatibility overrides."), Opt{"additionalProperties", true})
}

func overrideNames(props []Prop) map[string]bool {
	out := map[string]bool{}
	for _, p := range props {
		out[p.Name] = true
	}
	return out
}

// propsOf reads the properties of an Object schema back, so schemas compose the way `schema.properties` does in TypeScript.
func propsOf(s *Schema) []Prop {
	properties, _ := s.get("properties")
	required := map[string]bool{}
	if list, ok := s.get("required"); ok {
		for _, name := range list.([]any) {
			required[name.(string)] = true
		}
	}
	var out []Prop
	for _, kv := range properties.(*Schema).kv {
		out = append(out, Prop{Name: kv.K, Schema: kv.V.(*Schema), Optional: !required[kv.K]})
	}
	return out
}

// CompatSchemaEntry is one entry of CompatSchemasByApi: an API and the name of its compat schema.
type CompatSchemaEntry struct{ API, Schema string }

// CompatSchemasByApi is compat-schema.ts CompatSchemasByApi: the compatibility schema of each API whose models accept compat,
// in Pi's key order (Pi 4954d760 adds azure-openai-responses and openai-codex-responses, which share the Responses schema).
func CompatSchemasByApi() []CompatSchemaEntry {
	return []CompatSchemaEntry{
		{"openai-completions", "OpenAICompletionsCompatSchema"},
		{"openai-responses", "OpenAIResponsesCompatSchema"},
		{"azure-openai-responses", "OpenAIResponsesCompatSchema"},
		{"openai-codex-responses", "OpenAIResponsesCompatSchema"},
		{"anthropic-messages", "AnthropicMessagesCompatSchema"},
		{"bedrock-converse-stream", "BedrockCompatSchema"},
		{"mistral-conversations", "MistralConversationsCompatSchema"},
	}
}

func compatSchemaNamed(name string) *Schema {
	switch name {
	case "OpenAICompletionsCompatSchema":
		return OpenAICompletionsCompatSchema()
	case "OpenAIResponsesCompatSchema":
		return OpenAIResponsesCompatSchema()
	case "AnthropicMessagesCompatSchema":
		return AnthropicMessagesCompatSchema()
	case "BedrockCompatSchema":
		return BedrockCompatSchema()
	case "MistralConversationsCompatSchema":
		return MistralConversationsCompatSchema()
	}
	panic("configschema: no compat schema " + name)
}
