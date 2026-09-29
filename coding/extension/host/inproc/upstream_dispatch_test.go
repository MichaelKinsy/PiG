package inproc_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
)

func upstreamHandlerExtension(path, event string, handler extension.HandlerFn) extension.Extension {
	return extension.Extension{Path: path, Handlers: map[string][]extension.HandlerFn{event: {handler}}}
}

// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:538
// Go carries the current AbortSignal on the context.Context supplied to dispatch.
func TestUpstreamRunnerAbortSignal(t *testing.T) {
	r := inproc.NewRunner(nil, t.TempDir())
	controller, abort := context.WithCancel(t.Context())
	defer abort()
	ctx := r.DispatchContext(controller)
	if ctx.Done() != controller.Done() || ctx.Err() != nil || extension.FromContext(ctx) == nil {
		t.Fatal("dispatch lost the current cancellation signal")
	}
	abort()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("signal after abort: %v", ctx.Err())
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:849
func TestUpstreamRunnerBeforeAgentStart(t *testing.T) {
	t.Run("keeps ctx.getSystemPrompt() in sync with chained system prompt updates", func(t *testing.T) {
		cwd := t.TempDir()
		var exts []extension.Extension
		for i, suffix := range []string{"first", "second"} {
			exts = append(exts, upstreamHandlerExtension(fmt.Sprintf("before-agent-start-%d.ts", i+1), "before_agent_start", func(args ...any) (any, error) {
				current, err := extension.FromContext(args[1].(context.Context)).GetSystemPrompt()
				if err != nil {
					return nil, err
				}
				return &extension.BeforeAgentStartEventResult{SystemPrompt: new(current + "\n" + suffix)}, nil
			}))
		}
		r := inproc.NewRunner(exts, cwd)
		r.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
		var reported []string
		r.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err.Error) })
		options := extension.BuildSystemPromptOptions{Cwd: cwd, CustomPrompt: "base"}
		initial := prompts.BuildDefaultPrompt(prompts.Options{Cwd: cwd, CustomPrompt: "base", AppendMode: "replace"})
		result, err := r.EmitBeforeAgentStart(t.Context(), "hello", nil, initial, options)
		if err != nil || len(reported) != 0 || result == nil {
			t.Fatalf("result=%+v error=%v reported=%v", result, err, reported)
		}
		if len(result.Messages) != 0 || result.SystemPrompt == nil || !strings.HasPrefix(*result.SystemPrompt, "base") || !strings.HasSuffix(*result.SystemPrompt, "\nfirst\nsecond") {
			t.Fatalf("result=%+v", result)
		}
	})
}

func upstreamBoundaryPreview(entries []extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
	projected := make([]extension.ProjectedSessionEntry, len(entries))
	for i, entry := range entries {
		projected[i] = extension.ProjectedSessionEntry{SourceEntry: map[string]any{"type": "custom", "id": fmt.Sprintf("draft-%d", i), "parentId": nil, "timestamp": "", "customType": entry.Type}, Messages: []extension.AgentMessage{}}
	}
	return extension.BoundaryContextPreview{ContextEntries: projected, ContextMessages: []extension.AgentMessage{}, LLMMessages: []any{}, PendingMessages: []extension.AgentMessage{}, CanContinue: false}, nil
}

