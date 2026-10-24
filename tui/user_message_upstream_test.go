package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/markdowntransform"
)

// packages/coding-agent/test/user-message.test.ts:12
func TestUpstreamUserMessageOSCMarkers(t *testing.T) {
	t.Run("keeps user message height stable while moving closing OSC markers off line end", func(t *testing.T) {
		component := NewUserMessageComponent("hello", nil, 1, nil, "", "")
		lines := component.Render(20)
		if len(lines) != 3 {
			t.Fatalf("height=%d, want 3", len(lines))
		}
		upstreamContains(t, lines[0], "\x1b]133;A\x07")
		mdEqual(t, strings.HasSuffix(lines[0], "\x1b[49m"), true)
		upstreamExcludes(t, lines[0], "\x1b]133;B\x07")
		upstreamContains(t, lines[1], "hello")
		mdEqual(t, strings.HasPrefix(lines[2], "\x1b]133;B\x07\x1b]133;C\x07"), true)
		mdEqual(t, strings.HasSuffix(lines[2], "\x1b[49m"), true)
	})
}

// packages/coding-agent/test/user-message.test.ts:27
func TestUpstreamUserMessageTransformerChain(t *testing.T) {
	t.Run("chains Markdown transformers with user message context", func(t *testing.T) {
		var calls []string
		component := NewUserMessageComponent("The input is $x^2$.", nil, 1, []markdowntransform.MarkdownTransformer{
			func(markdown string, context markdowntransform.MarkdownTransformContext) string {
				calls = append(calls, "formula")
				want := markdowntransform.MarkdownTransformContext{MessageType: "user", IsStreaming: false, AvailableWidth: 78}
				if context != want {
					t.Fatalf("context=%+v, want %+v", context, want)
				}
				return strings.Replace(markdown, "$x^2$", "x²", 1)
			},
			func(markdown string, _ markdowntransform.MarkdownTransformContext) string {
				calls = append(calls, "suffix")
				return markdown + " Done."
			},
		}, "", "")
		upstreamContains(t, stripANSI(strings.Join(component.Render(80), "\n")), "The input is x². Done.")
		if !slices.Equal(calls, []string{"formula", "suffix"}) {
			t.Fatalf("calls=%q, want [formula suffix]", calls)
		}
	})
}

// packages/coding-agent/test/user-message.test.ts:46
func TestUpstreamUserMessageInvalidation(t *testing.T) {
	t.Run("reapplies Markdown transformers when invalidated", func(t *testing.T) {
		suffix := "before"
		component := NewUserMessageComponent("Message", nil, 1, []markdowntransform.MarkdownTransformer{
			func(markdown string, _ markdowntransform.MarkdownTransformContext) string {
				return markdown + " " + suffix
			},
		}, "", "")
		upstreamContains(t, stripANSI(strings.Join(component.Render(80), "\n")), "Message before")
		suffix = "after"
		component.Invalidate()
		upstreamContains(t, stripANSI(strings.Join(component.Render(80), "\n")), "Message after")
	})
}

// packages/coding-agent/test/user-message.test.ts: "passes persisted entry identity in transformer context; live leaves it undefined".
func TestUpstreamUserMessagePersistedEntryIdentity(t *testing.T) {
	type identity struct{ messageID, timestamp string }
	var captured []identity
	capture := func(markdown string, context markdowntransform.MarkdownTransformContext) string {
		captured = append(captured, identity{context.MessageID, context.Timestamp})
		return markdown
	}
	NewUserMessageComponent("restored", nil, 1, []markdowntransform.MarkdownTransformer{capture}, "entry-abc", "2025-01-15T10:30:00.000Z").Render(80)
	NewUserMessageComponent("live", nil, 1, []markdowntransform.MarkdownTransformer{capture}, "", "").Render(80)
	if want := []identity{{"entry-abc", "2025-01-15T10:30:00.000Z"}, {"", ""}}; !slices.Equal(captured, want) {
		t.Fatalf("captured %+v, want %+v", captured, want)
	}
}
