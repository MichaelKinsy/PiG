package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const mcpServerEnv = "HEADLESS_EXAMPLE_MCP_SERVER"

// TestMain makes the test binary the stdio MCP server the example connects to: one tool, `echo`.
func TestMain(m *testing.M) {
	if os.Getenv(mcpServerEnv) != "" {
		serveEcho()
		return
	}
	os.Exit(m.Run())
}

func serveEcho() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	for in.Scan() {
		var request struct {
			ID     *json.RawMessage `json:"id"`
			Method string           `json:"method"`
			Params struct {
				ProtocolVersion string         `json:"protocolVersion"`
				Arguments       map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if json.Unmarshal(in.Bytes(), &request) != nil || request.ID == nil {
			continue
		}
		result := map[string]any{}
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": request.Params.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "echo", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "echo", "description": "Echo the text", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}}}}}
		case "tools/call":
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": fmt.Sprint("echo: ", request.Params.Arguments["text"])}}}
		}
		line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		fmt.Printf("%s\n", line)
	}
}

// fakeAnthropic answers /v1/messages with an SSE stream: a call to the first MCP tool it is offered, then, once the tool
// result comes back, a text that quotes it. It records the tool names of every request.
func fakeAnthropic(t *testing.T, offered *[][]string) *httptest.Server {
	t.Helper()
	event := func(w http.ResponseWriter, name string, data any) {
		payload, _ := json.Marshal(data)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, payload)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools    []struct{ Name string } `json:"tools"`
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var names []string
		for _, tool := range body.Tools {
			names = append(names, tool.Name)
		}
		*offered = append(*offered, names)
		last := string(body.Messages[len(body.Messages)-1].Content)
		w.Header().Set("Content-Type", "text/event-stream")
		event(w, "message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "m1", "type": "message", "role": "assistant", "model": "x", "content": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1}}})
		stop := "end_turn"
		switch mcpTool := slices.IndexFunc(names, func(n string) bool { return strings.HasPrefix(n, "mcp__echo__") }); {
		case strings.Contains(last, "tool_result"):
			event(w, "content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
			event(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": fmt.Sprint("the tool said ", strings.Contains(last, "echo: hi"))}})
		case mcpTool >= 0:
			stop = "tool_use"
			event(w, "content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": "toolu_1", "name": names[mcpTool], "input": map[string]any{}}})
			event(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": `{"text":"hi"}`}})
		default:
			event(w, "content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
			event(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "no MCP tool offered"}})
		}
		event(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
		event(w, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop}, "usage": map[string]any{"output_tokens": 2}})
		event(w, "message_stop", map[string]any{"type": "message_stop"})
	}))
}

// The example binds its MCP extension, the server's tool reaches the provider request, the model calls it, and the
// result comes back, with Anthropic as the one provider and no terminal.
func TestRunCallsAnMCPToolThroughTheProvider(t *testing.T) {
	var offered [][]string
	server := fakeAnthropic(t, &offered)
	defer server.Close()

	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mcpConfig, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"echo": map[string]any{"command": executable, "env": map[string]string{mcpServerEnv: "1"}, "exposure": "direct"}}})
	for name, content := range map[string]string{
		"mcp.json":    string(mcpConfig),
		"models.json": fmt.Sprintf(`{"providers":{"anthropic":{"baseUrl":%q}}}`, server.URL),
	} {
		if err := os.WriteFile(filepath.Join(agentDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(t.TempDir())
	// A lane or the agent-dir guard exports PIG_CODING_AGENT_DIR, which DefaultAgentDir reads before PIG_HOME: every
	// variable that names the agent directory must point at this test's.
	for _, name := range []string{"PIG_HOME", "PI_HOME"} {
		t.Setenv(name, home)
	}
	for _, name := range []string{"PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR"} {
		t.Setenv(name, agentDir)
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	var out bytes.Buffer
	if err := run(ctx, "call the echo tool", &out); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "the tool said true" {
		t.Fatalf("output = %q, want the model's text after the tool result", got)
	}
	if len(offered) == 0 || !slices.Contains(offered[0], "mcp__echo__echo") {
		t.Fatalf("tools offered to the provider = %v, want the MCP server's mcp__echo__echo among them", offered)
	}
}

func TestRunRequiresAPrompt(t *testing.T) {
	if err := run(t.Context(), "", &bytes.Buffer{}); err == nil {
		t.Fatal("run with no prompt returned nil")
	}
}

// A program that embeds the extension host without the upgrade command links neither the SDK upgrade rules nor go/types behind them: classifying a failed extension build as SDK drift is the command's (runtimecell.SetDriftClassifier), and the type checker is 0.3 MB of the program.
func TestHeadlessEmbedDoesNotLinkTheTypeChecker(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for dependency := range strings.FieldsSeq(string(out)) {
		if dependency == "go/types" || strings.HasSuffix(dependency, "/extensions/sdk/upgrade") {
			t.Errorf("the headless embed depends on %s", dependency)
		}
	}
}

// The word segmenter's dictionaries (6.3 MB) belong to the terminal editor, which a program that draws no terminal never
// links (subprocess.EnableEditorViews registers it). A stock build of the example holds none of their bytes: the probe is
// the middle 256 bytes of each embedded dictionary, searched in the linked binary.
func TestHeadlessEmbedBinaryHoldsNoWordDictionary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the example")
	}
	binary := filepath.Join(t.TempDir(), "headless")
	build := exec.CommandContext(t.Context(), "go", "build", "-trimpath", "-buildvcs=false", "-ldflags", "-s -w", "-o", binary, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, data)
	}
	linked, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	dictionaries, err := filepath.Glob(filepath.Join("..", "..", "internal", "wordsegmenter", "*_dictionary.bin"))
	if err != nil || len(dictionaries) == 0 {
		t.Fatalf("word dictionaries = %v, %v", dictionaries, err)
	}
	for _, path := range dictionaries {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		const probe = 256
		if len(data) < 2*probe {
			t.Fatalf("%s is %d bytes, too short to probe", path, len(data))
		}
		middle := len(data) / 2
		if bytes.Contains(linked, data[middle:middle+probe]) {
			t.Errorf("the headless embed links %s: the terminal editor reached a program that draws no terminal", filepath.Base(path))
		}
	}
}
