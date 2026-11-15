// SPDX-License-Identifier: MIT

package coding

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// copilotFableRecord is the github-copilot zz-fixture-claude-fable-6 row of a refreshed models-store.json. The generated catalog and Pi's pin carry no such row, so
// the model reaches the session through the remote catalog alone. baseUrl is the faux Copilot endpoint.
const copilotFableRecord = `{"api":"anthropic-messages","baseUrl":"%s","compat":{"forceAdaptiveThinking":true,"supportsMidConvoSystemMessages":true},"contextWindow":1000000,"cost":{"input":8,"output":40,"cacheRead":0.2,"cacheWrite":10},"headers":{"User-Agent":"GitHubCopilotChat/0.35.0","Editor-Version":"vscode/1.107.0","Editor-Plugin-Version":"copilot-chat/0.35.0","Copilot-Integration-Id":"vscode-chat"},"id":"zz-fixture-claude-fable-6","input":["text","image"],"maxTokens":128000,"name":"Claude Fable 6","provider":"github-copilot","reasoning":true,"thinkingLevelMap":{"off":null,"xhigh":"xhigh","max":"max"},"type":"chat"}`

const copilotFableSummaryStream = `event: message_start
data: {"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","model":"zz-fixture-claude-fable-6","content":[],"usage":{"input_tokens":10,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"fable summary"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}

`

type copilotFableRequest struct {
	path, authorization string
	body                map[string]any
}

// copilotFableSession builds the session of a github-copilot zz-fixture-claude-fable-6 model that only the remote catalog lists, against a Copilot-shaped faux
// endpoint. Pi: packages/coding-agent/src/core/agent-session.ts (compact). Pi 1.5.2 sent the same request and showed the same text for each fault below
// when probed through the parity harness against the same faux server (the faults and the request shape are the contract of this file).
func copilotFableSession(t *testing.T, handler func(n int, w http.ResponseWriter)) (*Session, func() []copilotFableRequest) {
	t.Helper()
	var mu sync.Mutex
	var requests []copilotFableRequest
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		request := copilotFableRequest{path: r.URL.Path, authorization: r.Header.Get("Authorization")}
		if err := json.Unmarshal(raw, &request.body); err != nil {
			t.Errorf("request body: %v", err)
		}
		mu.Lock()
		requests = append(requests, request)
		n := len(requests)
		mu.Unlock()
		handler(n, w)
	}))
	t.Cleanup(endpoint.Close)
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PI_OFFLINE", "1")
	dir := t.TempDir()
	generatedAt := ai.GetBuiltinModelDataGeneratedAt()
	if generatedAt == nil {
		t.Fatal("the generated catalogs record no generation time")
	}
	entry := ai.ModelsStoreEntry{Models: mustStoredModels([]json.RawMessage{json.RawMessage(fmt.Sprintf(copilotFableRecord, endpoint.URL))}), CheckedAt: new(1.0), LastModified: new(*generatedAt + 1)}
	if err := ai.NewFileModelsStore(filepath.Join(dir, "models-store.json")).Write(t.Context(), "github-copilot", entry); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"compaction":{"keepRecentTokens":1},"retry":{"enabled":true,"maxRetries":2,"baseDelayMs":5}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	services.Registry().SetRuntimeAPIKey("github-copilot", "copilot-token")
	model, err := BuildModel("github-copilot/zz-fixture-claude-fable-6", services)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(services, SessionOptions{Model: model, ThinkingLevel: ai.ThinkingOff})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	now := time.Now().UnixMilli()
	persistQueueMessage(t, s, queueUser("message to compact", now-1000))
	persistQueueMessage(t, s, agent.AgentMessage{Assistant: queueAssistant(s, "assistant response to compact", 100, 0, ai.StopReasonStop, now-500)})
	s.RefreshContext()
	return s, func() []copilotFableRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]copilotFableRequest(nil), requests...)
	}
}

func copilotFableStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, copilotFableSummaryStream)
}

