package ai_test

import (
	"encoding/json"
	"testing"
)

// pi: packages/ai/src/api/openai-completions.ts

// openAICompletionsWireRows are the request shapes buildParams, convertMessages and convertTools have: each credential shape and route, each
// optional member of the request present and absent, each thinking format on and off, each block kind as the last one of the conversation,
// a poisoned history, and the objects a catalog entry or a caller supplies (routing, chat template values, sampling parameters, a tool choice).
func openAICompletionsWireRows() []providerWireRow {
	const api, id = "openai-completions", "m"
	called := func(provider string) string {
		return wireAssistant(api, provider, id, "toolUse", `{"type":"text","text":"I will read."},{"type":"thinking","thinking":"plan","thinkingSignature":"reasoning_content"},`+wireCall("call_1"))
	}
	full := func(provider string) json.RawMessage {
		return wireContext(wireSystem+","+wireTools, wireUserGo, called(provider), wireResult("call_1", wireText, false), wireUserNext)
	}
	rich := func(provider string) json.RawMessage {
		return wireContext(wireSystem+","+wireTools, wireLook, called(provider), wireResult("call_1", wireText+","+wireImage, false), wireNextLook)
	}
	after := func(content string, isError bool) json.RawMessage {
		return wireContext(wireTools, wireUserGo, called("custom"), wireResult("call_1", content, isError))
	}
	raw := func(text string) json.RawMessage { return json.RawMessage(text) }

	var rows []providerWireRow
	add := func(name string, row providerWireRow) {
		row.Name, row.API, row.ID = name, api, id
		if row.Provider == "" {
			row.Provider, row.BaseURL = "custom", "http://127.0.0.1:9/v1"
		}
		if row.APIKey == "" {
			row.APIKey = "k"
		}
		if row.Options == nil {
			row.Options = raw(`{}`)
		}
		if row.Context == nil {
			row.Context = full(row.Provider)
		}
		rows = append(rows, row)
	}

	// Each provider shape: an API key at the official address, an OAuth token, and OpenAI-compatible servers at their own addresses.
	for _, route := range []struct{ name, provider, baseURL, key string }{
		{"openai with an api key", "openai", "https://api.openai.com/v1", "sk-x"},
		{"github-copilot with oauth", "github-copilot", "https://api.individual.githubcopilot.com", "tid=1;exp=9999999999"},
		{"custom base url", "custom", "http://127.0.0.1:9/v1", "k"},
		{"openrouter", "openrouter", "https://openrouter.ai/api/v1", "k"},
		{"deepseek", "deepseek", "https://api.deepseek.com", "k"},
		{"zai", "zai", "https://api.z.ai/api/paas/v4", "k"},
		{"xai", "xai", "https://api.x.ai/v1", "k"},
		{"groq", "groq", "https://api.groq.com/openai/v1", "k"},
		{"cerebras", "cerebras", "https://api.cerebras.ai/v1", "k"},
		{"together", "together", "https://api.together.xyz/v1", "k"},
		{"moonshotai", "moonshotai", "https://api.moonshot.ai/v1", "k"},
		{"ollama", "ollama", "http://localhost:11434/v1", "k"},
		{"vercel-ai-gateway", "vercel-ai-gateway", "https://ai-gateway.vercel.sh/v1", "k"},
	} {
		row := providerWireRow{Provider: route.provider, BaseURL: route.baseURL, APIKey: route.key}
		add("route "+route.name+": replay", row)
		row.Context, row.Options = rich(route.provider), raw(`{"maxTokens":900,"temperature":0.2,"toolChoice":"auto","sessionId":"s1"}`)
		add("route "+route.name+": images, temperature, tool choice", row)
		row.Reasoning, row.Simple, row.Options = true, true, raw(`{"maxTokens":900,"reasoning":"high","toolChoice":"required","sessionId":"s1","cacheRetention":"long"}`)
		add("route "+route.name+": images, reasoning, tool choice, long retention", row)
	}

	// The optional members of the request.
	add("no system prompt, no tools, no options", providerWireRow{Context: wireContext("", wireUserGo)})
	add("no message", providerWireRow{Context: wireContext("")})
	add("no message, system prompt and tools", providerWireRow{Context: wireContext(wireSystem + "," + wireTools)})
	add("system prompt, no tools", providerWireRow{Context: wireContext(wireSystem, wireUserGo)})
	add("max_tokens field", providerWireRow{Compat: raw(`{"maxTokensField":"max_tokens"}`), Options: raw(`{"maxTokens":64}`)})
	add("max_completion_tokens field", providerWireRow{Compat: raw(`{"maxTokensField":"max_completion_tokens"}`), Options: raw(`{"maxTokens":64}`)})
	add("temperature zero", providerWireRow{Options: raw(`{"temperature":0}`)})
	add("store", providerWireRow{Compat: raw(`{"supportsStore":true}`)})
	add("no usage in streaming", providerWireRow{Compat: raw(`{"supportsUsageInStreaming":false}`)})
	add("tool stream and priority", providerWireRow{Compat: raw(`{"zaiToolStream":true,"vllmPriority":1}`), Options: raw(`{"toolChoice":"auto"}`)})
	add("strict tools", providerWireRow{Compat: raw(`{"supportsStrictMode":true}`)})
	add("developer role", providerWireRow{Reasoning: true, Compat: raw(`{"supportsDeveloperRole":true}`)})
	// A reasoning model that needs reasoning_content on every assistant message: the replayed thinking is assigned before tool_calls (:1342), the empty one after them (:1383-1387).
	add("required reasoning content, replayed thinking", providerWireRow{Reasoning: true, Compat: raw(`{"requiresReasoningContentOnAssistantMessages":true}`)})
	add("required reasoning content, empty after tool calls", providerWireRow{Reasoning: true, Compat: raw(`{"requiresReasoningContentOnAssistantMessages":true}`),
		Context: wireContext(wireTools, wireUserGo, wireAssistant(api, "custom", id, "toolUse", `{"type":"text","text":"I will read."},`+wireCall("call_1")), wireResult("call_1", wireText, false), wireUserNext)})
	add("required reasoning content, empty beside another reasoning field", providerWireRow{Reasoning: true, Compat: raw(`{"requiresReasoningContentOnAssistantMessages":true}`),
		Context: wireContext(wireTools, wireUserGo, wireAssistant(api, "custom", id, "toolUse", `{"type":"thinking","thinking":"plan","thinkingSignature":"reasoning"},`+wireCall("call_1")), wireResult("call_1", wireText, false), wireUserNext)})
	add("tool result name, assistant after tool result, thinking as text", providerWireRow{Compat: raw(`{"requiresToolResultName":true,"requiresAssistantAfterToolResult":true,"requiresThinkingAsText":true}`)})
	add("prompt cache key and long retention", providerWireRow{Compat: raw(`{"supportsLongCacheRetention":true}`), Options: raw(`{"sessionId":"s1","cacheRetention":"long"}`)})
	add("cache retention none", providerWireRow{Provider: "openai", BaseURL: "https://api.openai.com/v1", Options: raw(`{"sessionId":"s1","cacheRetention":"none"}`)})
	add("anthropic cache control", providerWireRow{Compat: raw(`{"cacheControlFormat":"anthropic"}`), Context: rich("custom")})
	add("anthropic cache control, long retention", providerWireRow{Compat: raw(`{"cacheControlFormat":"anthropic","supportsLongCacheRetention":true}`), Options: raw(`{"cacheRetention":"long"}`)})

	// A tool choice object is the caller's (params.tool_choice = options.toolChoice, :860): its member order is the caller's.
	add("tool choice object", providerWireRow{Options: raw(`{"toolChoice":{"type":"function","function":{"name":"read"}}}`)})
	add("tool choice object, function first", providerWireRow{Options: raw(`{"toolChoice":{"function":{"name":"read"},"type":"function"}}`)})

	// Each thinking format, with reasoning on and off (:876-940).
	for _, format := range []struct{ name, compat string }{
		{"openai", `{"thinkingFormat":"openai","supportsReasoningEffort":true}`},
		{"zai", `{"thinkingFormat":"zai","supportsReasoningEffort":true}`},
		{"qwen", `{"thinkingFormat":"qwen","supportsReasoningEffort":true}`},
		{"qwen with a budget field", `{"thinkingFormat":"qwen","thinkingTokenBudgetField":"thinking_budget","supportsThinkingTokenBudget":true}`},
		{"qwen-chat-template", `{"thinkingFormat":"qwen-chat-template"}`},
		{"chat-template", `{"thinkingFormat":"chat-template","chatTemplateKwargs":{"preserve_thinking":true,"enable_thinking":{"$var":"thinking.enabled"},"budget":{"$var":"thinking.budget"},"effort":{"$var":"thinking.effort","omitWhenOff":true},"a":1}}`},
		{"chat-template, sorted entry", `{"thinkingFormat":"chat-template","chatTemplateKwargs":{"a":1,"enable_thinking":{"$var":"thinking.enabled"},"preserve_thinking":true}}`},
		{"baseten", `{"thinkingFormat":"baseten","supportsReasoningEffort":true,"chatTemplateArgs":{"thinking":{"$var":"thinking.enabled"},"effort":{"$var":"thinking.effort"},"clear":false}}`},
		{"deepseek", `{"thinkingFormat":"deepseek","supportsReasoningEffort":true}`},
		{"openrouter", `{"thinkingFormat":"openrouter"}`},
	} {
		add("thinking "+format.name+": on", providerWireRow{Reasoning: true, Compat: raw(format.compat), Options: raw(`{"reasoningEffort":"high","maxTokens":4096}`)})
		add("thinking "+format.name+": off", providerWireRow{Reasoning: true, Compat: raw(format.compat)})
	}

	// Routing a catalog entry supplies: params.provider is the entry's own object (:979-980); the gateway options are built only, order (:984-990).
	add("openrouter routing", providerWireRow{Compat: raw(`{"openRouterRouting":{"order":["a","b"],"only":["a"],"ignore":["c"],"quantizations":["fp8"],"max_price":{"prompt":1,"completion":2},"allow_fallbacks":false}}`)})
	add("openrouter routing, sorted entry", providerWireRow{Compat: raw(`{"openRouterRouting":{"allow_fallbacks":false,"ignore":["c"],"only":["a"],"order":["a","b"]}}`)})
	add("vercel gateway routing", providerWireRow{Compat: raw(`{"vercelGatewayRouting":{"order":["x"],"only":["y"]}}`)})
	add("vercel gateway routing, order alone", providerWireRow{Compat: raw(`{"vercelGatewayRouting":{"order":["x"]}}`)})

	// Sampling parameters are assigned last, in the order the entry and the caller list them (:994-997).
	add("model sampling parameters", providerWireRow{Model: raw(`{"samplingParams":{"top_p":0.9,"min_p":0.1,"repetition_penalty":1.1}}`)})
	add("request sampling parameters", providerWireRow{Options: raw(`{"temperature":0.5,"samplingParams":{"top_p":0.9,"min_p":0.1,"temperature":0.3}}`)})

	// Each block kind as the last one of the conversation.
	add("last block: user string", providerWireRow{Context: wireContext(wireTools, wireUserGo)})
	add("last block: user text", providerWireRow{Context: wireContext(wireTools, wireUserNext)})
	add("last block: user image after text", providerWireRow{Context: wireContext("", wireNextLook)})
	add("last block: user text after image", providerWireRow{Context: wireContext("", wireLook)})
	add("last block: tool result text", providerWireRow{Context: after(wireText, false)})
	add("last block: tool result with an image", providerWireRow{Context: after(wireText+","+wireImage, false)})
	add("last block: tool result image alone", providerWireRow{Context: after(wireImage, false)})
	add("last block: tool result without content", providerWireRow{Context: after("", false)})
	add("last block: error tool result", providerWireRow{Context: after(wireText, true)})
	add("last block: assistant text", providerWireRow{Context: wireContext("", wireUserGo, wireAssistant(api, "custom", id, "stop", wireText))})
	add("last block: assistant thinking", providerWireRow{Context: wireContext("", wireUserGo, wireAssistant(api, "custom", id, "stop", wireText+`,{"type":"thinking","thinking":"plan","thinkingSignature":"reasoning"}`))})
	add("last block: assistant tool call", providerWireRow{Context: wireContext(wireTools, wireUserGo, called("custom"))})

	add("poisoned history", providerWireRow{Context: wirePoisoned(api, "custom", id)})
	add("poisoned history, anthropic cache control, reasoning", providerWireRow{Reasoning: true, Simple: true, Compat: raw(`{"cacheControlFormat":"anthropic","thinkingFormat":"zai"}`), Options: raw(`{"reasoning":"low"}`), Context: wirePoisoned(api, "custom", id)})
	return rows
}

func TestOpenAICompletionsWireBodyMatchesPi(t *testing.T) {
	checkProviderWireRows(t, openAICompletionsWireRows())
}
