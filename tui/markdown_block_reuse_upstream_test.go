package tui

import (
	"runtime"
	"slices"
	"strings"
	"testing"
)

// countingMarkdownTheme is the upstream test theme whose highlighter records each call as "lang:code" and colors every
// line green (markdown.test.ts countingTheme).
func countingMarkdownTheme(calls *[]string) *MarkdownTheme {
	theme := mdUpstreamTheme()
	theme.HighlightCode = func(code, lang string) []string {
		*calls = append(*calls, lang+":"+code)
		lines := strings.Split(code, "\n")
		for i, line := range lines {
			lines[i] = mdChalk("green")(line)
		}
		return lines
	}
	return theme
}

// Ports the "Block reuse" tests of packages/tui/test/markdown.test.ts.
func TestUpstreamMarkdownBlockReuse(t *testing.T) {
	t.Run("highlights a code block once across streamed text and resizes, and again after invalidate", func(t *testing.T) {
		mdCaseSetup(t)
		pauseGC(t) // the renders run in one JavaScript job, which keeps a WeakRef's target alive
		var calls []string
		markdown := NewMarkdownWithOptions("```ts\nx\n```", 0, 0, countingMarkdownTheme(&calls), nil, nil)
		markdown.Render(80)
		markdown.SetText("```ts\nx\n```\n\nafter")
		markdown.Render(40)
		if want := []string{"ts:x"}; !slices.Equal(calls, want) {
			t.Fatalf("calls = %q, want %q", calls, want)
		}
		markdown.Invalidate()
		markdown.Render(40)
		if want := []string{"ts:x", "ts:x"}; !slices.Equal(calls, want) {
			t.Fatalf("calls after Invalidate = %q, want %q", calls, want)
		}
	})

	t.Run("holds rendered blocks weakly, so a full garbage collection releases them", func(t *testing.T) {
		mdCaseSetup(t)
		var calls []string
		markdown := NewMarkdownWithOptions("```ts\nx\n```", 0, 0, countingMarkdownTheme(&calls), nil, nil)
		markdown.Render(80)
		// Nothing but the weak reference holds the memos once Render returns, as nothing holds a WeakRef's target once its job ends.
		runtime.GC()
		if markdown.memosRef.Value() != nil {
			t.Fatal("the memos of the last render survived a full garbage collection")
		}
		markdown.Render(40)
		if want := []string{"ts:x", "ts:x"}; !slices.Equal(calls, want) {
			t.Fatalf("calls = %q, want %q", calls, want)
		}
	})

	t.Run("releases the reused highlighting when streaming finishes", func(t *testing.T) {
		mdCaseSetup(t)
		pauseGC(t)
		var calls []string
		markdown := NewMarkdownWithOptions("```ts\nx\n```", 0, 0, countingMarkdownTheme(&calls), nil, nil)
		markdown.Render(80)
		markdown.FinishStreaming()
		markdown.Render(40)
		if want := []string{"ts:x", "ts:x"}; !slices.Equal(calls, want) {
			t.Fatalf("calls = %q, want %q", calls, want)
		}
	})

	t.Run("keeps only the code of its last render", func(t *testing.T) {
		mdCaseSetup(t)
		pauseGC(t)
		var calls []string
		markdown := NewMarkdownWithOptions("```ts\nx\n```", 0, 0, countingMarkdownTheme(&calls), nil, nil)
		markdown.Render(80)
		markdown.SetText("```ts\ny\n```")
		markdown.Render(80)
		markdown.SetText("```ts\nx\n```")
		markdown.Render(80)
		if want := []string{"ts:x", "ts:y", "ts:x"}; !slices.Equal(calls, want) {
			t.Fatalf("calls = %q, want %q", calls, want)
		}
	})

	t.Run("reuses the highlighting of code inside a list item and a quote", func(t *testing.T) {
		mdCaseSetup(t)
		pauseGC(t)
		var calls []string
		text := "- item\n\n  ```ts\n  x\n  ```\n\n> ```ts\n> y\n> ```"
		markdown := NewMarkdownWithOptions(text, 0, 0, countingMarkdownTheme(&calls), nil, nil)
		markdown.Render(80)
		markdown.Render(30)
		markdown.SetText(text + "\n\nafter")
		markdown.Render(80)
		if want := []string{"ts:x", "ts:y"}; !slices.Equal(calls, want) {
			t.Fatalf("calls = %q, want %q", calls, want)
		}
	})

	t.Run("renders a block again when the block after it changes its spacing", func(t *testing.T) {
		mdCaseSetup(t)
		markdown := NewMarkdownWithOptions("# Title", 0, 0, mdUpstreamTheme(), nil, nil)
		markdown.Render(80)
		markdown.SetText("# Title\nfoo")
		got := make([]string, 0, 3)
		for _, line := range markdown.Render(80) {
			got = append(got, strings.TrimSpace(stripANSI(line)))
		}
		if want := []string{"Title", "", "foo"}; !slices.Equal(got, want) {
			t.Fatalf("lines = %q, want %q", got, want)
		}
	})

	t.Run("follows changed reference link definitions", func(t *testing.T) {
		previous := GetCapabilities()
		t.Cleanup(func() { SetCapabilities(previous) })
		SetCapabilities(TerminalCapabilities{})
		markdown := NewMarkdownWithOptions("[a][x]\n\n[x]: https://one.example", 0, 0, mdUpstreamTheme(), nil, nil)
		markdown.Render(80)
		markdown.SetText("[a][x]\n\n[x]: https://two.example")
		if out := stripANSI(strings.Join(markdown.Render(80), "\n")); !strings.Contains(out, "https://two.example") {
			t.Fatalf("a changed definition was not followed: %q", out)
		}
	})
}

// A Markdown that reuses the highlighting of its earlier renders renders every prefix of a document, at a width that
// changes during the stream, exactly as a new component given that prefix does, for a highlighter tui does not know.
func TestMarkdownReusedHighlightingEqualsAFreshRender(t *testing.T) {
	mdCaseSetup(t)
	document := "Intro **text**.\n\n```go\nfunc a() {}\n```\n\n- item\n\n  ```ts\n  const x = 1\n  ```\n\n> ```py\n> y = 2\n> ```\n\n```go\nfunc a() {}\n```\n\nEnd.\n"
	var calls []string
	streamed := NewMarkdownWithOptions("", 1, 0, countingMarkdownTheme(&calls), nil, nil)
	for end := 0; end <= len(document); end++ {
		width := 20 + end%40
		prefix := document[:end]
		streamed.SetText(prefix)
		var ignored []string
		want := NewMarkdownWithOptions(prefix, 1, 0, countingMarkdownTheme(&ignored), nil, nil).Render(width)
		if got := streamed.Render(width); !slices.Equal(got, want) {
			t.Fatalf("prefix of %d bytes at width %d: %q\n got %q\nwant %q", end, width, prefix, got, want)
		}
	}
}
