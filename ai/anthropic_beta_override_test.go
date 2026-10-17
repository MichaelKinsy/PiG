package ai

import (
	"testing"
)

// pig divergence (D130): Pi #10759. An explicit anthropic-beta header replaces the computed betas (anthropic-auth-token.test.ts), but Pi's buildParams (anthropic-messages.ts:1154-1164, :1235-1241) still sends thinking.block_binding and the mid-conversation output_config markers of a managed-effort model, whose betas the override removed, so the API rejects the request with 400 ("messages.1.output_config: Extra inputs are not permitted", or "thinking.adaptive.block_binding: Extra inputs are not permitted" without the markers). PiG sends each managed field only with its beta: block_binding with thinking-binding-controls-2026-08-01, and the markers with mid-conversation-output-config-2026-07-01; without the markers the active effort is the top-level output_config effort.
func TestAnthropicManagedEffortBodyFollowsTheBetasSent(t *testing.T) {
	const (
		binding  = "thinking-binding-controls-2026-08-01"
		midConvo = "mid-conversation-output-config-2026-07-01"
	)
	history := Context{Messages: []Message{
		UserMessage{Content: UserText("one"), Timestamp: 1},
		AssistantMessage{
			Content: []AssistantContentBlock{TextContent{Text: "answer"}},
			API:     APIAnthropicMessages, Provider: "anthropic", Model: "claude-opus-5-5",
			ProviderThinkingLevel: "high", StopReason: StopReasonStop, Timestamp: 2,
		},
		UserMessage{Content: UserText("two"), Timestamp: 3},
	}}
	for _, tc := range []struct {
		name         string
		computed     bool
		modelHeaders map[string]string
		headers      ProviderHeaders
		wantBetas    string
		wantBinding  bool
		wantMarkers  []string
		wantEffort   string
	}{
		{
			name:        "computed betas keep every managed field",
			computed:    true,
			wantBetas:   midConvo + "," + binding,
			wantBinding: true, wantMarkers: []string{"high", "low"}, wantEffort: "high",
		},
		{
			name:         "a model header override without the managed betas",
			modelHeaders: map[string]string{"anthropic-beta": "interleaved-thinking-2025-05-14"},
			wantBetas:    "interleaved-thinking-2025-05-14",
			wantEffort:   "low",
		},
		{
			name:       "a null request header suppresses every beta",
			headers:    ProviderHeaders{"anthropic-beta": nil},
			wantEffort: "low",
		},
		{
			name:        "an override listing both managed betas keeps every managed field",
			headers:     ProviderHeaders{"anthropic-beta": new("custom-beta," + binding + "," + midConvo)},
			wantBetas:   "custom-beta," + binding + "," + midConvo,
			wantBinding: true, wantMarkers: []string{"high", "low"}, wantEffort: "high",
		},
		{
			name:        "the binding beta alone keeps block_binding and drops the markers",
			headers:     ProviderHeaders{"anthropic-beta": new(binding)},
			wantBetas:   binding,
			wantBinding: true, wantEffort: "low",
		},
		{
			name:        "the mid-conversation beta alone keeps the markers and drops block_binding",
			headers:     ProviderHeaders{"anthropic-beta": new(midConvo)},
			wantBetas:   midConvo,
			wantMarkers: []string{"high", "low"}, wantEffort: "high",
		},
		{
			// A beta is matched by its whole name, as the API does: a longer name that starts with a managed beta is another beta.
			name:       "betas that only start with the managed names keep neither field",
			headers:    ProviderHeaders{"anthropic-beta": new(binding + "-v2," + midConvo + "-v2")},
			wantBetas:  binding + "-v2," + midConvo + "-v2",
			wantEffort: "low",
		},
	} {
		// The managed fields follow the betas sent whatever the credential: an OAuth token only adds Pi's Claude Code betas to the computed list (getBetaFeatures, anthropic-messages.ts:1106), and an override replaces those too.
		for _, auth := range []struct {
			name, key, computedPrefix string
		}{
			{name: "API key", key: "anthropic-key"},
			{name: "OAuth token", key: "sk-ant-oat01-test", computedPrefix: "claude-code-20250219,oauth-2025-04-20,"},
		} {
			t.Run(auth.name+"/"+tc.name, func(t *testing.T) {
				clearAnthropicAuthEnv(t)
				cfg := AnthropicConfig{APIKey: auth.key, Model: "claude-opus-5-5", ProviderID: "anthropic", ExtraHeaders: tc.modelHeaders}
				captured, _ := runAnthropicWire(t, cfg, history, StreamOptions{Effort: "low", Headers: tc.headers}, endTurn)
				wantBetas := tc.wantBetas
				if tc.computed {
					wantBetas = auth.computedPrefix + wantBetas
				}
				if got := captured.header.Get("anthropic-beta"); got != wantBetas {
					t.Errorf("anthropic-beta = %q, want %q", got, wantBetas)
				}
				thinking, _ := captured.body["thinking"].(map[string]any)
				if thinking["type"] != "adaptive" {
					t.Errorf("thinking = %v, want adaptive", thinking)
				}
				if _, got := thinking["block_binding"]; got != tc.wantBinding {
					t.Errorf("thinking.block_binding present = %v, want %v (thinking %v)", got, tc.wantBinding, thinking)
				}
				var markers []string
				for _, message := range jsonItems(captured.body["messages"].([]any)) {
					if message["role"] == "system" {
						config, _ := message["output_config"].(map[string]any)
						effort, _ := config["effort"].(string)
						markers = append(markers, effort)
					}
				}
				if len(markers) != len(tc.wantMarkers) || (len(markers) > 0 && markers[0] != tc.wantMarkers[0]) || (len(markers) > 1 && markers[1] != tc.wantMarkers[1]) {
					t.Errorf("output_config markers = %q, want %q", markers, tc.wantMarkers)
				}
				config, _ := captured.body["output_config"].(map[string]any)
				if config["effort"] != tc.wantEffort {
					t.Errorf("output_config = %v, want effort %q", config, tc.wantEffort)
				}
			})
		}
	}
}