func TestUpstreamRunnerBoundary(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:891
	t.Run("chains shared draft proposals and preserves omitted result fields", func(t *testing.T) {
		type observation struct {
			entries      int
			continuation bool
			preview      int
		}
		var observations []observation
		observe := func(event *extension.AgentBeforeSettleEvent) {
			observations = append(observations, observation{len(event.Entries), event.Continue, len(event.Context.ContextEntries)})
		}
		first := upstreamHandlerExtension("<inline:first>", "agent_before_settle", func(args ...any) (any, error) {
			event := args[0].(*extension.AgentBeforeSettleEvent)
			observe(event)
			event.Entries = append(event.Entries, extension.SessionBoundaryDraft{Type: "custom", CustomType: "first", Data: 1})
			return extension.BoundaryResult{Continue: new(true)}, nil
		})
		second := upstreamHandlerExtension("<inline:second>", "agent_before_settle", func(args ...any) (any, error) {
			observe(args[0].(*extension.AgentBeforeSettleEvent))
			return extension.BoundaryResult{Entries: new([]extension.SessionBoundaryDraft{})}, nil
		})
		r := inproc.NewRunner([]extension.Extension{first, second}, t.TempDir())
		result, err := r.EmitBoundary(t.Context(), &extension.AgentBeforeSettleEvent{Type: "agent_before_settle", BoundaryState: extension.BoundaryState{Outcome: extension.AgentActivityCompleted}}, upstreamBoundaryPreview)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(observations, []observation{{0, false, 0}, {1, true, 1}}) {
			t.Fatalf("observations=%v", observations)
		}
		if len(result.Entries) != 0 || !result.Continue {
			t.Fatalf("result=%+v", result)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:955
	t.Run("reports invalid boundary previews and lets later handlers repair the proposal", func(t *testing.T) {
		secondRan := false
		first := upstreamHandlerExtension("<inline:invalid>", "agent_before_settle", func(...any) (any, error) {
			return extension.BoundaryResult{Entries: new([]extension.SessionBoundaryDraft{{Type: "context_edit", TargetID: "missing", Replacement: json.RawMessage("null")}})}, nil
		})
		second := upstreamHandlerExtension("<inline:repair>", "agent_before_settle", func(args ...any) (any, error) {
			secondRan = true
			if len(args[0].(*extension.AgentBeforeSettleEvent).Entries) != 1 {
				t.Fatal("missing invalid proposal")
			}
			return extension.BoundaryResult{Entries: new([]extension.SessionBoundaryDraft{})}, nil
		})
		r := inproc.NewRunner([]extension.Extension{first, second}, t.TempDir())
		var reported []string
		r.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err.Error) })
		result, err := r.EmitBoundary(t.Context(), &extension.AgentBeforeSettleEvent{Type: "agent_before_settle", BoundaryState: extension.BoundaryState{Outcome: extension.AgentActivityCompleted}}, func(entries []extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
			if slices.ContainsFunc(entries, func(entry extension.SessionBoundaryDraft) bool { return entry.Type == "context_edit" }) {
				return extension.BoundaryContextPreview{}, errors.New("Entry missing not found")
			}
			return upstreamBoundaryPreview(nil)
		})
		if err != nil {
			t.Fatal(err)
		}
		if !secondRan || !slices.Contains(reported, "Invalid boundary entries: Entry missing not found") || len(result.Entries) != 0 || !result.Valid {
			t.Fatalf("result=%+v secondRan=%v reported=%v", result, secondRan, reported)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:1003
	t.Run("keeps shared mutations made before a handler throws", func(t *testing.T) {
		ext := upstreamHandlerExtension("<inline:1>", "agent_before_settle", func(args ...any) (any, error) {
			event := args[0].(*extension.AgentBeforeSettleEvent)
			event.Entries = append(event.Entries, extension.SessionBoundaryDraft{Type: "custom", CustomType: "kept"})
			return nil, errors.New("boundary failed")
		})
		r := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
		var reported []string
		r.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err.Error) })
		result, err := r.EmitBoundary(t.Context(), &extension.AgentBeforeSettleEvent{Type: "agent_before_settle", BoundaryState: extension.BoundaryState{Outcome: extension.AgentActivityCompleted}}, func([]extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
			return upstreamBoundaryPreview(nil)
		})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(result.Entries, []extension.SessionBoundaryDraft{{Type: "custom", CustomType: "kept"}}) || !slices.Equal(reported, []string{"boundary failed"}) {
			t.Fatalf("result=%+v reported=%v", result, reported)
		}
	})
}

