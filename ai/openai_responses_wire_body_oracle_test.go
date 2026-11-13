package ai_test

import (
	"encoding/json"
	"strconv"
	"testing"
)

// pi: packages/ai/src/api/openai-responses.ts
// pi: packages/ai/src/api/openai-responses-shared.ts
// pi: packages/ai/src/api/azure-openai-responses.ts

// openAIResponsesWireRows are the request shapes buildParams and convertResponsesMessages have: each credential shape and route, each
// optional member of the request, each input item kind as the last one (messages, function calls and outputs, replayed reasoning items, the
// items of client tool search and of additional tools), a poisoned history, and a caller's tool choice object.
func openAIResponsesWireRows() []providerWireRow {
	const api, id = "openai-responses", "gpt-test"
	quoted := func(text string) string {
		encoded, _ := json.Marshal(text)
		return string(encoded)
	}
	// A reasoning item is stored as the text of the item the provider sent and replayed as that object (openai-responses-shared.ts:330-340).
	reasoning := func(item string) string {
		return `{"type":"thinking","thinking":"plan","thinkingSignature":` + quoted(item) + `}`
	}
	signedText := `{"type":"text","text":"I will read.","textSignature":` + quoted(`{"v":1,"id":"msg_1","phase":"commentary"}`) + `}`
	called := func(provider string) string {
		return wireAssistant(api, provider, id, "toolUse", reasoning(`{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"plan"}],"encrypted_content":"enc"}`)+","+signedText+","+wireCall("call_1|fc_1"))
	}
	full := func(provider string) json.RawMessage {
		return wireContext(wireSystem+","+wireTools, wireUserGo, called(provider), wireResult("call_1|fc_1", wireText, false), wireUserNext)
	}
	rich := func(provider string) json.RawMessage {
		return wireContext(wireSystem+","+wireTools, wireLook, called(provider), wireResult("call_1|fc_1", wireText+","+wireImage, false), wireNextLook)
	}
	after := func(content string, isError bool) json.RawMessage {
		return wireContext(wireTools, wireUserGo, called("openai"), wireResult("call_1|fc_1", content, isError))
	}
	raw := func(text string) json.RawMessage { return json.RawMessage(text) }
	loaded := func(stamp int, text string, tools string) string {
		return `{"role":"system","content":` + quoted(text) + `,"timestamp":` + strconv.Itoa(stamp) + `,"toolsAdded":[` + tools + `]}`
	}
	removed := `{"role":"system","content":"","timestamp":6,"toolsRemoved":[{"name":"read"}]}`

	var rows []providerWireRow
	add := func(name string, row providerWireRow) {
		row.Name, row.ID = name, id
		if row.API == "" {
			row.API = api
		}
		if row.Provider == "" {
			row.Provider, row.BaseURL = "openai", "https://api.openai.com/v1"
		}
		if row.APIKey == "" {
			row.APIKey = "sk-x"
		}
		if row.Options == nil {
			row.Options = raw(`{}`)
		}
		if row.Context == nil {
			row.Context = full(row.Provider)
		}
		rows = append(rows, row)
	}

	for _, route := range []struct{ name, api, provider, baseURL, key string }{
		{"openai with an api key", api, "openai", "https://api.openai.com/v1", "sk-x"},
		{"github-copilot with oauth", api, "github-copilot", "https://api.individual.githubcopilot.com", "tid=1;exp=9999999999"},
		{"custom base url", api, "custom", "http://127.0.0.1:9/v1", "k"},
		{"xai", api, "xai", "https://api.x.ai/v1", "k"},
		{"opencode", api, "opencode", "https://opencode.ai/zen/v1", "k"},
		{"azure", "azure-openai-responses", "azure-openai-responses", "https://resource.openai.azure.com/openai/v1", "k"},
	} {
		row := providerWireRow{API: route.api, Provider: route.provider, BaseURL: route.baseURL, APIKey: route.key}
		add("route "+route.name+": replay", row)
		row.Context, row.Options = rich(route.provider), raw(`{"maxTokens":900,"temperature":0.2,"toolChoice":"auto","sessionId":"s1"}`)
		add("route "+route.name+": images, temperature, tool choice", row)
		row.Reasoning, row.Simple, row.Options = true, true, raw(`{"maxTokens":900,"reasoning":"high","toolChoice":"required","sessionId":"s1","cacheRetention":"long"}`)
		add("route "+route.name+": images, reasoning, tool choice, long retention", row)
	}

	add("no system prompt, no tools, no options", providerWireRow{Context: wireContext("", wireUserGo)})
	add("no message", providerWireRow{Context: wireContext("")})
	add("system prompt, no tools", providerWireRow{Context: wireContext(wireSystem, wireUserGo)})
	add("system prompt of a reasoning model", providerWireRow{Reasoning: true, Context: wireContext(wireSystem, wireUserGo)})
	add("max tokens", providerWireRow{Options: raw(`{"maxTokens":64}`)})
	add("temperature zero", providerWireRow{Options: raw(`{"temperature":0}`)})
	for _, retention := range []string{"none", "short", "long"} {
		add("session, retention "+retention, providerWireRow{Options: raw(`{"sessionId":"s1","cacheRetention":"` + retention + `"}`)})
	}
	add("service tier flex", providerWireRow{Options: raw(`{"serviceTier":"flex"}`)})
	add("service tier priority", providerWireRow{Options: raw(`{"serviceTier":"priority"}`)})
	add("text verbosity", providerWireRow{Options: raw(`{"textVerbosity":"high"}`)})
	add("reasoning effort and summary", providerWireRow{Reasoning: true, Options: raw(`{"reasoningEffort":"high","reasoningSummary":"detailed"}`)})
	add("reasoning summary alone", providerWireRow{Reasoning: true, Options: raw(`{"reasoningSummary":"concise"}`)})
	add("reasoning off", providerWireRow{Reasoning: true, Simple: true, Options: raw(`{"reasoning":"off"}`)})
	add("reasoning model, no reasoning option", providerWireRow{Reasoning: true})
	add("tool choice none", providerWireRow{Options: raw(`{"toolChoice":"none"}`)})
	// A tool choice object is the caller's (params.tool_choice = options.toolChoice): its member order is the caller's.
	add("tool choice object", providerWireRow{Options: raw(`{"toolChoice":{"type":"function","name":"read"}}`)})
	add("tool choice object, name first", providerWireRow{Options: raw(`{"toolChoice":{"name":"read","type":"function"}}`)})

	// Each input item kind as the last one.
	add("last item: user string", providerWireRow{Context: wireContext(wireTools, wireUserGo)})
	add("last item: user text", providerWireRow{Context: wireContext(wireTools, wireUserNext)})
	add("last item: user image after text", providerWireRow{Context: wireContext("", wireNextLook)})
	add("last item: user text after image", providerWireRow{Context: wireContext("", wireLook)})
	add("last item: function call output text", providerWireRow{Context: after(wireText, false)})
	add("last item: function call output with an image", providerWireRow{Context: after(wireText+","+wireImage, false)})
	add("last item: function call output image alone", providerWireRow{Context: after(wireImage, false)})
	add("last item: function call output without content", providerWireRow{Context: after("", false)})
	add("last item: error function call output", providerWireRow{Context: after(wireText, true)})
	add("last item: assistant message", providerWireRow{Context: wireContext("", wireUserGo, wireAssistant(api, "openai", id, "stop", signedText))})
	add("last item: assistant message without a signature", providerWireRow{Context: wireContext("", wireUserGo, wireAssistant(api, "openai", id, "stop", wireText))})
	add("last item: reasoning", providerWireRow{Reasoning: true, Context: wireContext("", wireUserGo, wireAssistant(api, "openai", id, "stop", signedText+","+reasoning(`{"type":"reasoning","id":"rs_2","summary":[],"encrypted_content":"enc"}`)))})
	// The provider writes id before type; the stored text is replayed as it was stored.
	add("last item: reasoning as the provider wrote it", providerWireRow{Reasoning: true, Context: wireContext("", wireUserGo, wireAssistant(api, "openai", id, "stop", signedText+","+reasoning(`{"id":"rs_2","type":"reasoning","encrypted_content":"enc","summary":[{"text":"plan","type":"summary_text"}]}`)))})
	// Pi pushes JSON.parse(thinkingSignature) into the input (openai-responses-shared.ts:337-338): whatever the stored text holds is the item.
	stored := func(signature string) json.RawMessage {
		return wireContext("", wireUserGo, wireAssistant(api, "openai", id, "stop", signedText+","+reasoning(signature)), wireUserNext)
	}
	add("stored reasoning with members of its own", providerWireRow{Reasoning: true, Context: stored(`{"id":"rs_3","type":"reasoning","status":"completed","encrypted_content":"enc","summary":[],"content":[{"text":"t <b>","type":"reasoning_text"}],"x":{"b":1.0,"a":2,"1":"one"}}`)})
	add("stored signature of another object", providerWireRow{Reasoning: true, Context: stored(`{"x":1,"type":"message"}`)})
	add("stored signature of a string", providerWireRow{Reasoning: true, Context: stored(`"plain"`)})
	add("stored signature of null", providerWireRow{Reasoning: true, Context: stored(`null`)})
	// JSON.parse throws: Pi sends no request.
	add("stored signature that is not JSON", providerWireRow{Reasoning: true, Context: stored(`EqQB`)})
	add("stored signature that is not JSON, poisoned history", providerWireRow{Reasoning: true, Context: wireContext(wireSystem+","+wireTools, wireUserGo,
		wireAssistant(api, "openai", id, "toolUse", reasoning(`{"id":"rs_4","type":"reasoning"`)+","+wireCall("call_1|fc_1")), wireResult("call_1|fc_1", wireText, false),
		wireAssistant(api, "openai", id, "error", ``), wireUserNext)})

	// azure-openai-responses.ts buildParams (:204-262) reads no cache retention, service tier or max output token compat.
	azure := providerWireRow{API: "azure-openai-responses", Provider: "azure-openai-responses", BaseURL: "https://resource.openai.azure.com/openai/v1", APIKey: "k"}
	for _, option := range []struct{ name, compat, options string }{
		{"session, retention none", ``, `{"sessionId":"s1","cacheRetention":"none"}`},
		{"session, retention short", ``, `{"sessionId":"s1","cacheRetention":"short"}`},
		{"session, retention long", `{"supportsLongCacheRetention":true}`, `{"sessionId":"s1","cacheRetention":"long"}`},
		{"retention long, explicit cache mode", `{"supportsExplicitPromptCacheMode":true,"supportsLongCacheRetention":true}`, `{"cacheRetention":"long"}`},
		{"retention none, explicit cache mode", `{"supportsExplicitPromptCacheMode":true}`, `{"cacheRetention":"none"}`},
		{"prompt cache key disabled", `{"promptCacheKeyMode":"disabled"}`, `{"sessionId":"s1"}`},
		{"service tier", ``, `{"serviceTier":"priority"}`},
		{"max tokens, model without max_output_tokens", `{"supportsMaxOutputTokens":false}`, `{"maxTokens":64}`},
		{"text verbosity", ``, `{"textVerbosity":"high"}`},
	} {
		row := azure
		if option.compat != "" {
			row.Compat = raw(option.compat)
		}
		row.Options = raw(option.options)
		add("azure: "+option.name, row)
	}
	add("last item: function call", providerWireRow{Context: wireContext(wireTools, wireUserGo, called("openai"))})

	// Tools declared during the conversation (openai-responses-shared.ts:236-264): client tool search, additional tools, or a notice.
	for _, transport := range []struct{ name, compat string }{
		{"tool search", `{"supportsMidConvoSystemMessages":true,"supportsToolSearch":true}`},
		{"additional tools", `{"supportsMidConvoSystemMessages":true,"supportsAdditionalTools":true}`},
		{"mid-conversation system", `{"supportsMidConvoSystemMessages":true}`},
		{"folded", `{}`},
	} {
		compat := raw(transport.compat)
		add(transport.name+": last item a declaration", providerWireRow{Reasoning: true, Compat: compat, Context: wireContext("", loaded(0, "base", wireReadTool), wireUserGo, loaded(2, "", wireFindTool))})
		add(transport.name+": two declarations and a removal", providerWireRow{Reasoning: true, Compat: compat,
			Context: wireContext("", loaded(0, "base", wireReadTool), wireUserGo, loaded(2, "update", wireFindTool), wireUserNext, removed, loaded(7, "", wireReadTool+","+wireFindTool), wireUserNext)})
		add(transport.name+": declaration after a call", providerWireRow{Compat: compat,
			Context: wireContext("", loaded(0, "base", wireReadTool), wireUserGo, called("openai"), wireResult("call_1|fc_1", wireText, false), loaded(4, "", wireFindTool), wireUserNext)})
	}

	add("poisoned history", providerWireRow{Context: wirePoisoned(api, "openai", id)})
	add("poisoned history, reasoning", providerWireRow{Reasoning: true, Simple: true, Options: raw(`{"reasoning":"low"}`), Context: wirePoisoned(api, "openai", id)})
	return rows
}

// wireRowsOf are the rows of one API kind.
func wireRowsOf(rows []providerWireRow, api string) []providerWireRow {
	var kept []providerWireRow
	for _, row := range rows {
		if row.API == api {
			kept = append(kept, row)
		}
	}
	return kept
}

func TestOpenAIResponsesWireBodyMatchesPi(t *testing.T) {
	checkProviderWireRows(t, wireRowsOf(openAIResponsesWireRows(), "openai-responses"))
}

// azure-openai-responses.ts builds its own request (buildParams, :173-262) over the shared converter.
func TestAzureOpenAIResponsesWireBodyMatchesPi(t *testing.T) {
	checkProviderWireRows(t, wireRowsOf(openAIResponsesWireRows(), "azure-openai-responses"))
}
