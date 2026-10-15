package configschema

// Mirrors packages/ai/src/providers/compat-schema.ts (first part: shared fields, routing and the OpenAI schemas).

func sessionAffinityFormat(opts ...Opt) *Schema {
	return Union([]*Schema{Literal("openai"), Literal("openai-nosession"), Literal("openrouter")}, opts...)
}

func thinkingTokenBudgetField(opts ...Opt) *Schema {
	return Union([]*Schema{Literal("thinking_token_budget"), Literal("thinking_budget"), Literal("thinking_budget_tokens")}, opts...)
}

func SessionAffinityFormatSchema() *Schema {
	return sessionAffinityFormat(D("Session-affinity header format used to route related requests consistently."))
}

func ThinkingTokenBudgetFieldSchema() *Schema {
	return thinkingTokenBudgetField(D(`Top-level request field name used by OpenAI-compatible endpoints to cap reasoning tokens. "thinking_token_budget" is used by vLLM, "thinking_budget" by Qwen, DashScope, or SGLang, and "thinking_budget_tokens" by llama.cpp.`))
}

func literals(values ...string) []*Schema {
	out := make([]*Schema, len(values))
	for i, v := range values {
		out[i] = Literal(v)
	}
	return out
}

func ChatTemplateKwargValueSchema() *Schema {
	return Union([]*Schema{
		String(), Number(), Boolean(), Null(),
		Object([]Prop{
			P("$var", Union(literals("thinking.enabled", "thinking.effort", "thinking.budget"))),
			Optional("omitWhenOff", Boolean()),
		}),
	})
}

func percentileCutoffs(metric string) *Schema {
	return Object([]Prop{
		Optional("p50", Number(D(metric+" at the 50th percentile."))),
		Optional("p75", Number(D(metric+" at the 75th percentile."))),
		Optional("p90", Number(D(metric+" at the 90th percentile."))),
		Optional("p99", Number(D(metric+" at the 99th percentile."))),
	})
}

func optionalCompatBoolean(name string, opts ...Opt) Prop { return Optional(name, Boolean(opts...)) }

func optionalSessionAffinityFormat(opts ...Opt) Prop {
	return Optional("sessionAffinityFormat", sessionAffinityFormat(opts...))
}

func priceUnion(description string) *Schema {
	return Union([]*Schema{Number(), String()}, D(description))
}

func OpenRouterRoutingSchema() *Schema {
	sortObject := Object([]Prop{
		Optional("by", String(D(`The sorting metric, such as "price", "throughput", or "latency".`))),
		Optional("partition", Union([]*Schema{String(), Null()}, D(`Partitioning strategy: "model" or "none".`), Default("model"))),
	})
	return Object([]Prop{
		Optional("allow_fallbacks", Boolean(D("Whether to allow backup providers to serve requests."), Default(true))),
		Optional("require_parameters", Boolean(D("Whether to filter providers to only those that support all parameters in the request."), Default(false))),
		Optional("data_collection", Union(literals("deny", "allow"), D(`Data collection setting. "allow": allow providers that may store or train on data. "deny": only use providers that do not collect user data.`), Default("allow"))),
		Optional("zdr", Boolean(D("Whether to restrict routing to only ZDR (Zero Data Retention) endpoints."))),
		Optional("enforce_distillable_text", Boolean(D("Whether to restrict routing to only models that allow text distillation."))),
		Optional("order", Array(String(), D("An ordered list of provider names or slugs to try in sequence, falling back to the next if unavailable."))),
		Optional("only", Array(String(), D("List of provider names or slugs to exclusively allow for this request."))),
		Optional("ignore", Array(String(), D("List of provider names or slugs to skip for this request."))),
		Optional("quantizations", Array(String(), D(`A list of quantization levels to filter providers by, for example ["fp16", "bf16", "fp8", "fp6", "int8", "int4", "fp4", "fp32"].`))),
		Optional("sort", Union([]*Schema{String(), sortObject}, D(`Sorting strategy. Can be a string such as "price", "throughput", or "latency", or an object.`))),
		Optional("max_price", Object([]Prop{
			Optional("prompt", priceUnion("Price per million prompt tokens.")),
			Optional("completion", priceUnion("Price per million completion tokens.")),
			Optional("image", priceUnion("Price per image.")),
			Optional("audio", priceUnion("Price per audio unit.")),
			Optional("request", priceUnion("Price per request.")),
		}, D("Maximum price per million tokens in USD."))),
		Optional("preferred_min_throughput", Union([]*Schema{Number(), percentileCutoffs("Minimum tokens per second")}, D("Preferred minimum throughput in tokens per second. A number applies to p50."))),
		Optional("preferred_max_latency", Union([]*Schema{Number(), percentileCutoffs("Maximum latency in seconds")}, D("Preferred maximum latency in seconds. A number applies to p50."))),
	}, D("OpenRouter provider routing preferences. Controls which upstream providers OpenRouter routes requests to. Sent as the provider field in the OpenRouter API request body. See https://openrouter.ai/docs/guides/routing/provider-selection."))
}

