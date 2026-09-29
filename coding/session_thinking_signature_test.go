package coding

import (
	"bytes"
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

// Pi anthropic-messages.ts:641-648 preserves an explicit empty signature, and :1333-1347 replays it as thinking for allowEmptySignature models. Exercise the Session write/reopen boundary rather than synthesizing a retained Go block.
func TestSessionPreservesEmptyThinkingSignatureThroughReopenAndReplay(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	requests := make(chan []byte, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"model\":\"model\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"reasoning\",\"signature\":\"\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"answer\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()
	agentDir := t.TempDir()
	models := fmt.Sprintf(`{"providers":{"custom-anthropic":{"baseUrl":%q,"api":"anthropic-messages","apiKey":"test-key","models":[{"id":"model","name":"Model","reasoning":true,"contextWindow":1048576,"maxTokens":1024,"compat":{"allowEmptySignature":true}}]}}}`, server.URL)
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	model, err := BuildModel("custom-anthropic/model", services)
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(services, SessionOptions{Model: model, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if session != nil {
			if err := session.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	if _, err := session.Send(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
	<-requests
	path := session.Path()
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	session = nil
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(stored, []byte(`"thinkingSignature":""`)) {
		t.Fatalf("persisted history lost explicit empty signature: %s", stored)
	}
	session, err = NewSession(services, SessionOptions{Model: model, ResumePath: path, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	var assistant []ai.AssistantContentBlock
	for _, message := range session.agent.Messages() {
		if message.Assistant != nil {
			assistant = message.Assistant.Content
		}
	}
	encoded, err := json.Marshal(assistant)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `[{"type":"thinking","thinking":"reasoning","thinkingSignature":""},{"type":"text","text":"answer"}]` {
		t.Fatalf("reopened content = %s", encoded)
	}
	if _, err := session.Send(t.Context(), "continue"); err != nil {
		t.Fatal(err)
	}
	var request struct {
		Messages []struct {
			Role    string            `json:"role"`
			Content []json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(<-requests, &request); err != nil {
		t.Fatal(err)
	}
	for _, message := range request.Messages {
		if message.Role == "assistant" {
			for _, raw := range message.Content {
				var block struct {
					Type      string  `json:"type"`
					Thinking  string  `json:"thinking"`
					Signature *string `json:"signature"`
				}
				if err := json.Unmarshal(raw, &block); err != nil {
					t.Fatal(err)
				}
				if block.Type == "thinking" && block.Thinking == "reasoning" && block.Signature != nil && *block.Signature == "" {
					return
				}
			}
		}
	}
	t.Fatalf("replay lost the signed-empty thinking block: %#v", request.Messages)
}
