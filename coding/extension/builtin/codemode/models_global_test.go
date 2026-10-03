package codemode

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// scriptModels is a model registry that records what `models.*` asks of it (execute.ts createModelGlobals uses
// ctx.modelRegistry through CodemodeModelRuntime).
type scriptModels struct {
	mu         sync.Mutex
	classified []*ai.ClassifierModel
	images     []ai.ImagesContext
	models     []ai.AnyModel
}

func (r *scriptModels) GetModelsOfType(modelType ai.ModelType, provider ...string) []ai.AnyModel {
	var out []ai.AnyModel
	for _, model := range r.models {
		if model.ModelType() == modelType && (len(provider) == 0 || provider[0] == "" || model.ProviderID() == provider[0]) {
			out = append(out, model)
		}
	}
	return out
}

func (r *scriptModels) GetAvailableOfType(_ context.Context, modelType ai.ModelType, provider ...string) ([]ai.AnyModel, error) {
	return r.GetModelsOfType(modelType, provider...), nil
}

func (r *scriptModels) GetModelOfType(modelType ai.ModelType, provider, id string) ai.AnyModel {
	for _, model := range r.GetModelsOfType(modelType, provider) {
		if model.ModelID() == id {
			return model
		}
	}
	return nil
}

func (r *scriptModels) Classify(_ context.Context, model *ai.ClassifierModel, _ ai.ClassifierContext, _ ...ai.ModelsClassifierOptions) ai.ClassifierResult {
	r.mu.Lock()
	r.classified = append(r.classified, model)
	r.mu.Unlock()
	return ai.ClassifierResult{
		API: model.API, Provider: model.Provider, Model: model.ID, StopReason: ai.ClassifierStopReasonStop,
		Answers: ai.ClassifierAnswers{{ID: "approved", Answer: ai.ClassifierBoolAnswer{Probability: 0.9}}},
		Usage:   &ai.Usage{Input: 300, TotalTokens: 300, Cost: ai.UsageCost{Input: 0.001, Total: 0.001}},
	}
}

// GenerateImages completes CodemodeModelRuntime (tool.ts, 1.0.0). It records the request and generates nothing.
func (r *scriptModels) GenerateImages(_ context.Context, model *ai.ImageModel, request ai.ImagesContext, _ ...ai.ModelsImagesOptions) ai.AssistantImages {
	r.mu.Lock()
	r.images = append(r.images, request)
	r.mu.Unlock()
	return ai.AssistantImages{API: model.API, Provider: model.Provider, Model: model.ID, StopReason: ai.ImagesStopReasonError, ErrorMessage: "not used"}
}

func modelsToolContext(registry any) context.Context {
	base := extension.NewContext("", nil, func() error { return nil }, extension.ContextActions{ModelRegistry: registry})
	return extension.WithToolContext(context.Background(), extension.NewToolContext(base, "call", context.Background(), extension.ToolActions{}))
}

func runModelsScript(t *testing.T, registry any, code string) (map[string]any, agent.AgentToolResult) {
	t.Helper()
	params, _ := json.Marshal(map[string]string{"code": code})
	result, err := Execute(modelsToolContext(registry), "call", params, nil, Options{Models: true})
	if err != nil {
		t.Fatal(err)
	}
	var text string
	for _, item := range result.Content[1:] {
		if block, ok := item.(ai.TextContent); ok {
			text = block.Text
		}
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatalf("script result %q (error=%v): %v", text, result.IsError, err)
	}
	return value, result
}

