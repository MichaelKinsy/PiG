package sdk

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// What an extension reaches through ctx.modelRegistry for the typed model operations of Pi 0.99.1 (.upstream/v0.99.1/packages/coding-agent/src/core/model-registry.ts:71-79,135-177) and through the image and classifier implementations of a provider (core/extensions/types.ts:1896-1898).

const typedRegistryState = `{"models":[{"id":"gpt","provider":"openai","api":"openai-completions"},{"id":"opus","provider":"anthropic","api":"anthropic-messages"}],` +
	`"typedModels":[{"type":"image","id":"flux","provider":"openrouter","api":"openrouter-images","output":["image"]},` +
	`{"type":"classifier","id":"jev-latest","provider":"typesafe","api":"typesafe-system-one","contextWindow":64000},` +
	`{"type":"classifier","id":"~typesafe/jev-latest","provider":"openrouter","api":"typesafe-system-one","contextWindow":64000}],` +
	`"providers":{},"registered":[]}`

func modelIDs(models []map[string]any) []string {
	ids := []string{}
	for _, model := range models {
		ids = append(ids, model["provider"].(string)+"/"+model["id"].(string))
	}
	return ids
}

func TestOrderedObjectKeepsKeyOrder(t *testing.T) {
	object := NewOrderedObject("zed", 1, "10", "ten", "alpha", map[string]any{"n": 2})
	encoded, err := json.Marshal(object)
	if err != nil || string(encoded) != `{"zed":1,"10":"ten","alpha":{"n":2}}` {
		t.Fatalf("encoded = %s, %v", encoded, err)
	}
	var decoded OrderedObject
	if err := json.Unmarshal([]byte(`{"b":true,"a":null,"1":[1,2]}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded.Keys(); !reflect.DeepEqual(got, []string{"b", "a", "1"}) {
		t.Fatalf("keys = %v", got)
	}
	if value, ok := decoded.Get("1"); !ok || !reflect.DeepEqual(value, []any{float64(1), float64(2)}) {
		t.Fatalf("value = %v, %v", value, ok)
	}
	if _, ok := decoded.Get("missing"); ok {
		t.Fatal("a missing key was found")
	}
}

// model-registry.ts:145-161 and model-runtime.ts getModelsOfType: the typed reads answer from the host's registry state, chat models from "models" and the others from "typedModels", in the host's order and optionally for one provider.
func TestModelRegistryTypedReadsUseTheHostState(t *testing.T) {
	ext := New("typed")
	type reads struct {
		classifiers, chat, images, none, inProvider []map[string]any
		found, missing, viaGet, chatModel           map[string]any
		errs                                        []error
	}
	got := make(chan reads, 1)
	ext.Command("read", "", func(ctx Context, _ string) error {
		var r reads
		registry := ctx.ModelRegistry()
		note := func(err error) { r.errs = append(r.errs, err) }
		var err error
		r.classifiers, err = registry.GetModelsOfType(ModelTypeClassifier)
		note(err)
		r.chat, err = registry.GetModelsOfType(ModelTypeChat)
		note(err)
		r.images, err = registry.GetModelsOfType(ModelTypeImage, "openrouter")
		note(err)
		r.none, err = registry.GetModelsOfType(ModelType("audio"))
		note(err)
		r.inProvider, err = registry.GetModelsOfType(ModelTypeClassifier, "typesafe")
		note(err)
		r.found, err = registry.FindOfType(ModelTypeClassifier, "typesafe", "jev-latest")
		note(err)
		r.missing, err = registry.FindOfType(ModelTypeClassifier, "typesafe", "missing")
		note(err)
		r.viaGet, err = registry.GetModelOfType(ModelTypeImage, "openrouter", "flux")
		note(err)
		// The same id is a chat model only when its type is chat.
		r.chatModel, err = registry.GetModelOfType(ModelTypeChat, "openai", "gpt")
		note(err)
		got <- r
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	calls, resp := runSurfaceCommand(t, host, "read", func(call *callMsg) *callResultMsg {
		return &callResultMsg{Result: json.RawMessage(typedRegistryState)}
	})
	if resp.Error != nil {
		t.Fatalf("response = %+v", resp)
	}
	for _, call := range calls {
		if call.Method != "getModelRegistryState" {
			t.Fatalf("a typed read called %s; it answers from the registry state", call.Method)
		}
	}
	r := recv(t, got)
	for i, err := range r.errs {
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}
	if want := []string{"typesafe/jev-latest", "openrouter/~typesafe/jev-latest"}; !reflect.DeepEqual(modelIDs(r.classifiers), want) {
		t.Fatalf("classifiers = %v, want %v", modelIDs(r.classifiers), want)
	}
	if want := []string{"openai/gpt", "anthropic/opus"}; !reflect.DeepEqual(modelIDs(r.chat), want) {
		t.Fatalf("chat = %v, want %v", modelIDs(r.chat), want)
	}
	if want := []string{"openrouter/flux"}; !reflect.DeepEqual(modelIDs(r.images), want) {
		t.Fatalf("images of openrouter = %v", modelIDs(r.images))
	}
	if len(r.none) != 0 {
		t.Fatalf("an unknown type lists %v", modelIDs(r.none))
	}
	if want := []string{"typesafe/jev-latest"}; !reflect.DeepEqual(modelIDs(r.inProvider), want) {
		t.Fatalf("classifiers of typesafe = %v", modelIDs(r.inProvider))
	}
	if r.found == nil || r.found["contextWindow"] != float64(64000) || r.found["type"] != "classifier" || r.missing != nil || r.viaGet == nil || r.viaGet["api"] != "openrouter-images" || r.chatModel == nil || r.chatModel["id"] != "gpt" {
		t.Fatalf("found = %v, missing = %v, viaGet = %v, chat = %v", r.found, r.missing, r.viaGet, r.chatModel)
	}
}

// model-registry.ts:135-143: getAvailableOfType awaits the host. The type and the provider are what the host filters on, and its error is the caller's.
func TestModelRegistryGetAvailableOfTypeCallsTheHost(t *testing.T) {
	ext := New("typed")
	type result struct {
		models []map[string]any
		err    error
	}
	got := make(chan result, 3)
	ext.Command("read", "", func(ctx Context, _ string) error {
		registry := ctx.ModelRegistry()
		models, err := registry.GetAvailableOfType(ModelTypeClassifier, "typesafe")
		got <- result{models, err}
		models, err = registry.GetAvailableOfType(ModelTypeImage)
		got <- result{models, err}
		models, err = registry.GetAvailableOfType(ModelType("audio"))
		got <- result{models, err}
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	calls, resp := runSurfaceCommand(t, host, "read", func(call *callMsg) *callResultMsg {
		args := decodeArgs(t, call.Args)
		switch args["type"] {
		case "classifier":
			return &callResultMsg{Result: json.RawMessage(`[{"type":"classifier","id":"jev-latest","provider":"typesafe"}]`)}
		case "image":
			return &callResultMsg{Result: json.RawMessage(`[]`)}
		}
		return &callResultMsg{Error: &errorInfo{Message: "Unknown model type: audio"}}
	})
	if resp.Error != nil || len(calls) != 3 {
		t.Fatalf("calls = %+v, response = %+v", calls, resp)
	}
	if calls[0].Method != "getAvailableOfType" || string(calls[0].Args) != `{"type":"classifier","provider":"typesafe"}` || string(calls[1].Args) != `{"type":"image"}` {
		t.Fatalf("calls = %s %s / %s", calls[0].Method, calls[0].Args, calls[1].Args)
	}
	first := recv(t, got)
	if first.err != nil || len(first.models) != 1 || first.models[0]["id"] != "jev-latest" {
		t.Fatalf("classifiers = %+v", first)
	}
	if second := recv(t, got); second.err != nil || len(second.models) != 0 {
		t.Fatalf("images = %+v", second)
	}
	if third := recv(t, got); third.err == nil || !strings.Contains(third.err.Error(), "Unknown model type: audio") {
		t.Fatalf("audio = %+v", third)
	}
}

var jevModel = map[string]any{"type": "classifier", "id": "jev-latest", "provider": "typesafe", "api": "typesafe-system-one", "contextWindow": float64(64000)}

func approvalContext() ClassifierContext {
	return ClassifierContext{
		State: map[string]any{"text": "Looks good"},
		Questions: NewOrderedObject(
			"tone", map[string]any{"type": "choice", "instructions": "Which tone?", "criteria": NewOrderedObject("warm", "Warm", "cold", "Cold")},
			"approved", map[string]any{"type": "bool", "instructions": "Does this express approval?", "criteria": map[string]any{"true": "Approval", "false": "No approval"}},
		),
	}
}

// model-registry.ts:170-177 and model-runtime.ts:800-815: classify sends the model, the state and the questions in their order, and the answers come back in the service's order. It never rejects.
func TestModelRegistryClassifyRoundTripsOrderedQuestionsAndAnswers(t *testing.T) {
	ext := New("typed")
	got := make(chan ClassifierResult, 2)
	ext.Command("classify", "", func(ctx Context, _ string) error {
		key := "sk-explicit"
		attempts := 2
		temperature := 0.5
		got <- ctx.ModelRegistry().Classify(jevModel, approvalContext(), &ClassifierOptions{APIKey: &key, Headers: map[string]*string{"X-Trace": nil}, MaxRetries: &attempts, Temperature: &temperature})
		got <- ctx.ModelRegistry().Classify(jevModel, approvalContext(), nil)
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	calls, resp := runSurfaceCommand(t, host, "classify", func(call *callMsg) *callResultMsg {
		if strings.Contains(string(call.Args), "sk-explicit") {
			return &callResultMsg{Result: json.RawMessage(`{"api":"typesafe-system-one","provider":"typesafe","model":"jev-latest","answers":{"approved":{"type":"bool","probability":0.8},"tone":{"type":"choice","choice":"warm","probabilities":{"warm":0.7,"cold":0.3},"confidence":0.4}},"usage":{"input":12,"output":3,"cacheRead":0,"cacheWrite":0,"totalTokens":15,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":7}`)}
		}
		return &callResultMsg{Error: &errorInfo{Message: "host went away"}}
	})
	if resp.Error != nil || len(calls) != 2 {
		t.Fatalf("calls = %+v, response = %+v", calls, resp)
	}
	if calls[0].Method != "classify" {
		t.Fatalf("method = %s", calls[0].Method)
	}
	want := `{"model":{"api":"typesafe-system-one","contextWindow":64000,"id":"jev-latest","provider":"typesafe","type":"classifier"},` +
		`"context":{"state":{"text":"Looks good"},"questions":{"tone":{"criteria":{"warm":"Warm","cold":"Cold"},"instructions":"Which tone?","type":"choice"},"approved":{"criteria":{"false":"No approval","true":"Approval"},"instructions":"Does this express approval?","type":"bool"}}},` +
		`"options":{"apiKey":"sk-explicit","headers":{"X-Trace":null},"maxRetries":2,"temperature":0.5}}`
	if string(calls[0].Args) != want {
		t.Fatalf("classify args =\n%s\nwant\n%s", calls[0].Args, want)
	}
	if strings.Contains(string(calls[1].Args), `"options"`) {
		t.Fatalf("omitted options were sent: %s", calls[1].Args)
	}
	ok := recv(t, got)
	if ok.StopReason != "stop" || ok.Model != "jev-latest" || ok.Provider != "typesafe" || ok.Timestamp != 7 || ok.Usage["totalTokens"] != float64(15) {
		t.Fatalf("result = %+v", ok)
	}
	// The answers keep the service's order, which is not the questions' order.
	if !reflect.DeepEqual(ok.Answers.Keys(), []string{"approved", "tone"}) {
		t.Fatalf("answer order = %v", ok.Answers.Keys())
	}
	if tone, _ := ok.Answers.Get("tone"); tone.(map[string]any)["choice"] != "warm" {
		t.Fatalf("tone = %v", tone)
	}
	// A failed host call is a result, as Pi's classifierErrorResult makes one for any failure.
	failed := recv(t, got)
	if failed.StopReason != "error" || !strings.Contains(failed.ErrorMessage, "host went away") || failed.Provider != "typesafe" || failed.Model != "jev-latest" || failed.API != "typesafe-system-one" || failed.Timestamp == 0 {
		t.Fatalf("failed = %+v", failed)
	}
}

// model-runtime.ts:800-815: an aborted classification is a result with stopReason "aborted", not an error; the request lifetime is the signal.
func TestModelRegistryClassifyReportsACancelledRequestAsAnAbortedResult(t *testing.T) {
	ext := New("typed")
	got := make(chan ClassifierResult, 1)
	ext.Command("classify", "", func(ctx Context, _ string) error {
		got <- ctx.ModelRegistry().Classify(jevModel, approvalContext(), nil)
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "req-classify", Request: &requestMsg{Method: "command", Tool: "classify"}})
	for call := host.readEnvelope(t); call.Type != msgCall; call = host.readEnvelope(t) {
	}
	host.writeEnvelope(t, envelope{Type: msgCancel, ID: "req-classify", Cancel: &cancelMsg{RequestID: "req-classify", Reason: "test"}})
	result := recv(t, got)
	if result.StopReason != "aborted" || result.Provider != "typesafe" || result.Model != "jev-latest" || result.API != "typesafe-system-one" || result.Answers == nil || len(result.Answers.Keys()) != 0 {
		t.Fatalf("result = %+v", result)
	}
}

// model-registry.ts:161-168: registerVirtualModel and unregisterVirtualModel of the facade are the runtime's, the ones pi.registerVirtualModel reaches (loader.ts:480-495).
func TestModelRegistryVirtualModelsReachTheHostLikeTheContextOnes(t *testing.T) {
	ext := New("router")
	route := func(Context, ModelRouteRequest) (ModelRoute, error) { return ModelRoute{}, nil }
	got := make(chan error, 3)
	ext.Command("go", "", func(ctx Context, _ string) error {
		registry := ctx.ModelRegistry()
		got <- registry.RegisterVirtualModel(VirtualModel{Provider: "router", ID: "late", Name: "Late", ContextWindow: 1000, Route: route})
		got <- registry.RegisterVirtualModel(VirtualModel{Provider: "router", ID: "claimed", Name: "Claimed", Route: route})
		got <- registry.RegisterVirtualModel(VirtualModel{Provider: "router", ID: "no-route", Name: "No route"})
		registry.UnregisterVirtualModel("router", "late")
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	calls, resp := runSurfaceCommand(t, host, "go", func(call *callMsg) *callResultMsg {
		if strings.Contains(string(call.Args), "claimed") {
			return &callResultMsg{Error: &errorInfo{Message: "virtual model router/claimed is the id of a physical model"}}
		}
		return &callResultMsg{}
	})
	if resp.Error != nil || len(calls) != 3 {
		t.Fatalf("calls = %+v, response = %+v", calls, resp)
	}
	if calls[0].Method != "registerVirtualModel" || string(calls[0].Args) != `{"provider":"router","id":"late","name":"Late","contextWindow":1000}` || calls[2].Method != "unregisterVirtualModel" || string(calls[2].Args) != `{"provider":"router","id":"late"}` {
		t.Fatalf("calls = %s %s / %s %s", calls[0].Method, calls[0].Args, calls[2].Method, calls[2].Args)
	}
	if err := recv(t, got); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	if err := recv(t, got); err == nil || !strings.Contains(err.Error(), "is the id of a physical model") {
		t.Fatalf("refused registration error = %v", err)
	}
	if err := recv(t, got); err == nil || !strings.Contains(err.Error(), "must define a route") {
		t.Fatalf("a model without a route: %v", err)
	}
}

func imageRequest() string {
	return `{"model":{"type":"image","id":"flux","provider":"ops","api":"test-images","output":["image"]},"context":{"input":[{"type":"text","text":"a red circle"}]},"options":{"apiKey":"sk-ops"}}`
}

// types.ts:1896-1898: a provider config's images and classifiers are implementations keyed by API. The callbacks stay in the extension; the register payload names the APIs and the host runs each through a provider_operation request.
func TestProviderConfigImplementationsAreDeclaredAndRunInTheExtension(t *testing.T) {
	ext := New("ops")
	type seen struct {
		model   map[string]any
		request map[string]any
		options ProviderOperationOptions
	}
	imageSeen := make(chan seen, 1)
	classifierSeen := make(chan ClassifierContext, 1)
	ext.RegisterProvider("ops", ProviderConfig{
		"baseUrl": "https://ops.test/v1", "apiKey": "key",
		"models": []any{
			map[string]any{"id": "flux", "name": "Flux", "type": "image", "api": "test-images", "input": []string{"text"}, "output": []string{"image"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}},
			map[string]any{"id": "cls", "name": "Cls", "type": "classifier", "api": "test-classifier", "input": []string{"text"}, "contextWindow": 1000, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}},
		},
		"images": map[string]ProviderImagesFunc{"test-images": func(model, request map[string]any, options ProviderOperationOptions) (map[string]any, error) {
			imageSeen <- seen{model, request, options}
			return map[string]any{"api": "test-images", "provider": "ops", "model": "flux", "output": []any{map[string]any{"type": "image", "data": "aGk=", "mimeType": "image/png"}}, "stopReason": "stop", "timestamp": 1}, nil
		}},
		"classifiers": map[string]ProviderClassifyFunc{"test-classifier": func(model map[string]any, request ClassifierContext, options ProviderOperationOptions) (ClassifierResult, error) {
			classifierSeen <- request
			if request.State["text"] == "fail" {
				return ClassifierResult{}, context.DeadlineExceeded
			}
			return ClassifierResult{API: "test-classifier", Provider: "ops", Model: "cls", Answers: NewOrderedObject("approved", map[string]any{"type": "bool", "probability": 0.25}, "again", map[string]any{"type": "bool", "probability": 0.5}), StopReason: "stop", Timestamp: 2}, nil
		}},
	})
	host, reg, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	if len(reg.Providers) != 1 || !reflect.DeepEqual(reg.Providers[0].ImageAPIs, []string{"test-images"}) || !reflect.DeepEqual(reg.Providers[0].ClassifierAPIs, []string{"test-classifier"}) {
		t.Fatalf("providers = %+v", reg.Providers)
	}
	if config := string(reg.Providers[0].Config); strings.Contains(config, `"images"`) || strings.Contains(config, `"classifiers"`) || !strings.Contains(config, `"type":"image"`) || !strings.Contains(config, `"contextWindow":1000`) {
		t.Fatalf("config on the wire = %s", config)
	}

	_, resp := runSurfaceRequest(t, host, "img-1", &requestMsg{Method: "provider_operation", Tool: "ops", Args: json.RawMessage(`{"kind":"images","api":"test-images",` + imageRequest()[1:])}, nil)
	if resp.Error != nil {
		t.Fatalf("images failed: %+v", resp.Error)
	}
	got := recv(t, imageSeen)
	if got.model["id"] != "flux" || got.request["input"].([]any)[0].(map[string]any)["text"] != "a red circle" || got.options.Values["apiKey"] != "sk-ops" || got.options.Signal == nil {
		t.Fatalf("image callback saw %+v", got)
	}
	var generated map[string]any
	if err := json.Unmarshal(resp.Result, &generated); err != nil || generated["stopReason"] != "stop" || generated["model"] != "flux" {
		t.Fatalf("image result = %s, %v", resp.Result, err)
	}

	question := `{"model":{"type":"classifier","id":"cls","provider":"ops","api":"test-classifier"},"context":{"state":{"text":"ok"},"questions":{"b":{"type":"bool"},"a":{"type":"bool"}}}}`
	_, resp = runSurfaceRequest(t, host, "cls-1", &requestMsg{Method: "provider_operation", Tool: "ops", Args: json.RawMessage(`{"kind":"classifiers","api":"test-classifier",` + question[1:])}, nil)
	if resp.Error != nil {
		t.Fatalf("classify failed: %+v", resp.Error)
	}
	if request := recv(t, classifierSeen); !reflect.DeepEqual(request.Questions.Keys(), []string{"b", "a"}) {
		t.Fatalf("the callback lost the question order: %v", request.Questions.Keys())
	}
	var classified struct {
		Answers json.RawMessage `json:"answers"`
		Stop    string          `json:"stopReason"`
	}
	if err := json.Unmarshal(resp.Result, &classified); err != nil || classified.Stop != "stop" || string(classified.Answers) != `{"approved":{"probability":0.25,"type":"bool"},"again":{"probability":0.5,"type":"bool"}}` {
		t.Fatalf("classify result = %s, %v", resp.Result, err)
	}

	// An error the callback returns is the request's error, which the host turns into an error result, as Pi's runtime does for a rejected classify.
	_, resp = runSurfaceRequest(t, host, "cls-2", &requestMsg{Method: "provider_operation", Tool: "ops", Args: json.RawMessage(`{"kind":"classifiers","api":"test-classifier","model":{"type":"classifier","id":"cls","provider":"ops"},"context":{"state":{"text":"fail"},"questions":{}}}`)}, nil)
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "deadline") {
		t.Fatalf("callback error response = %+v", resp)
	}
	// A request for an API the extension did not implement fails naming it.
	_, resp = runSurfaceRequest(t, host, "cls-3", &requestMsg{Method: "provider_operation", Tool: "ops", Args: json.RawMessage(`{"kind":"classifiers","api":"other","model":{"id":"x"},"context":{"state":{},"questions":{}}}`)}, nil)
	// upstream: provider-composer.ts composeModelProvider, `Provider ${providerId} has no classifier implementation for "${model.api}"`.
	if resp.Error == nil || resp.Error.Message != `Provider ops has no classifier implementation for "other"` {
		t.Fatalf("unknown API response = %+v", resp)
	}
}

// types.ts:1896-1898 for a Provider object: generateImages and classify are members of the object (pi-ai Provider), declared with the other methods and run through provider_call.
func TestProviderObjectDeclaresAndRunsGenerateImagesAndClassify(t *testing.T) {
	ext := New("objects")
	provider := &Provider{
		ID: "pixels", Name: "Pixels",
		Auth: ProviderAuth{APIKey: &APIKeyAuth{Name: "Pixels key", Resolve: func(APIKeyAuthInput) (*AuthResult, error) { return &AuthResult{Auth: map[string]any{}}, nil }}},
		GetModels: func() ([]map[string]any, error) {
			return []map[string]any{{"id": "flux", "name": "Flux", "type": "image", "api": "test-images"}}, nil
		},
		Stream: func(map[string]any, map[string]any, ProviderStreamOptions) (*ModelEventStream, error) {
			return nil, nil
		},
		StreamSimple: func(map[string]any, map[string]any, ProviderStreamOptions) (*ModelEventStream, error) {
			return nil, nil
		},
		GenerateImages: func(model, request map[string]any, options ProviderOperationOptions) (map[string]any, error) {
			return map[string]any{"model": model["id"], "n": len(request["input"].([]any)), "key": options.Values["apiKey"], "stopReason": "stop"}, nil
		},
		Classify: func(model map[string]any, request ClassifierContext, options ProviderOperationOptions) (ClassifierResult, error) {
			return ClassifierResult{Model: model["id"].(string), Answers: NewOrderedObject("q", map[string]any{"type": "bool", "probability": 0.5}), StopReason: "stop"}, nil
		},
	}
	if err := ext.RegisterNativeProvider(provider); err != nil {
		t.Fatal(err)
	}
	host, reg, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	if len(reg.Providers) != 1 || reg.Providers[0].Native == nil {
		t.Fatalf("providers = %+v", reg.Providers)
	}
	native := reg.Providers[0].Native
	if !containsAll(native.Methods, "generateImages", "classify") {
		t.Fatalf("methods = %v", native.Methods)
	}
	call := func(method, params string) *responseMsg {
		_, resp := runSurfaceRequest(t, host, "obj-"+method, &requestMsg{Method: "provider_call", Tool: native.Key, Args: json.RawMessage(`{"method":"` + method + `","params":` + params + `}`)}, nil)
		return resp
	}
	resp := call("generateImages", `{"model":{"id":"flux"},"context":{"input":[{"type":"text","text":"x"}]},"options":{"apiKey":"sk"}}`)
	if resp.Error != nil || string(resp.Result) != `{"key":"sk","model":"flux","n":1,"stopReason":"stop"}` {
		t.Fatalf("generateImages = %s, %+v", resp.Result, resp.Error)
	}
	resp = call("classify", `{"model":{"id":"cls"},"context":{"state":{},"questions":{"q":{"type":"bool"}}},"options":{}}`)
	if resp.Error != nil || !strings.Contains(string(resp.Result), `"model":"cls"`) || !strings.Contains(string(resp.Result), `"answers":{"q":{"probability":0.5,"type":"bool"}}`) {
		t.Fatalf("classify = %s, %+v", resp.Result, resp.Error)
	}
	// A provider without the members does not declare them, as Pi's Provider leaves them undefined.
	bare := &Provider{ID: "bare", Name: "Bare", Auth: provider.Auth, GetModels: provider.GetModels, Stream: provider.Stream, StreamSimple: provider.StreamSimple}
	decl, err := providerDeclaration(bare, "bare-key")
	if err != nil || containsAll(decl.Methods, "generateImages") || containsAll(decl.Methods, "classify") {
		t.Fatalf("bare methods = %v, %v", decl.Methods, err)
	}
}

func containsAll(list []string, wanted ...string) bool {
	for _, want := range wanted {
		found := false
		for _, item := range list {
			found = found || item == want
		}
		if !found {
			return false
		}
	}
	return true
}

// model-runtime.ts registerProvider merges a re-registration's defined values over the previous one, so a second registration of the same provider without images or classifiers keeps the implementations of the first; the host keeps the APIs it wired, so the extension must keep their callbacks.
func TestProviderReRegistrationKeepsImplementationsItDoesNotRedefine(t *testing.T) {
	ext := New("ops")
	ran := make(chan string, 2)
	ext.RegisterProvider("ops", ProviderConfig{
		"baseUrl": "https://ops.test/v1",
		"images": map[string]ProviderImagesFunc{"test-images": func(map[string]any, map[string]any, ProviderOperationOptions) (map[string]any, error) {
			ran <- "first images"
			return map[string]any{"stopReason": "stop"}, nil
		}},
		"classifiers": map[string]ProviderClassifyFunc{"test-classifier": func(map[string]any, ClassifierContext, ProviderOperationOptions) (ClassifierResult, error) {
			ran <- "first classifier"
			return ClassifierResult{StopReason: "stop"}, nil
		}},
	})
	ext.RegisterProvider("ops", ProviderConfig{
		"baseUrl": "https://ops.test/v2",
		"classifiers": map[string]ProviderClassifyFunc{"test-classifier": func(map[string]any, ClassifierContext, ProviderOperationOptions) (ClassifierResult, error) {
			ran <- "second classifier"
			return ClassifierResult{StopReason: "stop"}, nil
		}},
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	_, resp := runSurfaceRequest(t, host, "img-1", &requestMsg{Method: "provider_operation", Tool: "ops", Args: json.RawMessage(`{"kind":"images","api":"test-images",` + imageRequest()[1:])}, nil)
	if resp.Error != nil {
		t.Fatalf("images after a re-registration without images: %+v", resp.Error)
	}
	_, resp = runSurfaceRequest(t, host, "cls-1", &requestMsg{Method: "provider_operation", Tool: "ops", Args: json.RawMessage(`{"kind":"classifiers","api":"test-classifier","model":{"id":"cls"},"context":{"state":{},"questions":{}}}`)}, nil)
	if resp.Error != nil {
		t.Fatalf("classifiers: %+v", resp.Error)
	}
	if first, second := recv(t, ran), recv(t, ran); first != "first images" || second != "second classifier" {
		t.Fatalf("ran %q then %q, want the kept images and the redefined classifier", first, second)
	}
}
