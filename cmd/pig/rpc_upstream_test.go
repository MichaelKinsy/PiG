package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// The upstream suite uses live Anthropic. This fixture preserves its model,
// prompts, RPC process and persistence path, replacing only the remote server.
// Small replies use keepRecentTokens=1 so manual compaction has a compactable prefix.
type upstreamRPC struct {
	p           *rpcProcess
	seen        []rpcRecord
	next        int
	agentDir    string
	mu          sync.Mutex
	requests    []json.RawMessage
	release     chan struct{}
	releaseOnce sync.Once
	started     chan struct{}
	startedOnce sync.Once
}

func newUpstreamRPC(t *testing.T, blocked bool) *upstreamRPC {
	t.Helper()
	f := &upstreamRPC{release: make(chan struct{}), started: make(chan struct{})}
	if !blocked {
		f.unblock()
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		f.mu.Lock()
		f.requests = append(f.requests, append(json.RawMessage(nil), body...))
		f.mu.Unlock()
		f.startedOnce.Do(func() { close(f.started) })
		select {
		case <-f.release:
		case <-r.Context().Done():
			return
		}
		var request struct {
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Error(err)
			return
		}
		text := "hello"
		if bytes := string(body); strings.Contains(bytes, "test123") {
			text = "test123"
		}
		if strings.Contains(string(body), "Create a structured context checkpoint summary") {
			text = "## Goal\nContinue the conversation.\n## Progress\nSaid hello."
		}
		if strings.Contains(string(body), "What was the exact output of the echo command") {
			// Read the value from the actual provider payload, not a fixture variable.
			// A dropped bash context must produce the wrong answer and fail the test.
			text = "missing bash context"
			for _, message := range request.Messages {
				var blocks []map[string]any
				_ = json.Unmarshal(message.Content, &blocks)
				for _, block := range blocks {
					s, _ := block["text"].(string)
					for line := range strings.SplitSeq(s, "\n") {
						if strings.HasPrefix(line, "unique-") {
							text = line
						}
					}
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		encoded, _ := json.Marshal(text)
		_, _ = fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_fixture\",\"usage\":{\"input_tokens\":25,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%s}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":12}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", encoded)
	}))
	t.Cleanup(server.Close)
	home := t.TempDir()
	f.agentDir = filepath.Join(home, "agent")
	if err := os.MkdirAll(f.agentDir, 0700); err != nil {
		t.Fatal(err)
	}
	models := map[string]any{"providers": map[string]any{"anthropic": map[string]any{"baseUrl": server.URL, "api": "anthropic-messages", "apiKey": "fixture-key", "models": []any{map[string]any{"id": "claude-sonnet-4-5", "name": "Claude Sonnet 4.5", "reasoning": true, "contextWindow": 200000, "maxTokens": 8192}}}}}
	for name, value := range map[string]any{"models.json": models, "settings.json": map[string]any{"compaction": map[string]any{"enabled": false, "keepRecentTokens": 1}, "retry": map[string]any{"enabled": false}}} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.agentDir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	f.p = startRPCProcessAt(t, t.TempDir(), []string{"HOME=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + f.agentDir, "PIG_TEST_FAUX=", "ANTHROPIC_API_KEY=", "ANTHROPIC_OAUTH_TOKEN="}, "--offline", "--no-extensions", "--no-skills", "--no-context-files", "--system-prompt", "Test", "--provider", "anthropic", "--model", "claude-sonnet-4-5")
	t.Cleanup(f.unblock)
	return f
}

func (f *upstreamRPC) waitForProviderRequest() {
	f.p.t.Helper()
	select {
	case <-f.started:
	case <-time.After(f.p.budget):
		f.p.t.Fatal("provider request did not start")
	}
}

func (f *upstreamRPC) unblock() { f.releaseOnce.Do(func() { close(f.release) }) }
func (f *upstreamRPC) await(what string, done func(rpcRecord) bool) {
	f.p.t.Helper()
	f.p.await(what, func(r rpcRecord) bool { f.seen = append(f.seen, r); return done(r) })
}
func (f *upstreamRPC) response(id string) rpcRecord {
	f.p.t.Helper()
	var response rpcRecord
	f.await(id, func(r rpcRecord) bool {
		if r["type"] == "response" && r["id"] == id {
			response = r
			return true
		}
		return false
	})
	return response
}
func (f *upstreamRPC) command(typ string, fields rpcRecord) rpcRecord {
	f.p.t.Helper()
	f.next++
	id := fmt.Sprintf("request-%d", f.next)
	if fields == nil {
		fields = rpcRecord{}
	}
	fields["type"] = typ
	fields["id"] = id
	f.p.sendJSON(fields)
	r := f.response(id)
	if r["success"] != true {
		f.p.t.Fatalf("%s: %#v", typ, r)
	}
	data, _ := r["data"].(map[string]any)
	return data
}
func (f *upstreamRPC) prompt(text string) []rpcRecord {
	f.p.t.Helper()
	start := len(f.seen)
	f.command("prompt", rpcRecord{"message": text})
	f.await("settled", func(r rpcRecord) bool { return r["type"] == "agent_settled" })
	return f.seen[start:]
}
func (f *upstreamRPC) finish() {
	f.p.t.Helper()
	f.p.closeInput()
	timer := time.NewTimer(f.p.budget)
	defer timer.Stop()
	for {
		select {
		case r, ok := <-f.p.records:
			if !ok {
				if f.p.outputErr != nil {
					f.p.t.Fatal(f.p.outputErr)
				}
				f.p.waitForExit("after draining all responses")
				return
			}
			f.seen = append(f.seen, r)
		case <-timer.C:
			f.p.t.Fatal("RPC output did not close")
		}
	}
}
func (f *upstreamRPC) diskEntries() []rpcRecord {
	f.p.t.Helper()
	var files []string
	err := filepath.WalkDir(filepath.Join(f.agentDir, "sessions"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		f.p.t.Fatal(err)
	}
	if len(files) != 1 {
		f.p.t.Fatalf("session files=%v", files)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		f.p.t.Fatal(err)
	}
	var entries []rpcRecord
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var entry rpcRecord
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			f.p.t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	return entries
}
func rpcEntryIDs(entries []any) []any {
	ids := make([]any, len(entries))
	for i, e := range entries {
		ids[i] = e.(map[string]any)["id"]
	}
	return ids
}
func assertRPCDefined(t *testing.T, v any) {
	t.Helper()
	if v == nil {
		t.Fatal("expected defined value")
	}
}

func TestRPCModeUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/rpc.test.ts:36
	t.Run("should get state", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		s := f.command("get_state", nil)
		m := s["model"].(map[string]any)
		if m["provider"] != "anthropic" || m["id"] != "claude-sonnet-4-5" || s["isStreaming"] != false || s["messageCount"] != float64(0) {
			t.Fatalf("state=%v", s)
		}
		f.finish()
	})
	// .upstream/v0.87.1/packages/coding-agent/test/rpc.test.ts:47
	t.Run("should save messages to session file", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		events := f.prompt("Reply with just the word 'hello'")
		n := 0
		for _, e := range events {
			if e["type"] == "message_end" {
				n++
			}
		}
		if n < 2 {
			t.Fatalf("message_end count=%d", n)
		}
		entries := f.diskEntries()
		if entries[0]["type"] != "session" {
			t.Fatal(entries[0])
		}
		var roles []any
		for _, e := range entries {
			if e["type"] == "message" {
				roles = append(roles, e["message"].(map[string]any)["role"])
			}
		}
		if len(roles) < 2 || !slices.Contains(roles, any("user")) || !slices.Contains(roles, any("assistant")) {
			t.Fatal(roles)
		}
		f.finish()
	})
	// .upstream/v0.87.1/packages/coding-agent/test/rpc.test.ts:89
	t.Run("should handle manual compaction", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		f.prompt("Say hello")
		r := f.command("compact", nil)
		assertRPCDefined(t, r["summary"])
		if r["tokensBefore"].(float64) <= 0 {
			t.Fatal(r)
		}
		n := 0
		for _, e := range f.diskEntries() {
			if e["type"] == "compaction" {
				n++
				assertRPCDefined(t, e["summary"])
			}
		}
		if n != 1 {
			t.Fatalf("compactions=%d", n)
		}
		f.finish()
	})
	// .upstream/v0.87.1/packages/coding-agent/test/rpc.test.ts:119
	t.Run("should execute bash command", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		r := f.command("bash", rpcRecord{"command": "echo hello"})
		if strings.TrimSpace(r["output"].(string)) != "hello" || r["exitCode"] != float64(0) || r["cancelled"] != false {
			t.Fatal(r)
		}
		f.finish()
	})
	// .upstream/v0.87.1/packages/coding-agent/test/rpc.test.ts:128
	t.Run("should add bash output to context", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		f.prompt("Say hi")
		value := fmt.Sprintf("test-%d", time.Now().UnixMilli())
		f.command("bash", rpcRecord{"command": "echo " + value})
		n := 0
		for _, e := range f.diskEntries() {
			m, _ := e["message"].(map[string]any)
			if e["type"] == "message" && m["role"] == "bashExecution" {
				n++
				if !strings.Contains(m["output"].(string), value) {
					t.Fatal(m)
				}
			}
		}
		if n != 1 {
			t.Fatalf("bash messages=%d", n)
		}
		f.finish()
	})
	// .upstream/v0.87.1/packages/coding-agent/test/rpc.test.ts:160
	t.Run("should include bash output in LLM context", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		value := fmt.Sprintf("unique-%d", time.Now().UnixMilli())
		f.command("bash", rpcRecord{"command": "echo " + value})
		events := f.prompt("What was the exact output of the echo command I just ran? Reply with just the value, nothing else.")
		found := false
		for _, e := range events {
			m, _ := e["message"].(map[string]any)
			if e["type"] == "message_end" && m["role"] == "assistant" {
				for _, c := range m["content"].([]any) {
					b := c.(map[string]any)
					if b["type"] == "text" && strings.Contains(b["text"].(string), value) {
						found = true
					}
				}
			}
		}
		if !found {
			t.Fatalf("bash value not in assistant response: %v", events)
		}
		f.finish()
	})
	// .upstream/v0.87.1/packages/coding-agent/test/rpc.test.ts:184
	t.Run("should set and get thinking level", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		f.command("set_thinking_level", rpcRecord{"level": "high"})
		if s := f.command("get_state", nil); s["thinkingLevel"] != "high" {
			t.Fatal(s)
		}
		f.finish()
	})
	// .upstream/v0.87.1/packages/coding-agent/test/rpc.test.ts:195
	t.Run("should cycle thinking level", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		before := f.command("get_state", nil)["thinkingLevel"]
		r := f.command("cycle_thinking_level", nil)
		assertRPCDefined(t, r)
		if r["level"] == before {
			t.Fatal(r)
		}
		if s := f.command("get_state", nil); s["thinkingLevel"] != r["level"] {
			t.Fatal(s)
		}
		f.finish()
	})
	// .upstream/v0.87.1/packages/coding-agent/test/rpc.test.ts:212
	t.Run("should get available thinking levels", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		levels := f.command("get_available_thinking_levels", nil)["levels"].([]any)
		s := f.command("get_state", nil)
		if len(levels) == 0 || !slices.Contains(levels, s["thinkingLevel"]) {
			t.Fatalf("levels=%v state=%v", levels, s)
		}
		if r := f.command("cycle_thinking_level", nil); r != nil {
			if !slices.Contains(levels, r["level"]) || (len(levels) > 1 && r["level"] == s["thinkingLevel"]) {
				t.Fatal(r)
			}
		}
		f.finish()
	})
	// .upstream/v0.87.1/packages/coding-agent/test/rpc.test.ts:234
	t.Run("should get available models", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		models := f.command("get_available_models", nil)["models"].([]any)
		if len(models) == 0 {
			t.Fatal("empty models")
		}
		for _, v := range models {
			m := v.(map[string]any)
			assertRPCDefined(t, m["provider"])
			assertRPCDefined(t, m["id"])
			if m["contextWindow"].(float64) <= 0 {
				t.Fatal(m)
			}
			if _, ok := m["reasoning"].(bool); !ok {
				t.Fatal(m)
			}
		}
		f.finish()
	})
	// .upstream/v0.87.1/packages/coding-agent/test/rpc.test.ts:249
	t.Run("should get session stats", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		f.prompt("Hello")
		s := f.command("get_session_stats", nil)
		assertRPCDefined(t, s["sessionFile"])
		assertRPCDefined(t, s["sessionId"])
		if s["userMessages"].(float64) < 1 || s["assistantMessages"].(float64) < 1 {
			t.Fatal(s)
		}
		f.finish()
	})
	// .upstream/v0.87.1/packages/coding-agent/test/rpc.test.ts:262
	t.Run("should create new session", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		f.prompt("Hello")
		if s := f.command("get_state", nil); s["messageCount"].(float64) <= 0 {
			t.Fatal(s)
		}
		f.command("new_session", nil)
		if s := f.command("get_state", nil); s["messageCount"] != float64(0) {
			t.Fatal(s)
		}
		f.finish()
	})
	// .upstream/v0.87.1/packages/coding-agent/test/rpc.test.ts:280
	t.Run("should export to HTML", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		f.prompt("Hello")
		path := f.command("export_html", nil)["path"].(string)
		if !strings.HasSuffix(path, ".html") {
			t.Fatal(path)
		}
		// Upstream test and child share cwd; this test isolates the child, so resolve its returned relative path there.
		if _, err := os.Stat(filepath.Join(f.p.cmd.Dir, path)); err != nil {
			t.Fatal(err)
		}
		f.finish()
	})
	// .upstream/v0.87.1/packages/coding-agent/test/rpc.test.ts:293
	t.Run("should get last assistant text", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		if r := f.command("get_last_assistant_text", nil); r["text"] != nil {
			t.Fatal(r)
		}
		f.prompt("Reply with just: test123")
		if r := f.command("get_last_assistant_text", nil); !strings.Contains(r["text"].(string), "test123") {
			t.Fatal(r)
		}
		f.finish()
	})
	// .upstream/v0.87.1/packages/coding-agent/test/rpc.test.ts:308
	t.Run("should get session entries with since cursor", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		f.prompt("Reply with just 'ok'")
		r := f.command("get_entries", nil)
		entries := r["entries"].([]any)
		if len(entries) < 2 {
			t.Fatal(entries)
		}
		ids := rpcEntryIDs(entries)
		for _, id := range ids {
			assertRPCDefined(t, id)
		}
		if r["leafId"] != ids[len(ids)-1] {
			t.Fatal(r)
		}
		since := f.command("get_entries", rpcRecord{"since": ids[0]})
		if !reflect.DeepEqual(rpcEntryIDs(since["entries"].([]any)), ids[1:]) || since["leafId"] != r["leafId"] {
			t.Fatal(since)
		}
		f.p.send(`{"id":"bad","type":"get_entries","since":"nonexistent-id"}`)
		bad := f.response("bad")
		if bad["success"] != false || !strings.Contains(bad["error"].(string), "Entry not found") {
			t.Fatal(bad)
		}
		f.finish()
	})
	// .upstream/v0.87.1/packages/coding-agent/test/rpc.test.ts:329
	t.Run("should get session tree", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		f.prompt("Reply with just 'ok'")
		r := f.command("get_entries", nil)
		tree := f.command("get_tree", nil)
		if tree["leafId"] != r["leafId"] {
			t.Fatal(tree)
		}
		nodes := tree["tree"].([]any)
		if len(nodes) != 1 {
			t.Fatal(tree)
		}
		var ids []any
		for len(nodes) == 1 {
			node := nodes[0].(map[string]any)
			ids = append(ids, node["entry"].(map[string]any)["id"])
			nodes = node["children"].([]any)
		}
		if len(nodes) != 0 || !reflect.DeepEqual(ids, rpcEntryIDs(r["entries"].([]any))) {
			t.Fatalf("chain=%v entries=%v", ids, r)
		}
		f.finish()
	})
	// .upstream/v0.87.1/packages/coding-agent/test/rpc.test.ts:350
	t.Run("should retain pre-compaction entries in get_entries", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		f.prompt("Reply with just 'ok'")
		before := rpcEntryIDs(f.command("get_entries", nil)["entries"].([]any))
		f.command("compact", nil)
		after := f.command("get_entries", nil)["entries"].([]any)
		if len(after) < len(before) || !reflect.DeepEqual(rpcEntryIDs(after[:len(before)]), before) {
			t.Fatal(after)
		}
		found := false
		for _, e := range after {
			if e.(map[string]any)["type"] == "compaction" {
				found = true
			}
		}
		if !found {
			t.Fatal("no compaction entry")
		}
		f.finish()
	})
	// .upstream/v0.87.1/packages/coding-agent/test/rpc.test.ts:364
	t.Run("should set and get session name", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		if s := f.command("get_state", nil); s["sessionName"] != nil {
			t.Fatal(s)
		}
		f.prompt("Reply with just 'ok'")
		f.command("set_session_name", rpcRecord{"name": "my-test-session"})
		if s := f.command("get_state", nil); s["sessionName"] != "my-test-session" {
			t.Fatal(s)
		}
		n := 0
		for _, e := range f.diskEntries() {
			if e["type"] == "session_info" {
				n++
				if e["name"] != "my-test-session" {
					t.Fatal(e)
				}
			}
		}
		if n != 1 {
			t.Fatalf("session_info entries=%d", n)
		}
		f.finish()
	})
}
