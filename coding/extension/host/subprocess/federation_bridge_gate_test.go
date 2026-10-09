//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// The D74 bridge gate for anthropic-messages mirrors anthropic-messages.ts:614-615 and 932-937 (0.99.2): federation
// replaces the request-auth assertion only for provider "anthropic" with the rule, organization and identity token file
// all set (getAnthropicFederation, anthropic-messages.ts:343-372) in options.env or the process env.
func TestNodeBridgeFederationGate(t *testing.T) {
	for _, tc := range []struct {
		command string
		env     map[string]string
		calls   int
	}{
		{command: "federation_incomplete"},
		{command: "federation_empty_value"},
		{command: "federation_other_provider"},
		{command: "federation_no_env"},
		{command: "federation_process_env", env: map[string]string{"ANTHROPIC_FEDERATION_RULE_ID": "fdrl_test", "ANTHROPIC_ORGANIZATION_ID": "org-test", "ANTHROPIC_IDENTITY_TOKEN_FILE": "/nonexistent/identity.jwt"}, calls: 1},
	} {
		t.Run(tc.command, func(t *testing.T) {
			for _, name := range []string{"ANTHROPIC_FEDERATION_RULE_ID", "ANTHROPIC_ORGANIZATION_ID", "ANTHROPIC_IDENTITY_TOKEN_FILE"} {
				t.Setenv(name, tc.env[name])
			}
			shortSockDir(t)
			host := newTestHost(t)
			bridge := NewUIBridge(func() {})
			models := []map[string]any{
				{"id": "anthropic/claude-test", "modelId": "claude-test", "provider": map[string]any{"id": "anthropic"}, "api": "anthropic-messages"},
				{"id": "minimax/claude-test", "modelId": "claude-test", "provider": map[string]any{"id": "minimax"}, "api": "anthropic-messages"},
			}
			bridge.SetHostAction("getModelInfo", func() map[string]any { return models[0] })
			bridge.SetHostAction("getModels", func() []map[string]any { return models })
			var calls int
			bridge.SetHostAction("streamModel", func(_ context.Context, _ map[string]any, _ map[string]any) (*ai.AssistantMessageEventStream, error) {
				calls++
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
			ext, err := host.Load(ctx, ExtConfig{Name: "federation-bridge-gate", Source: filepath.Join("testdata", "federation-bridge-gate.mjs"), Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			if err := ext.Commands[tc.command].Handler(ctx, ""); err != nil {
				t.Errorf("command: %v", err)
			}
			if calls != tc.calls {
				t.Fatalf("host stream calls = %d, want %d", calls, tc.calls)
			}
		})
	}
}
