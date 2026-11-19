package codingagent

// pi: packages/coding-agent/src/core/model-runtime.ts streamSimple (:744-776: the caller's options go on as `rest`, no transport added); the pi-ai compat stream functions an extension imports pass the options it gave

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// An extension's model call reaches the provider with the options the extension gave, as in Pi: the settings' transport is not one of them. The two calls PiG routes an extension's stream through (the interactive mode's
// own and the streamModel host action) both carry a transport only when the request names one.
func TestExtensionModelStreamsCarryNoTransportOfTheSettings(t *testing.T) {
	model := &ai.Model{ID: "model", ProviderMeta: ai.ProviderMetadata{ProviderID: "provider"}}
	for _, route := range []string{"streamForSubprocess", "the streamModel host action"} {
		t.Run(route, func(t *testing.T) {
			handle := &modelRequestCaptureHandle{recordingCompactHandle: &recordingCompactHandle{}}
			mode := &InteractiveMode{opts: InteractiveModeOptions{
				SessionHandle: handle,
				ModelBuilder:  func(string) (*ai.Model, error) { return model, nil },
				Settings:      Settings{Transport: string(ai.TransportWebSocket)},
			}}
			stream := mode.streamForSubprocess
			if route != "streamForSubprocess" {
				bridge := &captureUIBridge{}
				mode.opts.SubprocessUIBridge = bridge
				detach := mode.wireSubprocessHostCallbacks()
				defer detach()
				stream = bridge.actions["streamModel"].(func(context.Context, map[string]any, map[string]any) (*ai.AssistantMessageEventStream, error))
			}
			wire := map[string]any{"provider": "provider", "modelId": "model"}
			if _, err := stream(t.Context(), wire, map[string]any{"messages": []any{}}); err != nil {
				t.Fatal(err)
			}
			if handle.options.Transport != "" {
				t.Errorf("a request that names no transport reached the provider with transport %q (the settings' value), want none", handle.options.Transport)
			}
			if _, err := stream(t.Context(), wire, map[string]any{"messages": []any{}, "transport": "sse"}); err != nil {
				t.Fatal(err)
			}
			if handle.options.Transport != ai.TransportSSE {
				t.Errorf("a request that names transport sse reached the provider with %q", handle.options.Transport)
			}
		})
	}
}
