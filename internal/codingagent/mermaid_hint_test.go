package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

func transformMermaid(t *testing.T, md string, streaming bool, width int) string {
	t.Helper()
	tr := createMermaidMarkdownTransformer(func() string { return "streaming" }, nil)
	return tr(md, extension.MarkdownTransformContext{
		MessageType:    extension.MarkdownMessageAssistant,
		IsStreaming:    streaming,
		AvailableWidth: width,
	})
}

func fence(body string) string { return "```mermaid\n" + body + "\n```" }

// TestMermaidUnrenderableHint covers D50: a diagram PiG declines
// to draw gets one hint line naming the cause: a grammar rejection, or an
// overflow reported with both widths: instead of upstream's silent fallback to
// the raw code fence.
func TestMermaidUnrenderableHint(t *testing.T) {
	const hintPrefix = "Mermaid diagram not rendered:"

	t.Run("semicolon inside a statement is named", func(t *testing.T) {
		// ';' is mermaid's statement separator, so `A->>B: a;b` parses as a
		// message plus a bogus statement `b` and the whole diagram fails. This
		// is the case that silently produced a raw dump.
		out := transformMermaid(t, fence("sequenceDiagram\n  A->>B: seq [1;1:1A]"), false, 200)
		if !strings.Contains(out, hintPrefix) {
			t.Fatalf("no hint emitted:\n%s", out)
		}
		if !strings.Contains(out, "';'") || !strings.Contains(out, "quote") {
			t.Errorf("hint does not name the semicolon cause or the fix:\n%s", out)
		}
		if !strings.Contains(out, "sequence") {
			t.Errorf("hint does not name the diagram kind:\n%s", out)
		}
		// The source must survive: the hint explains, it does not replace.
		if !strings.Contains(out, "A->>B: seq [1;1:1A]") {
			t.Errorf("raw source lost:\n%s", out)
		}
	})

	t.Run("unrecognized diagram type is named", func(t *testing.T) {
		out := transformMermaid(t, fence("gitGraph\n  commit"), false, 200)
		if !strings.Contains(out, "unrecognized diagram type") {
			t.Errorf("want unrecognized-type hint, got:\n%s", out)
		}
	})

	t.Run("no semicolon claim when there is none", func(t *testing.T) {
		out := transformMermaid(t, fence("sequenceDiagram\n  A->>"), false, 200)
		if strings.Contains(out, "';'") {
			t.Errorf("hint blames a semicolon that is not present:\n%s", out)
		}
	})

	t.Run("trailing semicolon is not blamed", func(t *testing.T) {
		// A trailing ';' is a valid terminator and renders, so no hint at all.
		out := transformMermaid(t, fence("sequenceDiagram\n  A->>B: hi;"), false, 200)
		if strings.Contains(out, hintPrefix) {
			t.Errorf("valid diagram produced a hint:\n%s", out)
		}
	})

	t.Run("suppressed while streaming", func(t *testing.T) {
		// A half-typed diagram fails on nearly every delta; hinting there would
		// flicker a warning under the user's cursor as they type.
		out := transformMermaid(t, fence("sequenceDiagram\n  A->>B: seq [1;1:1A]"), true, 200)
		if strings.Contains(out, hintPrefix) {
			t.Errorf("hint emitted while streaming:\n%s", out)
		}
	})

	t.Run("oversized diagram names the measured width", func(t *testing.T) {
		// The case that cost three round trips in practice: the diagram parses
		// and lays out fine, it is simply wider than the area, and the silent
		// fallback is indistinguishable from a syntax error. Naming both numbers
		// is the whole fix: the author then knows to shorten, not to re-guess
		// the grammar.
		md := fence("sequenceDiagram\n  participant Alice\n  participant Bob\n  Alice->>Bob: hello there")
		out := transformMermaid(t, md, false, 12)
		if !strings.Contains(out, hintPrefix) {
			t.Fatalf("no hint emitted for an oversized diagram:\n%s", out)
		}
		if !strings.Contains(out, "12") {
			t.Errorf("hint does not name the available width:\n%s", out)
		}
		if !strings.Contains(out, "columns") {
			t.Errorf("hint does not name the measured width:\n%s", out)
		}
		// The source must survive: the hint explains, it does not replace.
		if !strings.Contains(out, "Alice->>Bob: hello there") {
			t.Errorf("raw source lost:\n%s", out)
		}
	})

	t.Run("oversized hint is suppressed while streaming", func(t *testing.T) {
		// Same reason as the grammar path: a diagram grows as it is typed and
		// would exceed the area on most deltas.
		md := fence("sequenceDiagram\n  participant Alice\n  participant Bob\n  Alice->>Bob: hello there")
		out := transformMermaid(t, md, true, 12)
		if strings.Contains(out, hintPrefix) {
			t.Errorf("oversized hint emitted while streaming:\n%s", out)
		}
	})

	t.Run("unknown available width stays silent", func(t *testing.T) {
		// Width 0 means the caller has not measured the area yet (early layout).
		// "0 columns available" is not an actionable claim, so say nothing.
		md := fence("sequenceDiagram\n  A->>B: hello")
		out := transformMermaid(t, md, false, 0)
		if strings.Contains(out, hintPrefix) {
			t.Errorf("hint emitted with an unmeasured width:\n%s", out)
		}
		if out != md {
			t.Errorf("want silent raw fallback, got:\n%s", out)
		}
	})

	t.Run("valid diagram renders with no hint", func(t *testing.T) {
		out := transformMermaid(t, fence("sequenceDiagram\n  A->>B: hello"), false, 200)
		if strings.Contains(out, hintPrefix) {
			t.Errorf("hint emitted for a good diagram:\n%s", out)
		}
		if !strings.ContainsAny(out, "┌└─▶│") {
			t.Errorf("diagram not drawn:\n%s", out)
		}
	})
}

