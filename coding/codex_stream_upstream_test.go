package coding

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/gorilla/websocket"
)

func TestModelRuntimeCodexSimpleThinkingAndAutoTransport(t *testing.T) {
	for _, transport := range []ai.Transport{ai.TransportSSE, ai.TransportAuto} {
		t.Run(string(transport), func(t *testing.T) {
			session := "runtime-codex-" + string(transport)
			closed := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				terminal := `{"type":"response.completed","response":{"id":"resp_runtime","status":"completed","end_turn":false}}`
				if transport == ai.TransportAuto {
					if !websocket.IsWebSocketUpgrade(r) {
						http.Error(w, "expected WebSocket", 400)
						return
					}
					connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
					if err != nil {
						return
					}
					defer connection.Close()
					defer close(closed)
					if _, _, err = connection.ReadMessage(); err != nil {
						return
					}
					_ = connection.WriteMessage(websocket.TextMessage, []byte(terminal))
					_, _, _ = connection.ReadMessage()
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(w, "data: %s\n\n", terminal)
			}))
			defer server.Close()
			defer ai.CloseOpenAICodexWebSocketSessions(session)
			agentDir := t.TempDir()
			token := "aaa." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acc_test"}}`)) + ".bbb"
			config := fmt.Sprintf(`{"providers":{"openai-codex":{"api":"openai-codex-responses","baseUrl":%q,"apiKey":%q,"models":[{"id":"gpt-5.5","name":"Mapped Codex","reasoning":true,"input":["text"],"contextWindow":400000,"maxTokens":128000,"thinkingLevelMap":{"minimal":"low","xhigh":"xhigh"}}]}}}`, server.URL, token)
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
			model, err := BuildModel("openai-codex/gpt-5.5", services)
			if err != nil {
				t.Fatal(err)
			}
			var request struct {
				Reasoning struct{ Effort, Summary string }
			}
			result := services.ModelRuntime().StreamSimple(t.Context(), model, ai.Context{SystemPrompt: "You are a helpful assistant.", Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Say hello"), Timestamp: 1}}}, ai.StreamOptions{APIKey: token, Transport: transport, SessionID: session, Thinking: ai.ThinkingLevelXHigh, OnPayload: func(value any, _ *ai.Model) (any, error) {
				encoded, err := json.Marshal(value)
				if err == nil {
					err = json.Unmarshal(encoded, &request)
				}
				return nil, err
			}}).Result()
			if result.StopReason != ai.StopReasonStop || result.EndTurn == nil || *result.EndTurn || request.Reasoning.Effort != "xhigh" || request.Reasoning.Summary != "auto" {
				t.Fatalf("response=%#v reasoning=%#v", result, request.Reasoning)
			}
			if transport == ai.TransportAuto {
				stats := ai.GetOpenAICodexWebSocketDebugStats(session)
				if stats == nil || stats.CachedContextRequests != 1 || stats.FullContextRequests != 1 {
					t.Fatalf("stats=%#v", stats)
				}
				ai.CloseOpenAICodexWebSocketSessions(session)
				<-closed
				ai.ResetOpenAICodexWebSocketDebugStats(session)
			}
		})
	}
}

// Regression test for Pi fcc33744b (#12269): ModelRuntime.StreamSimple on a GPT-6 Codex
// model with supportsMidConvoEffort and omitted Thinking (how the coding agent sends "off")
// must send effort "none" as a configuration_update item with fixed reasoning.effort "medium".
func TestModelRuntimeCodexStreamSimpleOffSendsConfigurationUpdateNone(t *testing.T) {
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"type":"response.completed","response":{"id":"resp_off","status":"completed"}}`)
	}))
	defer server.Close()

	agentDir := t.TempDir()
	token := "aaa." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acc_test"}}`)) + ".bbb"
	config := fmt.Sprintf(`{"providers":{"openai-codex":{"api":"openai-codex-responses","baseUrl":%q,"apiKey":%q,"models":[{"id":"gpt-6-sol","name":"GPT-6 Sol","reasoning":true,"input":["text"],"contextWindow":400000,"maxTokens":128000,"compat":{"supportsMidConvoEffort":true},"thinkingLevelMap":{"off":"none"}}]}}}`, server.URL, token)
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	model, err := BuildModel("openai-codex/gpt-6-sol", services)
	if err != nil {
		t.Fatal(err)
	}

	result := services.ModelRuntime().StreamSimple(t.Context(), model, ai.Context{
		Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Say hello"), Timestamp: 1}},
	}, ai.StreamOptions{APIKey: token, Transport: ai.TransportSSE, OnPayload: func(value any, _ *ai.Model) (any, error) {
		encoded, err := json.Marshal(value)
		if err == nil {
			captured = encoded
		}
		return nil, err
	}}).Result()
	if result.StopReason != ai.StopReasonStop {
		t.Fatalf("StopReason = %q (%s), want stop", result.StopReason, result.ErrorMessage)
	}
	if result.ProviderThinkingLevel != "none" {
		t.Errorf("ProviderThinkingLevel = %q, want %q", result.ProviderThinkingLevel, "none")
	}

	var req struct {
		Reasoning *struct {
			Effort  string `json:"effort"`
			Summary string `json:"summary"`
		} `json:"reasoning"`
		Input []struct {
			Type      string `json:"type"`
			Role      string `json:"role"`
			Reasoning *struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
		} `json:"input"`
	}
	if err := json.Unmarshal(captured, &req); err != nil {
		t.Fatalf("unmarshal request: %v (raw=%s)", err, captured)
	}
	if req.Reasoning == nil || req.Reasoning.Effort != "none" || req.Reasoning.Summary != "auto" {
		t.Errorf("reasoning = %+v, want {Effort:none Summary:auto}", req.Reasoning)
	}
	var summary []string
	for _, item := range req.Input {
		if item.Type == "configuration_update" && item.Reasoning != nil {
			summary = append(summary, "configuration_update:"+item.Reasoning.Effort)
		} else if item.Role != "" {
			summary = append(summary, item.Role)
		} else {
			summary = append(summary, item.Type)
		}
	}
	wantSummary := []string{"user"}
	if fmt.Sprint(summary) != fmt.Sprint(wantSummary) {
		t.Errorf("input = %v, want %v (raw=%s)", summary, wantSummary, captured)
	}
}