func VercelGatewayRoutingSchema() *Schema {
	return Object([]Prop{
		Optional("only", Array(String(), D(`List of provider slugs to exclusively use for this request, for example ["bedrock", "anthropic"].`))),
		Optional("order", Array(String(), D(`List of provider slugs to try in order, for example ["anthropic", "openai"].`))),
	}, D("Vercel AI Gateway routing preferences. Controls which upstream providers the gateway routes requests to. See https://vercel.com/docs/ai-gateway/models-and-providers/provider-options."))
}

func AnthropicAllowedFallbackModelSchema() *Schema {
	return Object([]Prop{
		P("provider", String(Opt{"minLength", 1})),
		P("model", String(Opt{"minLength", 1})),
		P("cost", ModelCostSchema()),
	}, D("An Anthropic server-side refusal fallback model with local pricing metadata."))
}

func OpenAICompletionsCompatSchema() *Schema {
	kwargs := ChatTemplateKwargValueSchema()
	return Object([]Prop{
		Optional("supportsStore", Boolean(D("Whether the provider supports the store field. Default: auto-detected from URL."))),
		optionalCompatBoolean("supportsDeveloperRole", D("Whether the provider supports the developer role instead of system. Default: auto-detected from URL.")),
		Optional("supportsReasoningEffort", Boolean(D("Whether the provider supports reasoning_effort. Default: auto-detected from URL."))),
		Optional("supportsUsageInStreaming", Boolean(D("Whether the provider supports stream_options.include_usage for token usage in streaming responses."), Default(true))),
		Optional("supportsFinishReason", Boolean(D("Whether streamed responses include finish_reason. When false, pi infers stop or toolUse when the stream ends."), Default(true))),
		Optional("maxTokensField", Union(literals("max_completion_tokens", "max_tokens"), D("Which field to use for max tokens. Default: auto-detected from URL."))),
		Optional("requiresToolResultName", Boolean(D("Whether tool results require the name field. Default: auto-detected from URL."))),
		Optional("requiresAssistantAfterToolResult", Boolean(D("Whether a user message after tool results requires an assistant message in between. Default: auto-detected from URL."))),
		Optional("requiresThinkingAsText", Boolean(D("Whether thinking blocks must be converted to text blocks with <thinking> delimiters. Default: auto-detected from URL."))),
		Optional("requiresReasoningContentOnAssistantMessages", Boolean(D("Whether all replayed assistant messages must include an empty reasoning_content field when reasoning is enabled. Default: auto-detected from URL."))),
		Optional("thinkingFormat", Union(literals("openai", "openrouter", "deepseek", "together", "baseten", "zai", "qwen", "chat-template", "qwen-chat-template", "string-thinking", "ant-ling"),
			D(`Format for reasoning or thinking parameters. When omitted, Pi auto-detects the format from the provider URL. "openai" uses reasoning_effort, "openrouter" uses reasoning.effort, "deepseek" uses thinking.type plus reasoning_effort when supported, "together" uses reasoning.enabled plus reasoning_effort when supported, "baseten" uses configurable chat_template_args plus reasoning_effort when supported, "zai" uses thinking.type, "qwen" uses top-level enable_thinking, "qwen-chat-template" uses chat_template_kwargs.enable_thinking and preserve_thinking, "chat-template" uses configurable chat_template_kwargs, "string-thinking" uses top-level thinking, and "ant-ling" uses reasoning.effort only when the mapped effort is non-null.`))),
		Optional("chatTemplateKwargs", Record(kwargs, D(`Kwargs sent as chat_template_kwargs when thinkingFormat is "chat-template". Use $var with "thinking.enabled", "thinking.effort", or "thinking.budget" for pi-controlled values.`))),
		Optional("chatTemplateArgs", Record(kwargs, D(`Arguments sent as chat_template_args when thinkingFormat is "baseten". Use $var with "thinking.enabled", "thinking.effort", or "thinking.budget" for pi-controlled values.`))),
		Optional("openRouterRouting", OpenRouterRoutingSchema()),
		Optional("vercelGatewayRouting", VercelGatewayRoutingSchema()),
		Optional("zaiToolStream", Boolean(D("Whether z.ai supports top-level tool_stream for streaming tool call deltas."), Default(false))),
		Optional("thinkingTokenBudgetField", thinkingTokenBudgetField(D(`Top-level request field used to cap reasoning tokens from thinkingBudgets. Reasoning and the answer share max_tokens on these endpoints. "thinking_token_budget" is vLLM, "thinking_budget" is Qwen, DashScope, or SGLang, and "thinking_budget_tokens" is llama.cpp. Off by default and not set on the generated catalog.`))),
		Optional("supportsThinkingTokenBudget", Boolean(D(`Alias for thinkingTokenBudgetField: "thinking_token_budget" (vLLM). Prefer thinkingTokenBudgetField.`), Default(false))),
		optionalCompatBoolean("supportsOpenAIGrammarTools", D("Whether the provider supports OpenAI custom tools with Lark or regex grammar formats. When false, grammar-constrained tools fall back to normal function tools. The generated catalog enables this for capable models."), Default(false)),
		optionalCompatBoolean("supportsMidConvoSystemMessages", D("Whether the exact model accepts system or developer messages after the conversation has started. When false, later system messages are folded into the leading system message. The generated catalog enables this for verified models."), Default(false)),
		Optional("supportsMidConvoToolAdditions", Boolean(D("Whether system messages can introduce additional tools mid-conversation. Requires supportsMidConvoSystemMessages. The generated catalog enables this for capable models."), Default(false))),
		optionalCompatBoolean("supportsStrictMode", D("Whether the provider supports the strict field in tool definitions. Generated capable models enable it explicitly."), Default(false)),
		Optional("cacheControlFormat", Literal("anthropic", D("Cache control convention for prompt caching. Anthropic applies cache_control markers to the system prompt, last tool definition, and last user, assistant, or tool-result text content."))),
		optionalCompatBoolean("sendSessionAffinityHeaders", D("Whether to send session-affinity data from options.sessionId. Default: true for OpenRouter endpoints, false otherwise.")),
		optionalSessionAffinityFormat(D("Session-affinity header format. openai sends session_id, x-client-request-id, and x-session-affinity; openai-nosession sends x-client-request-id and x-session-affinity; openrouter sends x-session-id. Does not affect prompt_cache_key. Default: auto-detected.")),
		optionalCompatBoolean("supportsLongCacheRetention", D(`Whether the provider supports long prompt cache retention (prompt_cache_retention: "24h" or Anthropic-style cache_control.ttl: "1h", depending on format). Default: auto-detected from provider and URL.`)),
		Optional("vllmPriority", Number(D("vLLM scheduler priority sent as the top-level priority request field. Lower values are handled earlier and the server default is 0. Only meaningful with --scheduling-policy priority. Off by default and not set on the generated catalog."))),
	}, D("Compatibility settings for OpenAI-compatible completions APIs. Use this to override URL-based auto-detection for custom providers."), Opt{"additionalProperties", true})
}

