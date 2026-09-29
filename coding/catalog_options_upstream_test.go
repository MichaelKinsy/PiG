package coding

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Fireworks catalog regressions must survive models.json overlays and the same
// ModelRuntime.Complete path used by interactive, print and RPC sessions.
func TestCatalogOptionsThroughModelRuntime(t *testing.T) {
	for _, tc := range []struct {
		name, provider, id, api, compat string
		thinking                        ai.ThinkingLevel
		retention                       ai.CacheRetention
	}{
		{"Kimi K3 native effort", "fireworks", "accounts/fireworks/models/kimi-k3", "openai-completions", `{}`, ai.ThinkingMax, ai.CacheRetentionShort},
		{"Kimi K3 replacement uses its own map", "fireworks", "accounts/fireworks/models/kimi-k3", "openai-completions", `{}`, ai.ThinkingMax, ai.CacheRetentionShort},
		{"Anthropic configured compat wins", "anthropic", "claude-opus-4-8", "anthropic-messages", `{"supportsMidConvoSystemMessages":false,"supportsMidConvoToolChanges":false,"supportsMidConvoEffort":false}`, ai.ThinkingOff, ai.CacheRetentionShort},
		{"OpenRouter session header", "openrouter", "anthropic/claude-opus-4.8", "anthropic-messages", `{}`, ai.ThinkingOff, ai.CacheRetentionShort},
		{"OpenRouter explicit cache off", "openrouter", "anthropic/claude-opus-4.8", "anthropic-messages", `{}`, ai.ThinkingOff, ai.CacheRetentionNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var payload map[string]any
			var headers http.Header
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				headers = r.Header.Clone()
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if tc.api == "openai-completions" {
					_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				} else {
					_, _ = io.WriteString(w, "event: message_start\ndata: {\"message\":{\"id\":\"fixture\",\"usage\":{\"input_tokens\":1}}}\n\nevent: message_delta\ndata: {\"delta\":{\"stop_reason\":\"end_turn\"}}\n\nevent: message_stop\ndata: {}\n\n")
				}
			}))
			defer server.Close()
			agentDir := t.TempDir()
			config := fmt.Sprintf(`{"providers":{%q:{"baseUrl":%q,"apiKey":"test-key","api":%q,"models":[{"id":%q,"reasoning":true,"compat":%s}]}}}`, tc.provider, server.URL, tc.api, tc.id, tc.compat)
			if tc.name == "Kimi K3 native effort" {
				// provider-composer.ts:modelFromJson replaces custom definitions; modelOverrides retains the catalog's native max effort.
				config = fmt.Sprintf(`{"providers":{%q:{"baseUrl":%q,"apiKey":"test-key","modelOverrides":{%q:{"compat":%s}}}}}`, tc.provider, server.URL, tc.id, tc.compat)
			}
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
			runtime := services.ModelRuntime()
			model := runtime.GetModel(tc.provider, tc.id)
			if model == nil {
				t.Fatal("model not found")
			}
			result := runtime.Complete(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Use the tool")}}, Tools: []ai.ToolSchema{{Name: "lookup", Description: "Look up a value", Parameters: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}}}}}, ai.StreamOptions{Thinking: tc.thinking, SessionID: "catalog-session", CacheRetention: tc.retention})
			if result.StopReason != ai.StopReasonStop {
				t.Fatalf("result = %+v", result)
			}
			switch tc.provider {
			case "fireworks":
				// .upstream/v0.87.1/packages/ai/src/api/openai-completions.ts:1639
				wantEffort := "max"
				if tc.name == "Kimi K3 replacement uses its own map" {
					wantEffort = "high"
				}
				if payload["reasoning_effort"] != wantEffort {
					t.Fatalf("effort = %v, want %s", payload["reasoning_effort"], wantEffort)
				}
			case "anthropic":
				// .upstream/v0.87.1/packages/ai/src/api/anthropic-messages.ts:206-219,1114-1146
				tools, ok := payload["tools"].([]any)
				if !ok || len(tools) != 1 {
					t.Fatalf("tools = %v, want only the declared lookup tool", payload["tools"])
				}
			case "openrouter":
				// .upstream/v0.87.1/packages/ai/src/api/anthropic-messages.ts:563-564,966-968
				want := "catalog-session"
				if tc.retention == ai.CacheRetentionNone {
					want = ""
				}
				if headers.Get("x-session-id") != want || headers.Get("x-session-affinity") != "" {
					t.Fatalf("headers = %v", headers)
				}
				if tc.retention == ai.CacheRetentionNone {
					data, err := json.Marshal(payload)
					if err != nil {
						t.Fatal(err)
					}
					var fields any
					if err := json.Unmarshal(data, &fields); err != nil {
						t.Fatal(err)
					}
					assertNoCatalogCacheControl(t, fields)
				}
			}
		})
	}
}

func assertNoCatalogCacheControl(t *testing.T, value any) {
	t.Helper()
	switch value := value.(type) {
	case map[string]any:
		if _, ok := value["cache_control"]; ok {
			t.Fatalf("unexpected cache_control: %v", value)
		}
		for _, child := range value {
			assertNoCatalogCacheControl(t, child)
		}
	case []any:
		for _, child := range value {
			assertNoCatalogCacheControl(t, child)
		}
	}
}
