// Package modeltypes is the Go SDK extension of the typed model operation conformance rows: ctx.modelRegistry's typed reads, getAvailableOfType, classify and registerVirtualModel, and the image and classifier implementations of a provider config and of a Provider object. Fused, isolated and packed realizations run this one factory. testdata/modeltypes-rust and the inline Python fixture of model_types_python_test.go carry the same tools, so one table of rows proves every SDK.
package modeltypes

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync/atomic"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Name is the extension's registered name.
const Name = "model-types"

var zeroCost = map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}

func text(value any) (string, error) {
	data, err := json.Marshal(value)
	return string(data), err
}

func ids(models []map[string]any) []string {
	out := []string{}
	for _, model := range models {
		out = append(out, fmt.Sprintf("%v/%v", model["provider"], model["id"]))
	}
	return out
}

// imageImpl answers with a value only this code produces: the model id and the prompt, and the key the host resolved.
func imageImpl(model, request map[string]any, options sdk.ProviderOperationOptions) (map[string]any, error) {
	prompt := request["input"].([]any)[0].(map[string]any)["text"].(string)
	if prompt == "fail" {
		return nil, errors.New("image failed")
	}
	key, _ := options.Values["apiKey"].(string)
	return map[string]any{
		"api": model["api"], "provider": model["provider"], "model": model["id"], "responseId": key,
		"output":     []any{map[string]any{"type": "image", "data": "img:" + fmt.Sprint(model["id"]) + ":" + prompt, "mimeType": "image/png"}},
		"stopReason": "stop", "timestamp": 1,
	}, nil
}

// cancelled counts the classifications the host cancelled while they waited.
var cancelled atomic.Int64

// classifyImpl answers each question with the length of the state's text plus ten per image, in the reverse of the question order.
func classifyImpl(model map[string]any, request sdk.ClassifierContext, options sdk.ProviderOperationOptions) (sdk.ClassifierResult, error) {
	state, _ := request.State["text"].(string)
	switch state {
	case "fail":
		return sdk.ClassifierResult{}, errors.New("classifier failed")
	case "hang":
		<-options.Signal.Done()
		cancelled.Add(1)
		return sdk.ClassifierResult{}, errors.New("classifier cancelled")
	}
	keys := request.Questions.Keys()
	pairs := make([]any, 0, len(keys)*2)
	for _, key := range slices.Backward(keys) {
		pairs = append(pairs, key, map[string]any{"type": "bool", "probability": float64(len(state)+10*len(request.Images)) / 100})
	}
	return sdk.ClassifierResult{API: fmt.Sprint(model["api"]), Provider: fmt.Sprint(model["provider"]), Model: fmt.Sprint(model["id"]), Answers: sdk.NewOrderedObject(pairs...), StopReason: "stop", Timestamp: 2}, nil
}

func approvalContext(stateText string) sdk.ClassifierContext {
	return sdk.ClassifierContext{
		State: map[string]any{"text": stateText},
		Questions: sdk.NewOrderedObject(
			"tone", map[string]any{"type": "choice", "instructions": "Which tone?", "criteria": sdk.NewOrderedObject("warm", "Warm", "cold", "Cold")},
			"approved", map[string]any{"type": "bool", "instructions": "Does this express approval?", "criteria": map[string]any{"true": "Approval", "false": "No approval"}},
		),
	}
}

