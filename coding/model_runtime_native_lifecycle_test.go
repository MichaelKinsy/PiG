package coding

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestNativeProviderInvalidRefreshDoesNotPublish(t *testing.T) {
	// provider-composer.ts validates refreshed extension models before publishing its synchronous catalog.
	services, _ := nativeCompatServices(t, "", nil)
	runtime := services.ModelRuntime()
	if err := runtime.RegisterProvider("invalid-refresh", ProviderConfigInput{BaseURL: "https://fixture.invalid", APIKey: "key", Models: []ai.AnyModel{nativeCompatModel("base", "invalid-refresh", "https://fixture.invalid")}, RefreshModels: func(ai.RefreshModelsContext) ([]ai.AnyModel, error) {
		return []ai.AnyModel{&ai.Model{ID: "invalid", DisplayName: "Invalid"}}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	result := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)})
	if result.Errors["invalid-refresh"] == nil {
		t.Error("invalid refreshed model was not rejected")
	}
	if runtime.GetModel("invalid-refresh", "base") == nil || runtime.GetModel("invalid-refresh", "invalid") != nil {
		t.Error("invalid refresh replaced the last valid catalog")
	}
}

func TestNativeProviderRunsThroughSession(t *testing.T) {
	services, _ := nativeCompatServices(t, "", nil)
	raw := nativeCompatModel("session", "native-session", "https://fixture.invalid")
	raw.Capabilities.ContextWindow = 100000
	provider := nativeCompatProvider(raw)
	calls := 0
	provider.StreamSimple = func(_ context.Context, m *ai.Model, _ ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		calls++
		if options.APIKey != "key" || m.ProviderMeta.ProviderID != "native-session" {
			t.Errorf("native request model=%+v key=%q", m, options.APIKey)
		}
		message := &ai.AssistantMessage{API: m.ProviderMeta.API, Provider: m.ProviderMeta.ProviderID, Model: m.ID, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "native done"}}, StopReason: ai.StopReasonStop}
		stream := ai.NewAssistantMessageEventStream()
		_ = stream.Push(ai.StartEvent{Partial: message})
		_ = stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: message})
		return stream, nil
	}
	if err := services.ModelRuntime().RegisterNativeProvider(provider); err != nil {
		t.Fatal(err)
	}
	selected, err := BuildModel(provider.ID+"/"+raw.ID, services)
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(services, SessionOptions{Model: selected, SystemPrompt: "test", SkipBuiltinTools: true, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	messages, err := session.Send(t.Context(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	var replies []ai.AssistantContentBlock
	for _, message := range messages {
		if message.Assistant != nil {
			replies = append(replies, message.Assistant.Content...)
		}
	}
	if calls != 1 || !reflect.DeepEqual(replies, []ai.AssistantContentBlock{ai.TextContent{Text: "native done"}}) {
		t.Fatalf("calls=%d messages=%+v", calls, messages)
	}
}
