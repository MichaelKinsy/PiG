package ai_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// .upstream/v0.87.1/packages/ai/test/providers.test.ts:50
func TestBuiltinModelsRuntimeRegistersCatalog(t *testing.T) {
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	models := services.ModelRuntime().GetModels()
	providers := map[string]int{}
	for _, model := range models {
		providers[model.ProviderMeta.ProviderID]++
	}
	catalog := ai.ListProviders()
	if len(providers) != len(catalog) || providers["anthropic"] == 0 || len(models) <= 500 {
		t.Fatalf("runtime providers=%d catalog providers=%d models=%d", len(providers), len(catalog), len(models))
	}
	for _, provider := range catalog {
		if providers[provider] == 0 {
			t.Fatal("provider has no runtime models", provider)
		}
	}
	anthropic := services.ModelRuntime().GetModel("anthropic", "claude-haiku-4-5")
	if anthropic == nil || anthropic.ProviderMeta.API != ai.APIAnthropicMessages {
		t.Fatal("missing Anthropic API model")
	}
	radius := services.ModelRuntime().GetModel("radius", "balanced")
	if radius == nil || radius.ProviderMeta.API != ai.APIPiMessages || radius.ProviderMeta.ProviderID != "radius" {
		t.Fatal("missing Radius model")
	}
	if !slices.Contains(catalog, "anthropic") {
		t.Fatal("missing catalog provider")
	}
}

// .upstream/v0.87.1/packages/ai/test/providers.test.ts:734
func TestFauxQueuedResponsesThroughModelRuntime(t *testing.T) {
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
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
