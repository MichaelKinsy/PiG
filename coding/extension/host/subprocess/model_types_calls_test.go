package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// model-registry.ts:135-143 (getAvailableOfType), :170-177 (classify) and, in Pi 1.0.0, :181-188 (generateImages) as host calls of the extension API. Every SDK sends these calls, so the dispatch is the one contract they share.

func TestGetAvailableOfTypeCallPassesTypeAndProviderToTheAction(t *testing.T) {
	bridge := NewUIBridge(func() {})
	var gotType, gotProvider string
	bridge.SetActions(&HostCallbacks{GetAvailableOfType: func(_ context.Context, modelType, provider string) ([]map[string]any, error) {
		gotType, gotProvider = modelType, provider
		return []map[string]any{{"id": "jev-latest", "provider": "typesafe", "type": modelType}}, nil
	}})
	result, err := bridge.handleCall(t.Context(), "ext", nil, &CallPayload{Method: "getAvailableOfType", Args: json.RawMessage(`{"type":"classifier","provider":"typesafe"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if gotType != "classifier" || gotProvider != "typesafe" {
		t.Fatalf("action saw type %q provider %q", gotType, gotProvider)
	}
	var models []map[string]any
	if err := json.Unmarshal(result.Result, &models); err != nil || len(models) != 1 || models[0]["type"] != "classifier" {
		t.Fatalf("result = %s, %v", result.Result, err)
	}
	// An omitted provider is every provider.
	if _, err := bridge.handleCall(t.Context(), "ext", nil, &CallPayload{Method: "getAvailableOfType", Args: json.RawMessage(`{"type":"image"}`)}); err != nil || gotType != "image" || gotProvider != "" {
		t.Fatalf("omitted provider: type %q provider %q, err %v", gotType, gotProvider, err)
	}
}

func TestGetAvailableOfTypeCallFailsWithTheActionsError(t *testing.T) {
	bridge := NewUIBridge(func() {})
	bridge.SetActions(&HostCallbacks{GetAvailableOfType: func(context.Context, string, string) ([]map[string]any, error) {
		return nil, errors.New("Unknown model type: audio")
	}})
	if _, err := bridge.handleCall(t.Context(), "ext", nil, &CallPayload{Method: "getAvailableOfType", Args: json.RawMessage(`{"type":"audio"}`)}); err == nil || err.Error() != "Unknown model type: audio" {
		t.Fatalf("error = %v", err)
	}
	// A host without a Session has no available models of any type.
	bare := NewUIBridge(func() {})
	result, err := bare.handleCall(t.Context(), "ext", nil, &CallPayload{Method: "getAvailableOfType", Args: json.RawMessage(`{"type":"classifier"}`)})
	if err != nil || string(result.Result) != "[]" {
		t.Fatalf("no action: %s, %v", result.Result, err)
	}
}

func TestClassifyCallPassesModelContextAndOptionsToTheAction(t *testing.T) {
	bridge := NewUIBridge(func() {})
	var model, options map[string]any
	var request json.RawMessage
	bridge.SetActions(&HostCallbacks{Classify: func(_ context.Context, m map[string]any, r json.RawMessage, o map[string]any) (json.RawMessage, error) {
		model, request, options = m, r, o
		return json.RawMessage(`{"stopReason":"stop","answers":{"z":{"type":"bool","probability":0.25},"a":{"type":"bool","probability":0.5}}}`), nil
	}})
	args := `{"model":{"type":"classifier","provider":"typesafe","id":"jev-latest"},"context":{"state":{"text":"hi"},"questions":{"approved":{"type":"bool"}}},"options":{"apiKey":"sk","temperature":0.5}}`
	result, err := bridge.handleCall(t.Context(), "ext", nil, &CallPayload{Method: "classify", Args: json.RawMessage(args)})
	if err != nil {
		t.Fatal(err)
	}
	// The request reaches the action as the caller's bytes, so the order of its questions is the caller's.
	if want := `{"state":{"text":"hi"},"questions":{"approved":{"type":"bool"}}}`; string(request) != want {
		t.Fatalf("action saw request %s, want %s", request, want)
	}
	if model["id"] != "jev-latest" || options["apiKey"] != "sk" || options["temperature"] != 0.5 {
		t.Fatalf("action saw model %v, options %v", model, options)
	}
	// The result reaches the extension as the action's bytes, so the order of its answers is the service's.
	if want := `{"stopReason":"stop","answers":{"z":{"type":"bool","probability":0.25},"a":{"type":"bool","probability":0.5}}}`; string(result.Result) != want {
		t.Fatalf("result = %s, want %s", result.Result, want)
	}
	// Options are optional.
	options = map[string]any{"stale": true}
	if _, err := bridge.handleCall(t.Context(), "ext", nil, &CallPayload{Method: "classify", Args: json.RawMessage(`{"model":{"provider":"typesafe","id":"jev-latest"},"context":{"state":{},"questions":{}}}`)}); err != nil || len(options) != 0 {
		t.Fatalf("omitted options: %v, %v", options, err)
	}
}

// model-registry.ts:170: classify never rejects. A host that cannot classify answers with an error result naming the model, as classifierErrorResult does.
func TestClassifyCallWithoutAnActionAnswersAnErrorResult(t *testing.T) {
	bridge := NewUIBridge(func() {})
	result, err := bridge.handleCall(t.Context(), "ext", nil, &CallPayload{Method: "classify", Args: json.RawMessage(`{"model":{"type":"classifier","provider":"typesafe","id":"jev-latest","api":"typesafe-system-one"},"context":{"state":{},"questions":{}}}`)})
	if err != nil {
		t.Fatalf("classify rejected: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(result.Result, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["stopReason"] != "error" || decoded["provider"] != "typesafe" || decoded["model"] != "jev-latest" || decoded["api"] != "typesafe-system-one" || decoded["errorMessage"] == "" {
		t.Fatalf("result = %v", decoded)
	}
}

// model-registry.ts:181-188 (Pi 1.0.0): generateImages never rejects. A host that cannot generate images answers with an error result naming the model and with no output, as imageErrorResult does.
func TestGenerateImagesCallWithoutAnActionAnswersAnErrorResult(t *testing.T) {
	bridge := NewUIBridge(func() {})
	result, err := bridge.handleCall(t.Context(), "ext", nil, &CallPayload{Method: "generateImages", Args: json.RawMessage(`{"model":{"type":"image","provider":"openrouter","id":"flux","api":"openrouter-images"},"context":{"input":[{"type":"text","text":"a fox"}]}}`)})
	if err != nil {
		t.Fatalf("generateImages rejected: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(result.Result, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["stopReason"] != "error" || decoded["provider"] != "openrouter" || decoded["model"] != "flux" || decoded["api"] != "openrouter-images" || decoded["errorMessage"] != "image generation is not available" || !reflect.DeepEqual(decoded["output"], []any{}) {
		t.Fatalf("result = %v", decoded)
	}
}

// The generateImages call hands the host action the caller's model, the raw images context and the options.
func TestGenerateImagesCallForwardsModelContextAndOptions(t *testing.T) {
	bridge := NewUIBridge(func() {})
	var gotModel, gotOptions map[string]any
	var gotContext json.RawMessage
	bridge.SetHostAction("generateImages", func(_ context.Context, model map[string]any, request json.RawMessage, options map[string]any) (json.RawMessage, error) {
		gotModel, gotContext, gotOptions = model, request, options
		return json.RawMessage(`{"api":"openrouter-images","provider":"openrouter","model":"flux","output":[{"type":"image","data":"aGk=","mimeType":"image/png"}],"stopReason":"stop","timestamp":1}`), nil
	})
	result, err := bridge.handleCall(t.Context(), "ext", nil, &CallPayload{Method: "generateImages", Args: json.RawMessage(`{"model":{"type":"image","provider":"openrouter","id":"flux"},"context":{"input":[{"type":"text","text":"a fox"}]},"options":{"apiKey":"sk-img"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if gotModel["id"] != "flux" || string(gotContext) != `{"input":[{"type":"text","text":"a fox"}]}` || gotOptions["apiKey"] != "sk-img" {
		t.Fatalf("action saw model %v, context %s, options %v", gotModel, gotContext, gotOptions)
	}
	if !strings.Contains(string(result.Result), `"data":"aGk="`) {
		t.Fatalf("result = %s", result.Result)
	}
}

// The calls await the host, so each starts in lane order and then runs on its own goroutine (a Promise-returning upstream API).
func TestTypedModelCallsStartAsync(t *testing.T) {
	for _, method := range []string{"getAvailableOfType", "classify", "generateImages"} {
		if !startsAsync(method) {
			t.Errorf("%s is a Promise upstream and must start async", method)
		}
	}
}
