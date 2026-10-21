package cli

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A compaction request goes through the Agent's stream function, which adds before_provider_headers (sdk.ts buildRequestOptions); before_provider_request, after_provider_response and provider_stream_event are Agent options that summaries do not carry (compaction.ts createSummarizationOptions), so an agent request emits all four and a compaction request only the first. Run through the real binary in RPC mode.
func TestRPCCompactionEmitsOnlyProviderHeadersEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"## Summary\nok"},"finish_reason":null}]}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`+"\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	var session strings.Builder
	session.WriteString(`{"type":"session","version":3,"id":"hooks","timestamp":"2026-10-01T00:00:00.000Z","cwd":"."}` + "\n")
	parent, n := "null", 0
	add := func(message string) {
		n++
		id := fmt.Sprintf("e%03d", n)
		fmt.Fprintf(&session, `{"type":"message","id":%q,"parentId":%s,"timestamp":"2026-10-01T00:00:00.000Z","message":%s}`+"\n", id, parent, message)
		parent = `"` + id + `"`
	}
	usage := `{"input":100,"output":10,"cacheRead":0,"cacheWrite":0,"totalTokens":110,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`
	for k := range 14 {
		add(fmt.Sprintf(`{"role":"user","content":[{"type":"text","text":%q}],"timestamp":%d}`, strings.Repeat(fmt.Sprintf("question %d ", k), 400), 1759276900000+k*10))
		add(fmt.Sprintf(`{"role":"assistant","content":[{"type":"text","text":%q}],"api":"openai-completions","provider":"mock","model":"qwen","usage":%s,"stopReason":"stop","timestamp":%d}`, strings.Repeat(fmt.Sprintf("answer %d ", k), 400), usage, 1759276900005+k*10))
	}

	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	models := fmt.Sprintf(`{"providers":{"mock":{"baseUrl":%q,"api":"openai-completions","apiKey":"x","models":[{"id":"qwen","name":"qwen","reasoning":false,"input":["text"],"contextWindow":128000,"maxTokens":4096,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}}}`, server.URL+"/v1")
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"compaction":{"keepRecentTokens":2000}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(current, []byte(session.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	extension, err := filepath.Abs(filepath.Join("testdata", "provider-hooks-log.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(dir, "report.log")
	p := startRPCProcessAt(t, dir, []string{"PIG_CODING_AGENT_DIR=" + agentDir, "PI_CODING_AGENT_DIR=" + agentDir, "PIG_HOME=" + dir, "WIRING_REPORT=" + report}, "--offline", "--no-extensions", "-e", extension, "--session", current, "--model", "mock/qwen")
	await := func(id string) {
		t.Helper()
		var response rpcRecord
		p.await("the "+id+" response", func(record rpcRecord) bool {
			if record["type"] == "response" && record["id"] == id {
				response = record
				return true
			}
			return false
		})
		if response["success"] != true {
			t.Fatalf("%s answered %v", id, response)
		}
	}
	events := func() []string {
		data, _ := os.ReadFile(report)
		return strings.Fields(string(data))
	}
	p.send(`{"id":"compact","type":"compact"}`)
	await("compact")
	compacted := events()
	if len(compacted) == 0 {
		t.Fatal("compaction emitted no before_provider_headers event")
	}
	for _, event := range compacted {
		if event != "before_provider_headers" {
			t.Fatalf("compaction emitted %v, want only before_provider_headers", compacted)
		}
	}
	p.send(`{"id":"prompt","type":"prompt","message":"hello"}`)
	await("prompt")
	p.await("the agent request to settle", func(record rpcRecord) bool { return record["type"] == "agent_end" })
	agentEvents := events()[len(compacted):]
	if want := []string{"before_provider_headers", "before_provider_request", "after_provider_response", "provider_stream_event"}; !slices.Equal(slices.Compact(agentEvents), want) {
		t.Fatalf("agent request emitted %v, want %v in order", agentEvents, want)
	}
}
