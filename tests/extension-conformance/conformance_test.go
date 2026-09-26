// Package extensionconformance is a cross-transport behavioral gate: it
// records observable extension behavior through the in-process runner and
// through the subprocess host, then asserts the two recordings are
// byte-identical. Drift between the transports is a wire-format / dispatch
// regression even when the per-transport unit tests still pass.
//
// Scope (intentionally focused: see pig/AGENTS.md "Behavioral
// testing rule: SDK wire format round-trips"):
//
//   - tool calls: success, thrown errors, and structured is_error results.
//   - commands: success, immediate and awaited errors, completion ordering,
//     ui.Notify/ui.SetStatus side effects, and
//     host action calls for sendMessage/sendUserMessage/setSessionName/appendEntry.
//   - lifecycle events: session_start/session_shutdown.
//
// The suite compares in-process Go and the Go, Node/TypeScript, Rust, and
// Python subprocess runtimes so SDK or wire-protocol drift is caught at the
// transport boundary.
package extensionconformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tests/extension-conformance/testfixture"
)

// recording is the normalized cross-transport observation that gets
// compared byte-for-byte. Anything that could legitimately differ between
// transports (timing, identifiers, paths) is excluded by construction.
type recording struct {
	EchoContent             string              `json:"echo_content"`
	PreparedContent         string              `json:"prepared_content"`
	EchoIsError             bool                `json:"echo_is_error"`
	ToolError               string              `json:"tool_error"`
	ToolIsErrorContent      string              `json:"tool_is_error_content"`
	ToolIsError             bool                `json:"tool_is_error"`
	CommandError            string              `json:"command_error"`
	CommandNotify           []string            `json:"command_notify"`
	StatusUpdates           []string            `json:"status_updates"`
	ActionCalls             []string            `json:"action_calls"`
	SessionEventNotify      []string            `json:"session_event_notify"`
	AgentBeforeSettle       string              `json:"agent_before_settle"`
	ProjectTrustDecision    string              `json:"project_trust_decision"`
	ProjectTrustErrors      []string            `json:"project_trust_errors"`
	RegisteredToolNames     []string            `json:"registered_tool_names"`
	RegisteredCommandNames  []string            `json:"registered_command_names"`
	ToolPromptGuidelines    map[string][]string `json:"tool_prompt_guidelines"`    // tool name → guidelines
	ToolConstrainedSampling map[string]string   `json:"tool_constrained_sampling"` // tool name → canonical constrained sampling JSON
	ContextProbe            string              `json:"context_probe"`             // ctx.mode + ctx.getSystemPromptOptions() summary
	SessionLogProbe         string              `json:"session_log_probe"`
	DialogProbe             string              `json:"dialog_probe"`
	UIPromptEvents          []string            `json:"ui_prompt_events"`
	FocusedProbe            string              `json:"focused_probe"`
	MessageRenderer         []string            `json:"message_renderer"`
	ToolRenderer            []string            `json:"tool_renderer"`
	EntryRenderer           []string            `json:"entry_renderer"`
	LoginDefinition         string              `json:"login_definition"`
	LoginError              string              `json:"login_error"`
	StatusBurst             []string            `json:"status_burst"`
	RichContent             string              `json:"rich_content"`
	RichImages              []ai.ImageContent   `json:"rich_images"`
	RichTerminate           bool                `json:"rich_terminate"`
}

type harness struct {
	runner  *inproc.Runner
	host    *subprocess.Host
	notify  *[]string
	status  *[]string
	actions *[]string
	ui      *recordingUI
	bridge  *subprocess.UIBridge
	cleanup func()
}

type harnessCase struct {
	name    string
	make    func(*testing.T) *harness
	extName string
}

func allHarnessCases() []harnessCase {
	return []harnessCase{
		{name: "inproc-go", make: makeInprocGoHarness, extName: inprocFixtureName},
		{name: "subprocess-go", make: makeSubprocessGoHarness, extName: "sdk-fixture"},
		{name: "subprocess-node", make: makeSubprocessNodeHarness, extName: "node-sdk-fixture"},
		{name: "subprocess-node-packed", make: makeSubprocessNodePackedHarness, extName: "node-sdk-fixture"},
		{name: "subprocess-rust", make: makeSubprocessRustHarness, extName: "rust-sdk-fixture"},
		{name: "subprocess-python", make: makeSubprocessPythonHarness, extName: "python-sdk-fixture"},
		{name: "fused-go", make: makeFusedGoHarness, extName: "sdk-fixture"},
	}
}

func sdkHarnessCases() []harnessCase {
	cases := allHarnessCases()
	return cases[1:]
}

type conformanceLinesComponent struct {
	lines []string
}

// conformanceWidthComponent renders one line naming the width it is drawn at.
type conformanceWidthComponent struct {
	line func(width int) string
}

func (c *conformanceWidthComponent) Render(width int) []string { return []string{c.line(width)} }
func (*conformanceWidthComponent) Invalidate()                 {}

func (c *conformanceLinesComponent) Render(int) []string { return append([]string(nil), c.lines...) }
func (*conformanceLinesComponent) Invalidate()           {}

func TestConformance_TransportsMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixture)")
	}

	cases := allHarnessCases()

	var baseline recording
	var baselineName string
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			got := captureRecording(t, h)
			if i == 0 {
				baseline = got
				baselineName = tc.name
				return
			}
			if baselineName == "" {
				t.Skip("baseline subtest was filtered out by -run; run the full suite")
			}
			if !reflect.DeepEqual(got, baseline) {
				gb, _ := json.MarshalIndent(got, "", "  ")
				wb, _ := json.MarshalIndent(baseline, "", "  ")
				t.Fatalf("transport %q diverged from %q baseline\n--- baseline (%s) ---\n%s\n--- got (%s) ---\n%s",
					tc.name, baselineName, baselineName, wb, tc.name, gb)
			}
		})
	}
}

func TestModelStreamingSDKsMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping model stream conformance in short mode (builds subprocess fixtures)")
	}
	cases := sdkHarnessCases()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			var mu sync.Mutex
			var identities []string
			var requests []map[string]any
			h.bridge.SetHostAction("streamModel", func(_ context.Context, model map[string]any, request map[string]any) (*ai.AssistantMessageEventStream, error) {
				provider, _ := model["provider"].(string)
				modelID, _ := model["modelId"].(string)
				if modelID == "" {
					modelID, _ = model["id"].(string)
				}
				mu.Lock()
				identities = append(identities, provider+"/"+modelID)
				requests = append(requests, request)
				mu.Unlock()
				if modelID == "protocol-error" {
					return nil, errors.New("transport boom")
				}
				if modelID == "unknown" {
					stream := ai.NewAssistantMessageEventStream()
					errMessage := &ai.AssistantMessage{Provider: provider, Model: modelID, StopReason: ai.StopReasonError, ErrorMessage: "unknown model"}
					if err := stream.Push(ai.ErrorEvent{Reason: ai.StopReasonError, Error: errMessage}); err != nil {
						return nil, err
					}
					return stream, nil
				}
				return conformanceModelStream(provider, modelID)
			})
			command, ok := findCommand(h.runner, "model-stream-probe")
			if !ok {
				t.Fatal("model-stream-probe command not registered")
			}
			if err := command.Handler(ctx, ""); err != nil {
				t.Fatal(err)
			}
			if err := ctx.Err(); err != nil {
				t.Fatalf("model stream command exceeded bounded context: %v", err)
			}
			waitFor(t, func() bool {
				return slices.Contains(*h.notify, "model-stream=ok:info")
			})
			mu.Lock()
			got := append([]string(nil), identities...)
			gotRequests := append([]map[string]any(nil), requests...)
			mu.Unlock()
			if len(got) != 6 {
				t.Fatalf("model calls = %v, want four declared, one unknown, and one protocol error", got)
			}
			for i, request := range gotRequests {
				if !reflect.DeepEqual(request, conformanceFieldRichModelRequest()) {
					t.Fatalf("model request %d = %#v, want %#v", i, request, conformanceFieldRichModelRequest())
				}
			}
			for i, identity := range got {
				want := "conformance/declared"
				if i == 4 {
					want = "conformance/unknown"
				}
				if i == 5 {
					want = "conformance/protocol-error"
				}
				if identity != want {
					t.Fatalf("model call %d identity = %q, want %q", i, identity, want)
				}
			}
		})
	}
}

type productionToolProvider struct{ calls int }

func (*productionToolProvider) ID() string   { return "production-tool-provider" }
func (*productionToolProvider) Close() error { return nil }
func (provider *productionToolProvider) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	provider.calls++
	stream := ai.NewAssistantMessageEventStream()
	if provider.calls == 1 {
		arguments := ai.JsonObject{"path": "x", "nested": ai.JsonObject{"depth": float64(2)}}
		call := ai.ToolCall{ID: "production-call", Name: "production_tool", Arguments: arguments}
		partial := &ai.AssistantMessage{Provider: provider.ID(), Model: "production-model", Content: []ai.AssistantContentBlock{call}, StopReason: ai.StopReasonPending}
		terminal := &ai.AssistantMessage{Provider: provider.ID(), Model: "production-model", Content: []ai.AssistantContentBlock{call}, StopReason: ai.StopReasonToolUse}
		for _, event := range []ai.AssistantMessageEvent{
			ai.StartEvent{Partial: partial},
			ai.ToolCallStartEvent{ContentIndex: 0, Partial: partial},
			ai.ToolCallDeltaEvent{ContentIndex: 0, Delta: `{"path":"x","nested":{"depth":2}}`, Partial: partial},
			ai.ToolCallEndEvent{ContentIndex: 0, ToolCall: call, Partial: partial},
			ai.DoneEvent{Reason: ai.StopReasonToolUse, Message: terminal},
		} {
			if err := stream.Push(event); err != nil {
				return nil, err
			}
		}
		return stream, nil
	}
	partial := &ai.AssistantMessage{Provider: provider.ID(), Model: "production-model", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "finished"}}, StopReason: ai.StopReasonPending}
	terminal := &ai.AssistantMessage{Provider: provider.ID(), Model: "production-model", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "finished"}}, StopReason: ai.StopReasonStop}
	for _, event := range []ai.AssistantMessageEvent{
		ai.StartEvent{Partial: partial},
		ai.TextStartEvent{ContentIndex: 0, Partial: partial},
		ai.TextDeltaEvent{ContentIndex: 0, Delta: "finished", Partial: partial},
		ai.TextEndEvent{ContentIndex: 0, Content: "finished", Partial: partial},
		ai.DoneEvent{Reason: ai.StopReasonStop, Message: terminal},
	} {
		if err := stream.Push(event); err != nil {
			return nil, err
		}
	}
	return stream, nil
}

type productionTool struct{}

func (productionTool) Name() string                           { return "production_tool" }
func (productionTool) Label() string                          { return "Production tool" }
func (productionTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeSequential }
func (productionTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "production_tool", Description: "Production tool", Parameters: map[string]any{"type": "object"}}
}
func (productionTool) Execute(_ context.Context, _ string, _ json.RawMessage, update agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	update("working", map[string]any{"progress": float64(1)})
	return agent.AgentToolResult{
		Content: "done",
		Images:  []ai.ImageContent{{Data: "aW1n", MimeType: "image/png"}},
		Details: map[string]any{"nested": map[string]any{"value": "kept"}},
		IsError: true,
	}, nil
}

