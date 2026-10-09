// rpc_mode_test.go: unit tests for RPC command parsing and event serialisation.
//
// These tests exercise the types and helpers without a live session.
// Per AGENTS.md: no real API calls; no real network.

package cli

// pi: packages/coding-agent/src/modes/rpc/rpc-mode.ts

// pi: packages/coding-agent/src/modes/rpc/rpc-types.ts

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/sessionentry"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	codingcompaction "github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

func TestRPCTaskGroupClosesAgainstConcurrentStarts(t *testing.T) {
	var group rpcTaskGroup
	start := make(chan struct{})
	var callers sync.WaitGroup
	var ran atomic.Int64
	for range 100 {
		callers.Go(func() {
			<-start
			group.Go(func() { ran.Add(1) })
		})
	}
	close(start)
	group.CloseAndWait()
	callers.Wait()
	group.CloseAndWait()
	if group.Go(func() { ran.Add(1) }) {
		t.Fatal("task group accepted work after close")
	}
	if got := ran.Load(); got > 100 {
		t.Fatalf("ran %d tasks, want at most 100", got)
	}
}

func TestRPCRunEmitsSessionStartOnce(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "rpc_mode_runtime.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "EmitSessionStartTransition" {
			count++
		}
		return true
	})
	if count != 1 {
		t.Fatalf("rpc_mode_runtime.go contains %d EmitSessionStartTransition calls, want the one rebind announcement", count)
	}
	rpc, err := parser.ParseFile(token.NewFileSet(), "rpc_mode.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(rpc, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && strings.HasPrefix(selector.Sel.Name, "EmitSessionStart") {
				t.Errorf("runRPCMode calls %s directly; session_start belongs to the rebind", selector.Sel.Name)
			}
		}
		return true
	})
}

type rpcEventRunnerStub struct {
	result any
	event  any
}

func (r *rpcEventRunnerStub) Emit(_ context.Context, event any) (any, error) {
	r.event = event
	return r.result, nil
}

func TestRPCSessionReplacementCancellationEvents(t *testing.T) {
	switchRunner := &rpcEventRunnerStub{result: extension.SessionBeforeSwitchResult{Cancel: true}}
	cancelled, err := rpcBeforeSessionSwitch(context.Background(), switchRunner, "resume", "/session.jsonl")
	if err != nil || !cancelled {
		t.Fatalf("switch cancellation = %v, %v", cancelled, err)
	}
	switchEvent, ok := switchRunner.event.(extension.SessionBeforeSwitchEvent)
	if !ok || switchEvent.Reason != "resume" || switchEvent.TargetSessionFile != "/session.jsonl" {
		t.Fatalf("switch event = %#v", switchRunner.event)
	}

	forkRunner := &rpcEventRunnerStub{result: extension.SessionBeforeForkResult{Cancel: true}}
	cancelled, err = rpcBeforeSessionFork(context.Background(), forkRunner, "entry-1", "before")
	if err != nil || !cancelled {
		t.Fatalf("fork cancellation = %v, %v", cancelled, err)
	}
	forkEvent, ok := forkRunner.event.(extension.SessionBeforeForkEvent)
	if !ok || forkEvent.EntryID != "entry-1" || forkEvent.Position != "before" {
		t.Fatalf("fork event = %#v", forkRunner.event)
	}
}

func TestRPCMessageUpdateWireContainsOnlyDeltaEvent(t *testing.T) {
	partial := jsonTestPartial(ai.TextContent{Text: "prior next"})
	out, err := rpcAgentEvent(jsonTestUpdate(ai.TextDeltaEvent{ContentIndex: 0, Delta: " next", Partial: partial}, partial))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var events []map[string]any
	if err := json.Unmarshal(data, &events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("want one text_delta, got %s", data)
	}
	for _, event := range events {
		if _, exists := event["message"]; exists {
			t.Fatalf("message_update leaked cumulative message: %s", data)
		}
		assistantEvent, ok := event["assistantMessageEvent"].(map[string]any)
		if !ok {
			t.Fatalf("assistantMessageEvent missing: %s", data)
		}
		if _, exists := assistantEvent["partial"]; exists {
			t.Fatalf("message_update leaked cumulative partial: %s", data)
		}
	}
	delta := events[0]["assistantMessageEvent"].(map[string]any)
	if delta["type"] != "text_delta" || delta["delta"] != " next" {
		t.Fatalf("unexpected delta event: %s", data)
	}
}

// ─── TestRPCCommandParse ──────────────────────────────────────────────────────

// TestRPCCommandParse verifies that all three supported command types
// (prompt, abort, get_state) parse cleanly from JSON lines.
func TestRPCCommandParse(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantType string
		wantID   rpcRequestID
	}{
		{
			name:     "prompt with id",
			input:    `{"id":"abc","type":"prompt","message":"hello"}`,
			wantType: "prompt",
			wantID:   rpcStringID("abc"),
		},
		{
			name:     "prompt without id",
			input:    `{"type":"prompt","message":"say hi"}`,
			wantType: "prompt",
		},
		{
			name:     "abort",
			input:    `{"id":"x1","type":"abort"}`,
			wantType: "abort",
			wantID:   rpcStringID("x1"),
		},
		{
			name:     "get_state",
			input:    `{"type":"get_state"}`,
			wantType: "get_state",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, err := parseRPCCommand([]byte(tc.input))
			if err != nil {
				t.Fatalf("parseRPCCommand(%q): unexpected error: %v", tc.input, err)
			}
			if string(env.Type) != tc.wantType {
				t.Errorf("Type: got %q, want %q", env.Type, tc.wantType)
			}
			if !reflect.DeepEqual(env.ID, tc.wantID) {
				t.Errorf("ID: got %s, want %s", env.ID, tc.wantID)
			}
			// Raw bytes must be set and round-trip cleanly.
			if len(env.Raw) == 0 {
				t.Error("Raw bytes are empty")
			}
		})
	}
}

// TestRPCNewCommandsParse verifies additional command envelopes parse.
func TestRPCNewCommandsParse(t *testing.T) {
	cases := []struct {
		name, input, wantType string
	}{
		{"set_model", `{"type":"set_model","provider":"openai","modelId":"gpt-4"}`, "set_model"},
		{"compact", `{"type":"compact"}`, "compact"},
		{"compact with instructions", `{"type":"compact","customInstructions":"keep code"}`, "compact"},
		{"set_auto_compaction", `{"type":"set_auto_compaction","enabled":true}`, "set_auto_compaction"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, err := parseRPCCommand([]byte(tc.input))
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			if string(env.Type) != tc.wantType {
				t.Errorf("type = %q, want %q", env.Type, tc.wantType)
			}
		})
	}
}