func TestCompactionOfARefreshedCopilotFableModelSendsTheModelRequest(t *testing.T) {
	s, requests := copilotFableSession(t, func(_ int, w http.ResponseWriter) { copilotFableStream(w) })
	// The thinkingLevelMap of the model has off: null, so the session level off is the lowest level the model supports.
	if level := s.ThinkingLevel(); level != ai.ThinkingMinimal {
		t.Fatalf("session level=%q", level)
	}
	if _, err := s.Compact(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	got := requests()
	if len(got) != 1 {
		t.Fatalf("requests=%d", len(got))
	}
	request := got[0]
	if request.path != "/v1/messages" || request.authorization != "Bearer copilot-token" {
		t.Fatalf("path=%q authorization=%q", request.path, request.authorization)
	}
	// The summary request names the refreshed model, asks for the output limit of the model (Pi 6fcb4867: no fixed cap), sends adaptive thinking at the
	// clamped level, and carries no tools.
	want := map[string]any{"model": "zz-fixture-claude-fable-6", "max_tokens": float64(128000), "stream": true, "thinking": map[string]any{"type": "adaptive", "display": "summarized"}, "output_config": map[string]any{"effort": "low"}}
	for key, value := range want {
		if fmt.Sprint(request.body[key]) != fmt.Sprint(value) {
			t.Errorf("%s=%v, want %v", key, request.body[key], value)
		}
	}
	if _, ok := request.body["tools"]; ok {
		t.Error("summary request carries tools")
	}
}

func TestCompactionFaultsOfARefreshedCopilotFableModelShowTheProviderText(t *testing.T) {
	const overloaded = "503 Sorry, the upstream model provider is currently experiencing high demand. Please try another model.\n"
	const promptCount = `{"error":{"message":"prompt token count of 1050000 exceeds the limit of 1000000","code":"model_max_prompt_tokens_exceeded"}}`
	reply := func(status int, contentType, body string) func(int, http.ResponseWriter) {
		return func(_ int, w http.ResponseWriter) {
			w.Header().Set("Content-Type", contentType)
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		}
	}
	for _, tc := range []struct {
		name     string
		handler  func(int, http.ResponseWriter)
		wantErr  string
		requests int // per Compact call: the first plus the retries of the retry policy for a transient status
	}{
		{"429 with retry-after", func(n int, w http.ResponseWriter) {
			w.Header().Set("Retry-After", "1")
			reply(429, "text/plain", "429 too many requests\n")(n, w)
		}, "429 429 too many requests", 3},
		{"prompt token count over the window", reply(400, "application/json", promptCount), "400 " + promptCount, 1},
		{"503 high demand", reply(503, "text/plain", overloaded), "503 " + overloaded, 3},
		{"expired token", reply(401, "application/json", `{"type":"error","error":{"type":"authentication_error","message":"Unauthorized: token expired"}}`), "Unauthorized: token expired", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, requests := copilotFableSession(t, tc.handler)
			var ends []agent.CompactionEndEvent
			s.Subscribe(func(event agent.AgentEvent) {
				if end, ok := event.(agent.CompactionEndEvent); ok {
					ends = append(ends, end)
				}
			})
			entries := len(s.inner.Load().GetBranch())
			for attempt := 1; attempt <= 2; attempt++ {
				_, err := s.Compact(t.Context(), "")
				// The user sees the text of the provider after the prefix of Pi, never a bare "Compaction failed".
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("attempt %d: error=%v, want it to contain %q", attempt, err, tc.wantErr)
				}
				end := ends[len(ends)-1]
				if end.Aborted || end.HasResult || end.ErrorMessage != "Compaction failed: "+err.Error() {
					t.Fatalf("attempt %d: compaction_end=%+v", attempt, end)
				}
				// An errored compaction writes no entry, as in Pi, and /compact can run again at once: it repeats the request.
				if got := len(s.inner.Load().GetBranch()); got != entries {
					t.Fatalf("attempt %d: %d entries, want %d", attempt, got, entries)
				}
				if got := len(requests()); got != attempt*tc.requests {
					t.Fatalf("attempt %d: %d requests, want %d", attempt, got, attempt*tc.requests)
				}
			}
		})
	}
}
