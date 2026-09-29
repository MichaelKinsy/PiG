package coding

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestModelRuntimeReplaysSerializedFailedHistoryThroughBothOpenAIAPIs(t *testing.T) {
	for _, api := range []ai.API{ai.APIOpenAICompletions, ai.APIOpenAIResponses} {
		for _, stop := range []string{"error", "aborted"} {
			t.Run(string(api)+"/"+stop, func(t *testing.T) {
				raw := fmt.Sprintf(`{"role":"assistant","api":%q,"provider":"openai","model":"replay-target","content":[{"type":"thinking","thinking":"poisoned history","thinkingSignature":"{\"type\":\"reasoning\",\"id\":\"rs_poisoned\"}"},{"type":"text","text":""},{"type":"toolCall","id":"orphan","name":"tool","arguments":{}}],"stopReason":%q,"timestamp":1}`, api, stop)
				var stored agent.AgentMessage
				if err := json.Unmarshal([]byte(raw), &stored); err != nil {
					t.Fatal(err)
				}
				received := make(chan string, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var payload map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						http.Error(w, err.Error(), 400)
						return
					}
					input := payload["input"]
					if api == ai.APIOpenAICompletions {
						input = payload["messages"]
					}
					received <- string(input)
					if strings.Contains(string(input), "poisoned") || strings.Contains(string(input), "orphan") {
						http.Error(w, "incomplete assistant replayed", 400)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if api == ai.APIOpenAICompletions {
						_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
					} else {
						_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
					}
				}))
				defer server.Close()
				dir := t.TempDir()
				config := fmt.Sprintf(`{"providers":{"openai":{"api":%q,"baseUrl":%q,"apiKey":"test","models":[{"id":"replay-target","name":"Replay target","reasoning":true,"input":["text"],"contextWindow":128000,"maxTokens":4096}]}}}`, api, server.URL)
				if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0600); err != nil {
					t.Fatal(err)
				}
				services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				model, err := BuildModel("openai/replay-target", services)
				if err != nil {
					t.Fatal(err)
				}
				history := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("before")}, stored.Assistant.LLMMessage(), ai.UserMessage{Content: ai.UserText("continue")}}}
				result := services.ModelRuntime().Stream(t.Context(), model, history, ai.StreamOptions{APIKey: "test"}).Result()
				if result.StopReason != ai.StopReasonStop {
					t.Fatalf("response=%#v", result)
				}
				input := <-received
				var messages []map[string]any
				if err := json.Unmarshal([]byte(input), &messages); err != nil || len(messages) != 2 {
					t.Fatalf("input=%s err=%v", input, err)
				}
				for _, message := range messages {
					if message["role"] != "user" {
						t.Fatalf("failed history survived: %s", input)
					}
				}
			})
		}
	}
}