// Extension returns the fixture extension.
func Extension() *sdk.Extension {
	e := sdk.New(Name)
	object := sdk.Schema{"type": "object"}

	e.RegisterProvider("ops", sdk.ProviderConfig{
		"baseUrl": "https://ops.test/v1", "apiKey": "ops-key",
		"models": []any{
			map[string]any{"id": "flux", "name": "Flux", "type": "image", "api": "test-images", "input": []string{"text"}, "output": []string{"image", "text"}, "cost": zeroCost},
			map[string]any{"id": "cls", "name": "Cls", "type": "classifier", "api": "test-classifier", "input": []string{"text"}, "contextWindow": 1000, "cost": zeroCost},
		},
		"images":      map[string]sdk.ProviderImagesFunc{"test-images": imageImpl},
		"classifiers": map[string]sdk.ProviderClassifyFunc{"test-classifier": classifyImpl},
	})
	if err := e.RegisterNativeProvider(&sdk.Provider{
		ID: "pixels", Name: "Pixels",
		Auth: sdk.ProviderAuth{APIKey: &sdk.APIKeyAuth{Name: "Pixels key", Resolve: func(sdk.APIKeyAuthInput) (*sdk.AuthResult, error) {
			return &sdk.AuthResult{Auth: map[string]any{"apiKey": "pixels-key"}}, nil
		}}},
		GetModels: func() ([]map[string]any, error) {
			return []map[string]any{{"id": "flux", "name": "Flux", "type": "image", "api": "test-images", "input": []string{"text"}, "output": []string{"image"}, "cost": zeroCost, "baseUrl": "https://pixels.test/v1"}, {"id": "cls", "name": "Cls", "type": "classifier", "api": "test-classifier", "input": []string{"text"}, "contextWindow": 1000, "cost": zeroCost, "baseUrl": "https://pixels.test/v1"}}, nil
		},
		Stream: func(map[string]any, map[string]any, sdk.ProviderStreamOptions) (*sdk.ModelEventStream, error) {
			return nil, errors.New("unused")
		},
		StreamSimple: func(map[string]any, map[string]any, sdk.ProviderStreamOptions) (*sdk.ModelEventStream, error) {
			return nil, errors.New("unused")
		},
		GenerateImages: imageImpl,
		Classify:       classifyImpl,
	}); err != nil {
		panic(err)
	}

	e.RegisterTool(sdk.ToolDefinition{
		Name: "typed_reads", Label: "typed_reads", Description: "Reads typed models from the registry.", Parameters: object,
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			registry := ctx.ModelRegistry()
			report := map[string]any{}
			read := func(key string, models []map[string]any, err error) error {
				if err != nil {
					return err
				}
				report[key] = ids(models)
				return nil
			}
			classifiers, err := registry.GetModelsOfType(sdk.ModelTypeClassifier)
			if err := read("classifiers", classifiers, err); err != nil {
				return nil, err
			}
			chat, err := registry.GetModelsOfType(sdk.ModelTypeChat)
			if err := read("chat", chat, err); err != nil {
				return nil, err
			}
			images, err := registry.GetModelsOfType(sdk.ModelTypeImage, "openrouter")
			if err := read("images", images, err); err != nil {
				return nil, err
			}
			available, err := registry.GetAvailableOfType(sdk.ModelTypeClassifier, "typesafe")
			if err := read("available", available, err); err != nil {
				return nil, err
			}
			found, err := registry.FindOfType(sdk.ModelTypeClassifier, "typesafe", "jev-latest")
			if err != nil {
				return nil, err
			}
			missing, err := registry.FindOfType(sdk.ModelTypeClassifier, "typesafe", "missing")
			if err != nil {
				return nil, err
			}
			report["foundWindow"], report["missing"] = found["contextWindow"], missing
			return text(report)
		},
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "classify_probe", Label: "classify_probe", Description: "Classifies through the registry.", Parameters: object,
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			registry := ctx.ModelRegistry()
			model, err := registry.FindOfType(sdk.ModelTypeClassifier, "typesafe", "jev-latest")
			if err != nil || model == nil {
				return nil, fmt.Errorf("no classifier model: %w", err)
			}
			key := "sk-conf"
			result := registry.Classify(model, approvalContext("Looks good"), &sdk.ClassifierOptions{APIKey: &key})
			failed := registry.Classify(model, approvalContext("fail"), nil)
			shown := approvalContext("Looks good")
			shown.Images = []sdk.ImageContent{{Data: "aW1hZ2U=", MimeType: "image/png"}}
			withImages := registry.Classify(model, shown, nil)
			probability, _ := result.Answers.Get("approved")
			imagesProbability, _ := withImages.Answers.Get("approved")
			return text(map[string]any{"stop": result.StopReason, "answers": result.Answers.Keys(), "approved": probability.(map[string]any)["probability"], "imagesApproved": imagesProbability.(map[string]any)["probability"], "model": result.Model, "failedStop": failed.StopReason, "failedMessage": failed.ErrorMessage, "failedProvider": failed.Provider})
		},
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "images_probe", Label: "images_probe", Description: "Generates images through the registry.", Parameters: object,
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			registry := ctx.ModelRegistry()
			model, err := registry.FindOfType(sdk.ModelTypeImage, "openrouter", "flux")
			if err != nil || model == nil {
				return nil, fmt.Errorf("no image model: %w", err)
			}
			key := "sk-img"
			prompt := map[string]any{"input": []any{map[string]any{"type": "text", "text": "a red circle"}}}
			result := registry.GenerateImages(model, prompt, &sdk.ImagesOptions{APIKey: &key})
			missing := maps.Clone(model)
			missing["id"] = "gone"
			failed := registry.GenerateImages(missing, prompt, nil)
			output, _ := result["output"].([]any)
			return text(map[string]any{"stop": result["stopReason"], "model": result["model"], "output": output, "failedStop": failed["stopReason"], "failedMessage": failed["errorMessage"], "failedModel": failed["model"], "failedOutput": failed["output"]})
		},
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "virtual_probe", Label: "virtual_probe", Description: "Registers virtual models through the registry.", Parameters: object,
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			registry := ctx.ModelRegistry()
			route := func(sdk.Context, sdk.ModelRouteRequest) (sdk.ModelRoute, error) {
				return sdk.ModelRoute{Model: map[string]any{"provider": "p", "id": "m"}, ThinkingLevel: "off"}, nil
			}
			first := registry.RegisterVirtualModel(sdk.VirtualModel{Provider: "router", ID: "late", Name: "Late", ContextWindow: 1000, Route: route})
			refused := registry.RegisterVirtualModel(sdk.VirtualModel{Provider: "router", ID: "claimed", Name: "Claimed", Route: route})
			registry.UnregisterVirtualModel("router", "late")
			message := ""
			if refused != nil {
				message = refused.Error()
			}
			return text(map[string]any{"first": first == nil, "refused": strings.TrimSpace(message)})
		},
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "ops_status", Label: "ops_status", Description: "Reports what the host's provider requests did in the extension.", Parameters: object,
		Execute: func(sdk.Context, map[string]any) (any, error) {
			return text(map[string]any{"cancelled": cancelled.Load()})
		},
	})
	registerLateProbe(e)
	registerThemeProbe(e)
	return e
}