func runProductionToolExecution(t *testing.T, runner *inproc.Runner) agent.ToolResultMessage {
	t.Helper()
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	provider := &productionToolProvider{}
	session, err := coding.NewSession(services, coding.SessionOptions{
		Model: &ai.Model{ID: "production-model", Provider: provider, ProviderMeta: ai.ProviderMetadata{ProviderID: provider.ID()}},
		Tools: []agent.AgentTool{productionTool{}}, SkipBuiltinTools: true, Runner: runner,
		SessionDir: t.TempDir(), EventBufferSize: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if _, err := session.Send(context.Background(), "run the production tool"); err != nil {
		t.Fatal(err)
	}
	for _, message := range session.Inner().BuildContext(nil) {
		if message.ToolResult != nil && message.ToolResult.ToolCallID == "production-call" {
			return *message.ToolResult
		}
	}
	t.Fatal("persisted ToolResultMessage not found")
	return agent.ToolResultMessage{}
}

func TestProductionToolExecutionPayloadsMatchAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping production extension event conformance in short mode")
	}
	cases := allHarnessCases()
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			h := test.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			h.ui.ClearRecorded()
			persisted := runProductionToolExecution(t, h.runner)
			if len(persisted.Content) != 2 {
				t.Fatalf("persisted content = %#v, want ordered text and image", persisted.Content)
			}
			text, textOK := persisted.Content[0].(ai.TextContent)
			image, imageOK := persisted.Content[1].(ai.ImageContent)
			details, detailsOK := persisted.Details.(map[string]any)
			nested, nestedOK := details["nested"].(map[string]any)
			if !textOK || !imageOK || !detailsOK || !nestedOK {
				t.Fatalf("persisted ToolResultMessage lost typed payload: %#v", persisted)
			}
			want := []string{
				"tool-update=production_tool:x:2:working:1:info",
				fmt.Sprintf("tool-end=production_tool:%s:%d:%s:%s:%v:%t:info", text.Text, len(persisted.Content), image.Data, image.MimeType, nested["value"], persisted.IsError),
			}
			toolEvents := func() []string {
				var result []string
				for _, event := range h.ui.Recorded() {
					if strings.HasPrefix(event, "tool-update=") || strings.HasPrefix(event, "tool-end=") {
						result = append(result, event)
					}
				}
				return result
			}
			waitFor(t, func() bool { return len(toolEvents()) >= len(want) })
			got := toolEvents()
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("production tool events = %#v, persisted oracle = %#v", got, want)
			}
		})
	}
}

func TestModelEventPayloadsMatchAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping extension event conformance in short mode")
	}
	cases := sdkHarnessCases()
	want := []string{
		"message-update=text_delta:2:delta:false:info",
		"tool-update=read:{\"path\":\"x\"}:working:1:info",
		"tool-end=read:2:aW1n:kept:true:info",
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			h := test.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			h.ui.ClearRecorded()
			events := []any{
				extension.MessageUpdateEvent{Type: "message_update", Message: map[string]any{"role": "assistant"}, AssistantMessageEvent: ai.TextDeltaEvent{ContentIndex: 2, Delta: "delta"}},
				extension.ToolExecutionUpdateEvent{Type: "tool_execution_update", ToolCallID: "call", ToolName: "read", Args: map[string]any{"path": "x"}, PartialResult: map[string]any{"content": "working", "details": map[string]any{"progress": 1}}},
				extension.ToolExecutionEndEvent{Type: "tool_execution_end", ToolCallID: "call", ToolName: "read", Result: map[string]any{"content": []any{map[string]any{"type": "text", "text": "done"}, map[string]any{"type": "image", "data": "aW1n", "mimeType": "image/png"}}, "details": map[string]any{"nested": map[string]any{"value": "kept"}}}, IsError: true},
			}
			for _, event := range events {
				if _, err := h.runner.Emit(ctx, event); err != nil {
					t.Fatalf("emit %T: %v", event, err)
				}
			}
			waitFor(t, func() bool { return len(h.ui.Recorded()) >= len(want) })
			if got := h.ui.Recorded(); !reflect.DeepEqual(got, want) {
				t.Fatalf("event payloads = %#v, want %#v", got, want)
			}
		})
	}
}

func conformanceFieldRichModelRequest() map[string]any {
	zeta := "last-first"
	middle := "middle"
	return map[string]any{
		"systemPrompt": "conformance-system",
		"messages": []any{
			map[string]any{
				"role":    "system",
				"content": []any{map[string]any{"type": "text", "text": "signed system", "textSignature": "system-signature"}},
				"sections": ai.OrderedSections{
					{Name: "zeta", Value: &zeta},
					{Name: "alpha", Value: nil},
					{Name: "middle", Value: &middle},
				},
				"timestamp": float64(41),
			},
			map[string]any{"role": "user", "content": "hello", "timestamp": float64(42)},
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "prior", "textSignature": "signed"}}, "api": "openai-responses", "provider": "prior-provider", "model": "prior-model", "usage": map[string]any{"input": float64(1), "output": float64(2), "cacheRead": float64(3), "cacheWrite": float64(4), "totalTokens": float64(10), "cost": map[string]any{"input": 0.1, "output": 0.2, "cacheRead": 0.3, "cacheWrite": 0.4, "total": 1.0}}, "stopReason": "stop", "timestamp": float64(43)},
		},
		"tools": []any{map[string]any{
			"name": "lookup", "description": "lookup", "parameters": map[string]any{"type": "object"},
			"constrainedSampling": map[string]any{"type": "grammar", "variants": map[string]any{"openai_lark": "start: NUMBER"}},
		}},
		"maxTokens": float64(321), "temperature": 0.65, "samplingParams": map[string]any{"topP": 0.8},
		"thinkingBudgets": map[string]any{"minimal": float64(11), "low": float64(22), "medium": float64(33), "high": float64(44)},
		"thinking":        "high", "isReasoning": true, "env": map[string]any{"WIRE_ENV": "request-value", "SECOND_ENV": "distinct-value"},
		"headers": map[string]any{"X-Wire": "yes", "X-Remove": nil}, "sessionId": "conformance-session", "transport": "sse",
	}
}

func conformanceModelStream(provider, model string) (*ai.AssistantMessageEventStream, error) {
	stream := ai.NewAssistantMessageEventStream()
	partial := &ai.AssistantMessage{API: ai.API("faux"), Provider: provider, Model: model, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "streamed"}}, StopReason: ai.StopReasonPending}
	terminal := &ai.AssistantMessage{API: ai.API("faux"), Provider: provider, Model: model, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "streamed"}}, StopReason: ai.StopReasonStop}
	for _, event := range []ai.AssistantMessageEvent{
		ai.StartEvent{Partial: partial},
		ai.TextStartEvent{ContentIndex: 0, Partial: partial},
		ai.TextDeltaEvent{ContentIndex: 0, Delta: "streamed", Partial: partial},
		ai.TextEndEvent{ContentIndex: 0, Content: "streamed", Partial: partial},
		ai.DoneEvent{Reason: ai.StopReasonStop, Message: terminal},
		ai.TextDeltaEvent{ContentIndex: 0, Delta: "ignored", Partial: partial},
	} {
		if err := stream.Push(event); err != nil {
			return nil, err
		}
	}
	return stream, nil
}

func TestRemoteComponentInvalidationSDKsMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}

	cases := sdkHarnessCases()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			h.ui.ClearRecorded()
			command, ok := findCommand(h.runner, "timer-focused-probe")
			if !ok {
				t.Fatal("timer-focused-probe command not registered")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := command.Handler(ctx, ""); err != nil {
				t.Fatal(err)
			}
			var others []string
			waitFor(t, func() bool {
				_, others = splitPromptNotifications(h.ui.Recorded())
				return len(others) > 0
			})
			got := others[0]
			var frame int
			if _, err := fmt.Sscanf(got, "timer=%d disposed=true detached=true:info", &frame); err != nil {
				t.Fatalf("timer lifecycle recording = %q: %v", got, err)
			}
			if frame < 2 {
				t.Fatalf("timer rendered frame %d, want at least 2 without input", frame)
			}
		})
	}
}

