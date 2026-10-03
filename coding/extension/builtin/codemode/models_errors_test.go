package codemode

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// The recovery errors of the `models` globals and of the guarded `tools` and `models` objects match Pi 1.0.0.
// testdata/models-argument-errors.pi-1.0.0.json is the output of testdata/models-argument-errors.js run by Pi 1.0.0's
// executeCodemode (dist/extensions/codemode/execute.js) over the same catalog, with Pi's CODEMODE_DOCS_PATH replaced
// by <DOCS>. testdata/models-argument-errors.pi.mjs regenerates it from the pinned Pi package.
//
// upstream: execute.ts runModelCall, checkClassifierContext, checkImagesContext, describeValue, models.getModelOfType;
// prelude-source.ts guard.
func TestModelsGlobalArgumentErrorsMatchPi(t *testing.T) {
	judge := &ai.ClassifierModel{ID: "judge", Name: "Judge", API: "test-classifier", Provider: "scorer", BaseURL: "https://c", Input: []string{"text"}, ContextWindow: 1000}
	painter := &ai.ImageModel{ID: "painter", Name: "Painter", API: "test-images", Provider: "scorer", BaseURL: "https://i", Input: []string{"text", "image"}, Output: []string{"text", "image"}}
	chatty := &ai.Model{ID: "chatty", DisplayName: "Chatty", Input: []string{"text"}, ProviderMeta: ai.ProviderMetadata{ProviderID: "scorer", API: ai.APIOpenAICompletions, BaseURL: "https://x"}}
	registry := &scriptModels{models: []ai.AnyModel{judge, painter, chatty}}
	code, err := os.ReadFile("testdata/models-argument-errors.js")
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile("testdata/models-argument-errors.pi-1.0.0.json")
	if err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]string{"code": string(code)})
	base := extension.NewContext("", nil, func() error { return nil }, extension.ContextActions{ModelRegistry: registry})
	tc := extension.NewToolContext(base, "call", context.Background(), extension.ToolActions{GetCallableTools: func() []extension.AgentTool {
		return []extension.AgentTool{{Name: "bash", Description: "b", Parameters: json.RawMessage(`{"type":"object"}`)}}
	}})
	result, err := Execute(extension.WithToolContext(context.Background(), tc), "call", params, nil, Options{Models: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 2 {
		t.Fatalf("result = %+v", result.Content)
	}
	text := strings.ReplaceAll(result.Content[1].(ai.TextContent).Text, DocsPath(), "<DOCS>")
	var got, want map[string]any
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("script value %q: %v", text, err)
	}
	if err := json.Unmarshal(golden, &want); err != nil {
		t.Fatal(err)
	}
	for key, expected := range want {
		if !reflect.DeepEqual(got[key], expected) {
			t.Errorf("%s = %v\n want %v", key, got[key], expected)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d results, want %d", len(got), len(want))
	}
	if len(registry.classified) != 0 {
		t.Errorf("a malformed call reached the classifier: %v", registry.classified)
	}
}

// checkImagesContext returns the object it checked (execute.ts:160-183), so a block member it does not check, such as
// a textSignature that is not a string, never fails models.generateImages(); the image model gets the checked blocks.
func TestModelsGenerateImagesPassesTheCheckedContextOn(t *testing.T) {
	painter := &ai.ImageModel{ID: "painter", Name: "Painter", API: "test-images", Provider: "scorer", BaseURL: "https://i", Input: []string{"text", "image"}, Output: []string{"text", "image"}}
	registry := &scriptModels{models: []ai.AnyModel{painter}}
	value, _ := runModelsScript(t, registry, `const result = await models.generateImages({ provider: "scorer", id: "painter" }, { input: [
		{ type: "text", text: "a cat", textSignature: 5 },
		{ type: "image", data: "eA==", mimeType: "image/png", detail: "high" },
		{ type: "text", text: "in a hat", textSignature: "sig" },
	] });
	return { stopReason: result.stopReason, errorMessage: result.errorMessage };`)
	if value["stopReason"] != "error" || value["errorMessage"] != "not used" {
		t.Fatalf("script value = %v, want the registry's result", value)
	}
	want := []ai.ImagesContext{{Input: []ai.ContentBlock{ai.TextContent{Text: "a cat"}, ai.ImageContent{Data: "eA==", MimeType: "image/png"}, ai.TextContent{Text: "in a hat", TextSignature: "sig"}}}}
	if !reflect.DeepEqual(registry.images, want) {
		t.Fatalf("image requests = %#v\nwant %#v", registry.images, want)
	}
}