// TestRPCSetModelFields verifies set_model field extraction.
func TestRPCSetModelFields(t *testing.T) {
	input := []byte(`{"type":"set_model","provider":"anthropic","modelId":"claude-3"}`)
	env, err := parseRPCCommand(input)
	if err != nil {
		t.Fatal(err)
	}
	var cmd RPCSetModelCommand
	if err := json.Unmarshal(env.Raw, &cmd); err != nil {
		t.Fatal(err)
	}
	if cmd.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", cmd.Provider)
	}
	if cmd.ModelID != "claude-3" {
		t.Errorf("modelId = %q, want claude-3", cmd.ModelID)
	}
}

// TestRPCCompactFields verifies compact field extraction.
func TestRPCCompactFields(t *testing.T) {
	input := []byte(`{"type":"compact","customInstructions":"preserve API docs"}`)
	env, err := parseRPCCommand(input)
	if err != nil {
		t.Fatal(err)
	}
	var cmd RPCCompactCommand
	if err := json.Unmarshal(env.Raw, &cmd); err != nil {
		t.Fatal(err)
	}
	if cmd.CustomInstructions != "preserve API docs" {
		t.Errorf("customInstructions = %q, want 'preserve API docs'", cmd.CustomInstructions)
	}
}
func TestRPCCommandParseError(t *testing.T) {
	_, err := parseRPCCommand([]byte(`not valid json`))
	if err == nil {
		t.Fatal("expected parse error for malformed JSON, got nil")
	}
}