// captureRecording drives the same scenario through whichever transport
// `h` wraps, then returns the normalized observation.
func captureRecording(t *testing.T, h *harness) recording {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	registeredToolNames := toolNames(h.runner)
	registeredCommandNames := commandNames(h.runner)

	tool, ok := findTool(h.runner, "echo")
	if !ok {
		t.Fatal("echo tool not registered")
	}
	result, err := tool.Definition.Execute(ctx, "tc-conformance-1",
		json.RawMessage(`{"text":"hello"}`), nil)
	if err != nil {
		t.Fatalf("echo execute: %v", err)
	}
	tr, ok := result.(agent.AgentToolResult)
	if !ok {
		t.Fatalf("echo result type = %T, want agent.AgentToolResult", result)
	}

	richTool, ok := findTool(h.runner, "rich_tool")
	if !ok {
		t.Fatal("rich_tool not registered")
	}
	richResult, err := richTool.Definition.Execute(ctx, "tc-conformance-rich", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatalf("rich_tool execute: %v", err)
	}
	rich, ok := richResult.(agent.AgentToolResult)
	if !ok {
		t.Fatalf("rich_tool result type = %T, want agent.AgentToolResult", richResult)
	}

	preparedTool, ok := findTool(h.runner, "prepared_tool")
	if !ok {
		t.Fatal("prepared_tool not registered")
	}
	preparedArgs := json.RawMessage(`{"legacy":"hello"}`)
	if preparedTool.Definition.PrepareArguments != nil {
		preparedArgs, err = preparedTool.Definition.PrepareArguments(preparedArgs)
		if err != nil {
			t.Fatalf("prepare arguments: %v", err)
		}
	}
	preparedResult, err := preparedTool.Definition.Execute(ctx, "tc-conformance-prepare", preparedArgs, nil)
	if err != nil {
		t.Fatalf("prepared_tool execute: %v", err)
	}
	preparedTR, ok := preparedResult.(agent.AgentToolResult)
	if !ok {
		t.Fatalf("prepared_tool result type = %T, want agent.AgentToolResult", preparedResult)
	}

	toolErrDef, ok := findTool(h.runner, "tool_error")
	if !ok {
		t.Fatal("tool_error tool not registered")
	}
	_, toolErr := toolErrDef.Definition.Execute(ctx, "tc-conformance-err", json.RawMessage(`{}`), nil)
	if toolErr == nil {
		t.Fatal("tool_error execute error = nil")
	}

	softErrDef, ok := findTool(h.runner, "tool_is_error")
	if !ok {
		t.Fatal("tool_is_error tool not registered")
	}
	softResult, err := softErrDef.Definition.Execute(ctx, "tc-conformance-soft", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatalf("tool_is_error execute: %v", err)
	}
	softTR, ok := softResult.(agent.AgentToolResult)
	if !ok {
		t.Fatalf("tool_is_error result type = %T, want agent.AgentToolResult", softResult)
	}

	*h.notify = nil
	h.ui.ClearStatuses()
	cmd, ok := findCommand(h.runner, "ping")
	if !ok {
		t.Fatal("ping command not registered")
	}
	if err := cmd.Handler(ctx, ""); err != nil {
		t.Fatalf("ping command: %v", err)
	}

	commandErrDef, ok := findCommand(h.runner, "command_error")
	if !ok {
		t.Fatal("command_error command not registered")
	}
	commandErr := commandErrDef.Handler(ctx, "")
	if commandErr == nil {
		t.Fatal("command_error error = nil")
	}

	awaitedCommand, ok := findCommand(h.runner, "command_awaited_error")
	if !ok {
		t.Fatal("command_awaited_error command not registered")
	}
	awaitedStart := time.Now()
	awaitedErr := awaitedCommand.Handler(ctx, "")
	if elapsed := time.Since(awaitedStart); elapsed < 100*time.Millisecond {
		t.Fatalf("command_awaited_error returned after %v, before awaited work completed", elapsed)
	}
	if awaitedErr == nil || !strings.Contains(awaitedErr.Error(), "awaited command exploded") {
		t.Fatalf("command_awaited_error error = %v", awaitedErr)
	}

	statusCmd, ok := findCommand(h.runner, "status")
	if !ok {
		t.Fatal("status command not registered")
	}
	if err := statusCmd.Handler(ctx, ""); err != nil {
		t.Fatalf("status command: %v", err)
	}
	waitFor(t, func() bool { return len(h.ui.Recorded()) > 0 && len(h.ui.Statuses()) > 0 })
	commandNotify := append([]string(nil), (*h.notify)...)
	statusUpdates := h.ui.Statuses()

	statusBurst := captureStatusBurst(ctx, t, h)

	*h.actions = nil
	for _, name := range []string{"send_message", "send_message_default", "send_message_no_turn", "send_user_message", "set_session_name", "append_entry"} {
		cmd, ok := findCommand(h.runner, name)
		if !ok {
			t.Fatalf("%s command not registered", name)
		}
		if err := cmd.Handler(ctx, ""); err != nil {
			t.Fatalf("%s command: %v", name, err)
		}
	}
	waitFor(t, func() bool { return len(*h.actions) >= 6 })
	actionCalls := append([]string(nil), (*h.actions)...)

	*h.notify = nil
	loginCmd, ok := findCommand(h.runner, "login-probe")
	if !ok {
		t.Fatal("login-probe command not registered")
	}
	if err := loginCmd.Handler(ctx, ""); err != nil {
		t.Fatalf("login-probe command: %v", err)
	}
	waitFor(t, func() bool { return len(h.ui.LoginDefinitions()) == 1 && len(h.ui.Recorded()) > 0 })
	loginDefinitions := h.ui.LoginDefinitions()
	loginDefinition := loginDefinitions[0]
	loginError := h.ui.Recorded()[0]

	*h.notify = nil
	if _, err := h.runner.Emit(ctx, extension.SessionStartEvent{Type: "session_start", Reason: "startup"}); err != nil {
		t.Fatalf("session_start emit: %v", err)
	}
	if _, err := h.runner.Emit(ctx, extension.SessionShutdownEvent{Type: "session_shutdown", Reason: "quit"}); err != nil {
		t.Fatalf("session_shutdown emit: %v", err)
	}
	if _, err := h.runner.Emit(ctx, extension.SessionInfoChangedEvent{Type: "session_info_changed", Name: "conformance-session"}); err != nil {
		t.Fatalf("session_info_changed emit: %v", err)
	}
	if _, err := h.runner.Emit(ctx, extension.SessionBeforeCompactEvent{Type: "session_before_compact", Reason: "threshold", WillRetry: true}); err != nil {
		t.Fatalf("session_before_compact emit: %v", err)
	}
	if _, err := h.runner.Emit(ctx, extension.SessionCompactEvent{Type: "session_compact", Reason: "threshold", WillRetry: true, FromExtension: true}); err != nil {
		t.Fatalf("session_compact emit: %v", err)
	}
	if _, err := h.runner.Emit(ctx, extension.SessionCompactFailedEvent{Type: "session_compact_failed", Reason: "overflow", ErrorMessage: "recovery failed", Aborted: false, WillRetry: false, FromExtension: true}); err != nil {
		t.Fatalf("session_compact_failed emit: %v", err)
	}
	if _, err := h.runner.Emit(ctx, extension.TurnEndEvent{Type: "turn_end", MessageEntryID: "assistant-entry", ToolResultEntryIds: []string{"tool-entry"}}); err != nil {
		t.Fatalf("turn_end emit: %v", err)
	}
	waitFor(t, func() bool { return len(*h.notify) >= 7 })
	sessionEventNotify := append([]string(nil), (*h.notify)...)

	*h.notify = nil
	var boundaryErrors []*extension.ExtensionError
	removeBoundaryListener := h.runner.AddErrorListener(func(err *extension.ExtensionError) { boundaryErrors = append(boundaryErrors, err) })
	boundary, err := h.runner.EmitBoundary(ctx, "agent_before_settle", extension.AgentActivityCompleted,
		func(entries []extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
			return extension.BoundaryContextPreview{
				ContextEntries: make([]extension.ProjectedSessionEntry, len(entries)),
				CanContinue:    len(entries) > 0,
			}, nil
		})
	removeBoundaryListener()
	if err != nil {
		t.Fatalf("agent_before_settle emit: %v", err)
	}
	if len(boundaryErrors) != 1 {
		t.Fatalf("boundary must surface only the deliberate handler failure: %+v", boundaryErrors)
	}
	if !boundary.Valid || !boundary.Continue || len(boundary.Entries) != 1 || boundary.Entries[0].CustomType != "conformance-boundary" {
		t.Fatalf("boundary mutation/error/result chain: %+v", boundary)
	}
	waitFor(t, func() bool { return len(*h.notify) > 0 })
	agentBeforeSettle := fmt.Sprintf("%s:result=%d:%t", (*h.notify)[0], len(boundary.Entries), boundary.Continue)

	projectTrust, projectTrustErrors, err := inproc.EmitProjectTrust(h.runner, ctx, extension.ProjectTrustEvent{
		Type: "project_trust",
		Cwd:  "/fixture/project",
	})
	if err != nil {
		t.Fatalf("project_trust emit: %v", err)
	}
	if len(projectTrustErrors) != 1 {
		t.Fatalf("project_trust errors: %+v", projectTrustErrors)
	}
	projectTrustErrorMessages := []string{projectTrustErrors[0].Error}
	if projectTrust == nil {
		t.Fatal("project_trust returned no decisive result")
	}
	projectTrustDecision := string(projectTrust.Trusted)

	// ctx.mode + ctx.getSystemPromptOptions() (#40/#41). The probe command
	// reads both and notifies a deterministic summary. The inproc baseline
	// dispatches command handlers directly, so attach a CommandContext
	// (the subprocess SDKs build their own Context over the wire).
	*h.notify = nil
	probeCmd, ok := findCommand(h.runner, "context-probe")
	if !ok {
		t.Fatal("context-probe command not registered")
	}
	probeCtx := ctx
	if h.host == nil {
		cc := h.runner.CreateCommandContext()
		probeCtx = extension.WithCommandContext(extension.WithContext(ctx, cc.Context), cc)
	}
	if err := probeCmd.Handler(probeCtx, ""); err != nil {
		t.Fatalf("context-probe command: %v", err)
	}
	waitFor(t, func() bool { return len(*h.notify) > 0 })
	contextProbe := (*h.notify)[0]

	*h.notify = nil
	sessionLogCmd, ok := findCommand(h.runner, "session-log-probe")
	if !ok {
		t.Fatal("session-log-probe command not registered")
	}
	if err := sessionLogCmd.Handler(probeCtx, ""); err != nil {
		t.Fatalf("session-log-probe command: %v", err)
	}
	waitFor(t, func() bool { return len(*h.notify) > 0 })
	sessionLogProbe := (*h.notify)[0]

	// Each dialog reports ui_prompt_start/ui_prompt_end without awaiting
	// handlers, so the reports arrive alongside the command's own summary.
	h.ui.ClearRecorded()
	dialogCmd, ok := findCommand(h.runner, "dialog-probe")
	if !ok {
		t.Fatal("dialog-probe command not registered")
	}
	if err := dialogCmd.Handler(probeCtx, ""); err != nil {
		t.Fatalf("dialog-probe command: %v", err)
	}
	waitFor(t, func() bool {
		prompts, others := splitPromptNotifications(h.ui.Recorded())
		return len(others) > 0 && len(prompts) >= 8
	})
	uiPromptEvents, dialogOthers := splitPromptNotifications(h.ui.Recorded())
	dialogProbe := dialogOthers[0]

	h.ui.ClearRecorded()
	focusedCmd, ok := findCommand(h.runner, "focused-probe")
	if !ok {
		t.Fatal("focused-probe command not registered")
	}
	if err := focusedCmd.Handler(probeCtx, ""); err != nil {
		t.Fatalf("focused-probe command: %v", err)
	}
	waitFor(t, func() bool {
		prompts, others := splitPromptNotifications(h.ui.Recorded())
		return len(others) > 0 && len(prompts) >= 2
	})
	focusedPrompts, focusedOthers := splitPromptNotifications(h.ui.Recorded())
	focusedProbe := focusedOthers[0]
	uiPromptEvents = append(uiPromptEvents, focusedPrompts...)
	// Upstream runner.ts queues void emit(event): cross-event completion is unordered.
	wantPromptCompletions := slices.Clone(wantUIPromptEvents)
	slices.Sort(wantPromptCompletions)
	slices.Sort(uiPromptEvents)
	if !slices.Equal(uiPromptEvents, wantPromptCompletions) {
		t.Fatalf("ui_prompt events =\n%s\nwant\n%s", strings.Join(uiPromptEvents, "\n"), strings.Join(wantUIPromptEvents, "\n"))
	}

	renderer := h.runner.MessageRenderer("conformance-message")
	if renderer == nil {
		t.Fatal("conformance-message renderer not registered")
	}
	component, ok := renderer(extension.CustomMessageRef{
		CustomType: "conformance-message",
		Content:    "hello",
		Display:    true,
	}, extension.MessageRenderOptions{Expanded: true}, nil).(interface {
		Render(int) []string
		Invalidate()
	})
	if !ok {
		t.Fatal("message renderer did not return a component")
	}
	var messageRenderer []string
	waitFor(t, func() bool {
		messageRenderer = component.Render(72)
		return len(messageRenderer) == 1 && messageRenderer[0] == "renderer:hello:expanded=true:width=72"
	})

	entryRenderer := h.runner.EntryRenderer("conformance-entry")
	if entryRenderer == nil {
		t.Fatal("conformance-entry renderer not registered")
	}
	entryComponent, ok := entryRenderer(
		map[string]any{"customType": "conformance-entry", "data": "hi-entry"},
		extension.EntryRenderOptions{Expanded: true}, nil,
	).(interface {
		Render(int) []string
		Invalidate()
	})
	if !ok {
		t.Fatal("entry renderer did not return a component")
	}
	var entryRendererLines []string
	waitFor(t, func() bool {
		entryRendererLines = entryComponent.Render(72)
		return len(entryRendererLines) == 1 && entryRendererLines[0] == "entryrenderer:hi-entry:expanded=true:width=72"
	})

	renderProbe, ok := h.runner.GetToolDefinition("render_probe")
	if !ok || renderProbe.RenderShell != extension.ToolRenderShellSelf || renderProbe.RenderCall == nil || renderProbe.RenderResult == nil {
		t.Fatalf("render_probe definition = %+v, want a self shell with both renderers", renderProbe)
	}
	renderContext := extension.ToolRenderContext{
		Args: json.RawMessage(`{"topic":"alpha"}`), ToolCallID: "render-probe-1", Card: "conformance-card",
		State: map[string]any{}, Invalidate: func() {}, ExecutionStarted: true, ArgsComplete: true,
	}
	renderCall, ok := renderProbe.RenderCall(json.RawMessage(`{"topic":"alpha"}`), nil, renderContext).(interface{ Render(int) []string })
	if !ok {
		t.Fatal("renderCall did not return a component")
	}
	var toolRenderer []string
	waitFor(t, func() bool {
		lines := renderCall.Render(72)
		return len(lines) == 1 && lines[0] == "toolrender:call:alpha:partial=false:calls=1:width=72"
	})
	toolRenderer = append(toolRenderer, renderCall.Render(72)...)
	renderResult, ok := renderProbe.RenderResult(agent.AgentToolResult{Content: "out", Details: map[string]any{"k": "v"}}, extension.ToolRenderResultOptions{Expanded: true}, nil, renderContext).(interface{ Render(int) []string })
	if !ok {
		t.Fatal("renderResult did not return a component")
	}
	waitFor(t, func() bool {
		lines := renderResult.Render(72)
		return len(lines) == 1 && lines[0] == "toolrender:result:out:v:expanded=true:calls=1:width=72"
	})
	toolRenderer = append(toolRenderer, renderResult.Render(72)...)

	return recording{
		ToolRenderer:            toolRenderer,
		EchoContent:             tr.Content,
		PreparedContent:         preparedTR.Content,
		EchoIsError:             tr.IsError,
		ToolError:               toolErr.Error(),
		ToolIsErrorContent:      softTR.Content,
		ToolIsError:             softTR.IsError,
		CommandError:            commandErr.Error(),
		CommandNotify:           commandNotify,
		StatusUpdates:           statusUpdates,
		ActionCalls:             actionCalls,
		SessionEventNotify:      sessionEventNotify,
		AgentBeforeSettle:       agentBeforeSettle,
		ProjectTrustDecision:    projectTrustDecision,
		ProjectTrustErrors:      projectTrustErrorMessages,
		RegisteredToolNames:     registeredToolNames,
		RegisteredCommandNames:  registeredCommandNames,
		ToolPromptGuidelines:    toolGuidelines(h.runner),
		ToolConstrainedSampling: toolConstrainedSampling(h.runner),
		ContextProbe:            contextProbe,
		SessionLogProbe:         sessionLogProbe,
		DialogProbe:             dialogProbe,
		UIPromptEvents:          uiPromptEvents,
		FocusedProbe:            focusedProbe,
		MessageRenderer:         messageRenderer,
		EntryRenderer:           entryRendererLines,
		LoginDefinition:         loginDefinition,
		LoginError:              loginError,
		StatusBurst:             statusBurst,
		RichContent:             rich.Content,
		RichImages:              rich.Images,
		RichTerminate:           rich.Terminate,
	}
}

// statusBurstCount is how many unawaited ui.setStatus calls status_burst makes.
// Upstream applies them in program order, so the burst reads back 0..N-1.
const statusBurstCount = 200