func upstreamToolResultEvent(id string) extension.CustomToolResultEvent {
	return extension.CustomToolResultEvent{ToolName: "my_tool", ToolResultEventBase: extension.ToolResultEventBase{Type: "tool_result", ToolCallID: id, Input: map[string]any{}, Content: []any{map[string]any{"type": "text", "text": "base"}}, IsError: false}, Details: map[string]any{"initial": true}}
}
func TestUpstreamRunnerToolResult(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:1034
	t.Run("chains content modifications across handlers", func(t *testing.T) {
		var exts []extension.Extension
		for i, text := range []string{"ext1", "ext2"} {
			exts = append(exts, upstreamHandlerExtension(fmt.Sprintf("tool-result-%d.ts", i+1), "tool_result", func(args ...any) (any, error) {
				event := args[0].(extension.CustomToolResultEvent)
				content := append(slices.Clone(event.Content), map[string]any{"type": "text", "text": text})
				return &extension.ToolResultEventResult{Content: content}, nil
			}))
		}
		result, err := inproc.NewRunner(exts, t.TempDir()).EmitToolResult(t.Context(), upstreamToolResultEvent("call-1"))
		if err != nil || result == nil || len(result.Content) != 3 {
			t.Fatalf("result=%+v error=%v", result, err)
		}
		if !reflect.DeepEqual(result.Content[0], map[string]any{"type": "text", "text": "base"}) {
			t.Fatalf("base=%v", result.Content[0])
		}
		var appended []string
		for _, item := range result.Content[1:] {
			if item := item.(map[string]any); item["type"] == "text" {
				appended = append(appended, item["text"].(string))
			}
		}
		slices.Sort(appended)
		if !slices.Equal(appended, []string{"ext1", "ext2"}) {
			t.Fatalf("appended=%v", appended)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:1081
	t.Run("preserves previous modifications when later handlers return partial patches", func(t *testing.T) {
		content := []any{map[string]any{"type": "text", "text": "first"}}
		details := map[string]any{"source": "ext1"}
		first := upstreamHandlerExtension("tool-result-partial-1.ts", "tool_result", func(...any) (any, error) {
			return &extension.ToolResultEventResult{Content: content, Details: details}, nil
		})
		second := upstreamHandlerExtension("tool-result-partial-2.ts", "tool_result", func(...any) (any, error) { return &extension.ToolResultEventResult{IsError: new(true)}, nil })
		result, err := inproc.NewRunner([]extension.Extension{first, second}, t.TempDir()).EmitToolResult(t.Context(), upstreamToolResultEvent("call-2"))
		want := &extension.ToolResultEventResult{Content: content, Details: details, IsError: new(true)}
		if err != nil || !reflect.DeepEqual(result, want) {
			t.Fatalf("result=%+v want=%+v error=%v", result, want, err)
		}
	})
}

func TestUpstreamRunnerRendererAndFork(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:723
	t.Run("gets message renderer by type", func(t *testing.T) {
		ext := extension.Extension{Path: "renderer.ts", MessageRenderers: map[string]extension.MessageRenderer{"my-type": func(extension.CustomMessage, extension.MessageRenderOptions, extension.Theme) extension.Component {
			return nil
		}}}
		r := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
		if r.MessageRenderer("my-type") == nil || r.MessageRenderer("not-exists") != nil {
			t.Fatal("renderer lookup mismatch")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:741
	t.Run("gets entry renderer by type", func(t *testing.T) {
		ext := extension.Extension{Path: "entry-renderer.ts", EntryRenderers: map[string]extension.EntryRenderer{"my-entry": func(extension.CustomEntry, extension.EntryRenderOptions, extension.Theme) extension.Component {
			return nil
		}}}
		r := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
		if r.EntryRenderer("my-entry") == nil || r.EntryRenderer("not-exists") != nil {
			t.Fatal("renderer lookup mismatch")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:1198
	t.Run("passes fork options through to the bound handler", func(t *testing.T) {
		type call struct {
			id      string
			options *extension.ForkOptions
		}
		var calls []call
		r := inproc.NewRunner(nil, t.TempDir())
		r.BindCommandActions(extension.CommandActions{Fork: func(id string, options *extension.ForkOptions) (extension.CancelledResult, error) {
			calls = append(calls, call{id, options})
			return extension.CancelledResult{Cancelled: false}, nil
		}})
		ctx := r.CreateCommandContext()
		if _, err := ctx.Fork("entry-1", nil); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(calls, []call{{"entry-1", nil}}) {
			t.Fatalf("calls=%v", calls)
		}
		options := &extension.ForkOptions{Position: "at"}
		if _, err := ctx.Fork("entry-2", options); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(calls, []call{{"entry-1", nil}, {"entry-2", options}}) {
			t.Fatalf("calls=%v", calls)
		}
	})
}
