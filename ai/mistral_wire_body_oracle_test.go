package ai_test

import (
	"encoding/json"
	"testing"
)

// pi: packages/ai/src/api/mistral-conversations.ts

// mistralWireRows are the request shapes buildChatPayload and toChatMessages have: each route, each optional member of the request, each
// tool choice, each block kind as the last one of the conversation and a poisoned history.
//
// The tool choice of a named function has no print-mode scenario: Pi's CLI passes no tool choice to a provider (nothing under
// packages/coding-agent/src names toolChoice), so no run of the two CLIs sends the object, and number 37 of providers-faux-streaming, held
// for one, is free. The object's member order is proven by the rows "tool choice object", "tool choice object, function first, extra
// members", "route ...: images, reasoning, tool choice object" and "poisoned history, reasoning, tool choice object" of this table, and in
// bytes by TestMistralMessagesPayloadMatchesPi, whose probes draw the object.
func mistralWireRows() []providerWireRow {
	const api, id = "mistral-conversations", "mistral-small-latest"
	called := func(provider string) string {
		return wireAssistant(api, provider, id, "toolUse", `{"type":"thinking","thinking":"plan"},{"type":"text","text":"I will read."},`+wireCall("abc123456"))
	}
	full := func(provider string) json.RawMessage {
		return wireContext(wireSystem+","+wireTools, wireUserGo, called(provider), wireResult("abc123456", wireText, false), wireUserNext)
	}
	rich := func(provider string) json.RawMessage {
		return wireContext(wireSystem+","+wireTools, wireLook, called(provider), wireResult("abc123456", wireText+","+wireImage, false), wireNextLook)
	}
	after := func(content string, isError bool) json.RawMessage {
		return wireContext(wireTools, wireUserGo, called("mistral"), wireResult("abc123456", content, isError))
	}
	raw := func(text string) json.RawMessage { return json.RawMessage(text) }

	var rows []providerWireRow
	add := func(name string, row providerWireRow) {
		row.Name, row.API, row.ID = name, api, id
		if row.Provider == "" {
			row.Provider, row.BaseURL = "mistral", "https://api.mistral.ai"
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

	for _, route := range []struct{ name, provider, baseURL string }{
		{"mistral with an api key", "mistral", "https://api.mistral.ai"},
		{"custom base url", "custom", "http://127.0.0.1:9"},
	} {
		row := providerWireRow{Provider: route.provider, BaseURL: route.baseURL}
		add("route "+route.name+": replay", row)
		row.Context, row.Options = rich(route.provider), raw(`{"maxTokens":900,"temperature":0.2,"toolChoice":"auto","sessionId":"s1"}`)
		add("route "+route.name+": images, temperature, tool choice", row)
		row.Reasoning, row.Simple, row.Options = true, true, raw(`{"maxTokens":900,"reasoning":"high","toolChoice":{"type":"function","function":{"name":"read"}},"sessionId":"s1"}`)
		add("route "+route.name+": images, reasoning, tool choice object", row)
	}

	add("no system prompt, no tools, no options", providerWireRow{Context: wireContext("", wireUserGo)})
	add("no message", providerWireRow{Context: wireContext("")})
	add("system prompt, no tools", providerWireRow{Context: wireContext(wireSystem, wireUserGo)})
	add("max tokens", providerWireRow{Options: raw(`{"maxTokens":64}`)})
	add("temperature zero", providerWireRow{Options: raw(`{"temperature":0}`)})
	add("session", providerWireRow{Options: raw(`{"sessionId":"s1","cacheRetention":"long"}`)})
	add("prompt mode", providerWireRow{Reasoning: true, Options: raw(`{"promptMode":"reasoning"}`)})
	add("reasoning effort", providerWireRow{Reasoning: true, Options: raw(`{"reasoningEffort":"high"}`)})
	for _, choice := range []string{"auto", "none", "any", "required"} {
		add("tool choice "+choice, providerWireRow{Options: raw(`{"toolChoice":"` + choice + `"}`)})
	}
	// Pi builds the object anew from the caller's function name (mapToolChoice, :895-906): type, then function, whatever the caller's order and members.
	add("tool choice object", providerWireRow{Options: raw(`{"toolChoice":{"type":"function","function":{"name":"read"}}}`)})
	add("tool choice object, function first, extra members", providerWireRow{Options: raw(`{"toolChoice":{"function":{"strict":true,"name":"find"},"type":"function","x":1}}`)})

	add("last block: user string", providerWireRow{Context: wireContext(wireTools, wireUserGo)})
	add("last block: user text", providerWireRow{Context: wireContext(wireTools, wireUserNext)})
	add("last block: user image after text", providerWireRow{Context: wireContext("", wireNextLook)})
	add("last block: user text after image", providerWireRow{Context: wireContext("", wireLook)})
	add("last block: tool result text", providerWireRow{Context: after(wireText, false)})
	add("last block: tool result with an image", providerWireRow{Context: after(wireText+","+wireImage, false)})
	add("last block: tool result image alone", providerWireRow{Context: after(wireImage, false)})
	add("last block: tool result without content", providerWireRow{Context: after("", false)})
	add("last block: error tool result", providerWireRow{Context: after(wireText, true)})
	add("last block: assistant text", providerWireRow{Context: wireContext("", wireUserGo, wireAssistant(api, "mistral", id, "stop", wireText))})
	add("last block: assistant thinking", providerWireRow{Context: wireContext("", wireUserGo, wireAssistant(api, "mistral", id, "stop", wireText+`,{"type":"thinking","thinking":"plan"}`))})
	add("last block: assistant tool call", providerWireRow{Context: wireContext(wireTools, wireUserGo, called("mistral"))})

	add("poisoned history", providerWireRow{Context: wirePoisoned(api, "mistral", id)})
	add("poisoned history, reasoning, tool choice object", providerWireRow{Reasoning: true, Simple: true, Options: raw(`{"reasoning":"low","toolChoice":{"type":"function","function":{"name":"read"}}}`), Context: wirePoisoned(api, "mistral", id)})
	return rows
}

func TestMistralWireBodyMatchesPi(t *testing.T) {
	checkProviderWireRows(t, mistralWireRows())
}