// captureStatusBurst runs the status_burst command, whose handler sets one
// status key statusBurstCount times without awaiting, and returns the updates
// for that key in the order the UI applied them.
func captureStatusBurst(ctx context.Context, t *testing.T, h *harness) []string {
	t.Helper()
	h.ui.ClearStatuses()
	burst, ok := findCommand(h.runner, "status_burst")
	if !ok {
		t.Fatal("status_burst command not registered")
	}
	if err := burst.Handler(ctx, ""); err != nil {
		t.Fatalf("status_burst command: %v", err)
	}
	var updates []string
	waitFor(t, func() bool {
		updates = updates[:0]
		for _, update := range h.ui.Statuses() {
			if strings.HasPrefix(update, "burst:") {
				updates = append(updates, update)
			}
		}
		return len(updates) >= statusBurstCount
	})
	return updates
}

// ── inproc transport ─────────────────────────────────────────────────────────

// inprocFixtureName is the source name for the in-process conformance fixture.
// Matches what upstream loader.ts would stamp as extension.sourceInfo on every
// tool. All inproc fixture tools set SourceInfo to this value (except tools
// with explicit source overrides like sourced_tool).
const inprocFixtureName = "inproc-conformance-fixture"

func makeInprocGoHarness(t *testing.T) *harness {
	t.Helper()
	notify := &[]string{}
	status := &[]string{}
	actions := &[]string{}
	ui := newRecordingUI(notify, status)
	runner := inproc.NewRunner([]extension.Extension{makeInprocFixture(ui, actions)}, t.TempDir())
	runner.SetUIContext(ui)
	// Mirror the subprocess host's mode + getSystemPromptOptions wiring so
	// the context-probe recording matches across all transports (#40/#41).
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{
		Mode:                   extension.ExtensionMode(conformanceMode),
		GetSystemPromptOptions: conformanceSystemPromptOptions,
		IsProjectTrusted:       func() bool { return conformanceProjectTrusted },
	}, nil)
	return &harness{runner: runner, notify: notify, status: status, actions: actions, ui: ui}
}

// conformanceMode is the fixed ctx.mode all transports report for the
// context-probe (#40).
const conformanceMode = "tui"

// conformanceProjectTrusted is the fixed ctx.isProjectTrusted() every
// transport reports for the context-probe.
//
// Deliberately false: every SDK falls back to trusted when it cannot reach the
// host, so binding true would let an SDK that never makes the call agree with
// the reference by accident.
const conformanceProjectTrusted = false

// conformanceSystemPromptOptions is the fixed ctx.getSystemPromptOptions()
// payload all transports return for the context-probe (#41).
func conformanceSystemPromptOptions() extension.BuildSystemPromptOptions {
	return extension.BuildSystemPromptOptions{
		CustomPrompt:  "conformance-prompt",
		SelectedTools: []string{"read", "bash"},
		Cwd:           "/probe-cwd",
	}
}

// makeInprocFixture is the in-process equivalent of testfixture.Extension. It must
// expose **exactly the same observable behavior** for the conformance gate
// to be meaningful.
func conformanceLoginDefinition() extension.LoginDefinition {
	return extension.LoginDefinition{
		Brand:       slices.Repeat([]string{strings.Repeat("A", 41)}, 5),
		Hero:        slices.Repeat([]string{strings.Repeat("A", 32)}, 14),
		Mascot:      slices.Repeat([]string{strings.Repeat("A", 16)}, 14),
		Palette:     map[string]string{"A": "#123ABC"},
		Name:        "Conformance Pig",
		Description: "Cross-language login fixture",
		Tagline:     "One canonical definition across every SDK",
	}
}

