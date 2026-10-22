package sessiongraph

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// plain is a value as the JSON of its type reads back, nil for a nil value.
func plain(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var out any
	if err := json.Unmarshal(encoded, &out); err != nil {
		panic(err)
	}
	return out
}

func pickModel(model map[string]any) any {
	if model == nil {
		return nil
	}
	reasoning, _ := model["reasoning"].(bool)
	return map[string]any{"id": model["id"], "provider": model["provider"], "api": model["api"], "name": model["name"], "reasoning": reasoning, "contextWindow": model["contextWindow"], "maxTokens": model["maxTokens"]}
}

func pickModels(models []map[string]any) []any {
	out := []any{}
	for _, model := range models {
		out = append(out, pickModel(model))
	}
	return out
}

func modelIDs(models []map[string]any, provider string) []string {
	out := []string{}
	for _, model := range models {
		if provider == "" || model["provider"] == provider {
			out = append(out, fmt.Sprint(model["id"]))
		}
	}
	return out
}

func errText(err error) string {
	if err == nil {
		return "no error"
	}
	return "error: " + err.Error()
}

func messageText(message map[string]any) string {
	var text strings.Builder
	blocks, _ := message["content"].([]any)
	for _, block := range blocks {
		if fields, ok := block.(map[string]any); ok {
			if value, ok := fields["text"].(string); ok {
				text.WriteString(value)
			}
		}
	}
	return text.String()
}