// upstream: execute.ts createModelGlobals. A script lists and finds models of a type without the credentials
// headers carry, classifies by provider and id only, and the classification's usage becomes the result's usage.
func TestModelsGlobalServesTheRegistryToScripts(t *testing.T) {
	judge := &ai.ClassifierModel{ID: "judge", Name: "Judge", API: "test-classifier", Provider: "scorer", BaseURL: "https://classifier.test/v1", Input: []string{"text"}, ContextWindow: 1000, Headers: map[string]string{"X-Secret": "hunter2"}}
	registry := &scriptModels{models: []ai.AnyModel{judge}}
	value, result := runModelsScript(t, registry, `
		const [model] = await models.getAvailableOfType("classifier", "scorer");
		const listed = await models.getModelsOfType("classifier");
		const outcome = await models.classify({ ...model, baseUrl: "https://evil.test" }, { state: { text: "good" }, questions: { approved: { type: "bool", instructions: "Approval?", criteria: { true: "yes", false: "no" } } } });
		const attempt = async (fn) => { try { await fn(); return "ok"; } catch (error) { return error.message; } };
		return {
			headers: "headers" in model, listed: listed.length, same: (await models.getModelOfType("classifier", "scorer", "judge")).id,
			missing: (await models.getModelOfType("classifier", "scorer", "nope")) === undefined,
			probability: outcome.answers.approved.probability,
			badType: await attempt(() => models.getModelsOfType("video")),
			unknown: await attempt(() => models.classify({ provider: "scorer", id: "nope" }, {})),
			noModel: await attempt(() => models.classify("judge", {})),
		};
	`)
	if result.IsError {
		t.Fatalf("script failed: %v", value)
	}
	want := map[string]any{
		"headers": false, "listed": float64(1), "same": "judge", "missing": true, "probability": 0.9,
		"badType": `Unknown model type "video". Use "chat", "image", or "classifier".`,
		// upstream 1.0.0 execute.ts runModelCall: the errors say how to recover.
		"unknown": `Unknown classifier model "scorer/nope". List the classifier models you can use with models.getAvailableOfType("classifier").`,
		"noModel": `models.classify() expects a classifier model as its first argument, got a string. List the classifier models you can use with models.getAvailableOfType("classifier").`,
	}
	for key, expected := range want {
		if value[key] != expected {
			t.Errorf("%s = %v, want %v", key, value[key], expected)
		}
	}
	if len(registry.classified) != 1 || registry.classified[0] != judge {
		t.Errorf("classified %v, want the registry's own model, not the script's", registry.classified)
	}
	if result.Usage == nil || result.Usage.Input != 300 || result.Usage.Cost.Total != 0.001 {
		t.Errorf("usage = %+v, want the classification's", result.Usage)
	}
	calls := result.Details.(ToolDetails).Calls
	if len(calls) != 1 || calls[0].Name != "models.classify" || calls[0].Args != "scorer/judge" || calls[0].Status != StatusOK || calls[0].Cost == nil || *calls[0].Cost != 0.001 || calls[0].ID != "call/models.classify/1" {
		t.Errorf("nested calls = %+v", calls)
	}
}

// upstream: execute.ts toModelInfo copies Pi's model object ({ ...model }) without headers. A chat model carries only
// Pi's fields: none of the extension wire's aliases, and no key for an optional field the model leaves unset.
func TestModelsGlobalListsPiModelObjects(t *testing.T) {
	chat := &ai.Model{ID: "fast", DisplayName: "Fast", Input: []string{"text"},
		ProviderMeta: ai.ProviderMetadata{ProviderID: "scorer", API: ai.APIOpenAICompletions, BaseURL: "https://chat.test/v1", Headers: map[string]string{"X-Secret": "hunter2"}},
		Capabilities: ai.ModelCapabilities{ContextWindow: 1000, MaxOutputTokens: 100, InputCostPer1M: 1}}
	judge := &ai.ClassifierModel{ID: "judge", Name: "Judge", API: "test-classifier", Provider: "scorer", BaseURL: "https://classifier.test/v1", Input: []string{"text"}, ContextWindow: 1000}
	value, result := runModelsScript(t, &scriptModels{models: []ai.AnyModel{chat, judge}}, `
		const [chat] = await models.getModelsOfType("chat");
		const [judge] = await models.getModelsOfType("classifier");
		return { chat: Object.keys(chat).sort().join(","), judge: Object.keys(judge).sort().join(","), name: chat.name, cost: chat.cost.input };
	`)
	if result.IsError {
		t.Fatalf("script failed: %v", value)
	}
	want := map[string]any{
		"chat":  "api,baseUrl,contextWindow,cost,id,input,maxTokens,name,provider,reasoning",
		"judge": "api,baseUrl,contextWindow,cost,id,input,name,provider,type",
		"name":  "Fast", "cost": float64(1),
	}
	for key, expected := range want {
		if value[key] != expected {
			t.Errorf("%s = %v, want %v", key, value[key], expected)
		}
	}
}

// upstream: createCodemodeExtension declares `models` unless its `models` option is false (index.ts `options.models ?? true`).
// In 1.0.0 the description names the `models` global in one line (tool.ts describeGlobals) instead of declaring its API.
func TestExtensionServesModelsUnlessDisabled(t *testing.T) {
	served, err := Extension(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if description := served.Tools[ToolName].Definition.Description; !strings.Contains(description, "`models`: classifiers and image generation") {
		t.Error("the extension does not declare `models` by default")
	}
	disabled, err := Extension(Options{DisableModels: true})
	if err != nil {
		t.Fatal(err)
	}
	if description := disabled.Tools[ToolName].Definition.Description; strings.Contains(description, "`models`") {
		t.Error("the extension declares `models` with DisableModels")
	}
}
