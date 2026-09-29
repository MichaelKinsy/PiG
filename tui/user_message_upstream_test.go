package tui

import (
	"strings"
	"testing"
)

// .upstream/v0.87.1/packages/coding-agent/test/user-message.test.ts:12
func TestUpstreamUserMessageOSCMarkers(t *testing.T) {
	t.Run("keeps user message height stable while moving closing OSC markers off line end", func(t *testing.T) {
		component := NewUserMessageBlock("hello")
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

// .upstream/v0.87.1/packages/coding-agent/test/user-message.test.ts:46
func TestUpstreamUserMessageInvalidation(t *testing.T) {
	t.Run("reapplies Markdown transformers when invalidated", func(t *testing.T) {
		suffix := "before"
		component := NewUserMessageBlock("Message")
		component.SetMarkdownTransform(func(markdown string, _ int) string { return markdown + " " + suffix })
		upstreamContains(t, stripANSI(strings.Join(component.Render(80), "\n")), "Message before")
		suffix = "after"
		component.Invalidate()
		upstreamContains(t, stripANSI(strings.Join(component.Render(80), "\n")), "Message after")
	})
}
