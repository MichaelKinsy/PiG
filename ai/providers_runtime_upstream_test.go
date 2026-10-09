package ai_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// .upstream/v0.99.1/packages/ai/test/providers.test.ts:56: builtinModels() registers builtinProviders(), each with
// models of every type (getAllModels), so the classifier-only typesafe provider is registered too.
func TestBuiltinModelsRuntimeRegistersCatalog(t *testing.T) {
	builtin := ai.BuiltinModels()
	providers := builtin.GetProviders()
	catalog := ai.ListProviders()
	if len(providers) != len(ai.BuiltinProviders()) || len(providers) != len(catalog) {
		t.Fatalf("runtime providers=%d builtin providers=%d catalog providers=%d", len(providers), len(ai.BuiltinProviders()), len(catalog))
	}
	if !slices.ContainsFunc(providers, func(p *ai.ModelsProvider) bool { return p.ID == "anthropic" }) || len(builtin.GetModels()) <= 500 {
		t.Fatalf("incomplete builtin runtime: %d models", len(builtin.GetModels()))
	}
	for _, provider := range providers {
		list := builtin.GetAllModels(provider.ID)
		if len(list) == 0 {
			t.Fatal("provider has no runtime models", provider.ID)
		}
		for _, model := range list {
			if model.ProviderID() != provider.ID {
				t.Fatal("model of another provider", provider.ID, model.ProviderID())
			}
		}
	}
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	anthropic := services.ModelRuntime().GetModel("anthropic", "claude-haiku-4-5")
	if anthropic == nil || anthropic.ProviderMeta.API != ai.APIAnthropicMessages {
		t.Fatal("missing Anthropic API model")
	}
	radius := services.ModelRuntime().GetModel("radius", "balanced")
	if radius == nil || radius.ProviderMeta.API != ai.APIPiMessages || radius.ProviderMeta.ProviderID != "radius" {
		t.Fatal("missing Radius model")
	}
}

// .upstream/v0.87.1/packages/ai/test/providers.test.ts:734
func TestFauxQueuedResponsesThroughModelRuntime(t *testing.T) {
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	provider.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("hello from faux")}, StopReason: "stop"})})
	result := services.ModelRuntime().CompleteSimple(t.Context(), provider.GetModel(), ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}, ai.StreamOptions{})
	if result.StopReason != ai.StopReasonStop || !reflect.DeepEqual(result.Content, []ai.AssistantContentBlock{ai.TextContent{Text: "hello from faux"}}) || provider.CallCount() != 1 {
		t.Fatalf("result=%#v calls=%d", result, provider.CallCount())
	}
}