func modelRuntimeScript(ctx sdk.Context, args map[string]any) map[string]any {
	r := map[string]any{}
	auth, models, store, server := fmt.Sprint(args["mrAuth"]), fmt.Sprint(args["mrModels"]), fmt.Sprint(args["mrStore"]), fmt.Sprint(args["mrServer"])
	rt := ok(sdk.ModelRuntimeCreate(ctx, sdk.ModelRuntimeCreateOptions{AuthPath: auth, ModelsPath: &models, ModelsStorePath: store}))
	local := ok(rt.GetModel("mr-local", "m1"))
	r["catalog"] = map[string]any{
		"error": orNil(ok(rt.GetError())), "models": pickModels(ok(rt.GetModels("mr-local"))), "all": len(ok(rt.GetAllModels("mr-local"))), "one": pickModel(local),
		"missing": ok(rt.GetModel("mr-local", "nope")) == nil, "builtinListed": len(modelIDs(ok(rt.GetModels()), "anthropic")) > 0,
		"ofType": modelIDs(ok(rt.GetModelsOfType(sdk.ModelTypeChat, "mr-local")), ""), "oneOfType": pickModel(ok(rt.GetModelOfType(sdk.ModelTypeChat, "mr-local", "m1"))),
		"imagesOfType": len(ok(rt.GetModelsOfType(sdk.ModelTypeImage, "mr-local"))), "physical": pickModel(ok(rt.GetPhysicalModel("mr-local", "m1"))),
		"physicalMissing": ok(rt.GetPhysicalModel("mr-local", "nope")) == nil,
	}
	override := "override"
	r["auth"] = map[string]any{
		"configured": ok(rt.HasConfiguredAuth("mr-local")), "unconfigured": ok(rt.HasConfiguredAuth("nope-prov")), "oauth": ok(rt.IsUsingOAuth("mr-local")), "subscription": ok(rt.IsUsingSubscription("mr-local")),
		"status": plain(ok(rt.GetProviderAuthStatus("mr-local"))), "storedStatus": plain(ok(rt.GetProviderAuthStatus("stored-prov"))), "unknownStatus": plain(ok(rt.GetProviderAuthStatus("nope-prov"))),
		"byProvider": plain(ok(rt.GetAuth("mr-local", nil))), "byModel": plain(ok(rt.GetAuth(local, nil))), "unconfiguredAuth": plain(ok(rt.GetAuth("nope-prov", nil))),
		"overridden": plain(ok(rt.GetAuth("mr-local", &sdk.AuthOverrides{APIKey: &override}))),
		"check":      plain(ok(rt.CheckAuth("mr-local"))), "uncheckedProvider": plain(ok(rt.CheckAuth("nope-prov"))), "compat": plain(ok(rt.GetCompatibilityRequestConfig(local))),
	}
	check(rt.SetRuntimeAPIKey("rt-prov", "rk"))
	withKey := map[string]any{"status": plain(ok(rt.GetProviderAuthStatus("rt-prov"))), "configured": ok(rt.HasConfiguredAuth("rt-prov")), "auth": plain(ok(rt.GetAuth("rt-prov", nil)))}
	check(rt.RemoveRuntimeAPIKey("rt-prov"))
	r["runtimeKey"] = map[string]any{"withKey": withKey, "removed": plain(ok(rt.GetProviderAuthStatus("rt-prov"))), "configuredAfter": ok(rt.HasConfiguredAuth("rt-prov"))}
	credentials := []map[string]any{}
	for _, credential := range ok(rt.ListCredentials()) {
		credentials = append(credentials, map[string]any{"providerId": credential.ProviderID, "type": credential.Type})
	}
	slices.SortFunc(credentials, func(a, b map[string]any) int {
		return strings.Compare(fmt.Sprint(a["providerId"]), fmt.Sprint(b["providerId"]))
	})
	r["credentials"] = credentials

	before := ok(rt.GetRegisteredProviderIDs())
	check(rt.RegisterProvider("reg-prov", map[string]any{
		"baseUrl": server, "apiKey": "regkey", "api": "openai-completions", "headers": map[string]any{"x-reg": "1"},
		"models": []any{map[string]any{"id": "r1", "name": "Reg One", "reasoning": false, "input": []any{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 4000, "maxTokens": 500}},
	}))
	check(rt.RegisterProvider("reg-prov", map[string]any{"baseUrl": server + "/v2"}))
	config := ok(rt.GetRegisteredProviderConfig("reg-prov"))
	configModels := []string{}
	for _, model := range config["models"].([]any) {
		configModels = append(configModels, fmt.Sprint(model.(map[string]any)["id"]))
	}
	r["registered"] = map[string]any{
		"before": before, "ids": ok(rt.GetRegisteredProviderIDs()), "models": pickModels(ok(rt.GetModels("reg-prov"))), "baseUrl": config["baseUrl"], "apiKey": config["apiKey"], "api": config["api"], "headers": plain(config["headers"]), "modelIds": configModels,
		"configured": ok(rt.HasConfiguredAuth("reg-prov")), "unregisteredConfig": ok(rt.GetRegisteredProviderConfig("nope-prov")) == nil,
		"rejected": errText(rt.RegisterProvider("bad-prov", map[string]any{"models": []any{map[string]any{"id": "x"}}})),
	}
	check(rt.UnregisterProvider("reg-prov"))
	r["registered"].(map[string]any)["after"] = map[string]any{"ids": ok(rt.GetRegisteredProviderIDs()), "models": len(ok(rt.GetModels("reg-prov"))), "config": ok(rt.GetRegisteredProviderConfig("reg-prov")) == nil}

	requests := []any{}
	check(rt.RegisterVirtualModel(sdk.VirtualModel{
		Provider: "mr-virtual", ID: "v1", Name: "Virtual One", ThinkingLevels: []string{"off", "high"}, ContextWindow: 1000, MaxTokens: 100, Input: []string{"text"},
		Route: func(_ sdk.Context, request sdk.ModelRouteRequest) (sdk.ModelRoute, error) {
			var state any
			if len(request.State) > 0 {
				_ = json.Unmarshal(request.State, &state)
			}
			requests = append(requests, map[string]any{"model": request.Model["id"], "thinkingLevel": request.ThinkingLevel, "reason": string(request.Reason), "messages": len(request.Messages), "state": state})
			physical, err := rt.GetPhysicalModel("mr-local", "m1")
			return sdk.ModelRoute{Model: physical, ThinkingLevel: "off", State: map[string]any{"n": len(requests)}}, err
		},
	}))
	virtual := ok(rt.GetModel("mr-virtual", "v1"))
	messages := []map[string]any{{"role": "user", "content": "hi", "timestamp": 1}}
	route := ok(rt.ResolveModel(virtual, messages, sdk.ResolveModelOptions{Reason: sdk.ModelRouteReasonUser, ThinkingLevel: "high"}))
	second := ok(rt.ResolveModel(virtual, messages, sdk.ResolveModelOptions{Reason: sdk.ModelRouteReasonContinuation, ThinkingLevel: "off", State: json.RawMessage(`{"n":1}`)}))
	_, unknown := rt.ResolveModel(local, []map[string]any{}, sdk.ResolveModelOptions{Reason: sdk.ModelRouteReasonUser, ThinkingLevel: "off"})
	r["virtual"] = map[string]any{
		"model": pickModel(virtual), "thinkingLevels": virtual["thinkingLevels"], "route": map[string]any{"model": pickModel(route["model"].(map[string]any)), "thinkingLevel": route["thinkingLevel"], "state": route["state"]}, "second": second["state"], "requests": requests,
		"physicalIsNull": ok(rt.GetPhysicalModel("mr-virtual", "v1")) == nil, "unknown": errText(unknown),
	}
	check(rt.UnregisterVirtualModel("mr-virtual", "v1"))
	r["virtual"].(map[string]any)["after"] = ok(rt.GetModel("mr-virtual", "v1")) == nil

	allow := false
	refreshed := ok(rt.Refresh(sdk.ModelsRefreshOptions{AllowNetwork: &allow}))
	r["refresh"] = map[string]any{"aborted": refreshed.Aborted, "errors": refreshed.Errors}
	r["available"] = map[string]any{
		"ofProvider": modelIDs(ok(rt.GetAvailable("mr-local")), ""), "all": modelIDs(ok(rt.GetAvailable()), "mr-local"), "snapshot": modelIDs(ok(rt.GetAvailableSnapshot()), "mr-local"),
		"allOfProvider": modelIDs(ok(rt.GetAllAvailable("mr-local")), ""), "ofType": modelIDs(ok(rt.GetAvailableOfType(sdk.ModelTypeChat, "mr-local")), ""), "unavailableProvider": len(ok(rt.GetAvailable("nope-prov"))),
	}

	conversation := map[string]any{"systemPrompt": "Be brief.", "messages": []any{map[string]any{"role": "user", "content": "say hello", "timestamp": 1}}}
	options := map[string]any{"maxTokens": 50}
	events := []string{}
	stream := rt.StreamSimple(local, conversation, options)
	for event := range stream.Events(context.Background()) {
		events = append(events, fmt.Sprint(event["type"]))
	}
	message := stream.Result()
	completed := rt.CompleteSimple(local, conversation, options)
	full := rt.Complete(local, conversation, options)
	unknownModel := map[string]any{}
	maps.Copy(unknownModel, local)
	unknownModel["provider"] = "nope-prov"
	r["stream"] = map[string]any{
		"events": events, "stopReason": message["stopReason"], "text": messageText(message), "provider": message["provider"], "model": message["model"], "api": message["api"],
		"completed": messageText(completed), "full": full["stopReason"], "unknownProvider": rt.CompleteSimple(unknownModel, conversation, map[string]any{})["stopReason"],
	}
	classified := rt.Classify(local, sdk.ClassifierContext{State: map[string]any{}, Questions: sdk.NewOrderedObject()}, nil)
	imaged := rt.GenerateImages(local, map[string]any{"input": []any{map[string]any{"type": "text", "text": "a cat"}}}, nil)
	r["typed"] = map[string]any{"classifyStop": classified.StopReason, "classifyError": orEmpty(classified.ErrorMessage), "imagesStop": imaged["stopReason"], "imagesError": imaged["errorMessage"]}

	seen, events2 := []any{}, []any{}
	credential := ok(rt.Login("openai", "api_key", sdk.AuthInteraction{
		Prompt: func(prompt map[string]any) (string, error) {
			seen = append(seen, prompt["type"])
			return "sk-login", nil
		},
		Notify: func(event map[string]any) error { events2 = append(events2, event["type"]); return nil },
	}, nil))
	listed := func() []string {
		ids := []string{}
		for _, entry := range ok(rt.ListCredentials()) {
			ids = append(ids, entry.ProviderID)
		}
		slices.Sort(ids)
		return ids
	}
	r["login"] = map[string]any{"credential": plain(credential), "prompts": seen, "events": events2, "listed": listed(), "status": plain(ok(rt.GetProviderAuthStatus("openai")))}
	check(rt.Logout("openai"))
	r["login"].(map[string]any)["afterLogout"] = listed()
	r["stored"] = ok(readJSONFile(auth))
	return r
}

func orEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func readJSONFile(path string) (any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out any
	return out, json.Unmarshal(data, &out)
}