func makeInprocFixture(ui extension.UIContext, actions *[]string) extension.Extension {
	var abortObserved atomic.Bool
	update := func(onUpdate extension.AgentToolUpdateCallback, text string) {
		if cb, ok := onUpdate.(agent.ToolUpdateCallback); ok {
			cb(text, nil)
		}
	}
	return extension.Extension{
		Path:         "inproc-conformance-fixture",
		ResolvedPath: "inproc-conformance-fixture",
		MessageRenderers: map[string]extension.MessageRenderer{
			"conformance-message": func(message extension.CustomMessage, options extension.MessageRenderOptions, _ extension.Theme) extension.Component {
				custom := message.(extension.CustomMessageRef)
				if custom.Content == "padding-options" {
					wire, _ := json.Marshal(options)
					return &conformanceLinesComponent{lines: []string{string(wire)}}
				}
				return &conformanceLinesComponent{lines: []string{fmt.Sprintf("renderer:%v:expanded=%t:width=72", custom.Content, options.Expanded)}}
			},
		},
		EntryRenderers: map[string]extension.EntryRenderer{
			"conformance-entry": func(entry extension.CustomEntry, options extension.EntryRenderOptions, _ extension.Theme) extension.Component {
				data, _ := entry.(map[string]any)
				return &conformanceLinesComponent{lines: []string{fmt.Sprintf("entryrenderer:%v:expanded=%t:width=72", data["data"], options.Expanded)}}
			},
		},
		Tools: map[string]extension.RegisteredTool{
			"render_probe": {
				Definition: extension.ToolDefinition{
					Name:        "render_probe",
					Description: "Render its own tool card",
					Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
					RenderShell: extension.ToolRenderShellSelf,
					Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
						return agent.AgentToolResult{Content: "render ok"}, nil
					},
					RenderCall: func(args json.RawMessage, _ extension.Theme, render extension.ToolRenderContext) extension.Component {
						state := render.State.(map[string]any)
						calls, _ := state["calls"].(int)
						state["calls"] = calls + 1
						var input map[string]any
						_ = json.Unmarshal(args, &input)
						return &conformanceWidthComponent{line: func(width int) string {
							return fmt.Sprintf("toolrender:call:%v:partial=%t:calls=%d:width=%d", input["topic"], render.IsPartial, calls+1, width)
						}}
					},
					RenderResult: func(result extension.AgentToolResult, options extension.ToolRenderResultOptions, _ extension.Theme, render extension.ToolRenderContext) extension.Component {
						value := result.(agent.AgentToolResult)
						details, _ := value.Details.(map[string]any)
						calls := render.State.(map[string]any)["calls"]
						return &conformanceWidthComponent{line: func(width int) string {
							return fmt.Sprintf("toolrender:result:%v:%v:expanded=%t:calls=%v:width=%d", value.Content, details["k"], options.Expanded, calls, width)
						}}
					},
				},
				SourceInfo: inprocFixtureName,
			},
			"update_tool": {
				Definition: extension.ToolDefinition{
					Name:        "update_tool",
					Description: "Stream two partial results",
					Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
					Execute: func(_ context.Context, _ string, _ json.RawMessage, onUpdate extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
						update(onUpdate, "step 1")
						update(onUpdate, "step 2")
						return agent.AgentToolResult{Content: "done"}, nil
					},
				},
				SourceInfo: inprocFixtureName,
			},
			"abort_tool": {
				Definition: extension.ToolDefinition{
					Name:        "abort_tool",
					Description: "Wait for the abort signal",
					Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
					Execute: func(ctx context.Context, _ string, _ json.RawMessage, onUpdate extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
						update(onUpdate, "waiting")
						<-ctx.Done()
						abortObserved.Store(true)
						return agent.AgentToolResult{Content: "aborted"}, nil
					},
				},
				SourceInfo: inprocFixtureName,
			},
			"rich_tool": {
				Definition: extension.ToolDefinition{
					Name:        "rich_tool",
					Description: "Return text, image, and terminate",
					Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
					Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
						return agent.AgentToolResult{
							Content:   "  padded  \ntail\n",
							Images:    []ai.ImageContent{{Data: "aW1n", MimeType: "image/png"}},
							Terminate: true,
						}, nil
					},
				},
				SourceInfo: inprocFixtureName,
			},
			"echo": {
				Definition: extension.ToolDefinition{
					Name:        "echo",
					Description: "Echo back the input",
					Parameters: json.RawMessage(
						`{"type":"object","required":["text"],` +
							`"properties":{"text":{"type":"string","description":"Text to echo"}}}`,
					),
					Execute: func(
						_ context.Context,
						_ string,
						params json.RawMessage,
						_ extension.AgentToolUpdateCallback,
					) (extension.AgentToolResult, error) {
						var input struct {
							Text string `json:"text"`
						}
						_ = json.Unmarshal(params, &input)
						return agent.AgentToolResult{
							Content: "echo: " + input.Text,
						}, nil
					},
				},
				SourceInfo: inprocFixtureName,
			},
			"prepared_tool": {
				Definition: extension.ToolDefinition{
					Name:        "prepared_tool",
					Description: "Transform legacy arguments before execution",
					Parameters:  json.RawMessage(`{"type":"object","required":["text"],"properties":{"text":{"type":"string"}}}`),
					PrepareArguments: func(raw json.RawMessage) (json.RawMessage, error) {
						var input map[string]any
						if err := json.Unmarshal(raw, &input); err != nil {
							return nil, err
						}
						return json.Marshal(map[string]any{"text": input["legacy"]})
					},
					Execute: func(_ context.Context, _ string, raw json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
						var input struct {
							Text string `json:"text"`
						}
						if err := json.Unmarshal(raw, &input); err != nil {
							return nil, err
						}
						return agent.AgentToolResult{Content: "prepared:" + input.Text}, nil
					},
				},
				SourceInfo: inprocFixtureName,
			},
			"tool_error": {
				Definition: extension.ToolDefinition{
					Name:        "tool_error",
					Description: "Return a thrown tool error",
					Parameters:  json.RawMessage(`{"type":"object"}`),
					Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
						return nil, errors.New("tool exploded")
					},
				},
				SourceInfo: inprocFixtureName,
			},
			"tool_is_error": {
				Definition: extension.ToolDefinition{
					Name:        "tool_is_error",
					Description: "Return a structured tool error result",
					Parameters:  json.RawMessage(`{"type":"object"}`),
					Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
						return agent.AgentToolResult{Content: "soft tool error", IsError: true}, nil
					},
				},
				SourceInfo: inprocFixtureName,
			},
			"guided_tool": {
				Definition: extension.ToolDefinition{
					Name:             "guided_tool",
					Description:      "Tool with prompt guidelines",
					Parameters:       json.RawMessage(`{"type":"object"}`),
					PromptGuidelines: []string{"Use guided_tool when the user asks for guided behavior."},
					Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
						return agent.AgentToolResult{Content: "guided"}, nil
					},
				},
				SourceInfo: inprocFixtureName,
			},
			"sourced_tool": {
				Definition: extension.ToolDefinition{
					Name:             "sourced_tool",
					Description:      "Tool with explicit source",
					Parameters:       json.RawMessage(`{"type":"object"}`),
					PromptGuidelines: []string{"Use sourced_tool to test per-tool source attribution."},
					Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
						return agent.AgentToolResult{Content: "sourced"}, nil
					},
				},
				SourceInfo: "mcp:test-server",
			},
			"grammar_tool": {
				Definition: extension.ToolDefinition{
					Name:        "grammar_tool",
					Description: "Tool with a grammar constrained sampling request",
					Parameters:  json.RawMessage(`{"type":"object"}`),
					ConstrainedSampling: json.RawMessage(
						`{"type":"grammar","variants":{"openai_lark":"start: NUMBER"}}`),
					Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
						return agent.AgentToolResult{Content: "grammar"}, nil
					},
				},
				SourceInfo: inprocFixtureName,
			},
		},
		Commands: map[string]extension.RegisteredCommand{
			"abort_probe": {
				Name:        "abort_probe",
				Description: "Report whether abort_tool saw its abort signal",
				Handler: func(context.Context, string) error {
					ui.Notify(fmt.Sprintf("abort:%t", abortObserved.Load()), "info")
					return nil
				},
			},
			"ping": {
				Name:        "ping",
				Description: "Respond with pong",
				Handler: func(_ context.Context, _ string) error {
					ui.Notify("pong", "info")
					return nil
				},
			},
			"liveness_host_call": {
				Name: "liveness_host_call", Description: "Exercise an awaited host call",
				Handler: func(context.Context, string) error { return nil },
			},
			"liveness_user_call": {
				Name: "liveness_user_call", Description: "Exercise an interactive host call",
				Handler: func(context.Context, string) error { return nil },
			},
			"liveness_fire_call": {
				Name: "liveness_fire_call", Description: "Exercise a no-result UI host call",
				Handler: func(context.Context, string) error { return nil },
			},
			"command_error": {
				Name:        "command_error",
				Description: "Return a command error",
				Handler: func(context.Context, string) error {
					return errors.New("command exploded")
				},
			},
			"command_awaited_error": {
				Name:        "command_awaited_error",
				Description: "Return an error after awaited work",
				Handler: func(context.Context, string) error {
					time.Sleep(150 * time.Millisecond)
					return errors.New("awaited command exploded")
				},
			},
			"status": {
				Name:        "status",
				Description: "Set a status entry",
				Handler: func(_ context.Context, _ string) error {
					ui.SetStatus("conformance", "ok")
					return nil
				},
			},
			"status_burst": {
				Name:        "status_burst",
				Description: "Set one status repeatedly without awaiting",
				Handler: func(_ context.Context, _ string) error {
					for i := range statusBurstCount {
						ui.SetStatus("burst", strconv.Itoa(i))
					}
					return nil
				},
			},
			"send_message": {
				Name:        "send_message",
				Description: "Send a custom message",
				Handler: func(context.Context, string) error {
					*actions = append(*actions, "sendMessage:notice:hello-custom:steer:true")
					return nil
				},
			},
			"send_message_default": {
				Name:        "send_message_default",
				Description: "Send a custom message with default options",
				Handler: func(context.Context, string) error {
					*actions = append(*actions, "sendMessage:notice:default:unset:unset")
					return nil
				},
			},
			"send_message_no_turn": {
				Name:        "send_message_no_turn",
				Description: "Send a custom message that never starts a turn",
				Handler: func(context.Context, string) error {
					*actions = append(*actions, "sendMessage:notice:no-turn:unset:false")
					return nil
				},
			},
			"send_user_message": {
				Name:        "send_user_message",
				Description: "Send a user message",
				Handler: func(_ context.Context, args string) error {
					var content any = "hello-user"
					if args != "" {
						if err := json.Unmarshal([]byte(args), &content); err != nil {
							return err
						}
					}
					return recordUserContent(actions, content, "followUp")
				},
			},
			"set_session_name": {
				Name:        "set_session_name",
				Description: "Set the session name",
				Handler: func(context.Context, string) error {
					*actions = append(*actions, "setSessionName:conformance-session")
					return nil
				},
			},
			"append_entry": {
				Name:        "append_entry",
				Description: "Append a custom entry",
				Handler: func(context.Context, string) error {
					*actions = append(*actions, "appendEntry:conformance-entry:hello-entry")
					return nil
				},
			},
			"login-probe": {
				Name:        "login-probe",
				Description: "Exercise semantic login submission and host errors",
				Handler: func(context.Context, string) error {
					definition := conformanceLoginDefinition()
					if err := ui.SetLogin(definition); err != nil {
						return err
					}
					definition.Brand[0] = definition.Brand[0][:40]
					err := ui.SetLogin(definition)
					if err == nil {
						return errors.New("invalid login definition was accepted")
					}
					ui.Notify(err.Error(), "error")
					return nil
				},
			},
			"context-probe": {
				Name:        "context-probe",
				Description: "Report ctx.mode + ctx.getSystemPromptOptions()",
				Handler: func(ctx context.Context, _ string) error {
					mode := ""
					trusted := true
					if c := extension.FromContext(ctx); c != nil {
						if m, err := c.Mode(); err == nil {
							mode = string(m)
						}
						if tr, err := c.IsProjectTrusted(); err == nil {
							trusted = tr
						}
					}
					var opts extension.BuildSystemPromptOptions
					if cc := extension.CommandContextFromContext(ctx); cc != nil {
						opts, _ = cc.GetSystemPromptOptions()
					}
					ui.Notify(fmt.Sprintf("mode=%s trusted=%t spo_prompt=%s spo_cwd=%s spo_tools=%s",
						mode, trusted, opts.CustomPrompt, opts.Cwd, strings.Join(opts.SelectedTools, ",")), "info")
					return nil
				},
			},
			"session-log-probe": {
				Name:        "session-log-probe",
				Description: "Read a paged session log",
				Handler: func(context.Context, string) error {
					ui.Notify("session entries=3 branch=3", "info")
					return nil
				},
			},
			"dialog-probe": {
				Name:        "dialog-probe",
				Description: "Exercise interactive dialog responses",
				Handler: func(ctx context.Context, _ string) error {
					dialogs := commandUI(ctx, ui)
					selected, selectErr := dialogs.Select(ctx, "Pick", []string{"first", "second"}, nil)
					input, inputErr := dialogs.Input(ctx, "Input", "placeholder", nil)
					edited, editorErr := dialogs.Editor(ctx, "Editor", "prefill")
					confirmed, confirmErr := dialogs.Confirm(ctx, "Confirm", "message", nil)
					if selectErr != nil || inputErr != nil || editorErr != nil || confirmErr != nil {
						return errors.Join(selectErr, inputErr, editorErr, confirmErr)
					}
					ui.Notify(fmt.Sprintf("select=%s input=%s editor=%s confirm=%t",
						selected, input, edited, confirmed), "info")
					return nil
				},
			},
			"focused-probe": {
				Name:        "focused-probe",
				Description: "Exercise focused subprocess UI",
				Handler: func(ctx context.Context, _ string) error {
					selected, err := commandUI(ctx, ui).Custom(ctx, nil, nil)
					if err != nil {
						return err
					}
					ui.Notify(fmt.Sprintf("focused=%v disposed=true", selected), "info")
					return nil
				},
			},
		},
		Handlers: map[string][]extension.HandlerFn{
			"tool_execution_update": {
				func(args ...any) (any, error) {
					event := args[0].(extension.ToolExecutionUpdateEvent)
					structured, _ := event.Args.(map[string]any)
					nested, _ := structured["nested"].(map[string]any)
					partial, _ := event.PartialResult.(map[string]any)
					details, _ := partial["details"].(map[string]any)
					ui.Notify(fmt.Sprintf("tool-update=%s:%v:%v:%v:%v", event.ToolName, structured["path"], nested["depth"], partial["content"], details["progress"]), "info")
					return nil, nil
				},
			},
			"tool_execution_end": {
				func(args ...any) (any, error) {
					event := args[0].(extension.ToolExecutionEndEvent)
					result, _ := event.Result.(map[string]any)
					content, _ := result["content"].([]any)
					var text, image map[string]any
					if len(content) > 0 {
						text, _ = content[0].(map[string]any)
					}
					if len(content) > 1 {
						image, _ = content[1].(map[string]any)
					}
					details, _ := result["details"].(map[string]any)
					nested, _ := details["nested"].(map[string]any)
					ui.Notify(fmt.Sprintf("tool-end=%s:%v:%d:%v:%v:%v:%t", event.ToolName, text["text"], len(content), image["data"], image["mimeType"], nested["value"], event.IsError), "info")
					return nil, nil
				},
			},
			// Typed tool events: the in-process reference narrows the upstream
			// PowerShell and bash variants with a type switch.
			"tool_call": {
				func(args ...any) (any, error) {
					var name string
					var input map[string]any
					switch event := args[0].(type) {
					case extension.PowerShellToolCallEvent:
						name, input = event.ToolName, asMap(event.Input)
					case extension.BashToolCallEvent:
						name, input = event.ToolName, asMap(event.Input)
					default:
						return nil, nil
					}
					ui.Notify(fmt.Sprintf("tool-call=%s:%v:%v", name, input["command"], input["timeout"]), "info")
					if input["command"] == "blocked-command" {
						return &extension.ToolCallEventResult{Block: true, Reason: "blocked " + name}, nil
					}
					return nil, nil
				},
			},
			"tool_result": {
				func(args ...any) (any, error) {
					var name string
					var content []any
					var details *extension.BashToolDetails
					switch event := args[0].(type) {
					case extension.PowerShellToolResultEvent:
						name, content, details = event.ToolName, event.Content, event.Details
					case extension.BashToolResultEvent:
						name, content, details = event.ToolName, event.Content, event.Details
					default:
						return nil, nil
					}
					var path any
					var lines any
					if details != nil {
						path = details.FullOutputPath
						if details.Truncation != nil {
							lines = details.Truncation.TotalLines
						}
					}
					var text any
					if len(content) > 0 {
						text = asMap(content[0])["text"]
					}
					ui.Notify(fmt.Sprintf("tool-result=%s:%v:%v:%v", name, path, lines, text), "info")
					return &extension.ToolResultEventResult{Content: []any{map[string]any{"type": "text", "text": name + " redacted"}}}, nil
				},
			},
			"project_trust": {
				func(...any) (any, error) {
					return nil, errors.New("trust-boom")
				},
				func(...any) (any, error) {
					return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustUndecided}, nil
				},
				func(...any) (any, error) {
					return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustYes, Remember: new(true)}, nil
				},
			},
			"ui_prompt_start": {
				func(args ...any) (any, error) {
					event := args[0].(extension.UIPromptStartEvent)
					ui.Notify(inprocPromptNotification(event.Type, event.Reason, event.Kind, event.Title), "info")
					return nil, nil
				},
			},
			"ui_prompt_end": {
				func(args ...any) (any, error) {
					event := args[0].(extension.UIPromptEndEvent)
					ui.Notify(inprocPromptNotification(event.Type, event.Reason, event.Kind, event.Title), "info")
					return nil, nil
				},
			},
			"agent_before_settle": {
				func(args ...any) (any, error) {
					event := args[0].(*extension.AgentBeforeSettleEvent)
					ui.Notify(fmt.Sprintf("agent_before_settle:%s:%d:%t:%d:%t", event.Outcome, len(event.Entries), event.Continue, len(event.Context.ContextEntries), event.Context.CanContinue), "info")
					event.Entries = append(event.Entries, extension.SessionBoundaryDraft{Type: "custom", CustomType: "kept"})
					return nil, nil
				},
				func(args ...any) (any, error) {
					event := args[0].(*extension.AgentBeforeSettleEvent)
					event.Entries = append(event.Entries, extension.SessionBoundaryDraft{Type: "custom", CustomType: "before-error"})
					return nil, errors.New("boundary failed")
				},
				func(args ...any) (any, error) {
					event := args[0].(*extension.AgentBeforeSettleEvent)
					if len(event.Entries) != 2 || len(event.Context.ContextEntries) != 2 || event.Entries[0].CustomType != "kept" || event.Entries[1].CustomType != "before-error" {
						return nil, errors.New("lost boundary mutation or preview")
					}
					entries := []extension.SessionBoundaryDraft{{Type: "custom", CustomType: "conformance-boundary"}}
					continued := true
					return extension.BoundaryResult{Entries: &entries, Continue: &continued}, nil
				},
			},
			"session_start": {
				func(args ...any) (any, error) {
					if event, ok := args[0].(extension.SessionStartEvent); ok {
						ui.Notify("session_start:"+event.Reason, "info")
					}
					return nil, nil
				},
			},
			"session_shutdown": {
				func(args ...any) (any, error) {
					if event, ok := args[0].(extension.SessionShutdownEvent); ok {
						ui.Notify("session_shutdown:"+event.Reason, "info")
					}
					return nil, nil
				},
			},
			"session_info_changed": {
				func(args ...any) (any, error) {
					if event, ok := args[0].(extension.SessionInfoChangedEvent); ok {
						ui.Notify("session_info_changed:"+event.Name, "info")
					}
					return nil, nil
				},
			},
			"session_before_compact": {
				func(args ...any) (any, error) {
					if event, ok := args[0].(extension.SessionBeforeCompactEvent); ok {
						ui.Notify(fmt.Sprintf("session_before_compact:%s:%t", event.Reason, event.WillRetry), "info")
					}
					return nil, nil
				},
			},
			"session_compact": {
				func(args ...any) (any, error) {
					if event, ok := args[0].(extension.SessionCompactEvent); ok {
						ui.Notify(fmt.Sprintf("session_compact:%s:%t:%t", event.Reason, event.WillRetry, event.FromExtension), "info")
					}
					return nil, nil
				},
			},
			"session_compact_failed": {
				func(args ...any) (any, error) {
					if event, ok := args[0].(extension.SessionCompactFailedEvent); ok {
						ui.Notify(fmt.Sprintf("session_compact_failed:%s:%s:%t:%t:%t", event.Reason, event.ErrorMessage, event.Aborted, event.WillRetry, event.FromExtension), "info")
					}
					return nil, nil
				},
			},
			"turn_end": {
				func(args ...any) (any, error) {
					if event, ok := args[0].(extension.TurnEndEvent); ok {
						ui.Notify(fmt.Sprintf("turn_end:%s:%s", event.MessageEntryID, event.ToolResultEntryIds[0]), "info")
					}
					return nil, nil
				},
			},
		},
	}
}

// commandUI returns the UI surface the runner bound to a command context, so
// in-process dialogs pass through the runner's ui_prompt scope exactly as an
// upstream extension's ctx.ui does. The fixture's own surface is the fallback.
func commandUI(ctx context.Context, fallback extension.UIContext) extension.UIContext {
	if c := extension.FromContext(ctx); c != nil {
		if ui, err := c.UI(); err == nil {
			return ui
		}
	}
	return fallback
}

