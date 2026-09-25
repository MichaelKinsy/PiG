package subprocess

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestNodeModelRegistryStreamsThroughHostRuntime(t *testing.T) {
	shortSockDir(t)
	host := newTestHost(t)
	bridge := NewUIBridge(func() {})
	model := map[string]any{"id": "openrouter/org/model/name", "modelId": "org/model/name", "provider": map[string]any{"id": "openrouter"}, "api": "openai-responses"}
	bridge.SetHostAction("getModelInfo", func() map[string]any { return model })
	bridge.SetHostAction("getModels", func() []map[string]any { return []map[string]any{model} })
	var calls int
	bridge.SetHostAction("streamModel", func(_ context.Context, model map[string]any, _ map[string]any) (*ai.AssistantMessageEventStream, error) {
		calls++
		if model["provider"] != "openrouter" || model["modelId"] != "org/model/name" {
			t.Fatalf("model = %#v", model)
		}
		start := &ai.AssistantMessage{Provider: "openrouter", Model: "org/model/name", StopReason: ai.StopReasonPending}
		partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "streamed"}}, Provider: "openrouter", Model: "org/model/name", StopReason: ai.StopReasonPending}
		final := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "streamed"}}, Provider: "openrouter", Model: "org/model/name", StopReason: ai.StopReasonStop}
		stream := ai.NewAssistantMessageEventStream()
		for _, event := range []ai.AssistantMessageEvent{
			ai.StartEvent{Partial: start}, ai.TextStartEvent{ContentIndex: 0, Partial: partial},
			ai.TextDeltaEvent{ContentIndex: 0, Delta: "streamed", Partial: partial},
			ai.TextEndEvent{ContentIndex: 0, Content: "streamed", Partial: partial},
			ai.DoneEvent{Reason: ai.StopReasonStop, Message: final},
		} {
			if err := stream.Push(event); err != nil {
				t.Fatal(err)
			}
		}
		return stream, nil
	})
	host.SetUIBridge(bridge)
	defer host.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ext, err := host.Load(ctx, ExtConfig{Name: "node-model-runtime", Source: filepath.Join("testdata", "node-model-runtime.mjs"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	command, ok := ext.Commands["model_registry_runtime"]
	if !ok {
		t.Fatal("model_registry_runtime command not registered")
	}
	if err := command.Handler(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("stream calls = %d, want 3", calls)
	}
}
