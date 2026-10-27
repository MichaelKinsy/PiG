package extensionconformance

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// The host sends an extension only the state that changed since its last push and skips a push the extension already holds, as the runtime's applyState keeps the fields a state_update omits. Every realization must report the same system prompt options after an identical state, a change to the options alone, and a change back; the Node runtime reads them from the replicated state, so a push that dropped or kept a stale copy shows up here.
func TestSystemPromptOptionsAcrossIdenticalAndPartialStatePushes(t *testing.T) {
	t.Parallel()
	cases := allHarnessCases()
	for _, language := range []string{"go", "rust", "python"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedUIHarness(t, language) }})
	}
	first := conformanceSystemPromptOptions()
	second := conformanceSystemPromptOptions()
	second.Cwd = "/changed-cwd"
	second.CustomPrompt = "changed-prompt"
	second.SelectedTools = []string{"edit"}
	sequence := []extension.BuildSystemPromptOptions{first, first, second, second, first}
	expected := []string{"spo_prompt=conformance-prompt spo_cwd=/probe-cwd spo_tools=read,bash", "spo_prompt=conformance-prompt spo_cwd=/probe-cwd spo_tools=read,bash", "spo_prompt=changed-prompt spo_cwd=/changed-cwd spo_tools=edit", "spo_prompt=changed-prompt spo_cwd=/changed-cwd spo_tools=edit", "spo_prompt=conformance-prompt spo_cwd=/probe-cwd spo_tools=read,bash"}
	var reference []string
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("state push conformance complete")
				}
			})
			current := &sequence[0]
			if h.host == nil {
				h.runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{
					GetSystemPromptOptions: func() *extension.BuildSystemPromptOptions { return current },
					IsProjectTrusted:       func() bool { return conformanceProjectTrusted },
				}, nil)
			} else {
				h.bridge.SetHostAction("getSystemPromptOptions", func() extension.BuildSystemPromptOptions { return *current })
			}
			command, ok := findCommand(h.runner, "context-probe")
			if !ok {
				t.Fatal("context-probe missing")
			}
			var got []string
			for i := range sequence {
				current = &sequence[i]
				cc := h.runner.CreateCommandContext()
				ctx := extension.WithCommandContext(extension.WithContext(t.Context(), cc.Context), cc)
				before := len(*h.notify)
				if err := command.Handler(ctx, ""); err != nil {
					t.Fatalf("probe %d: %v", i, err)
				}
				lines := (*h.notify)[before:]
				if len(lines) != 1 {
					t.Fatalf("probe %d notified %v", i, lines)
				}
				line := lines[0]
				_, rest, _ := strings.Cut(line, " spo_prompt=")
				fields := strings.SplitN("spo_prompt="+rest, " spo_shape=", 2)
				got = append(got, fields[0])
			}
			if !reflect.DeepEqual(got, expected) {
				t.Fatalf("options across pushes = %q, want %q", got, expected)
			}
			if h.host == nil {
				reference = got
			} else if reference != nil && !reflect.DeepEqual(got, reference) {
				t.Fatalf("options differ from the in-process reference: %q vs %q", got, reference)
			}
		})
	}
}

// Pi awaits every message_update handler before it reads the provider's next event (agent-loop.ts:436 `await emit`, agent.ts:610 `await listener`, agent-session.ts:1140 `await this._emitExtensionEvent`, runner.ts:1089-1118 `await handler(event, ctx)`), so a handler sees one event per provider delta, in the provider's order, and an event of another kind that follows is delivered after the last update's handlers finished. The Host keeps that: nothing merges, drops or reorders deltas, in the in-process reference and in every isolated, packed and fused realization. The state pushes before each delivery are partial (TestSystemPromptOptionsAcrossIdenticalAndPartialStatePushes); this row sends enough deltas back to back to make a merged or dropped delta visible.
func TestMessageUpdateDeliversEveryDeltaInOrderAcrossSDKs(t *testing.T) {
	t.Parallel()
	cases := sdkHarnessCases()
	for _, language := range []string{"go", "rust", "python"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedUIHarness(t, language) }})
	}
	const deltas = 48
	var want []string
	for i := range deltas {
		want = append(want, fmt.Sprintf("message-update=text_delta:%d:δ%d:false:info", i%3, i))
	}
	want = append(want, "tool-update=read:{\"path\":\"x\"}:working:1:info")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("delta order conformance complete")
				}
			})
			h.ui.ClearRecorded()
			for i := range deltas {
				event := extension.MessageUpdateEvent{Type: "message_update", Message: wireAgentMessage(map[string]any{"role": "assistant"}), AssistantMessageEvent: ai.TextDeltaEvent{ContentIndex: i % 3, Delta: fmt.Sprintf("δ%d", i)}}
				if _, err := h.runner.Emit(t.Context(), event); err != nil {
					t.Fatalf("emit delta %d: %v", i, err)
				}
			}
			update := extension.ToolExecutionUpdateEvent{Type: "tool_execution_update", ToolCallID: "call", ToolName: "read", Args: map[string]any{"path": "x"}, PartialResult: map[string]any{"content": []any{map[string]any{"type": "text", "text": "working"}}, "details": map[string]any{"progress": 1}}}
			if _, err := h.runner.Emit(t.Context(), update); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { return len(h.ui.Recorded()) >= len(want) })
			if got := h.ui.Recorded(); !reflect.DeepEqual(got, want) {
				t.Fatalf("delivered events differ from the provider's deltas\n got: %q\nwant: %q", got, want)
			}
		})
	}
}