// inprocPromptNotification renders a ui_prompt event as every SDK fixture
// does. An empty title is absent on the wire.
func inprocPromptNotification(eventType, reason string, kind extension.UIPromptKind, title string) string {
	if title == "" {
		title = "(none)"
	}
	return fmt.Sprintf("ui_prompt:%s:%s:%s:%s", eventType, reason, kind, title)
}

// uiPromptPrefix marks the fixtures' ui_prompt_start/ui_prompt_end reports.
const uiPromptPrefix = "ui_prompt:"

// splitPromptNotifications separates the asynchronous ui_prompt reports from
// the notifications a command sends itself.
func splitPromptNotifications(records []string) (prompts, others []string) {
	for _, record := range records {
		if strings.HasPrefix(record, uiPromptPrefix) {
			prompts = append(prompts, record)
		} else {
			others = append(others, record)
		}
	}
	return prompts, others
}

// wantUIPromptEvents is the upstream-derived sequence for dialog-probe then
// focused-probe: one start/end pair per outermost prompt, title only where
// the prompt has one, and no title for custom (runner.ts wrapUIPromptContext).
var wantUIPromptEvents = []string{
	"ui_prompt:ui_prompt_start:ui_prompt:select:Pick:info", "ui_prompt:ui_prompt_end:ui_prompt:select:Pick:info",
	"ui_prompt:ui_prompt_start:ui_prompt:input:Input:info", "ui_prompt:ui_prompt_end:ui_prompt:input:Input:info",
	"ui_prompt:ui_prompt_start:ui_prompt:editor:Editor:info", "ui_prompt:ui_prompt_end:ui_prompt:editor:Editor:info",
	"ui_prompt:ui_prompt_start:ui_prompt:confirm:Confirm:info", "ui_prompt:ui_prompt_end:ui_prompt:confirm:Confirm:info",
	"ui_prompt:ui_prompt_start:ui_prompt:custom:(none):info", "ui_prompt:ui_prompt_end:ui_prompt:custom:(none):info",
}

// recordingUI is a UIContext implementation that captures Notify calls into
// a shared slice. Every other method is a typed no-op; the conformance scope
// only depends on Notify.
type recordingUI struct {
	notify *[]string
	status *[]string
	logins []string

	// terminalInput captures the raw-input handler an extension registers, so
	// the conformance gate can feed keystrokes through each transport.
	terminalInputMu sync.Mutex
	terminalInput   extension.RemoteTerminalInputHandler

	// recordMu guards notify and status. The host calls Notify from the
	// goroutine servicing the extension socket while the test body reads the
	// slices, so the append and the read are on different goroutines.
	recordMu sync.Mutex
}

func newRecordingUI(notify, status *[]string) *recordingUI {
	return &recordingUI{notify: notify, status: status}
}

func (u *recordingUI) Select(context.Context, string, []string, extension.ExtensionUIDialogOptions) (string, error) {
	return "second", nil
}

func (u *recordingUI) Confirm(context.Context, string, string, extension.ExtensionUIDialogOptions) (bool, error) {
	return true, nil
}

func (u *recordingUI) Input(context.Context, string, string, extension.ExtensionUIDialogOptions) (string, error) {
	return "typed", nil
}

func (u *recordingUI) Notify(message, level string) {
	u.recordMu.Lock()
	defer u.recordMu.Unlock()
	*u.notify = append(*u.notify, message+":"+level)
}

// Recorded returns a copy of the messages seen so far. Tests must read through
// this rather than dereferencing the slice, which races with Notify.
func (u *recordingUI) Recorded() []string {
	u.recordMu.Lock()
	defer u.recordMu.Unlock()
	return append([]string(nil), (*u.notify)...)
}

func (u *recordingUI) ClearRecorded() {
	u.recordMu.Lock()
	*u.notify = nil
	u.recordMu.Unlock()
}

// RecordNotify lets a test's own bridge callback share the recorder's lock, so
// a bridge-level notify hook does not reintroduce the race Notify avoids.
func (u *recordingUI) RecordNotify(message, level string) {
	u.Notify(message, level)
}

func (u *recordingUI) OnTerminalInput(handler extension.TerminalInputHandler) func() {
	return u.OnRemoteTerminalInput("", func(_ context.Context, data string) extension.TerminalInputResult {
		return handler(data)
	})
}

// OnRemoteTerminalInput is the registration every subprocess transport uses.
func (u *recordingUI) OnRemoteTerminalInput(_ string, handler extension.RemoteTerminalInputHandler) func() {
	u.terminalInputMu.Lock()
	u.terminalInput = handler
	u.terminalInputMu.Unlock()
	return func() {
		u.terminalInputMu.Lock()
		u.terminalInput = nil
		u.terminalInputMu.Unlock()
	}
}

// sendTerminalInput feeds one chunk to the registered handler, reporting
// whether an extension consumed it. ok is false when nothing is subscribed.
func (u *recordingUI) sendTerminalInput(data string) (consumed, ok bool) {
	result, ok := u.terminalInputVerdict(data)
	return result.Consume, ok
}

// terminalInputVerdict feeds one chunk to the registered handler and returns
// the extension's full verdict.
func (u *recordingUI) terminalInputVerdict(data string) (extension.TerminalInputResult, bool) {
	u.terminalInputMu.Lock()
	handler := u.terminalInput
	u.terminalInputMu.Unlock()
	if handler == nil {
		return extension.TerminalInputResult{}, false
	}
	return handler(context.Background(), data), true
}
func (u *recordingUI) SetStatus(key, text string) {
	u.recordMu.Lock()
	defer u.recordMu.Unlock()
	*u.status = append(*u.status, key+":"+text)
}

// Statuses returns a copy of the status updates seen so far.
func (u *recordingUI) Statuses() []string {
	u.recordMu.Lock()
	defer u.recordMu.Unlock()
	return append([]string(nil), (*u.status)...)
}

// ClearStatuses forgets the status updates seen so far.
func (u *recordingUI) ClearStatuses() {
	u.recordMu.Lock()
	defer u.recordMu.Unlock()
	*u.status = nil
}
func (u *recordingUI) SetWorkingMessage(string)                              {}
func (u *recordingUI) SetWorkingVisible(bool)                                {}
func (u *recordingUI) SetWorkingIndicator(extension.WorkingIndicatorOptions) {}
func (u *recordingUI) SetHiddenThinkingLabel(string)                         {}
func (u *recordingUI) SetWidget(string, any, extension.ExtensionWidgetOptions) {
}
func (u *recordingUI) SetFooter(any) {}
func (u *recordingUI) SetHeader(any) {}
func (u *recordingUI) SetLogin(definition extension.LoginDefinition) error {
	if _, err := extension.ValidateLoginDefinition(definition); err != nil {
		return fmt.Errorf("invalid_login: %w", err)
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		return err
	}
	u.recordMu.Lock()
	u.logins = append(u.logins, string(encoded))
	u.recordMu.Unlock()
	return nil
}
func (u *recordingUI) LoginDefinitions() []string {
	u.recordMu.Lock()
	defer u.recordMu.Unlock()
	return append([]string(nil), u.logins...)
}
func (u *recordingUI) SetTitle(string)                               {}
func (u *recordingUI) Custom(context.Context, any, any) (any, error) { return "gamma", nil }
func (u *recordingUI) PasteToEditor(string)                          {}
func (u *recordingUI) SetEditorText(string)                          {}
func (u *recordingUI) GetEditorText() string                         { return "" }
func (u *recordingUI) Editor(context.Context, string, string) (string, error) {
	return "edited", nil
}
func (u *recordingUI) AddAutocompleteProvider(extension.AutocompleteProviderFactory) {}
func (u *recordingUI) SetEditorComponent(any)                                        {}
func (u *recordingUI) GetEditorComponent() any                                       { return nil }
func (u *recordingUI) Theme() extension.Theme                                        { return nil }
func (u *recordingUI) GetAllThemes() []extension.ThemeMeta                           { return nil }
func (u *recordingUI) GetTheme(string) (extension.Theme, error)                      { return nil, nil }
func (u *recordingUI) SetTheme(any) extension.SetThemeResult {
	return extension.SetThemeResult{Success: true}
}
func (u *recordingUI) GetToolsExpanded() bool { return false }
func (u *recordingUI) SetToolsExpanded(bool)  {}
func (u *recordingUI) RunRemoteOverlay(opts extension.RemoteOverlayOptions, host extension.RemoteOverlayHost, onHandle func(extension.RemoteOverlayHandle)) (any, bool) {
	handle := &conformanceOverlayHandle{closed: make(chan struct{}), changed: make(chan struct{}, 1)}
	onHandle(handle)
	if opts.Title == "Timer" {
		deadline := time.After(5 * time.Second)
		for {
			lines := handle.Lines()
			if len(lines) > 0 && strings.HasPrefix(lines[0], "timer frame=") &&
				!strings.Contains(lines[0], "timer frame=0 ") && !strings.Contains(lines[0], "timer frame=1 ") {
				break
			}
			select {
			case <-handle.changed:
			case <-deadline:
				return nil, false
			}
		}
		host.OnInput("\r")
	} else {
		host.OnInput("\x1b[6~")
		host.OnInput("\r")
	}
	select {
	case <-handle.closed:
		return handle.result, true
	case <-time.After(5 * time.Second):
		return nil, false
	}
}

type conformanceOverlayHandle struct {
	mu      sync.Mutex
	result  any
	lines   []string
	closed  chan struct{}
	changed chan struct{}
}

func (h *conformanceOverlayHandle) UpdateLines(lines []string) {
	h.mu.Lock()
	h.lines = append([]string(nil), lines...)
	h.mu.Unlock()
	select {
	case h.changed <- struct{}{}:
	default:
	}
}

func (h *conformanceOverlayHandle) Lines() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.lines...)
}

func (h *conformanceOverlayHandle) Close(result any) {
	h.mu.Lock()
	h.result = result
	select {
	case <-h.closed:
	default:
		close(h.closed)
	}
	h.mu.Unlock()
}

// ── subprocess-go transport ──────────────────────────────────────────────────

