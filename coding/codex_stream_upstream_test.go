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
