package sessiongraph

import (
	"context"
	"fmt"
	"maps"
	"slices"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// modelRegistryScript is `new ModelRegistry(runtime)` over the parts of testdata/session-graph.mjs modelRegistryScript; the report is Pi's own run's, except `builtin` (D78: Pi answers a builtin provider's Provider object, this SDK fails with the D78 error and the test expects that).
func modelRegistryScript(ctx sdk.Context, args map[string]any) map[string]any {
	r := map[string]any{}
	auth, models, store, server := fmt.Sprint(args["mrAuth"]), fmt.Sprint(args["mrModels"]), fmt.Sprint(args["mrStore"]), fmt.Sprint(args["mrServer"])
	rt := ok(sdk.ModelRuntimeCreate(ctx, sdk.ModelRuntimeCreateOptions{AuthPath: auth, ModelsPath: &models, ModelsStorePath: store}))
	registry := sdk.NewModelRegistry(rt)
	of := func(list []map[string]any, provider string) []map[string]any {
		out := []map[string]any{}
		for _, model := range list {
			if model["provider"] == provider {
				out = append(out, model)
			}
		}
		return out
	}
	local := ok(registry.Find("mr-local", "m1"))
	availableOfType := modelIDs(ok(registry.GetAvailableOfType(sdk.ModelTypeChat, "mr-local")), "")
	r["catalog"] = map[string]any{
		"error": orNil(ok(registry.GetError())), "all": pickModels(of(ok(registry.GetAll()), "mr-local")), "builtinListed": len(of(ok(registry.GetAll()), "anthropic")) > 0,
		"available": modelIDs(of(ok(registry.GetAvailable()), "mr-local"), ""), "one": pickModel(local), "missing": ok(registry.Find("mr-local", "nope")) == nil,
		"findOfType": pickModel(ok(registry.FindOfType(sdk.ModelTypeChat, "mr-local", "m1"))), "ofType": modelIDs(ok(registry.GetModelsOfType(sdk.ModelTypeChat, "mr-local")), ""), "oneOfType": pickModel(ok(registry.GetModelOfType(sdk.ModelTypeChat, "mr-local", "m1"))),
		"oneOfTypeMissing": ok(registry.GetModelOfType(sdk.ModelTypeImage, "mr-local", "m1")) == nil, "imagesOfType": len(ok(registry.GetModelsOfType(sdk.ModelTypeImage, "mr-local"))), "availableOfType": availableOfType,
	}
	unknown := map[string]any{}
	maps.Copy(unknown, local)
	unknown["provider"] = "nope-prov"
	apiKey := func(provider string) any {
		if key := registry.GetApiKeyForProvider(provider); key != nil {
			return *key
		}
		return nil
	}
	r["auth"] = map[string]any{
		"configured": ok(registry.HasConfiguredAuth(local)), "unconfigured": ok(registry.HasConfiguredAuth(unknown)), "oauth": ok(registry.IsUsingOAuth(local)),
		"status": plain(ok(registry.GetProviderAuthStatus("mr-local"))), "storedStatus": plain(ok(registry.GetProviderAuthStatus("stored-prov"))), "unknownStatus": plain(ok(registry.GetProviderAuthStatus("nope-prov"))),
		"displayLocal": ok(registry.GetProviderDisplayName("mr-local")), "displayBuiltin": ok(registry.GetProviderDisplayName("anthropic")), "displayUnknown": ok(registry.GetProviderDisplayName("nope-prov")),
		"providerAuth": plain(ok(registry.GetProviderAuth("mr-local"))), "providerAuthNone": plain(ok(registry.GetProviderAuth("nope-prov"))),
		"apiKey": apiKey("mr-local"), "apiKeyNone": apiKey("nope-prov"),
		"request": plain(ok(registry.GetApiKeyAndHeaders(local))), "requestUnconfigured": plain(ok(registry.GetApiKeyAndHeaders(unknown))),
	}

	before := ok(registry.GetRegisteredProviderIDs())
	check(registry.RegisterProvider("reg-prov", sdk.ProviderConfig{
		"baseUrl": server, "apiKey": "regkey", "api": "openai-completions", "headers": map[string]any{"x-reg": "1"},
		"models": []any{map[string]any{"id": "r1", "name": "Reg One", "reasoning": false, "input": []any{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 4000, "maxTokens": 500}},
	}))
	config := ok(registry.GetRegisteredProviderConfig("reg-prov"))
	configModels := []string{}
	for _, model := range config["models"].([]any) {
		configModels = append(configModels, fmt.Sprint(model.(map[string]any)["id"]))
	}
	r["registered"] = map[string]any{
		"before": before, "ids": ok(registry.GetRegisteredProviderIDs()), "models": pickModels(of(ok(registry.GetAll()), "reg-prov")), "baseUrl": config["baseUrl"], "apiKey": config["apiKey"], "api": config["api"], "headers": plain(config["headers"]), "modelIds": configModels,
		"unregisteredConfig": ok(registry.GetRegisteredProviderConfig("nope-prov")) == nil,
		"rejected":           errText(registry.RegisterProvider("bad-prov", sdk.ProviderConfig{"models": []any{map[string]any{"id": "x"}}})),
	}
	check(registry.UnregisterProvider("reg-prov"))
	r["registered"].(map[string]any)["after"] = map[string]any{"ids": ok(registry.GetRegisteredProviderIDs()), "models": len(of(ok(registry.GetAll()), "reg-prov")), "config": ok(registry.GetRegisteredProviderConfig("reg-prov")) == nil}

	nativeModel := map[string]any{"id": "n1", "name": "N One", "api": "nat-api", "provider": "nreg", "reasoning": false, "input": []any{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 4000, "maxTokens": 500}
	message := func(model map[string]any, text string) map[string]any {
		return map[string]any{
			"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}, "api": model["api"], "provider": model["provider"], "model": model["id"],
			"usage":      map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}},
			"stopReason": "stop", "timestamp": 1,
		}
	}
	scripted := func(tag string) sdk.ProviderStreamFunc {
		return func(model, _ map[string]any, _ sdk.ProviderStreamOptions) (*sdk.ModelEventStream, error) {
			stream := sdk.CreateAssistantMessageEventStream()
			stream.Push(map[string]any{"type": "done", "reason": "stop", "message": message(model, tag+":"+model["id"].(string))})
			return stream, nil
		}
	}
	provider := ok(sdk.CreateProvider(sdk.CreateProviderOptions{
		ID: "nreg", Name: "Native Reg",
		Auth: sdk.ProviderAuth{APIKey: &sdk.APIKeyAuth{Name: "Key", Check: func(sdk.APIKeyAuthInput) (*sdk.AuthCheck, error) {
			return &sdk.AuthCheck{Type: "api_key", Source: new("ext check")}, nil
		}, Resolve: func(sdk.APIKeyAuthInput) (*sdk.AuthResult, error) {
			return &sdk.AuthResult{Auth: map[string]any{"apiKey": "nk"}}, nil
		}}},
		Models: []map[string]any{nativeModel}, APIs: map[string]sdk.ProviderStreams{"nat-api": {Stream: scripted("n.stream"), StreamSimple: scripted("n.simple")}},
	}))
	check(registry.RegisterNativeProvider(provider))
	conversation := map[string]any{"systemPrompt": "Be brief.", "messages": []any{map[string]any{"role": "user", "content": "say hello", "timestamp": 1}}}
	nativeStream := registry.StreamSimple(nativeModel, conversation, map[string]any{})
	nativeEvents := []string{}
	for event := range nativeStream.Events(context.Background()) {
		nativeEvents = append(nativeEvents, fmt.Sprint(event["type"]))
	}
	nativeMessage := nativeStream.Result()
	offline := false
	ok(registry.Refresh(sdk.ModelsRefreshOptions{AllowNetwork: &offline}))
	idOf := func(p *sdk.Provider, err error) any {
		if p == nil {
			check(err)
			return nil
		}
		return p.ID
	}
	registered := ok(registry.GetRegisteredNativeProvider("nreg"))
	r["native"] = map[string]any{
		"registered": idOf(registered, nil), "same": registered == provider, "viaGetProvider": idOf(registry.GetProvider("nreg")), "viaRuntime": idOf(rt.GetProvider("nreg")),
		"viaRuntimeRegistered": idOf(rt.GetRegisteredNativeProvider("nreg")), "none": ok(registry.GetRegisteredNativeProvider("nope-prov")) == nil, "noneProvider": ok(registry.GetProvider("nope-prov")) == nil,
		"ids": ok(registry.GetRegisteredProviderIDs()), "displayName": ok(registry.GetProviderDisplayName("nreg")), "models": modelIDs(of(ok(registry.GetAll()), "nreg"), ""),
		"auth": plain(ok(registry.GetProviderAuth("nreg"))), "request": plain(ok(registry.GetApiKeyAndHeaders(nativeModel))),
		"events": nativeEvents, "text": messageText(nativeMessage), "stopReason": nativeMessage["stopReason"], "completed": registry.Complete(nativeModel, conversation, map[string]any{})["stopReason"],
		"configured": ok(registry.HasConfiguredAuth(nativeModel)),
	}
	check(registry.UnregisterProvider("nreg"))
	r["native"].(map[string]any)["after"] = map[string]any{
		"registered": ok(registry.GetRegisteredNativeProvider("nreg")) == nil, "provider": ok(registry.GetProvider("nreg")) == nil, "models": len(of(ok(registry.GetAll()), "nreg")),
	}
	// A stored credential makes registration mark the provider configured at once (model-runtime.ts markProvisionallyConfigured); its auth check finds no key, so the availability refresh the registration launched replaces the mark and the settled state is unconfigured.
	storedModel := map[string]any{}
	maps.Copy(storedModel, nativeModel)
	storedModel["provider"] = "stored-prov"
	storedProvider := ok(sdk.CreateProvider(sdk.CreateProviderOptions{
		ID: "stored-prov", Name: "Stored Native",
		Auth:   sdk.ProviderAuth{APIKey: &sdk.APIKeyAuth{Name: "Key", Check: func(sdk.APIKeyAuthInput) (*sdk.AuthCheck, error) { return nil, nil }, Resolve: func(sdk.APIKeyAuthInput) (*sdk.AuthResult, error) { return nil, nil }}},
		Models: []map[string]any{storedModel}, APIs: map[string]sdk.ProviderStreams{"nat-api": {Stream: scripted("s.stream"), StreamSimple: scripted("s.simple")}},
	}))
	check(registry.RegisterNativeProvider(storedProvider))
	ok(registry.Refresh(sdk.ModelsRefreshOptions{AllowNetwork: &offline}))
	r["native"].(map[string]any)["storedSettled"] = map[string]any{
		"configured": ok(registry.HasConfiguredAuth(storedModel)), "status": plain(ok(registry.GetProviderAuthStatus("stored-prov"))),
		"available": slices.ContainsFunc(ok(registry.GetAvailable()), func(model map[string]any) bool { return model["provider"] == "stored-prov" }),
	}
	check(registry.UnregisterProvider("stored-prov"))
	ok(registry.Refresh(sdk.ModelsRefreshOptions{AllowNetwork: &offline}))
	builtin := map[string]any{}
	if found, err := registry.GetProvider("anthropic"); err != nil {
		builtin["provider"] = "error: " + err.Error()
	} else {
		builtin["provider"] = found.ID
	}
	if all, err := rt.GetProviders(); err != nil {
		builtin["providers"] = "error: " + err.Error()
	} else if slices.ContainsFunc(all, func(candidate *sdk.Provider) bool { return candidate.ID == "anthropic" }) {
		builtin["providers"] = "anthropic"
	} else {
		builtin["providers"] = "absent"
	}
	r["builtin"] = builtin

	check(registry.RegisterVirtualModel(sdk.VirtualModel{
		Provider: "mr-virtual", ID: "v1", Name: "Virtual One", ThinkingLevels: []string{"off", "high"}, ContextWindow: 1000, MaxTokens: 100, Input: []string{"text"},
		Route: func(sdk.Context, sdk.ModelRouteRequest) (sdk.ModelRoute, error) {
			physical, err := rt.GetPhysicalModel("mr-local", "m1")
			return sdk.ModelRoute{Model: physical, ThinkingLevel: "off"}, err
		},
	}))
	virtual := ok(registry.Find("mr-virtual", "v1"))
	r["virtual"] = map[string]any{"model": pickModel(virtual), "thinkingLevels": virtual["thinkingLevels"]}
	registry.UnregisterVirtualModel("mr-virtual", "v1")
	r["virtual"].(map[string]any)["after"] = ok(registry.Find("mr-virtual", "v1")) == nil

	allow := false
	refreshed := ok(registry.Refresh(sdk.ModelsRefreshOptions{AllowNetwork: &allow}))
	r["refresh"] = map[string]any{"aborted": refreshed.Aborted, "errors": refreshed.Errors}
	options := map[string]any{"maxTokens": 50}
	events := []string{}
	stream := registry.StreamSimple(local, conversation, options)
	for event := range stream.Events(context.Background()) {
		events = append(events, fmt.Sprint(event["type"]))
	}
	result := stream.Result()
	full := registry.Complete(local, conversation, options)
	classified := registry.Classify(local, sdk.ClassifierContext{State: map[string]any{}, Questions: sdk.NewOrderedObject()}, nil)
	imaged := registry.GenerateImages(local, map[string]any{"input": []any{map[string]any{"type": "text", "text": "a cat"}}}, nil)
	r["stream"] = map[string]any{
		"events": events, "stopReason": result["stopReason"], "text": messageText(result), "provider": result["provider"], "model": result["model"], "api": result["api"], "full": full["stopReason"],
		"classifyStop": classified.StopReason, "classifyError": orEmpty(classified.ErrorMessage), "imagesStop": imaged["stopReason"], "imagesError": imaged["errorMessage"],
	}
	return r
}