func makeSubprocessGoHarness(t *testing.T) *harness {
	t.Helper()
	shortSockDir(t)

	binPath := buildSDKFixture(t)

	notify := &[]string{}
	status := &[]string{}
	actions := &[]string{}
	ui := newRecordingUI(notify, status)
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	bridge.SetNotifyFunc(ui.RecordNotify)
	bridge.SetActions(conformanceActions(actions))

	h := subprocess.NewHost(t.TempDir())
	h.SetMode(conformanceMode)
	h.SetUIBridge(bridge)

	// IMPORTANT: do NOT bind subprocess lifetime to a function-scoped
	// context. Host.Load passes ctx into exec.CommandContext via a
	// derived context.WithCancel; cancelling the load context kills the
	// extension binary. The conformance test reuses the loaded
	// extension across captureRecording, so the load context must
	// outlive the helper function. Lifetime is bound to host.Shutdown
	// via t.Cleanup in the caller.
	ext, err := h.Load(context.Background(), subprocess.ExtConfig{
		Name:    "sdk-fixture",
		Path:    binPath,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("subprocess Load: %v", err)
	}

	runner := inproc.NewRunner([]extension.Extension{*ext}, t.TempDir())
	bridge.SetUIPromptScope(runner)
	return &harness{runner: runner, host: h, notify: notify, status: status, actions: actions, ui: ui, bridge: bridge}
}

func makeFusedGoHarness(t *testing.T) *harness {
	t.Helper()

	notify := &[]string{}
	status := &[]string{}
	actions := &[]string{}
	ui := newRecordingUI(notify, status)
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	bridge.SetNotifyFunc(ui.RecordNotify)
	bridge.SetActions(conformanceActions(actions))

	host := subprocess.NewHost(t.TempDir())
	host.SetMode(conformanceMode)
	host.SetUIBridge(bridge)
	ext := testfixture.Extension()
	loaded, err := host.LoadInProcess(context.Background(), subprocess.ExtConfig{
		Name:    "sdk-fixture",
		Enabled: true,
	}, ext.RunWithConn)
	if err != nil {
		host.Shutdown("load failed")
		t.Fatalf("fused LoadInProcess: %v", err)
	}

	runner := inproc.NewRunner([]extension.Extension{*loaded}, t.TempDir())
	bridge.SetUIPromptScope(runner)
	return &harness{runner: runner, host: host, notify: notify, status: status, actions: actions, ui: ui, bridge: bridge}
}

func conformanceModel(id, name string) map[string]any {
	return map[string]any{
		"provider": "conformance", "id": id, "modelId": id, "api": "openai-responses", "name": name,
		"baseUrl": "https://models.invalid/v1", "reasoning": false, "input": []string{},
		"inputLimits":      map[string]any{"maxRequestBytes": 12345.0, "images": map[string]any{"maxPerMessage": 7.0, "maxPerRequest": 11.0, "resize": map[string]any{"maxWidth": 321.0, "maxHeight": 123.0, "maxBytes": 45678.0, "jpegQuality": 67.0}}},
		"cost":             map[string]any{"input": 0.0, "output": 7.0, "cacheRead": 0.5, "cacheWrite": 1.5, "tiers": []map[string]any{{"inputTokensAbove": 1000.0, "input": 1.0, "output": 2.0, "cacheRead": 0.0, "cacheWrite": 0.0}}},
		"thinkingLevelMap": map[string]any{"high": "configured-high"}, "promptCache": map[string]any{"short": 120.0},
		"contextWindow": 321000.0, "maxTokens": 1234.0, "samplingParams": map[string]any{"temperature": 0.0},
		"headers": map[string]any{"X-Test": "value"}, "compat": map[string]any{"supportsStrictMode": false},
	}
}

func conformanceActions(actions *[]string) *subprocess.HostCallbacks {
	return &subprocess.HostCallbacks{
		GetSystemPromptOptions: conformanceSystemPromptOptions,
		IsProjectTrusted:       func() bool { return conformanceProjectTrusted },
		GetModelInfo: func() map[string]any {
			return conformanceModel("current", "Current")
		},
		GetModel: func(providerID, modelID string) map[string]any {
			if providerID != "conformance" || (modelID != "current" && modelID != "declared" && modelID != "org/model/name") {
				return nil
			}
			return conformanceModel(modelID, strings.ToUpper(modelID[:1])+modelID[1:])
		},
		GetModels: func() []map[string]any {
			return []map[string]any{
				conformanceModel("current", "Current"),
				conformanceModel("declared", "Declared"),
				conformanceModel("org/model/name", "Slash model"),
			}
		},
		GetModelAuth: func(ctx context.Context, providerID, modelID string) map[string]any {
			extension.CallInitiated(ctx)
			if providerID != "conformance" || modelID != "declared" {
				return map[string]any{"ok": false, "error": "model auth not found"}
			}
			return map[string]any{
				"ok": true, "apiKey": "conformance-key", "headers": map[string]any{"X-Conformance-Auth": "yes"},
				"baseUrl": "https://models.invalid/v1", "env": map[string]any{"CONFORMANCE_AUTH": "yes"},
			}
		},
		GetSessionID:   func() string { return "conformance-session-id" },
		GetSessionFile: func() string { return "" },
		GetLeafID:      func() string { return "e3" },
		GetEntriesPage: func(cursor, _ int) ([]json.RawMessage, int, bool, string) {
			entries := []json.RawMessage{
				json.RawMessage(`{"id":"e1","type":"message","message":{"role":"user"}}`),
				json.RawMessage(`{"id":"e2","parentId":"e1","type":"message","message":{"role":"assistant"}}`),
				json.RawMessage(`{"id":"e3","parentId":"e2","type":"message","message":{"role":"user"}}`),
			}
			if cursor < 0 || cursor > len(entries) {
				cursor = 0
			}
			if cursor == len(entries) {
				return nil, cursor, false, "e3"
			}
			next := cursor + 1
			return entries[cursor:next], next, next < len(entries), "e3"
		},
		SendMessage: func(msg extension.CustomMessageRef, opts subprocess.SendMessageOptions) error {
			triggerTurn := "unset"
			if opts.TriggerTurn != nil {
				triggerTurn = strconv.FormatBool(*opts.TriggerTurn)
			}
			deliverAs := opts.DeliverAs
			if deliverAs == "" {
				deliverAs = "unset"
			}
			*actions = append(*actions, fmt.Sprintf("sendMessage:%s:%v:%s:%s", msg.CustomType, msg.Content, deliverAs, triggerTurn))
			return nil
		},
		SendUserMessage: func(content any, opts subprocess.SendUserMessageOptions) error {
			return recordUserContent(actions, content, opts.DeliverAs)
		},
		SetSessionName: func(name string) error {
			*actions = append(*actions, "setSessionName:"+name)
			return nil
		},
		AppendEntry: func(customType string, data any) error {
			*actions = append(*actions, fmt.Sprintf("appendEntry:%s:%v", customType, data))
			return nil
		},
	}
}

func makeSubprocessNodeHarness(t *testing.T) *harness {
	t.Helper()
	shortSockDir(t)
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for SDK conformance: %v", err)
	}

	modRoot := findModuleRoot(t)
	path := filepath.Join(modRoot, "tests", "extension-conformance", "testdata", "node-sdk-fixture", "main.mjs")
	notify := &[]string{}
	status := &[]string{}
	actions := &[]string{}
	ui := newRecordingUI(notify, status)
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	bridge.SetNotifyFunc(ui.RecordNotify)
	bridge.SetActions(conformanceActions(actions))

	h := subprocess.NewHost(t.TempDir())
	h.SetMode(conformanceMode)
	h.SetUIBridge(bridge)

	ext, err := h.Load(context.Background(), subprocess.ExtConfig{
		Name:    "node-sdk-fixture",
		Source:  path,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("node subprocess Load: %v", err)
	}

	runner := inproc.NewRunner([]extension.Extension{*ext}, t.TempDir())
	bridge.SetUIPromptScope(runner)
	return &harness{runner: runner, host: h, notify: notify, status: status, actions: actions, ui: ui, bridge: bridge}
}

// makeSubprocessNodePackedHarness is makeSubprocessNodeHarness's packed-cell
// counterpart: it loads the same node-sdk-fixture through Host.LoadAll
// alongside a second, empty Node factory extension, so the two are classified
// as packable (isPackableNode) and PlanCells packs them into one shared Node
// cell (CellStrategyPackedNode) instead of makeSubprocessNodeHarness's
// isolated Host.Load. The peer extension registers no tools, commands, or
// renderers, so it cannot change captureRecording's output; its only purpose
// is to force real packed-cell hosting so the conformance suite's Node cases
// prove packed and isolated are observationally identical, not just isolated.
func makeSubprocessNodePackedHarness(t *testing.T) *harness {
	t.Helper()
	shortSockDir(t)
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for SDK conformance: %v", err)
	}

	modRoot := findModuleRoot(t)
	path := filepath.Join(modRoot, "tests", "extension-conformance", "testdata", "node-sdk-fixture", "main.mjs")
	peer := filepath.Join(t.TempDir(), "node-cell-peer.mjs")
	if err := os.WriteFile(peer, []byte("export default function () {}\n"), 0o600); err != nil {
		t.Fatalf("write Node cell peer fixture: %v", err)
	}
	notify := &[]string{}
	status := &[]string{}
	actions := &[]string{}
	ui := newRecordingUI(notify, status)
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	bridge.SetNotifyFunc(ui.RecordNotify)
	bridge.SetActions(conformanceActions(actions))

	h := subprocess.NewHost(t.TempDir())
	h.SetMode(conformanceMode)
	h.SetUIBridge(bridge)

	exts, errs := h.LoadAll(context.Background(), []subprocess.ExtConfig{
		{Name: "node-sdk-fixture", Source: path, Enabled: true},
		{Name: "node-cell-peer", Source: peer, Enabled: true},
	})
	if len(errs) != 0 || len(exts) != 2 {
		t.Fatalf("packed Node LoadAll: %d loaded, %v", len(exts), errs)
	}

	runner := inproc.NewRunner(exts, t.TempDir())
	bridge.SetUIPromptScope(runner)
	return &harness{runner: runner, host: h, notify: notify, status: status, actions: actions, ui: ui, bridge: bridge}
}

func makeSubprocessPythonHarness(t *testing.T) *harness {
	t.Helper()
	shortSockDir(t)

	path := buildPythonSDKFixture(t)

	notify := &[]string{}
	status := &[]string{}
	actions := &[]string{}
	ui := newRecordingUI(notify, status)
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	bridge.SetNotifyFunc(ui.RecordNotify)
	bridge.SetActions(conformanceActions(actions))

	h := subprocess.NewHost(t.TempDir())
	h.SetMode(conformanceMode)
	h.SetUIBridge(bridge)

	ext, err := h.Load(context.Background(), subprocess.ExtConfig{
		Name:            "python-sdk-fixture",
		Path:            path,
		Enabled:         true,
		RuntimeLanguage: "python",
	})
	if err != nil {
		t.Fatalf("python subprocess Load: %v", err)
	}

	runner := inproc.NewRunner([]extension.Extension{*ext}, t.TempDir())
	bridge.SetUIPromptScope(runner)
	return &harness{runner: runner, host: h, notify: notify, status: status, actions: actions, ui: ui, bridge: bridge}
}

func makeSubprocessRustHarness(t *testing.T) *harness {
	t.Helper()
	shortSockDir(t)

	binPath := buildRustSDKFixture(t)

	notify := &[]string{}
	status := &[]string{}
	actions := &[]string{}
	ui := newRecordingUI(notify, status)
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	bridge.SetNotifyFunc(ui.RecordNotify)
	bridge.SetActions(conformanceActions(actions))

	h := subprocess.NewHost(t.TempDir())
	h.SetMode(conformanceMode)
	h.SetUIBridge(bridge)

	ext, err := h.Load(context.Background(), subprocess.ExtConfig{
		Name:    "rust-sdk-fixture",
		Path:    binPath,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("rust subprocess Load: %v", err)
	}

	runner := inproc.NewRunner([]extension.Extension{*ext}, t.TempDir())
	bridge.SetUIPromptScope(runner)
	return &harness{runner: runner, host: h, notify: notify, status: status, actions: actions, ui: ui, bridge: bridge}
}

// buildSDKFixture builds the same factory linked by the fused harness, or uses
// its dedicated prebuilt binary from the grouped verifier. Host integration
// tests use a separate fixture and prebuild.
func buildSDKFixture(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("PIG_TEST_CONFORMANCE_SDK_FIXTURE_BIN"); p != "" {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("PIG_TEST_CONFORMANCE_SDK_FIXTURE_BIN=%q does not exist: %v", p, err)
		}
		return p
	}
	modRoot := findModuleRoot(t)
	srcDir := filepath.Join(modRoot, "tests", "extension-conformance", "testfixture", "cmd")
	binPath := filepath.Join(t.TempDir(), testExecutableName("sdk-fixture"))
	// Build has its own generous timeout separate from the test context,
	// because under heavy parallel execution (-p 12 -count=3) the Go
	// build cache is contended and compilation alone can take 30s+.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", binPath, ".")
	cmd.Dir = srcDir
	// The fixture is a root-module package: build it through the repository's
	// go.work so it compiles against the local extensions/sdk. The root go.mod
	// requires the SDK at its release version without a replace, so GOWORK=off
	// would fetch the published SDK instead (or fail before the tag exists).
	// Workspace mode accepts only -mod=readonly or -mod=vendor.
	goflags := strings.Join(slices.DeleteFunc(strings.Fields(os.Getenv("GOFLAGS")), func(f string) bool { return f == "-mod=mod" }), " ")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK="+filepath.Join(modRoot, "go.work"), "GOFLAGS="+goflags)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build sdk-fixture: %v\n%s", err, out)
	}
	return binPath
}

func buildPythonSDKFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath(testPythonExecutable()); err != nil {
		t.Fatalf("%s is required for SDK conformance: %v", testPythonExecutable(), err)
	}
	modRoot := findModuleRoot(t)
	// Python extensions are launched through an explicit interpreter, so the
	// checked-in fixture does not need an executable bit and tests must not
	// mutate the source tree to add one.
	return filepath.Join(modRoot, "tests", "extension-conformance", "testdata", "python-sdk-fixture", "main.py")
}

