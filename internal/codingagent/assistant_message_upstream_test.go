package codingagent

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func upstreamAssistant(content ...ai.AssistantContentBlock) *agent.AssistantMessage {
	return &agent.AssistantMessage{Role: agent.RoleAssistant, Content: content, API: "openai-responses", Provider: "openai", ModelID: "gpt-4o-mini", Usage: &ai.Usage{}, StopReason: ai.StopReasonStop}
}

func upstreamAssistantBlock(message *agent.AssistantMessage, hidden bool, streaming *bool, transforms ...extension.MarkdownTransformer) *tui.AssistantMessageBlock {
	block := tui.NewAssistantMessageBlock(hidden)
	for _, kind := range []extension.MarkdownMessageType{extension.MarkdownMessageAssistant, extension.MarkdownMessageAssistantThinking} {
		transform := func(text string, width int) string {
			return createMarkdownTransform(kind, streaming != nil && *streaming, transforms)(text, width)
		}
		if kind == extension.MarkdownMessageAssistant {
			block.SetMarkdownTransform(transform)
		} else {
			block.SetThinkingMarkdownTransform(transform)
		}
	}
	block.SetMarkdownTransformState(func() string { return strconv.FormatBool(streaming != nil && *streaming) })
	if message != nil {
		updateAssistantMessageBlock(block, message)
	}
	return block
}