func OpenAIResponsesCompatSchema() *Schema {
	return Object([]Prop{
		optionalCompatBoolean("supportsDeveloperRole", D("Whether the provider supports the developer role instead of system."), Default(true)),
		optionalCompatBoolean("supportsMidConvoSystemMessages", D("Whether the exact model accepts developer or system messages after the conversation has started. When false, later system messages are folded into the leading system message. The generated catalog enables this for verified models."), Default(false)),
		optionalSessionAffinityFormat(D("Session-affinity header format. openai sends session_id and x-client-request-id; openai-nosession sends x-client-request-id; openrouter sends x-session-id. Does not affect prompt_cache_key. Default: auto-detected.")),
		optionalCompatBoolean("supportsLongCacheRetention", D(`Whether the provider supports long prompt cache retention. This uses prompt_cache_options.ttl: "30m" on GPT-5.6+ and prompt_cache_retention: "24h" on earlier models.`), Default(true)),
		optionalCompatBoolean("supportsStrictMode", D("Whether the provider supports strict JSON-schema function tools. Defaults are API-specific; generated OpenAI models enable it explicitly.")),
		optionalCompatBoolean("supportsOpenAIGrammarTools", D("Whether to emit OpenAI custom tools with Lark or regex grammar formats. When false, grammar-constrained tools fall back to normal function tools. The generated catalog enables this for capable models."), Default(false)),
		Optional("supportsAdditionalTools", Boolean(D("Whether the model supports message-anchored additional_tools input items."), Default(false))),
		Optional("supportsToolSearch", Boolean(D("Whether the model supports client-executed tool search for transcript-anchored additions."), Default(false))),
		Optional("supportsExplicitPromptCacheMode", Boolean(D("Whether the model accepts prompt_cache_options. Older OpenAI models reject the parameter."), Default(false))),
		Optional("supportsMidConvoEffort", Boolean(D("Whether thinking level changes are sent as configuration_update input items instead of changing reasoning.effort, so a change keeps the prompt cache. Only used by the openai-responses API, and ignored with Sign in with ChatGPT on the openai provider."), Default(false))),
		Optional("supportsMaxOutputTokens", Boolean(D("Whether the provider accepts max_output_tokens. Some Codex-protocol gateways reject it."), Default(true))),
	}, D("Compatibility settings for OpenAI Responses APIs."), Opt{"additionalProperties", true})
}
