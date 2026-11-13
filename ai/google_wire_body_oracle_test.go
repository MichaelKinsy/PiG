package ai_test

import (
	"encoding/json"
	"testing"
)

// pi: packages/ai/src/api/google-generative-ai.ts
// pi: packages/ai/src/api/google-vertex.ts
// pi: packages/ai/src/api/google-shared.ts

// googleWireRows are the request shapes buildParams, convertMessages and convertTools have, as the @google/genai client writes them: each
// route, each optional member of the generation config, each part kind as the last one of the conversation and a poisoned history.
func googleWireRows() []providerWireRow {
	const api, id, official = "google-generative-ai", "gemini-2.5-flash", "https://generativelanguage.googleapis.com/v1beta"
	called := func(api, provider, id string) string {
		return wireAssistant(api, provider, id, "toolUse", `{"type":"thinking","thinking":"plan","thinkingSignature":"c2ln"},{"type":"text","text":"I will read."},{"type":"toolCall","id":"call_1","name":"read","arguments":{"path":"a.txt","offset":2},"thoughtSignature":"c2ln"}`)
	}
	full := func(api, provider, id string) json.RawMessage {
		return wireContext(wireSystem+","+wireTools, wireUserGo, called(api, provider, id), wireResult("call_1", wireText, false), wireUserNext)
	}
	rich := func(api, provider, id string) json.RawMessage {
		return wireContext(wireSystem+","+wireTools, wireLook, called(api, provider, id), wireResult("call_1", wireText+","+wireImage, false), wireNextLook)
	}
	after := func(id, content string, isError bool) json.RawMessage {
		return wireContext(wireTools, wireUserGo, called(api, "google", id), wireResult("call_1", content, isError))
	}
	raw := func(text string) json.RawMessage { return json.RawMessage(text) }

	var rows []providerWireRow
	add := func(name string, row providerWireRow) {
		row.Name = name
		if row.API == "" {
			row.API = api
		}
		if row.ID == "" {
			row.ID = id
		}
		if row.Provider == "" {
			row.Provider, row.BaseURL = "google", official
		}
		if row.APIKey == "" {
			row.APIKey = "k"
		}
		if row.Options == nil {
			row.Options = raw(`{}`)
		}
		if row.Context == nil {
			row.Context = full(row.API, row.Provider, row.ID)
		}
		rows = append(rows, row)
	}

	for _, route := range []struct{ name, api, provider, baseURL, id string }{
		{"google with an api key", api, "google", official, id},
		{"google, gemini 3", api, "google", official, "gemini-3-pro-preview"},
		{"custom base url", api, "custom", "http://127.0.0.1:9/v1beta", id},
		{"vertex with an api key", "google-vertex", "google-vertex", official, id},
		{"vertex, gemini 3", "google-vertex", "google-vertex", official, "gemini-3-pro-preview"},
	} {
		row := providerWireRow{API: route.api, ID: route.id, Provider: route.provider, BaseURL: route.baseURL}
		add("route "+route.name+": replay", row)
		row.Context, row.Options = rich(route.api, route.provider, route.id), raw(`{"maxTokens":900,"temperature":0.2,"toolChoice":"auto"}`)
		add("route "+route.name+": images, temperature, tool choice", row)
		row.Reasoning, row.Simple, row.Options = true, true, raw(`{"maxTokens":900,"reasoning":"high","toolChoice":"any"}`)
		add("route "+route.name+": images, reasoning, tool choice", row)
	}

	add("no system prompt, no tools, no options", providerWireRow{Context: wireContext("", wireUserGo)})
	add("no message", providerWireRow{Context: wireContext("")})
	add("system prompt, no tools", providerWireRow{Context: wireContext(wireSystem, wireUserGo)})
	add("max tokens", providerWireRow{Options: raw(`{"maxTokens":64}`)})
	add("temperature zero", providerWireRow{Options: raw(`{"temperature":0}`)})
	add("max tokens and temperature", providerWireRow{Options: raw(`{"maxTokens":64,"temperature":0.7}`)})
	for _, choice := range []string{"auto", "any", "none"} {
		add("tool choice "+choice, providerWireRow{Options: raw(`{"toolChoice":"` + choice + `"}`)})
	}
	add("thinking with a budget", providerWireRow{Reasoning: true, Options: raw(`{"thinking":{"enabled":true,"budgetTokens":1024}}`)})
	add("thinking without a budget", providerWireRow{Reasoning: true, Options: raw(`{"thinking":{"enabled":true}}`)})
	add("thinking with a level", providerWireRow{ID: "gemini-3-pro-preview", Reasoning: true, Options: raw(`{"thinking":{"enabled":true,"level":"HIGH"}}`)})
	add("thinking disabled", providerWireRow{Reasoning: true, Options: raw(`{"thinking":{"enabled":false}}`)})
	add("thinking disabled, gemini 3", providerWireRow{ID: "gemini-3-pro-preview", Reasoning: true, Options: raw(`{"thinking":{"enabled":false}}`)})
	add("reasoning off", providerWireRow{Reasoning: true, Simple: true, Options: raw(`{"reasoning":"off"}`)})
	add("reasoning model, no reasoning option", providerWireRow{Reasoning: true})
	add("thinking, max tokens, temperature, tool choice", providerWireRow{Reasoning: true, Options: raw(`{"maxTokens":64,"temperature":0.7,"toolChoice":"none","thinking":{"enabled":true,"budgetTokens":1024}}`)})

	add("last part: user string", providerWireRow{Context: wireContext(wireTools, wireUserGo)})
	add("last part: user text", providerWireRow{Context: wireContext(wireTools, wireUserNext)})
	add("last part: user image after text", providerWireRow{Context: wireContext("", wireNextLook)})
	add("last part: user text after image", providerWireRow{Context: wireContext("", wireLook)})
	for _, model := range []string{id, "gemini-3-pro-preview"} {
		add("last part: function response text, "+model, providerWireRow{ID: model, Context: after(model, wireText, false)})
		add("last part: function response with an image, "+model, providerWireRow{ID: model, Context: after(model, wireText+","+wireImage, false)})
		add("last part: function response image alone, "+model, providerWireRow{ID: model, Context: after(model, wireImage, false)})
		add("last part: function response without content, "+model, providerWireRow{ID: model, Context: after(model, "", false)})
		add("last part: error function response, "+model, providerWireRow{ID: model, Context: after(model, wireText, true)})
	}
	add("last part: model text", providerWireRow{Context: wireContext("", wireUserGo, wireAssistant(api, "google", id, "stop", wireText))})
	add("last part: model thought", providerWireRow{Context: wireContext("", wireUserGo, wireAssistant(api, "google", id, "stop", wireText+`,{"type":"thinking","thinking":"plan","thinkingSignature":"c2ln"}`))})
	add("last part: function call", providerWireRow{Context: wireContext(wireTools, wireUserGo, called(api, "google", id))})

	add("poisoned history", providerWireRow{Context: wirePoisoned(api, "google", id)})
	add("poisoned history, gemini 3, reasoning", providerWireRow{ID: "gemini-3-pro-preview", Reasoning: true, Simple: true, Options: raw(`{"reasoning":"low"}`), Context: wirePoisoned(api, "google", "gemini-3-pro-preview")})
	return rows
}

func TestGoogleWireBodyMatchesPi(t *testing.T) {
	checkProviderWireRows(t, googleWireRows())
}
