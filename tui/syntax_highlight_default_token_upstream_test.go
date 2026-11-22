package tui

import (
	"slices"
	"strings"
	"testing"
)

// Ports packages/coding-agent/test/syntax-highlight.test.ts:108-140 (Pi 562da75f9, #12347):
// unscoped highlighted text takes the active theme foreground ("text") instead of the terminal default.
func TestSyntaxHighlightDefaultTokenUpstream(t *testing.T) {
	withTrueColor(t, true)
	orig := ActiveTheme().Name
	t.Cleanup(func() { SetTheme(orig) })
	LoadAllHighlightLanguages()

	t.Run("colors each line of Python docstrings independently and trailing plain text with theme text color", func(t *testing.T) {
		SetTheme("dark")
		code := "\"\"\"\nline one\n\nline two\n\"\"\"\nafter"
		ansi := ActiveTheme().GetFgAnsi("syntaxString")
		expected := []string{
			ansi + "\"\"\"\x1b[39m",
			ansi + "line one\x1b[39m",
			"",
			ansi + "line two\x1b[39m",
			ansi + "\"\"\"\x1b[39m",
			ActiveTheme().GetFgAnsi("text") + "after\x1b[39m",
		}
		if got := HighlightCode(code, "python"); !slices.Equal(got, expected) {
			t.Fatalf("HighlightCode:\n got=%q\nwant=%q", got, expected)
		}
		if got := NewMarkdownTheme().HighlightCode(code, "python"); !slices.Equal(got, expected) {
			t.Fatalf("MarkdownTheme.HighlightCode:\n got=%q\nwant=%q", got, expected)
		}
	})

	// Regression for #11092.
	t.Run("colors unscoped highlighted code with the theme foreground", func(t *testing.T) {
		SetTheme("light")
		lines := HighlightCode("plainIdentifier\nconst answer = 42;", "typescript")
		text := ActiveTheme().GetFgAnsi("text")
		if want := text + "plainIdentifier\x1b[39m"; lines[0] != want {
			t.Fatalf("line 0:\n got=%q\nwant=%q", lines[0], want)
		}
		if wantSub := text + " answer = \x1b[39m"; !strings.Contains(lines[1], wantSub) {
			t.Fatalf("line 1 %q does not contain %q", lines[1], wantSub)
		}
	})
}

// Runtime rendering check: a highlighted code block in Markdown (assistant message path)
// across light and dark themes, covering text between tokens, an empty line, and a nested
// token whose inner reset returns to the theme text color rather than the terminal default.
func TestMarkdownHighlightedCodeBlockDefaultTokenColors(t *testing.T) {
	withTrueColor(t, true)
	orig := ActiveTheme().Name
	t.Cleanup(func() { SetTheme(orig) })
	LoadAllHighlightLanguages()

	for _, themeName := range []string{"light", "dark"} {
		t.Run(themeName, func(t *testing.T) {
			SetTheme(themeName)
			textFg := ActiveTheme().GetFgAnsi("text")
			kwFg := ActiveTheme().GetFgAnsi("syntaxKeyword")
			numFg := ActiveTheme().GetFgAnsi("syntaxNumber")

			// Markdown fenced code block with:
			// - plain identifier line
			// - empty line
			// - keyword + plain text between tokens + number + trailing semicolon
			md := "```typescript\nplainIdentifier\n\nconst answer = 42;\n```"
			rendered := strings.Join(NewMarkdown(md).Render(80), "\n")

			if !strings.Contains(rendered, textFg+"plainIdentifier\x1b[39m") {
				t.Fatalf("theme=%s: plain identifier lacks text fg %q in:\n%q", themeName, textFg, rendered)
			}
			if !strings.Contains(rendered, kwFg+"const\x1b[39m"+textFg+" answer = \x1b[39m"+numFg+"42\x1b[39m"+textFg+";\x1b[39m") {
				t.Fatalf("theme=%s: text between tokens lacks text fg %q in:\n%q", themeName, textFg, rendered)
			}

			// Nested token: inner span closes and subsequent unscoped text must take textFg.
			nested := RenderHighlightedHtml(
				`outer <span class="hljs-keyword">inner</span> after`,
				cliHighlightTheme(ActiveTheme()),
			)
			wantNested := textFg + "outer \x1b[39m" + kwFg + "inner\x1b[39m" + textFg + " after\x1b[39m"
			if nested != wantNested {
				t.Fatalf("theme=%s: nested reset:\n got=%q\nwant=%q", themeName, nested, wantNested)
			}
		})
	}
}
