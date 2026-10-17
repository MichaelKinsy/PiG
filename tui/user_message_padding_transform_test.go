package tui

// pi: packages/coding-agent/src/modes/interactive/components/user-message.ts

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// Pi user-message.ts retains its transformer definitions across padding rebuilds. The Go host keeps the Markdown child so a padding change cannot discard its async transform owner or external-state cache key.
func TestUserMessagePaddingRetainsTransformState(t *testing.T) {
	block := NewUserMessageComponent("message", nil, 1, nil)
	state := "first"
	block.SetMarkdownTransform(func(text string, _ int) string { return text + " " + state })
	block.SetMarkdownTransformState(func() string { return state })
	block.Render(80)
	block.SetOutputPad(0)
	block.Render(80)
	state = "second"
	if got := strings.Join(block.Render(80), "\n"); !strings.Contains(got, "message second") || strings.Contains(got, "message first") {
		t.Fatalf("padding detached transform state: %q", got)
	}
}

func TestUserMessagePaddingReappliesTransform(t *testing.T) {
	block := NewUserMessageComponent("message", nil, 1, nil)
	suffix := "first"
	block.SetMarkdownTransform(func(text string, _ int) string { return text + " " + suffix })
	block.Render(80)
	suffix = "second"
	block.SetOutputPad(1)
	if got := strings.Join(block.Render(80), "\n"); !strings.Contains(got, "message second") {
		t.Fatalf("padding rebuild retained a cached transform result: %q", got)
	}
}

func TestUserMessagePaddingRetainsAsyncTransform(t *testing.T) {
	var workers sync.WaitGroup
	block := NewUserMessageComponent("raw", nil, 1, nil)
	block.SetAsyncMarkdownTransform(&AsyncMarkdownTransform{
		Context: t.Context(), Start: workers.Go,
		Prepare: func(string, int) func(context.Context) string {
			return func(context.Context) string { return "transformed" }
		},
	})
	block.Render(80)
	workers.Wait()
	block.Render(80)
	block.SetOutputPad(0)
	block.Render(80)
	workers.Wait()
	if got := strings.Join(block.Render(80), "\n"); !strings.Contains(got, "transformed") || strings.Contains(got, "raw") {
		t.Fatalf("padding detached async transform: %q", got)
	}
}

// user-message.ts:20-59: UserMessageComponent extends Container with one Markdown child, and its zone markers wrap the first and last line.
func TestUserMessageComponentIsAContainerOfOneMarkdown(t *testing.T) {
	block := NewUserMessageComponent("hi", nil, 1, nil)
	children := block.Children()
	if len(children) != 1 {
		t.Fatalf("children = %d, want 1", len(children))
	}
	if _, ok := children[0].(*Markdown); !ok {
		t.Fatalf("child is %T, want *Markdown", children[0])
	}
	lines := block.Render(20)
	if !strings.HasPrefix(lines[0], userMessageZoneStart) || !strings.HasPrefix(lines[len(lines)-1], userMessageZoneEnd) {
		t.Fatalf("zone markers missing: %q", lines)
	}
}