func buildRustSDKFixture(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("PIG_TEST_RUST_SDK_FIXTURE_BIN"); p != "" {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("PIG_TEST_RUST_SDK_FIXTURE_BIN=%q does not exist: %v", p, err)
		}
		return p
	}
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Fatalf("cargo is required for SDK conformance: %v", err)
	}
	modRoot := findModuleRoot(t)
	srcDir := filepath.Join(modRoot, "tests", "extension-conformance", "testdata", "rust-sdk-fixture")
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cargo", "build", "--release", "--quiet")
	cmd.Dir = srcDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build rust-sdk-fixture: %v\n%s", err, out)
	}
	targetDir := strings.TrimSpace(os.Getenv("CARGO_TARGET_DIR"))
	if targetDir == "" {
		targetDir = filepath.Join(srcDir, "target")
	} else if !filepath.IsAbs(targetDir) {
		targetDir = filepath.Join(srcDir, targetDir)
	}
	binPath := filepath.Join(targetDir, "release", testExecutableName("rust-sdk-fixture"))
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("rust-sdk-fixture binary missing: %v", err)
	}
	return binPath
}

// ── shared plumbing ──────────────────────────────────────────────────────────

func waitFor(t *testing.T, pred func() bool) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	sleep := 10 * time.Millisecond
	for time.Now().Before(deadline) {
		if pred() {
			return
		}
		time.Sleep(sleep)
		sleep = min(sleep*2, 200*time.Millisecond)
	}
	t.Fatal("condition not satisfied before timeout")
}

func toolNames(r *inproc.Runner) []string {
	tools := r.Tools()
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		out = append(out, tool.Definition.Name)
	}
	slices.Sort(out)
	return out
}

func commandNames(r *inproc.Runner) []string {
	commands := r.Commands()
	wanted := map[string]struct{}{"ping": {}, "command_error": {}, "status": {}}
	out := make([]string, 0, len(commands))
	for _, cmd := range commands {
		if _, ok := wanted[cmd.Name]; ok {
			out = append(out, cmd.Name)
		}
	}
	slices.Sort(out)
	return out
}

func findTool(r *inproc.Runner, name string) (extension.RegisteredTool, bool) {
	for _, tool := range r.Tools() {
		if tool.Definition.Name == name {
			return tool, true
		}
	}
	return extension.RegisteredTool{}, false
}

// toolGuidelines collects prompt_guidelines from all registered tools.
// Returns a map of tool name → guidelines (only for tools that have them).
func toolGuidelines(r *inproc.Runner) map[string][]string {
	out := map[string][]string{}
	for _, tool := range r.Tools() {
		if len(tool.Definition.PromptGuidelines) > 0 {
			out[tool.Definition.Name] = tool.Definition.PromptGuidelines
		}
	}
	return out
}

// toolConstrainedSampling gathers each tool's constrained sampling request as
// canonical JSON (map round-trip sorts keys), so every transport is compared on
// the same normalized shape regardless of the SDK's serialization order.
func toolConstrainedSampling(r *inproc.Runner) map[string]string {
	out := map[string]string{}
	for _, tool := range r.Tools() {
		raw := tool.Definition.ConstrainedSampling
		if len(raw) == 0 {
			continue
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			out[tool.Definition.Name] = "invalid:" + string(raw)
			continue
		}
		norm, err := json.Marshal(v)
		if err != nil {
			out[tool.Definition.Name] = "invalid:" + string(raw)
			continue
		}
		out[tool.Definition.Name] = string(norm)
	}
	return out
}

func findCommand(r *inproc.Runner, name string) (extension.RegisteredCommand, bool) {
	for _, cmd := range r.Commands() {
		if cmd.Name == name {
			return cmd.RegisteredCommand, true
		}
	}
	return extension.RegisteredCommand{}, false
}

func requireConformanceTool(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err == nil {
		return path
	}
	if os.Getenv("PIG_REQUIRE_EXTENSION_TOOLCHAINS") == "1" {
		t.Fatalf("required extension tool %s is unavailable: %v", name, err)
	}
	t.Skipf("%s not found: %v", name, err)
	return ""
}

func testExecutableName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func testPythonExecutable() string {
	if runtime.GOOS == "windows" {
		return "python"
	}
	return "python3"
}

// shortSockDir mirrors coding/extension/host/subprocess/integration_test.go;
// macOS Unix sockets have a 104-byte path limit and the default tmp dir is
// often too long for t.TempDir(). Each test gets an isolated /tmp child: using
// a shared /tmp/pig-test directory made subprocess tests flaky under package
// parallelism because another package's cleanup could remove this package's
// live listener socket.
func shortSockDir(t *testing.T) string {
	t.Helper()
	parent := "/tmp"
	if runtime.GOOS == "windows" {
		parent = os.TempDir()
	}
	sockDir, err := os.MkdirTemp(parent, "pig-test-*")
	if err != nil {
		t.Fatalf("mkdir short socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	t.Setenv("XDG_RUNTIME_DIR", sockDir)
	t.Setenv("TMPDIR", sockDir)
	t.Setenv("TMP", sockDir)
	t.Setenv("TEMP", sockDir)
	return sockDir
}

func findModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// TestConformance_SourceMetadata verifies that per-tool Source flows correctly
// through every transport. Unlike the recording-based conformance gate (which
// compares transport-invariant outputs), this test checks transport-specific
// source attribution:
//   - Every tool must have a non-empty SourceInfo (buildExtension default or explicit)
//   - sourced_tool must have SourceInfo = "mcp:test-server" (explicit override)
//   - Non-sourced tools must have SourceInfo = extension name (default stamping)
//
// This is a proper verification of D23 source metadata plumbing.
func TestConformance_SourceMetadata(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping source metadata test in short mode (builds fixtures)")
	}

	cases := allHarnessCases()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})

			tools := h.runner.Tools()
			if len(tools) == 0 {
				t.Fatal("no tools registered")
			}

			for _, tool := range tools {
				name := tool.Definition.Name
				source, ok := tool.SourceInfo.(string)
				if !ok || source == "" {
					t.Errorf("tool %q has empty/nil SourceInfo", name)
					continue
				}

				if name == "sourced_tool" {
					if source != "mcp:test-server" {
						t.Errorf("sourced_tool: SourceInfo = %q, want %q", source, "mcp:test-server")
					}
				} else {
					// All non-sourced tools should get the extension name as default source.
					if source != tc.extName {
						t.Errorf("tool %q: SourceInfo = %q, want default %q", name, source, tc.extName)
					}
				}
			}
		})
	}
}

// TestConformance_TerminalInput pins onTerminalInput across every transport.
// Earlier Go, Rust, and Python SDK implementations exposed versions of this API
// that could not work. The Node runtime now shares the same recording. Each
// fixture consumes one sentinel chunk and passes everything else, so a transport that ignores the verdict,
// consumes indiscriminately, or leaks the subscription fails here.
func TestConformance_TerminalInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}

	cases := sdkHarnessCases()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})

			if _, ok := h.ui.sendTerminalInput(terminalInputSentinel); ok {
				t.Fatal("host forwarded input before the extension subscribed")
			}

			runConformanceCommand(t, h, "term_subscribe")
			consumed, ok := h.ui.sendTerminalInput(terminalInputSentinel)
			if !ok {
				t.Fatal("extension subscribed but the host registered no input handler")
			}
			if !consumed {
				t.Error("sentinel chunk was not consumed by the subscribed extension")
			}
			if verdict, _ := h.ui.terminalInputVerdict("a"); verdict.Consume || verdict.Data != nil {
				t.Errorf("ordinary keystroke verdict = %+v; the editor must see it unchanged", verdict)
			}
			// Upstream applies a handler's data as the new input.
			if verdict, _ := h.ui.terminalInputVerdict(terminalInputRewriteSentinel); verdict.Consume || verdict.Data == nil || *verdict.Data != "rewritten" {
				t.Errorf("rewrite verdict = %+v, want data \"rewritten\"", verdict)
			}
			// Upstream's listener is synchronous with no deadline: a handler
			// that takes longer than any fixed input budget still decides, as
			// often as it runs, and keeps its subscription.
			for range 6 {
				if consumed, ok := h.ui.sendTerminalInput(terminalInputSlowSentinel); !ok || !consumed {
					t.Fatalf("slow handler verdict dropped: consumed=%v subscribed=%v", consumed, ok)
				}
			}

			runConformanceCommand(t, h, "term_unsubscribe")
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, ok := h.ui.sendTerminalInput(terminalInputSentinel); !ok {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("host kept forwarding input after unsubscribe")
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}

// terminalInputSentinel is the one chunk every SDK fixture consumes.
const terminalInputSentinel = "\x1b[99~"

// terminalInputRewriteSentinel is the chunk every SDK fixture rewrites to
// "rewritten".
const terminalInputRewriteSentinel = "\x1b[98~"

// terminalInputSlowSentinel is the chunk every SDK fixture consumes after
// 200 ms.
const terminalInputSlowSentinel = "\x1b[97~"

// runConformanceCommand invokes a fixture command, failing the test if it is
// missing or errors.
func runConformanceCommand(t *testing.T, h *harness, name string) {
	t.Helper()
	cmd, ok := findCommand(h.runner, name)
	if !ok {
		t.Fatalf("%s command not registered", name)
	}
	if err := cmd.Handler(context.Background(), ""); err != nil {
		t.Fatalf("%s command: %v", name, err)
	}
}

// TestConformance_Geometry pins that every SDK reports the same terminal
// geometry from the ready payload and updates it on a height_change
// notification. Width plumbing has existed since the first subprocess
// extension; height was added later and initially only reached the Go and node
// SDKs, so the Rust and Python surfaces silently returned nothing. A widget
// that reflows on height would have behaved differently per SDK.
func TestConformance_Geometry(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}

	cases := sdkHarnessCases()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})

			// Height reaches the extension only via the notification, so a
			// missing dispatch arm shows up as the pre-change value.
			h.host.NotifyHeight(37)

			var got string
			pollUntilConformance(t, 5*time.Second, "geometry never reported", func() bool {
				*h.notify = (*h.notify)[:0]
				runConformanceCommand(t, h, "report_geometry")
				for _, n := range *h.notify {
					if strings.HasPrefix(n, "geometry:") {
						got = n
						return true
					}
				}
				return false
			})

			if !strings.HasSuffix(got, "x37:info") {
				t.Errorf("%s reported %q, want height 37 from the height_change notification", tc.name, got)
			}
		})
	}
}

// pollUntilConformance retries fn until it returns true or the budget expires.
func pollUntilConformance(t *testing.T, budget time.Duration, msg string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out after %s: %s", budget, msg)
}

// Upstream passes every tool a live AbortSignal and an onUpdate that streams
// partial results. Each realization streams two updates in order before its
// result, and a tool waiting on its signal observes the caller's cancellation.
func TestToolSignalAndUpdatesSDKsMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	cases := []struct {
		name string
		make func(*testing.T) *harness
	}{
		{"inproc-go", makeInprocGoHarness},
		{"subprocess-go", makeSubprocessGoHarness},
		{"subprocess-node", makeSubprocessNodeHarness},
		{"subprocess-rust", makeSubprocessRustHarness},
		{"subprocess-python", makeSubprocessPythonHarness},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()

			tool, ok := findTool(h.runner, "update_tool")
			if !ok {
				t.Fatal("update_tool not registered")
			}
			var updates []string
			var onUpdate agent.ToolUpdateCallback = func(content string, _ any) { updates = append(updates, content) }
			result, err := tool.Definition.Execute(ctx, "tc-update", json.RawMessage(`{}`), onUpdate)
			if err != nil {
				t.Fatalf("update_tool: %v", err)
			}
			if final, _ := result.(agent.AgentToolResult); final.Content != "done" || !slices.Equal(updates, []string{"step 1", "step 2"}) {
				t.Fatalf("update_tool updates = %q result = %#v, want [step 1 step 2] then done", updates, result)
			}

			abortTool, ok := findTool(h.runner, "abort_tool")
			if !ok {
				t.Fatal("abort_tool not registered")
			}
			toolCtx, cancelTool := context.WithCancel(ctx)
			waiting := make(chan struct{})
			var once sync.Once
			var onWaiting agent.ToolUpdateCallback = func(string, any) { once.Do(func() { close(waiting) }) }
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _ = abortTool.Definition.Execute(toolCtx, "tc-abort", json.RawMessage(`{}`), onWaiting)
			}()
			select {
			case <-waiting:
			case <-ctx.Done():
				t.Fatal("abort_tool never reported that it was waiting")
			}
			cancelTool()
			<-done
			// The host returns at cancellation; the extension observes its
			// signal asynchronously, so probe until it reports.
			waitFor(t, func() bool {
				h.ui.ClearRecorded()
				runConformanceCommand(t, h, "abort_probe")
				return slices.Contains(h.ui.Recorded(), "abort:true:info")
			})
		})
	}
}
