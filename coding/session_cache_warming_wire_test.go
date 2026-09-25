package coding

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// anthropicWarmServer records every Messages API request body and header and
// answers with a 500k-token prompt, large enough that idle warming pays off.
type anthropicWarmServer struct {
	mu       sync.Mutex
	bodies   []map[string]any
	headers  []http.Header
	received chan struct{}
}

func (s *anthropicWarmServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	data, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(data, &body)
	s.mu.Lock()
	s.bodies = append(s.bodies, body)
	s.headers = append(s.headers, r.Header.Clone())
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	for _, line := range []string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"msg","model":"claude-opus-4-6","usage":{"input_tokens":500000,"output_tokens":0}}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
	} {
		_, _ = io.WriteString(w, line+"\n\n")
	}
	s.received <- struct{}{}
}

func (s *anthropicWarmServer) requests() ([]map[string]any, []http.Header) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.bodies...), append([]http.Header(nil), s.headers...)
}

// A warm request replays the session request byte for byte through the real
// Anthropic provider: same system, messages, tools, and cache_control
// breakpoints, with only max_tokens lowered to 1. Its usage is persisted as
// a cache_warm entry and reported as entry_appended. Uses a loopback server.
func TestCacheWarmingReplaysTheExactProviderPayload(t *testing.T) {
	server := &anthropicWarmServer{received: make(chan struct{}, 8)}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	tmp := t.TempDir()
	t.Setenv("PIG_HOME", tmp)
	t.Setenv("PI_CACHE_RETENTION", "")
	agentDir := filepath.Join(tmp, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A 12-second cache lifetime schedules the refresh two seconds out.
	models := `{"providers":{"anthropic":{"baseUrl":"` + httpServer.URL + `","apiKey":"test-key","modelOverrides":{"claude-opus-4-6":{"promptCache":{"short":12}}}}}}`
	for name, body := range map[string]string{"models.json": models, "settings.json": `{"cacheWarming":"idle"}`} {
		if err := os.WriteFile(filepath.Join(agentDir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	model, err := BuildModel("anthropic/claude-opus-4-6", services)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := NewSession(services, SessionOptions{Model: model, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	appended := make(chan json.RawMessage, 1)
	go func() {
		for event := range sess.Events() {
			if entry, ok := event.(agent.EntryAppendedEvent); ok {
				appended <- entry.Entry
			}
		}
	}()

	if _, err := sess.Send(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	<-server.received
	select {
	case <-server.received:
	case <-time.After(10 * time.Second):
		t.Fatal("no warm request within the refresh window")
	}
	bodies, headers := server.requests()
	original, warm := bodies[0], bodies[1]
	if warm["max_tokens"] != float64(1) {
		t.Fatalf("warm max_tokens = %v, want 1", warm["max_tokens"])
	}
	if encoded, _ := json.Marshal(original); !strings.Contains(string(encoded), `"cache_control":{"type":"ephemeral"}`) {
		t.Fatalf("session request carries no cache_control breakpoint: %s", encoded)
	}
	delete(original, "max_tokens")
	delete(warm, "max_tokens")
	if !reflect.DeepEqual(warm, original) {
		t.Fatalf("warm payload differs from the session request:\nwarm:     %v\noriginal: %v", warm, original)
	}
	// Content-Length differs only by the shorter max_tokens value.
	headers[0].Del("Content-Length")
	headers[1].Del("Content-Length")
	if !reflect.DeepEqual(headers[1], headers[0]) {
		t.Fatalf("warm headers differ:\nwarm:     %v\noriginal: %v", headers[1], headers[0])
	}

	select {
	case raw := <-appended:
		var entry struct {
			Type, Kind, Provider, Model string
			Usage                       ai.Usage
		}
		if err := json.Unmarshal(raw, &entry); err != nil {
			t.Fatal(err)
		}
		if entry.Type != "usage" || entry.Kind != "cache_warm" || entry.Provider != "anthropic" || entry.Usage.Input != 500_000 {
			t.Fatalf("entry_appended = %s", raw)
		}
		wantUsage := entry.Usage
		wantCost := ai.CalculateCost(model, &wantUsage)
		if entry.Usage.Cost != wantCost || wantCost.Total == 0 {
			t.Fatalf("entry_appended usage cost = %+v, want provider-calculated %+v", entry.Usage.Cost, wantCost)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no entry_appended for the refresh")
	}
	if stats := sess.Inner().Accounting(); stats.AssistantMessages != 1 || stats.Tokens.Input != 1_000_000 {
		t.Fatalf("session accounting = %+v, want one assistant and the refresh usage", stats)
	}
}