// TestRPCPromptFieldExtraction verifies the message field can be extracted
// from a prompt command's raw bytes.
func TestRPCPromptFieldExtraction(t *testing.T) {
	raw := `{"type":"prompt","message":"hello world"}`
	env, err := parseRPCCommand([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	var cmd RPCPromptCommand
	if err := json.Unmarshal(env.Raw, &cmd); err != nil {
		t.Fatalf("unmarshal prompt: %v", err)
	}
	if cmd.Message != "hello world" {
		t.Errorf("Message: got %q, want %q", cmd.Message, "hello world")
	}
}

// ─── TestRPCEventSerialize ────────────────────────────────────────────────────

// TestRPCEventSerialize verifies that event structs serialise to the expected
// JSON shape without HTML escaping.
func TestRPCEventSerialize(t *testing.T) {
	cases := []struct {
		name    string
		event   any
		wantKey string // a JSON key that must appear in output
		wantVal string // the value that key must have (substring match)
	}{
		{
			name:    "text_delta",
			event:   RPCTextDeltaEvent{Type: "text_delta", Content: "hello && world"},
			wantKey: `"type":"text_delta"`,
			wantVal: `"content":"hello && world"`, // && must not be escaped to \u0026\u0026
		},
		{
			name:    "message_end",
			event:   RPCMessageEndEvent{Type: "message_end", Text: "done <test>"},
			wantKey: `"type":"message_end"`,
			wantVal: `"text":"done <test>"`, // < must not be escaped
		},
		{
			name: "error",
			event: RPCErrorEvent{
				Type:    "error",
				Message: "something went wrong & more",
			},
			wantKey: `"type":"error"`,
			wantVal: `"message":"something went wrong & more"`,
		},
		{
			name: "tool_use",
			event: RPCToolUseEvent{
				Type: "tool_use",
				Name: "bash",
				Args: map[string]string{"command": "echo hi && ls"},
			},
			wantKey: `"type":"tool_use"`,
			wantVal: `"name":"bash"`,
		},
		{
			name: "tool_result",
			event: RPCToolResultEvent{
				Type:    "tool_result",
				Name:    "bash",
				Content: "output > /dev/null",
				IsError: false,
			},
			wantKey: `"type":"tool_result"`,
			wantVal: `"content":"output > /dev/null"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			writeJSONLine(&buf, tc.event)
			got := buf.String()
			if got == "" {
				t.Fatal("writeJSONLine produced empty output")
			}
			// Must end with newline.
			if got[len(got)-1] != '\n' {
				t.Errorf("output missing trailing newline: %q", got)
			}
			// Must be valid JSON.
			var m map[string]any
			if err := json.Unmarshal([]byte(got[:len(got)-1]), &m); err != nil {
				t.Fatalf("output is not valid JSON: %v\n%s", err, got)
			}
			// Check expected key/value substrings in the raw output.
			if !bytes.Contains(buf.Bytes(), []byte(tc.wantKey)) {
				t.Errorf("want key %q in output:\n%s", tc.wantKey, got)
			}
			if !bytes.Contains(buf.Bytes(), []byte(tc.wantVal)) {
				t.Errorf("want value %q in output:\n%s", tc.wantVal, got)
			}
		})
	}
}

func TestRPCSessionMutationEventShapes(t *testing.T) {
	entryEvent := RPCEntryAppendedEvent{
		Type: "entry_appended",
		Entry: RPCEntryAppendedEntry{
			Type: "custom", CustomType: "rpc-entry", Data: map[string]any{"value": "hello"},
			ID: "entry-1", Timestamp: "2026-01-01T00:00:00.000Z",
		},
	}
	data, err := json.Marshal(entryEvent)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"entry_appended","entry":{"type":"custom","customType":"rpc-entry","data":{"value":"hello"},"id":"entry-1","parentId":null,"timestamp":"2026-01-01T00:00:00.000Z"}}`
	if string(data) != want {
		t.Fatalf("entry_appended = %s, want %s", data, want)
	}

	for _, test := range []struct {
		name string
		want string
	}{
		{name: "work", want: `{"type":"session_info_changed","name":"work"}`},
		{name: "", want: `{"type":"session_info_changed"}`},
	} {
		data, err := json.Marshal(rpcSessionInfoChanged(test.name))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != test.want {
			t.Errorf("session_info_changed(%q) = %s, want %s", test.name, data, test.want)
		}
	}
}

// ─── TestRPCStateCommand ──────────────────────────────────────────────────────

// TestRPCStateCommand verifies that RPCSessionState serialises correctly and
// that rpcSuccess / rpcError produce the expected shapes.
func TestRPCModelValueUsesUnknownPiShape(t *testing.T) {
	data, err := json.Marshal(rpcModelValue(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"unknown","name":"unknown","api":"unknown","provider":"unknown","baseUrl":"","reasoning":false,"input":[],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":0,"maxTokens":0}`
	if string(data) != want {
		t.Fatalf("unknown model = %s, want %s", data, want)
	}
}

// Pi returns the selected model's input array verbatim, including an explicit empty array.
func TestRPCModelValuePreservesInputPresenceAndOrder(t *testing.T) {
	for _, tc := range []struct {
		name   string
		input  []string
		images bool
		want   string
	}{
		{"inferred text", nil, false, `["text"]`},
		{"inferred images", nil, true, `["text","image"]`},
		{"explicit empty overrides capability", []string{}, true, `[]`},
		{"explicit image only", []string{"image"}, false, `["image"]`},
		{"explicit order", []string{"image", "text"}, true, `["image","text"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := &ai.Model{Input: tc.input, Capabilities: ai.ModelCapabilities{SupportsImages: tc.images}}
			wire, err := json.Marshal(rpcModelValue(model).Input)
			if err != nil || string(wire) != tc.want {
				t.Fatalf("input = %s, %v; want %s", wire, err, tc.want)
			}
		})
	}
}

func TestRPCModelValueUsesFullPiShape(t *testing.T) {
	model := &ai.Model{
		ID: "reasoner", DisplayName: "Reasoner",
		Provider: fakeProviderForRPC{"example"},
		Capabilities: ai.ModelCapabilities{
			MaxThinking: ai.ThinkingLevelHigh, SupportsImages: true, ContextWindow: 200000,
			MaxOutputTokens: 8192, InputCostPer1M: 1, OutputCostPer1M: 2,
		},
		ProviderMeta: ai.ProviderMetadata{ProviderID: "example", API: ai.APIOpenAIResponses, BaseURL: "https://example.test", Reasoning: true},
		PromptCache:  ai.ModelPromptCache{"short": 300, "long": 3600},
	}
	data, err := json.Marshal(rpcModelValue(model))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"reasoner","name":"Reasoner","api":"openai-responses","provider":"example","baseUrl":"https://example.test","reasoning":true,"input":["text","image"],"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0},"promptCache":{"long":3600,"short":300},"contextWindow":200000,"maxTokens":8192}`
	if string(data) != want {
		t.Fatalf("model = %s, want %s", data, want)
	}
}

type fakeProviderForRPC struct{ id string }

func (p fakeProviderForRPC) ID() string { return p.id }
func (fakeProviderForRPC) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	partial := &ai.AssistantMessage{Provider: "example", Model: "test", StopReason: ai.StopReasonPending}
	final := &ai.AssistantMessage{Provider: "example", Model: "test", StopReason: ai.StopReasonStop}
	stream := ai.NewAssistantMessageEventStream()
	if err := stream.Push(ai.StartEvent{Partial: partial}); err != nil {
		return nil, err
	}
	if err := stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: final}); err != nil {
		return nil, err
	}
	return stream, nil
}
func (fakeProviderForRPC) Close() error { return nil }

// Pi: packages/coding-agent/src/core/compaction/compaction.ts:109 (CompactionResult.estimatedTokensAfter).
// Pi: packages/coding-agent/src/core/compaction/compaction.ts:113 (CompactionResult.details).
func TestRPCCompactionResultShape(t *testing.T) {
	result := rpcCompactionResult(&coding.CompactionResult{
		Summary: "summary", FirstKeptEntryID: "keep", TokensBefore: 100,
		EstimatedTokensAfter: 20,
		Usage:                &ai.Usage{Input: 10, Output: 2, TotalTokens: 12, Cost: ai.UsageCost{Total: 0.5}},
		Details:              codingcompaction.CompactionDetails{ReadFiles: []string{}, ModifiedFiles: []string{}},
	})
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`"firstKeptEntryId":"keep"`, `"estimatedTokensAfter":20`, `"totalTokens":12`, `"total":0.5`, `"details":{"readFiles":[],"modifiedFiles":[]}`} {
		if !bytes.Contains(data, []byte(fragment)) {
			t.Errorf("compaction result does not contain %s: %s", fragment, data)
		}
	}

	withoutOptional, err := json.Marshal(rpcCompactionResult(&coding.CompactionResult{
		Summary: "summary", FirstKeptEntryID: "keep", TokensBefore: 100, EstimatedTokensAfter: 20,
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"usage"`, `"details"`} {
		if bytes.Contains(withoutOptional, []byte(field)) {
			t.Errorf("compaction result retained absent %s: %s", field, withoutOptional)
		}
	}
}

func TestRPCCompactionEndEventShape(t *testing.T) {
	events, err := rpcAgentEvent(agent.CompactionEndEvent{
		Reason: "manual", Summary: "summary", FirstKeptEntryID: "keep",
		TokensBefore: 100, EstimatedTokensAfter: 20,
		Usage:   &ai.Usage{Input: 10, Output: 2, TotalTokens: 12},
		Details: map[string]any{"readFiles": []string{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d", len(events))
	}
	data, err := json.Marshal(events[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`"type":"compaction_end"`, `"reason":"manual"`, `"aborted":false`, `"willRetry":false`, `"firstKeptEntryId":"keep"`, `"totalTokens":12`} {
		if !bytes.Contains(data, []byte(fragment)) {
			t.Errorf("compaction event does not contain %s: %s", fragment, data)
		}
	}

	withoutDetails, err := rpcAgentEvent(agent.CompactionEndEvent{
		Reason: "manual", Summary: "summary", FirstKeptEntryID: "keep",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(withoutDetails[0])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`"details"`)) {
		t.Fatalf("compaction event retained absent details: %s", data)
	}
}

// Pi: packages/coding-agent/src/core/crash-log.ts:13 (RPCSessionState.sessionFile).
func TestRPCStateCommand(t *testing.T) {
	state := RPCSessionState{
		SessionID:    "sess-123",
		SessionFile:  "/tmp/sess.json",
		Model:        &RPCModel{ID: "gpt-4o", Provider: "openai"},
		IsStreaming:  false,
		MessageCount: 3,
	}

	resp := rpcSuccess(rpcStringID("req-1"), "get_state", state)

	var buf bytes.Buffer
	writeJSONLine(&buf, resp)

	var decoded map[string]any
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &decoded); err != nil {
		t.Fatalf("decode response: %v\n%s", err, buf.String())
	}

	checks := map[string]any{
		"type":    "response",
		"command": "get_state",
		"success": true,
		"id":      "req-1",
	}
	for k, want := range checks {
		got, ok := decoded[k]
		if !ok {
			t.Errorf("missing key %q in response", k)
			continue
		}
		if got != want {
			t.Errorf("key %q: got %v, want %v", k, got, want)
		}
	}

	data, ok := decoded["data"].(map[string]any)
	if !ok {
		t.Fatalf("data field missing or wrong type: %T", decoded["data"])
	}
	if data["sessionId"] != "sess-123" {
		t.Errorf("sessionId: got %v", data["sessionId"])
	}
	model, ok := data["model"].(map[string]any)
	if !ok || model["provider"] != "openai" || model["id"] != "gpt-4o" {
		t.Errorf("model: got %#v", data["model"])
	}
	if data["messageCount"] != float64(3) {
		t.Errorf("messageCount: got %v", data["messageCount"])
	}
}

// TestRPCErrorResponse verifies the error response shape.
func TestRPCErrorResponse(t *testing.T) {
	resp := rpcError(rpcStringID("req-2"), "prompt", "message is required")

	var buf bytes.Buffer
	writeJSONLine(&buf, resp)

	var decoded map[string]any
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["success"] != false {
		t.Errorf("success should be false, got %v", decoded["success"])
	}
	if decoded["error"] != "message is required" {
		t.Errorf("error field: got %v", decoded["error"])
	}
}

// TestRPCErrorResponseEchoesID covers the 0.79.x change where an error
// response echoes the request id (error(id, ...)) and omits it when the
// command carried none. This asserts the unknown-command builder + omitempty
// serialization that makes {"id":"...","type":"response",...} versus
// {"type":"response",...}.
func TestRPCErrorResponseEchoesID(t *testing.T) {
	unknown := func(command string) rpcUnknownCommandResponse {
		t.Helper()
		env, err := parseRPCCommand([]byte(command))
		if err != nil {
			t.Fatal(err)
		}
		return rpcUnknownCommand(env)
	}
	t.Run("present id is echoed first", func(t *testing.T) {
		var buf bytes.Buffer
		writeJSONLine(&buf, unknown(`{"id":"test-id","type":"does_not_exist"}`))
		line := string(bytes.TrimRight(buf.Bytes(), "\n"))
		want := `{"id":"test-id","type":"response","command":"does_not_exist","success":false,"error":"Unknown command: does_not_exist"}`
		if line != want {
			t.Errorf("error line:\n got %s\nwant %s", line, want)
		}
	})
	t.Run("omitted id stays omitted", func(t *testing.T) {
		var buf bytes.Buffer
		writeJSONLine(&buf, unknown(`{"type":"hello"}`))
		line := string(bytes.TrimRight(buf.Bytes(), "\n"))
		if want := `{"type":"response","command":"hello","success":false,"error":"Unknown command: hello"}`; line != want {
			t.Errorf("error line:\n got %s\nwant %s", line, want)
		}
	})
}

// Pi: packages/coding-agent/src/core/agent-session.ts:1609 (RPCSessionState.isCompacting); packages/coding-agent/src/core/agent-session.ts:1643 (RPCSessionState.sessionName); packages/coding-agent/src/core/agent-session.ts:3240 (RPCSessionState.autoCompactionEnabled); packages/coding-agent/src/core/bug-report.ts:144 (RPCSessionState.messageCount); packages/coding-agent/src/core/crash-log.ts:13 (RPCSessionState.sessionFile); packages/coding-agent/src/core/extensions/types.ts:1504 (RPCSessionState.isStreaming); packages/coding-agent/src/core/extensions/types.ts:346 (RPCSessionState.thinkingLevel); packages/coding-agent/src/core/settings-manager.ts:140 (RPCSessionState.steeringMode); packages/coding-agent/src/core/settings-manager.ts:141 (RPCSessionState.followUpMode); packages/coding-agent/src/main.ts:244 (RPCSessionState.sessionId).
func TestRPCSessionStateJSONShape(t *testing.T) {
	state := RPCSessionState{
		SessionID:             "sess-123",
		SessionFile:           "/tmp/sess.json",
		SessionName:           "named",
		Model:                 &RPCModel{ID: "gpt-4o", Provider: "openai"},
		ThinkingLevel:         "medium",
		SteeringMode:          "all",
		FollowUpMode:          "one-at-a-time",
		IsStreaming:           true,
		IsCompacting:          false,
		AutoCompactionEnabled: true,
		MessageCount:          3,
	}

	var buf bytes.Buffer
	writeJSONLine(&buf, state)

	var decoded map[string]any
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	checks := map[string]any{
		"sessionId":             "sess-123",
		"sessionFile":           "/tmp/sess.json",
		"sessionName":           "named",
		"thinkingLevel":         "medium",
		"steeringMode":          "all",
		"followUpMode":          "one-at-a-time",
		"isStreaming":           true,
		"isCompacting":          false,
		"autoCompactionEnabled": true,
		"messageCount":          float64(3),
	}
	for field, want := range checks {
		if got := decoded[field]; got != want {
			t.Fatalf("%s = %v, want %v", field, got, want)
		}
	}
	if _, exists := decoded["cwd"]; exists {
		t.Fatal("get_state contains non-upstream cwd field")
	}
	model, ok := decoded["model"].(map[string]any)
	if !ok || model["provider"] != "openai" || model["id"] != "gpt-4o" {
		t.Fatalf("model = %#v", decoded["model"])
	}
}

// ─── TestRPCExpansion3_8 ──────────────────────────────────────────────────────

// TestRPCNewCommandsParseExpanded verifies representative command types parse cleanly.
func TestRPCNewCommandsParseExpanded(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantType string
	}{
		{"steer", `{"type":"steer","message":"do this instead"}`, "steer"},
		{"follow_up", `{"type":"follow_up","message":"also do this"}`, "follow_up"},
		{"bash", `{"type":"bash","command":"ls -la"}`, "bash"},
		{"abort_bash", `{"type":"abort_bash"}`, "abort_bash"},
		{"set_auto_retry", `{"type":"set_auto_retry","enabled":true}`, "set_auto_retry"},
		{"set_session_name", `{"type":"set_session_name","name":"my-session"}`, "set_session_name"},
		{"cycle_thinking_level", `{"type":"cycle_thinking_level"}`, "cycle_thinking_level"},
		{"set_steering_mode", `{"type":"set_steering_mode","mode":"all"}`, "set_steering_mode"},
		{"set_follow_up_mode", `{"type":"set_follow_up_mode","mode":"one-at-a-time"}`, "set_follow_up_mode"},
		{"get_session_stats", `{"type":"get_session_stats"}`, "get_session_stats"},
		{"get_messages", `{"type":"get_messages"}`, "get_messages"},
		{"get_last_assistant_text", `{"type":"get_last_assistant_text"}`, "get_last_assistant_text"},
		{"get_fork_messages", `{"type":"get_fork_messages"}`, "get_fork_messages"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, err := parseRPCCommand([]byte(tc.input))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if string(env.Type) != tc.wantType {
				t.Errorf("type: got %q, want %q", env.Type, tc.wantType)
			}
		})
	}
}

// TestRPCSteerFieldExtraction verifies message extraction from steer command.
func TestRPCSteerFieldExtraction(t *testing.T) {
	input := `{"id":"s1","type":"steer","message":"redirect please"}`
	env, err := parseRPCCommand([]byte(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var cmd RPCSteerCommand
	if err := json.Unmarshal(env.Raw, &cmd); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cmd.Message != "redirect please" {
		t.Errorf("message: got %q", cmd.Message)
	}
	if string(cmd.ID) != `"s1"` {
		t.Errorf("id: got %s", cmd.ID)
	}
}

// TestRPCBashFieldExtraction verifies command extraction from bash command.
func TestRPCBashFieldExtraction(t *testing.T) {
	input := `{"id":"b1","type":"bash","command":"echo hello && cat file.txt"}`
	env, err := parseRPCCommand([]byte(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var cmd RPCBashCommand
	if err := json.Unmarshal(env.Raw, &cmd); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Verify && is not HTML-escaped (AGENTS.md rule: no json.Marshal for display).
	if cmd.Command != "echo hello && cat file.txt" {
		t.Errorf("command: got %q (expected raw &&, not HTML-escaped)", cmd.Command)
	}
}

// TestRPCBashResultSerialize verifies BashResult serializes with exitCode + output.
func TestRPCBashResultSerialize(t *testing.T) {
	result := RPCBashResult{Output: "hello\nworld\n", ExitCode: new(0)}
	resp := rpcSuccess(rpcStringID("b2"), "bash", result)

	var buf bytes.Buffer
	writeJSONLine(&buf, resp)

	var decoded map[string]any
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["command"] != "bash" {
		t.Errorf("command: got %v", decoded["command"])
	}
	data, ok := decoded["data"].(map[string]any)
	if !ok {
		t.Fatalf("data not a map: %T", decoded["data"])
	}
	if data["exitCode"] != float64(0) {
		t.Errorf("exitCode: got %v", data["exitCode"])
	}
	if data["output"] != "hello\nworld\n" {
		t.Errorf("output: got %v", data["output"])
	}
}

// TestRPCSessionStateExpanded verifies expanded RPCSessionState includes new fields.
// Pi: packages/coding-agent/src/core/agent-session.ts:1609 (RPCSessionState.isCompacting); packages/coding-agent/src/core/agent-session.ts:1643 (RPCSessionState.sessionName); packages/coding-agent/src/core/agent-session.ts:3240 (RPCSessionState.autoCompactionEnabled); packages/coding-agent/src/core/bug-report.ts:144 (RPCSessionState.messageCount); packages/coding-agent/src/core/extensions/types.ts:1504 (RPCSessionState.isStreaming); packages/coding-agent/src/core/extensions/types.ts:346 (RPCSessionState.thinkingLevel); packages/coding-agent/src/main.ts:244 (RPCSessionState.sessionId).
func TestRPCSessionStateExpanded(t *testing.T) {
	state := RPCSessionState{
		SessionID:             "sess-x",
		SessionName:           "my-session",
		Model:                 &RPCModel{ID: "claude-3-5-sonnet", Provider: "anthropic"},
		ThinkingLevel:         "low",
		IsStreaming:           false,
		IsCompacting:          true,
		AutoCompactionEnabled: true,
		MessageCount:          7,
	}

	var buf bytes.Buffer
	writeJSONLine(&buf, state)

	var decoded map[string]any
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	checks := []struct {
		field string
		want  any
	}{
		{"sessionName", "my-session"},
		{"thinkingLevel", "low"},
		{"isCompacting", true},
		{"autoCompactionEnabled", true},
		{"messageCount", float64(7)},
	}
	for _, c := range checks {
		if decoded[c.field] != c.want {
			t.Errorf("%s: got %v, want %v", c.field, decoded[c.field], c.want)
		}
	}
}

func TestRPCEntriesSince(t *testing.T) {
	aID := "a"
	bID := "b"
	entries := []codingagent.SessionEntry{
		rpcTestEntry("a", nil, "first"),
		rpcTestEntry("b", &aID, "second"),
		rpcTestEntry("c", &bID, "third"),
	}

	all, err := rpcEntriesSince(entries, "")
	if err != nil {
		t.Fatalf("rpcEntriesSince all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("all entries len = %d, want 3", len(all))
	}

	afterB, err := rpcEntriesSince(entries, "b")
	if err != nil {
		t.Fatalf("rpcEntriesSince after b: %v", err)
	}
	if len(afterB) != 1 || afterB[0].Base().ID != "c" {
		t.Fatalf("after b = %#v, want only c", afterB)
	}

	_, err = rpcEntriesSince(entries, "missing")
	if err == nil || err.Error() != "Entry not found: missing" {
		t.Fatalf("missing err = %v, want Entry not found", err)
	}
}

func TestRPCTreeSerializesUpstreamShape(t *testing.T) {
	aID := "a"
	bID := "b"
	root := &codingagent.SessionTreeNode{Children: []*codingagent.SessionTreeNode{
		{
			Entry:          rpcTestEntry("a", nil, "first"),
			Label:          "root label",
			LabelTimestamp: "2026-07-08T00:00:00Z",
			Children: []*codingagent.SessionTreeNode{
				{Entry: rpcTestEntry("b", &aID, "second")},
			},
		},
	}}

	got, err := json.Marshal(map[string]any{
		"tree":   rpcTree(root),
		"leafId": &bID,
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{
		`"tree":[{"entry":`,
		`"children":[{"entry":`,
		`"leafId":"b"`,
		`"label":"root label"`,
		`"labelTimestamp":"2026-07-08T00:00:00Z"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("tree json = %s, missing %s", text, want)
		}
	}
	if strings.Contains(text, `"Entry"`) || strings.Contains(text, `"Children"`) {
		t.Fatalf("tree json uses Go field names: %s", text)
	}
}

func rpcTestEntry(id string, parent *string, text string) codingagent.SessionEntry {
	raw, err := json.Marshal(map[string]any{
		"type":      "message",
		"id":        id,
		"parentId":  parent,
		"timestamp": "2026-07-08T00:00:00Z",
		"message": map[string]any{
			"role":      "user",
			"content":   []map[string]string{{"type": "text", "text": text}},
			"timestamp": 0,
		},
	})
	if err != nil {
		panic(err)
	}
	return sessionentry.DecodeSessionEntry(raw)
}

type fakeRPCCommandRunner struct {
	mu       sync.Mutex
	commands []extension.ResolvedCommand
	executed []string
	errors   []*extension.ExtensionError
}

func (f *fakeRPCCommandRunner) EmitError(err *extension.ExtensionError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errors = append(f.errors, err)
}

func (f *fakeRPCCommandRunner) Commands() []extension.ResolvedCommand {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]extension.ResolvedCommand(nil), f.commands...)
}

func (f *fakeRPCCommandRunner) Command(name string) (extension.ResolvedCommand, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, command := range f.commands {
		if command.InvocationName == name {
			return command, true
		}
	}
	return extension.ResolvedCommand{}, false
}

func (f *fakeRPCCommandRunner) ExecuteCommand(_ context.Context, name, args string) bool {
	if _, ok := f.Command(name); !ok {
		return false
	}
	f.mu.Lock()
	f.executed = append(f.executed, name+" "+args)
	f.mu.Unlock()
	return true
}

func TestRPCGetExtensionsResponseShape(t *testing.T) {
	envelope, err := parseRPCCommand([]byte(`{"id":"e1","type":"get_extensions"}`))
	if err != nil || envelope.Type != "get_extensions" {
		t.Fatalf("envelope=%#v error=%v", envelope, err)
	}
	for _, tc := range []struct {
		name  string
		paths []string
		want  string
	}{
		{"none", []string{}, `"paths":[]`},
		{"load order", []string{"builtin:one", "/ext/two"}, `"paths":["builtin:one","/ext/two"]`},
	} {
		var output bytes.Buffer
		writeJSONLine(&output, rpcSuccess(envelope.ID, "get_extensions", RPCGetExtensionsData{Paths: tc.paths}))
		want := `{"id":"e1","type":"response","command":"get_extensions","success":true,"data":{` + tc.want + "}}\n"
		if output.String() != want {
			t.Errorf("%s:\n got %s want %s", tc.name, output.String(), want)
		}
	}
}

func TestRPCGetCommandsParseAndExactEmptyResponse(t *testing.T) {
	envelope, err := parseRPCCommand([]byte(`{"type":"get_commands"}`))
	if err != nil || envelope.Type != "get_commands" {
		t.Fatalf("envelope=%#v error=%v", envelope, err)
	}
	var command RPCGetCommandsCommand
	if err := json.Unmarshal(envelope.Raw, &command); err != nil || command.Type != "get_commands" {
		t.Fatalf("command=%#v error=%v", command, err)
	}
	var output bytes.Buffer
	writeJSONLine(&output, rpcGetCommandsResponse(nil, headlessCommandCatalog{}))
	want := "{\"type\":\"response\",\"command\":\"get_commands\",\"success\":true,\"data\":{\"commands\":[]}}\n"
	if output.String() != want {
		t.Fatalf("response:\n got %s want %s", output.String(), want)
	}
}

// Pi: packages/coding-agent/src/core/slash-commands.ts:10 (SlashCommandInfo.sourceInfo).
func TestRPCGetCommandsCategoryAndLoaderOrderWithoutBuiltins(t *testing.T) {
	extensionA := RPCSourceInfo{Path: "/extensions/z", Source: "local", Scope: "user", Origin: "top-level", BaseDir: "/extensions"}
	extensionB := RPCSourceInfo{Path: "/extensions/a", Source: "pkg:a", Scope: "project", Origin: "package", BaseDir: "/pkg"}
	runner := &fakeRPCCommandRunner{commands: []extension.ResolvedCommand{
		{RegisteredCommand: extension.RegisteredCommand{Name: "z", Description: "first", SourceInfo: extensionA}, InvocationName: "z"},
		{RegisteredCommand: extension.RegisteredCommand{Name: "dup", Description: "second", SourceInfo: extensionA}, InvocationName: "dup:1"},
		{RegisteredCommand: extension.RegisteredCommand{Name: "dup", Description: "third", SourceInfo: extensionB}, InvocationName: "dup:2"},
		{RegisteredCommand: extension.RegisteredCommand{Name: "a", Description: "fourth", SourceInfo: extensionB}, InvocationName: "a"},
	}}
	promptZ := codingagent.PromptTemplate{Name: "prompt-z", Description: "prompt first", FilePath: "/prompts/z.md"}
	promptA := codingagent.PromptTemplate{Name: "prompt-a", Description: "prompt second", FilePath: "/prompts/a.md"}
	// Skill paths are native, as the loader builds them; sourceInfoForPath
	// matches a skill by filepath.Dir of its path.
	skillZ := &codingagent.SkillDef{Name: "z-skill", Description: "skill first", FilePath: filepath.FromSlash("/skills/z/SKILL.md"), BaseDir: filepath.FromSlash("/skills/z")}
	skillA := &codingagent.SkillDef{Name: "a-skill", Description: "skill second", FilePath: filepath.FromSlash("/skills/a/SKILL.md"), BaseDir: filepath.FromSlash("/skills/a")}
	catalog := headlessCommandCatalog{
		runner: runner, promptTemplates: []codingagent.PromptTemplate{promptZ, promptA}, skills: []*codingagent.SkillDef{skillZ, skillA},
		sourceInfo: map[string]codingagent.ResourceSourceInfo{
			promptZ.FilePath: {Path: promptZ.FilePath, Scope: "user", Origin: "top-level", Source: "local", BaseDir: "/prompts"},
			promptA.FilePath: {Path: promptA.FilePath, Scope: "project", Origin: "package", Source: "pkg:prompts", BaseDir: "/pkg"},
			skillZ.BaseDir:   {Path: skillZ.BaseDir, Scope: "user", Origin: "top-level", Source: "local", BaseDir: "/skills"},
			skillA.BaseDir:   {Path: skillA.BaseDir, Scope: "project", Origin: "package", Source: "pkg:skills", BaseDir: "/pkg"},
		},
	}
	commands := catalog.commands()
	gotNames := make([]string, len(commands))
	for i, command := range commands {
		gotNames[i] = command.Name
		if slices.Contains([]string{"help", "compact", "model", "settings"}, command.Name) {
			t.Fatalf("core TUI command leaked: %#v", command)
		}
	}
	wantNames := []string{"z", "dup:1", "dup:2", "a", "prompt-z", "prompt-a", "skill:z-skill", "skill:a-skill"}
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("order=%v want=%v", gotNames, wantNames)
	}
	if commands[1].Source != "extension" || commands[4].Source != "prompt" || commands[6].Source != "skill" {
		t.Fatalf("categories=%#v", commands)
	}
	if commands[2].SourceInfo != extensionB || commands[5].SourceInfo.Source != "pkg:prompts" || commands[7].SourceInfo.Source != "pkg:skills" {
		t.Fatalf("source info=%#v", commands)
	}
}

func TestRPCPromptRoutingPriorityAndInvocationConsistency(t *testing.T) {
	runner := &fakeRPCCommandRunner{commands: []extension.ResolvedCommand{{RegisteredCommand: extension.RegisteredCommand{Name: "skill:deploy"}, InvocationName: "skill:deploy"}}}
	template := codingagent.PromptTemplate{Name: "deploy", Content: "prompt $1"}
	path := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(path, []byte("skill body"), 0o600); err != nil {
		t.Fatal(err)
	}
	skill := &codingagent.SkillDef{Name: "deploy", Body: "stale cached body", FilePath: path, BaseDir: filepath.Dir(path)}
	catalog := headlessCommandCatalog{runner: runner, promptTemplates: []codingagent.PromptTemplate{template}, skills: []*codingagent.SkillDef{skill}}

	if expanded, handled := catalog.routePrompt(context.Background(), "/skill:deploy now"); !handled || expanded != "" {
		t.Fatalf("extension priority expanded=%q handled=%v", expanded, handled)
	}
	if got := runner.executed; !slices.Equal(got, []string{"skill:deploy now"}) {
		t.Fatalf("executed=%v", got)
	}
	runner.commands = nil
	if expanded, handled := catalog.routePrompt(context.Background(), "/skill:deploy now"); handled || !strings.Contains(expanded, `<skill name="deploy"`) || !strings.HasSuffix(expanded, "\n\nnow") {
		t.Fatalf("skill expansion=%q handled=%v", expanded, handled)
	}
	if expanded, handled := catalog.routePrompt(context.Background(), "/deploy now"); handled || expanded != "prompt now" {
		t.Fatalf("prompt expansion=%q handled=%v", expanded, handled)
	}
	if expanded, handled := catalog.routePrompt(context.Background(), "/unknown untouched"); handled || expanded != "/unknown untouched" {
		t.Fatalf("unknown slash=%q handled=%v", expanded, handled)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if expanded, handled := catalog.routePrompt(t.Context(), "/skill:deploy now"); handled || expanded != "/skill:deploy now" {
		t.Fatalf("missing skill expansion=%q handled=%v", expanded, handled)
	}
	if len(runner.errors) != 1 || runner.errors[0].Event != "skill_expansion" || runner.errors[0].ExtensionPath != path {
		t.Fatalf("skill errors=%v", runner.errors)
	}
}

// Pi: packages/coding-agent/src/cli/args.ts:44 (Args.noPromptTemplates).
func TestRPCResourceHandoffFlagsTrustExplicitAndPiglet(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	userPrompt := filepath.Join(agentDir, "prompts", "user.md")
	projectPrompt := filepath.Join(cwd, ".pig", "prompts", "project.md")
	explicitPrompt := filepath.Join(t.TempDir(), "explicit.md")
	for _, path := range []string{userPrompt, projectPrompt, explicitPrompt} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("---\ndescription: test\n---\nbody"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	explicitSkill := filepath.Join(t.TempDir(), "explicit-skill")
	if err := os.MkdirAll(explicitSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(explicitSkill, "SKILL.md"), []byte("---\nname: explicit\ndescription: test\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if got := collectPromptPaths(cwd, agentDir, sm, Args{NoPromptTemplates: true}, false); len(got) != 0 {
		t.Fatalf("no-prompt-templates inventory=%v", got)
	}
	if got := collectSkillInputs(cwd, agentDir, sm, Args{NoSkills: true}, nil); len(got) != 0 {
		t.Fatalf("no-skills inventory=%v", got)
	}
	flags := Args{NoPromptTemplates: true, PromptTemplates: []string{explicitPrompt}, NoSkills: true, Skills: []string{explicitSkill}}
	promptPaths := collectPromptPaths(cwd, agentDir, sm, flags, false)
	if !slices.Equal(promptPaths, []string{explicitPrompt}) {
		t.Fatalf("no-prompt explicit paths=%v", promptPaths)
	}
	templates := codingagent.LoadPromptTemplates("", "", promptPaths...).Templates
	if len(templates) != 1 || templates[0].Name != "explicit" {
		t.Fatalf("templates=%#v", templates)
	}
	skillPaths := collectSkillInputs(cwd, agentDir, sm, flags, nil)
	loaded, err := resolveAndLoadSkills(nil, skillPaths)
	if err != nil || len(loaded.Defs) != 1 || loaded.Defs[0].Name != "explicit" {
		t.Fatalf("skills=%#v error=%v", loaded, err)
	}
	trustedPaths := collectPromptPaths(cwd, agentDir, sm, Args{}, false)
	if !slices.Contains(trustedPaths, userPrompt) || slices.Contains(trustedPaths, projectPrompt) {
		t.Fatalf("project trust paths=%v", trustedPaths)
	}
	inline := rpcResolvedSkills([]*codingagent.SkillDef{{Name: "inline", Description: "piglet", Body: "body"}}, &piglet.Piglet{Name: "demo"})
	if len(inline) != 1 || inline[0].FilePath != "builtin:piglet" || inline[0].BaseDir != "." {
		t.Fatalf("Piglet inline source=%#v", inline)
	}
	got, expanded, failure := codingagent.ExpandSkillCommand("/skill:inline", inline)
	if !expanded || failure != nil || got != "<skill name=\"inline\" location=\"builtin:piglet\">\nReferences are relative to ..\n\nbody\n</skill>" {
		t.Fatalf("inline invocation=%q expanded=%v failure=%v", got, expanded, failure)
	}
}

func TestRPCGetCommandsConcurrentQueriesRemainJSONL(t *testing.T) {
	runner := &fakeRPCCommandRunner{commands: []extension.ResolvedCommand{{RegisteredCommand: extension.RegisteredCommand{Name: "one", SourceInfo: RPCSourceInfo{Path: "/one", Source: "local", Scope: "user", Origin: "top-level"}}, InvocationName: "one"}}}
	catalog := headlessCommandCatalog{runner: runner}
	var output bytes.Buffer
	var outputMu sync.Mutex
	var wait sync.WaitGroup
	for i := range 16 {
		wait.Add(1)
		go func(id int) {
			defer wait.Done()
			outputMu.Lock()
			writeJSONLine(&output, rpcGetCommandsResponse(rpcStringID(string(rune('a'+id))), catalog))
			outputMu.Unlock()
		}(i)
	}
	wait.Wait()
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 16 {
		t.Fatalf("JSONL lines=%d", len(lines))
	}
	for _, line := range lines {
		var response RPCResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil || response.Command != "get_commands" || !response.Success {
			t.Fatalf("line=%q response=%#v error=%v", line, response, err)
		}
	}
}

// Pi: packages/coding-agent/src/core/slash-commands.ts:10 (SlashCommandInfo.sourceInfo).
func TestRPCPackageResourceSourceInfo(t *testing.T) {
	prompt := codingagent.PromptTemplate{Name: "package-prompt", FilePath: "/packages/demo/prompts/run.md"}
	// Skill paths are native, as the loader builds them.
	skill := &codingagent.SkillDef{Name: "package-skill", FilePath: filepath.FromSlash("/packages/demo/skills/check/SKILL.md"), BaseDir: filepath.FromSlash("/packages/demo/skills/check")}
	info := codingagent.ResourceSourceInfo{Scope: "project", Origin: "package", Source: "github:demo/package", BaseDir: "/packages/demo"}
	catalog := headlessCommandCatalog{
		promptTemplates: []codingagent.PromptTemplate{prompt}, skills: []*codingagent.SkillDef{skill},
		sourceInfo: map[string]codingagent.ResourceSourceInfo{prompt.FilePath: info, skill.BaseDir: info},
	}
	commands := catalog.commands()
	if len(commands) != 2 || commands[0].SourceInfo.Source != "github:demo/package" || commands[0].SourceInfo.Origin != "package" || commands[1].SourceInfo.Source != "github:demo/package" || commands[1].SourceInfo.BaseDir != "/packages/demo" {
		t.Fatalf("commands=%#v", commands)
	}
}

func TestRPCExtensionConfigUsesResolvedProvenance(t *testing.T) {
	path := "/packages/demo/extensions/ext"
	info := codingagent.ResourceSourceInfo{Path: path, Scope: "project", Origin: "package", Source: "github:demo/package", BaseDir: "/packages/demo"}
	configs := rpcExtensionConfigs([]subprocess.ExtConfig{{Name: "demo", Source: path}}, "/workspace", "/agent", map[string]codingagent.ResourceSourceInfo{path: info})
	if len(configs) != 1 {
		t.Fatalf("configs=%#v", configs)
	}
	got := configs[0].SourceInfo
	if got.Path != path || got.Source != "github:demo/package" || got.Scope != "project" || got.Origin != "package" || got.BaseDir != "/packages/demo" {
		t.Fatalf("sourceInfo=%#v", configs[0].SourceInfo)
	}
}

func TestRPCGetCommandsExactRecordResponseShape(t *testing.T) {
	runner := &fakeRPCCommandRunner{commands: []extension.ResolvedCommand{{
		RegisteredCommand: extension.RegisteredCommand{
			Name: "deploy", Description: "Deploy", SourceInfo: RPCSourceInfo{Path: "/ext/deploy", Source: "local", Scope: "project", Origin: "top-level", BaseDir: "/ext"},
		},
		InvocationName: "deploy",
	}}}
	var output bytes.Buffer
	writeJSONLine(&output, rpcGetCommandsResponse(rpcStringID("commands-1"), headlessCommandCatalog{runner: runner}))
	want := "{\"id\":\"commands-1\",\"type\":\"response\",\"command\":\"get_commands\",\"success\":true,\"data\":{\"commands\":[{\"name\":\"deploy\",\"description\":\"Deploy\",\"source\":\"extension\",\"sourceInfo\":{\"path\":\"/ext/deploy\",\"source\":\"local\",\"scope\":\"project\",\"origin\":\"top-level\",\"baseDir\":\"/ext\"}}]}}\n"
	if output.String() != want {
		t.Fatalf("response:\n got %s want %s", output.String(), want)
	}
}

func TestRPCLegacyLeadingSlashExtensionNameIsAdvertisedAndInvokedOnce(t *testing.T) {
	runner := &fakeRPCCommandRunner{commands: []extension.ResolvedCommand{{RegisteredCommand: extension.RegisteredCommand{Name: "/legacy", SourceInfo: RPCSourceInfo{Path: "builtin:legacy", Source: "builtin", Scope: "temporary", Origin: "top-level"}}, InvocationName: "/legacy"}}}
	catalog := headlessCommandCatalog{runner: runner}
	commands := catalog.commands()
	if len(commands) != 1 || commands[0].Name != "legacy" {
		t.Fatalf("commands=%#v", commands)
	}
	if expanded, handled := catalog.routePrompt(context.Background(), "/legacy now"); !handled || expanded != "" || !slices.Equal(runner.executed, []string{"/legacy now"}) {
		t.Fatalf("expanded=%q handled=%v executed=%v", expanded, handled, runner.executed)
	}
}