// Covers the other two simple-stream caller boundaries that share prepareSimpleStreamOptions
// with ModelRuntime.StreamSimple: requestOptionsProvider.SimpleStreamOptions (compaction/completer)
// and ModelRuntime.GetProvider().StreamSimple (builtinStreamSimple).
func TestCodexSimpleStreamPreparationAcrossCallerBoundaries(t *testing.T) {
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"type":"response.completed","response":{"id":"resp_boundary","status":"completed"}}`)
	}))
	defer server.Close()

	agentDir := t.TempDir()
	token := "aaa." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acc_test"}}`)) + ".bbb"
	config := fmt.Sprintf(`{"providers":{"openai-codex":{"api":"openai-codex-responses","baseUrl":%q,"apiKey":%q,"models":[{"id":"gpt-6-sol","name":"GPT-6 Sol","reasoning":true,"input":["text"],"contextWindow":400000,"maxTokens":128000,"compat":{"supportsMidConvoEffort":true},"thinkingLevelMap":{"off":"none"}}]}}}`, server.URL, token)
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	model, err := BuildModel("openai-codex/gpt-6-sol", services)
	if err != nil {
		t.Fatal(err)
	}
	transcript := ai.NormalizeContext(ai.Context{
		Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Say hello"), Timestamp: 1}},
	})

	t.Run("requestOptionsProvider.SimpleStreamOptions", func(t *testing.T) {
		wrapped := newRequestOptionsProvider(model.Provider, "openai-codex", nil).(*requestOptionsProvider)
		got := wrapped.SimpleStreamOptions(model, transcript, ai.StreamOptions{ReasoningEffort: "high"})
		if got.ReasoningEffort != "none" {
			t.Fatalf("SimpleStreamOptions ReasoningEffort = %q, want %q", got.ReasoningEffort, "none")
		}
	})

	t.Run("GetProvider.StreamSimple", func(t *testing.T) {
		captured = nil
		provider := services.ModelRuntime().GetProvider("openai-codex")
		if provider == nil || provider.StreamSimple == nil {
			t.Fatalf("GetProvider(openai-codex) = %#v, want StreamSimple", provider)
		}
		stream, err := provider.StreamSimple(t.Context(), model, transcript, ai.StreamOptions{
			APIKey:    token,
			Transport: ai.TransportSSE,
			OnPayload: func(value any, _ *ai.Model) (any, error) {
				encoded, err := json.Marshal(value)
				if err == nil {
					captured = encoded
				}
				return nil, err
			},
		})
		if err != nil {
			t.Fatalf("GetProvider.StreamSimple: %v", err)
		}
		result := stream.Result()
		if result.StopReason != ai.StopReasonStop {
			t.Fatalf("StopReason = %q (%s), want stop", result.StopReason, result.ErrorMessage)
		}
		if result.ProviderThinkingLevel != "none" {
			t.Errorf("ProviderThinkingLevel = %q, want %q", result.ProviderThinkingLevel, "none")
		}
		var req struct {
			Reasoning *struct {
				Effort  string `json:"effort"`
				Summary string `json:"summary"`
			} `json:"reasoning"`
			Input []struct {
				Type      string `json:"type"`
				Role      string `json:"role"`
				Reasoning *struct {
					Effort string `json:"effort"`
				} `json:"reasoning"`
			} `json:"input"`
		}
		if err := json.Unmarshal(captured, &req); err != nil {
			t.Fatalf("unmarshal request: %v (raw=%s)", err, captured)
		}
		if req.Reasoning == nil || req.Reasoning.Effort != "none" || req.Reasoning.Summary != "auto" {
			t.Errorf("reasoning = %+v, want {Effort:none Summary:auto}", req.Reasoning)
		}
		var summary []string
		for _, item := range req.Input {
			if item.Type == "configuration_update" && item.Reasoning != nil {
				summary = append(summary, "configuration_update:"+item.Reasoning.Effort)
			} else if item.Role != "" {
				summary = append(summary, item.Role)
			} else {
				summary = append(summary, item.Type)
			}
		}
		wantSummary := []string{"user"}
		if fmt.Sprint(summary) != fmt.Sprint(wantSummary) {
			t.Errorf("input = %v, want %v (raw=%s)", summary, wantSummary, captured)
		}
	})
}