func TestAssistantMessageUpstream(t *testing.T) {
	const start, end, final = "\x1b]133;A\x07", "\x1b]133;B\x07", "\x1b]133;C\x07"
	plain := func(block *tui.AssistantMessageBlock, width int) string {
		return widthx.StripAnsi(strings.Join(block.Render(width), "\n"))
	}
	// .upstream/v0.87.1/packages/coding-agent/test/assistant-message.test.ts:37
	t.Run("adds OSC 133 zone markers to assistant messages without tool calls", func(t *testing.T) {
		block := upstreamAssistantBlock(upstreamAssistant(ai.TextContent{Text: "hello"}), false, nil)
		lines := block.Render(40)
		if len(lines) == 0 || !strings.Contains(lines[0], start) || !strings.HasPrefix(lines[len(lines)-1], end+final) {
			t.Fatalf("missing zone markers: %q", lines)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/assistant-message.test.ts:48
	t.Run("does not add OSC 133 zone markers when assistant message contains tool calls", func(t *testing.T) {
		block := upstreamAssistantBlock(upstreamAssistant(ai.TextContent{Text: "calling tool"}, ai.ToolCall{ID: "tool-1", Name: "read", Arguments: ai.JsonObject{"path": "file.txt"}}), false, nil)
		got := strings.Join(block.Render(60), "\n")
		for _, marker := range []string{start, end, final} {
			if strings.Contains(got, marker) {
				t.Fatalf("tool message contains zone marker: %q", got)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/assistant-message.test.ts:64
	t.Run("renders length stops with neutral truncation wording", func(t *testing.T) {
		message := upstreamAssistant(ai.ThinkingContent{Thinking: "private reasoning"})
		message.StopReason = ai.StopReasonLength
		got := plain(upstreamAssistantBlock(message, true, nil), 80)
		for _, want := range []string{"Thinking...", "Response was truncated before completion."} {
			if !strings.Contains(got, want) {
				t.Fatalf("missing %q: %q", want, got)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/assistant-message.test.ts:77
	t.Run("coalesces adjacent thinking blocks into one hidden thinking label", func(t *testing.T) {
		got := plain(upstreamAssistantBlock(upstreamAssistant(ai.ThinkingContent{Thinking: "first thought"}, ai.ThinkingContent{}, ai.ThinkingContent{Thinking: "second thought"}, ai.TextContent{Text: "answer"}), true, nil), 80)
		if strings.Count(got, "Thinking...") != 1 || !strings.Contains(got, "answer") {
			t.Fatalf("hidden runs: %q", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/assistant-message.test.ts:95
	t.Run("collapses individual thinking runs when clicked", func(t *testing.T) {
		block := upstreamAssistantBlock(upstreamAssistant(ai.ThinkingContent{Thinking: "first reasoning"}, ai.TextContent{Text: "answer"}, ai.ThinkingContent{Thinking: "second reasoning"}), false, nil)
		lines := block.Render(80)
		row := slices.IndexFunc(lines, func(line string) bool { return strings.Contains(widthx.StripAnsi(line), "first reasoning") })
		if row < 0 {
			t.Fatal("missing first reasoning")
		}
		result := tui.DispatchMouseEvent(block, tui.TuiMouseEvent{Type: tui.MouseClick, Button: tui.MouseButtonLeft, X: 1, Y: row, ScreenX: 1, ScreenY: row, Width: 80, Height: len(lines), ClickCount: 1})
		if result == nil || !result.Handled {
			t.Fatal("click not handled")
		}
		got := plain(block, 80)
		if strings.Contains(got, "first reasoning") || !strings.Contains(got, "Thinking...") || !strings.Contains(got, "second reasoning") {
			t.Fatalf("thinking run visibility: %q", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/assistant-message.test.ts:130
	t.Run("uses configured output padding for text and thinking", func(t *testing.T) {
		block := upstreamAssistantBlock(upstreamAssistant(ai.TextContent{Text: "hello"}, ai.ThinkingContent{Thinking: "reasoning"}), false, nil)
		got := plain(block, 80)
		if !strings.Contains(got, " hello") || !strings.Contains(got, " reasoning") {
			t.Fatalf("padded: %q", got)
		}
		block.SetOutputPad(0)
		for _, prefix := range []string{"hello", "reasoning"} {
			if !slices.ContainsFunc(block.Render(80), func(line string) bool { return strings.HasPrefix(widthx.StripAnsi(line), prefix) }) {
				t.Fatalf("missing unpadded %q: %q", prefix, plain(block, 80))
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/assistant-message.test.ts:154
	t.Run("chains Markdown transformers in registration order", func(t *testing.T) {
		var calls []string
		block := upstreamAssistantBlock(upstreamAssistant(ai.TextContent{Text: "The result is $x^2$."}), false, nil,
			func(text string, ctx extension.MarkdownTransformContext) string {
				calls = append(calls, "formula")
				if ctx != (extension.MarkdownTransformContext{MessageType: extension.MarkdownMessageAssistant, AvailableWidth: 78}) {
					t.Fatalf("context: %+v", ctx)
				}
				return strings.ReplaceAll(text, "$x^2$", "x²")
			},
			func(text string, _ extension.MarkdownTransformContext) string {
				calls = append(calls, "suffix")
				return text + " Done."
			},
		)
		if got := plain(block, 80); !strings.Contains(got, "The result is x². Done.") {
			t.Fatalf("transformed: %q", got)
		}
		if !slices.Equal(calls, []string{"formula", "suffix"}) {
			t.Fatalf("calls: %v", calls)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/assistant-message.test.ts:174
	t.Run("identifies partial assistant Markdown as streaming", func(t *testing.T) {
		streaming := true
		var states []bool
		block := upstreamAssistantBlock(nil, false, &streaming, func(text string, ctx extension.MarkdownTransformContext) string {
			states = append(states, ctx.IsStreaming)
			if ctx.IsStreaming {
				return text
			}
			return text + " transformed"
		})
		message := upstreamAssistant(ai.TextContent{Text: "partial"})
		updateAssistantMessageBlock(block, message)
		if got := plain(block, 80); strings.Contains(got, "transformed") {
			t.Fatalf("streaming: %q", got)
		}
		streaming = false
		updateAssistantMessageBlock(block, message)
		if got := plain(block, 80); !strings.Contains(got, "partial transformed") {
			t.Fatalf("final: %q", got)
		}
		if !slices.Equal(states, []bool{true, false}) {
			t.Fatalf("states: %v", states)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/assistant-message.test.ts:193
	t.Run("reapplies Markdown transformers when available width changes", func(t *testing.T) {
		var widths []int
		block := upstreamAssistantBlock(upstreamAssistant(ai.TextContent{Text: "answer"}), false, nil, func(text string, ctx extension.MarkdownTransformContext) string {
			widths = append(widths, ctx.AvailableWidth)
			return fmt.Sprintf("%s (%d)", text, ctx.AvailableWidth)
		})
		if got := plain(block, 80); !strings.Contains(got, "answer (78)") {
			t.Fatalf("wide: %q", got)
		}
		block.Render(80)
		if got := plain(block, 60); !strings.Contains(got, "answer (58)") {
			t.Fatalf("narrow: %q", got)
		}
		if !slices.Equal(widths, []int{78, 58}) {
			t.Fatalf("widths: %v", widths)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/assistant-message.test.ts:216
	t.Run("continues the Markdown transformer chain when a transformer throws", func(t *testing.T) {
		var calls []string
		block := upstreamAssistantBlock(upstreamAssistant(ai.TextContent{Text: "still visible"}), false, nil,
			func(text string, _ extension.MarkdownTransformContext) string {
				calls = append(calls, "first")
				return strings.ReplaceAll(text, "still", "remains")
			},
			func(string, extension.MarkdownTransformContext) string {
				calls = append(calls, "throw")
				panic("broken transformer")
			},
			func(text string, _ extension.MarkdownTransformContext) string {
				calls = append(calls, "last")
				return text + " after error"
			},
		)
		if got := plain(block, 80); !strings.Contains(got, "remains visible after error") {
			t.Fatalf("after error: %q", got)
		}
		if !slices.Equal(calls, []string{"first", "throw", "last"}) {
			t.Fatalf("calls: %v", calls)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/assistant-message.test.ts:245
	t.Run("transforms text and thinking Markdown without mutating the original message", func(t *testing.T) {
		message := upstreamAssistant(ai.TextContent{Text: "answer"}, ai.ThinkingContent{Thinking: "reasoning"})
		before := slices.Clone(message.Content)
		block := upstreamAssistantBlock(message, false, nil, func(text string, ctx extension.MarkdownTransformContext) string {
			return string(ctx.MessageType) + ":" + text
		})
		got := plain(block, 80)
		if !strings.Contains(got, "assistant:answer") || !strings.Contains(got, "assistant-thinking:reasoning") {
			t.Fatalf("contexts: %q", got)
		}
		if !reflect.DeepEqual(message.Content, before) {
			t.Fatalf("message mutated: %#v", message.Content)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/assistant-message.test.ts:266
	t.Run("uses configured output padding for user messages", func(t *testing.T) {
		for _, pad := range []int{1, 0} {
			block := tui.NewUserMessageBlock("hello")
			block.SetOutputPad(pad)
			prefix := strings.Repeat(" ", pad) + "hello"
			if !slices.ContainsFunc(block.Render(40), func(line string) bool { return strings.HasPrefix(widthx.StripAnsi(line), prefix) }) {
				t.Fatalf("padding %d: %q", pad, block.Render(40))
			}
		}
	})
}
