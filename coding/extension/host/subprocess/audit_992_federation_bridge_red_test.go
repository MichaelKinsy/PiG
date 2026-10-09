//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Red test from the 0.99.1 -> 0.99.2 packages/ai audit (audit-992-ai finding F6).
// The D74 builtin-API bridge (shims/pi-ai-bridge.mjs) rejects an anthropic-messages request that has no apiKey and no
// auth header with "No API key for provider: anthropic" before it reaches the host. Pi 0.99.2 sends it with workload
// identity federation when the federation variables are in options.env or the process env.
// Probe: real @earendil-works/pi-ai@0.99.2 compat completeSimple(anthropic model, ctx, {env: federation}) with no key
// attempts the token exchange ("Failed to reach token endpoint ...: TypeError: fetch failed" against a closed port).
func TestAudit992NodeBridgeForwardsFederationOnlyAnthropicRequest(t *testing.T) {
	shortSockDir(t)
	host := newTestHost(t)
	bridge := NewUIBridge(func() {})
	model := map[string]any{"id": "anthropic/claude-test", "modelId": "claude-test", "provider": map[string]any{"id": "anthropic"}, "api": "anthropic-messages"}
	bridge.SetHostAction("getModelInfo", func() map[string]any { return model })
	bridge.SetHostAction("getModels", func() []map[string]any { return []map[string]any{model} })
	var calls int
	var request map[string]any
	bridge.SetHostAction("streamModel", func(_ context.Context, _ map[string]any, got map[string]any) (*ai.AssistantMessageEventStream, error) {
		calls++
		request = got
		final := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "ok"}}, Provider: "anthropic", Model: "claude-test", StopReason: ai.StopReasonStop}
		stream := ai.NewAssistantMessageEventStream()
		if err := stream.Push(ai.StartEvent{Partial: &ai.AssistantMessage{Provider: "anthropic", Model: "claude-test", StopReason: ai.StopReasonPending}}); err != nil {
			return nil, err
		}
		if err := stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: final}); err != nil {
			return nil, err
		}
		return stream, nil
	})
	host.SetUIBridge(bridge)
	defer host.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ext, err := host.Load(ctx, ExtConfig{Name: "audit-992-federation-bridge", Source: filepath.Join("testdata", "audit-992-federation-bridge.mjs"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := ext.Commands["federation_complete"].Handler(ctx, ""); err != nil {
		t.Errorf("command: %v", err)
	}
	if calls != 1 {
		t.Fatalf("host stream calls = %d, want 1 (the bridge must not reject a federation-only anthropic request)", calls)
	}
	env, _ := request["env"].(map[string]any)
	if env["ANTHROPIC_FEDERATION_RULE_ID"] != "fdrl_test" {
		t.Fatalf("request env = %#v, want the federation variables forwarded", request["env"])
	}
}
