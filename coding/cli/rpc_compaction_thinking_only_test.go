package cli

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
)

// Pi #9602, reproduced end to end. Four responses stopped at the output limit while still thinking. The OpenAI-compatible converter omits
// them from later requests, so the server never counts them. Pi 1.1.0 wrote them whole into the summary request: with this session, a
// llama.cpp-style server that rejects a request over its window, and `compact`, the request was 46276 tokens against a 40000-token window
// and compact failed with "Summarization failed: 400: ... exceeds the available context size", while a prompt of the same session sent 20477 tokens.
// Pi 15e7bdc1 omits thinking from the serialized conversation, so the summary request stays inside the window and compaction succeeds.
func TestRPCCompactionSurvivesThinkingOnlyMessagesTheRequestsOmit(t *testing.T) {
	const window = 40_000
	var mu sync.Mutex
	var sizes []int
	carriedThinking := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request struct {
			Messages json.RawMessage `json:"messages"`
		}
		_ = json.Unmarshal(body, &request)
		tokens := len(request.Messages) / 4
		mu.Lock()
		sizes = append(sizes, tokens)
		carriedThinking = carriedThinking || strings.Contains(string(request.Messages), "reasoning step")
		mu.Unlock()
		if tokens > window {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, `{"error":{"message":"request (%d tokens) exceeds the available context size (%d tokens)","type":"exceed_context_size_error"}}`, tokens, window)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"## Summary\nok"},"finish_reason":null}]}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`+"\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	usage := func(total int) string {
		return fmt.Sprintf(`{"input":%d,"output":16000,"cacheRead":0,"cacheWrite":0,"totalTokens":%d,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`, total-16000, total)
	}
	var session strings.Builder
	session.WriteString(`{"type":"session","version":3,"id":"thinking-only","timestamp":"2026-10-01T00:00:00.000Z","cwd":"."}` + "\n")
	parent, n := "null", 0
	add := func(message string) {
		n++
		id := fmt.Sprintf("e%03d", n)
		fmt.Fprintf(&session, `{"type":"message","id":%q,"parentId":%s,"timestamp":"2026-10-01T00:00:00.000Z","message":%s}`+"\n", id, parent, message)
		parent = `"` + id + `"`
	}
	add(`{"role":"user","content":[{"type":"text","text":"question one"}],"timestamp":1759276800000}`)
	for k := range 4 {
		thinking := strings.Repeat(fmt.Sprintf("reasoning step %d about the problem. ", k), 1700)
		add(fmt.Sprintf(`{"role":"assistant","content":[{"type":"thinking","thinking":%q,"thinkingSignature":"reasoning_content"}],"api":"openai-completions","provider":"mock","model":"qwen","usage":%s,"stopReason":"length","timestamp":%d}`, thinking, usage(17_000+k*300), 1759276801000+k))
		add(fmt.Sprintf(`{"role":"user","content":[{"type":"text","text":"continue %d"}],"timestamp":%d}`, k, 1759276802000+k))
	}
	for k := range 14 {
		add(fmt.Sprintf(`{"role":"user","content":[{"type":"text","text":%q}],"timestamp":%d}`, strings.Repeat(fmt.Sprintf("tail question %d ", k), 180), 1759276900000+k*10))
		add(fmt.Sprintf(`{"role":"assistant","content":[{"type":"text","text":%q}],"api":"openai-completions","provider":"mock","model":"qwen","usage":%s,"stopReason":"stop","timestamp":%d}`, strings.Repeat(fmt.Sprintf("tail answer %d ", k), 180), strings.Replace(usage(1800), `"output":16000`, `"output":800`, 1), 1759276900005+k*10))
	}
	add(`{"role":"user","content":[{"type":"text","text":"last question"}],"timestamp":1759277900000}`)

	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	models := fmt.Sprintf(`{"providers":{"mock":{"baseUrl":%q,"api":"openai-completions","apiKey":"x","models":[{"id":"qwen","name":"qwen","reasoning":true,"input":["text"],"contextWindow":%d,"maxTokens":16000,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}}}`, server.URL+"/v1", window)
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(current, []byte(session.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	p := startRPCProcessAt(t, dir, []string{"PIG_CODING_AGENT_DIR=" + agentDir, "PI_CODING_AGENT_DIR=" + agentDir, "PIG_HOME=" + dir}, "--offline", "--no-extensions", "--session", current, "--model", "mock/qwen")
	if _, err := io.WriteString(p.stdin, `{"id":"c","type":"compact"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	var response rpcRecord
	p.await("the compact response", func(record rpcRecord) bool {
		if record["type"] == "response" {
			response = record
			return true
		}
		return false
	})
	mu.Lock()
	defer mu.Unlock()
	if response["success"] != true {
		t.Fatalf("compact answered %v; the summary request sent %v tokens against a %d-token window", response, sizes, window)
	}
	if len(sizes) != 2 || carriedThinking {
		t.Fatalf("compact sent %d summary requests of %v tokens (thinking carried: %v), want the history and turn-prefix requests without thinking", len(sizes), sizes, carriedThinking)
	}
}
