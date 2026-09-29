package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// Pi user-message.ts retains its transformer definitions across padding rebuilds. The Go host keeps the Markdown child so a padding change cannot discard its async transform owner or external-state cache key.
func TestUserMessagePaddingRetainsTransformState(t *testing.T) {
	block := NewUserMessageBlock("message")
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
	block := NewUserMessageBlock("message")
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
	block := NewUserMessageBlock("raw")
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