func TestHasInlineSemicolon(t *testing.T) {
	tests := []struct {
		src  string
		want bool
	}{
		{"A->>B: a;b", true},
		{"A->>B: seq [1;1:1A]", true},
		{"A->>B: hi;", false}, // terminator, not separator
		{"A->>B: hi", false},  // none
		{"A->>B: hi;\n  x", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := hasInlineSemicolon(tc.src); got != tc.want {
			t.Errorf("hasInlineSemicolon(%q) = %v, want %v", tc.src, got, tc.want)
		}
	}
}

// The hint is display-only. It is produced by a Markdown render transform, and
// the block's text is what the session stores and what later requests carry, so
// the hint reaches the person watching and never reaches the model.
//
// D50 is justified on the model acting on the hint as well as the reader. That
// half does not hold, and this test states the boundary so a future change that
// routes the hint into model-visible content is a deliberate one.
func TestTheMermaidHintNeverEntersModelVisibleText(t *testing.T) {
	const width = 60
	// A diagram the grammar rejects: a ';' inside a statement splits it. Uses a
	// parse failure rather than an overflow, because D58 narrows an over-wide
	// diagram until it fits, so overflow no longer reliably produces a hint.
	source := "```mermaid\nsequenceDiagram\n  A->>B: seq [1;1:1A]\n```\n"

	block := tui.NewAssistantMessageBlock(false)
	block.SetMarkdownTransformState(func() string { return "settled" })
	block.SetMarkdownTransform(func(markdown string, w int) string {
		ts := []extension.MarkdownTransformer{
			createMermaidMarkdownTransformer(func() string { return "streaming" }, nil),
		}
		return createMarkdownTransform(extension.MarkdownMessageAssistant, false, ts)(markdown, w)
	})
	block.SetTextDelta(source)

	painted := strings.Join(block.Render(width), "\n")
	if !strings.Contains(painted, "Mermaid diagram not rendered") {
		t.Fatalf("the diagram was expected to overflow %d columns and be hinted:\n%s", width, painted)
	}
	if block.Text() != source {
		t.Errorf("rendering changed the stored assistant text:\ngot  %q\nwant %q", block.Text(), source)
	}
}
