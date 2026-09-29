package tui

import (
	"strings"
	"testing"
)

// Ports packages/coding-agent/test/syntax-highlight.test.ts:99-118. The earlier utility cases exercise the Node runtime rather than the native lexer.
func TestSyntaxHighlightThemeUpstream(t *testing.T) {
	withTrueColor(t, true)
	SetTheme("dark")
	t.Run("colors diff additions and deletions in fenced diff blocks", func(t *testing.T) {
		lines := HighlightCode("-old\n+new\n", "diff")
		if lines[0] != "\x1b[38;2;204;102;102m-old\x1b[39m" || lines[1] != "\x1b[38;2;181;189;104m+new\x1b[39m" {
			t.Fatalf("diff colors=%q", lines)
		}
	})
	t.Run("preserves line count without a trailing newline", func(t *testing.T) {
		lines := HighlightCode("-old\n+new", "diff")
		if len(lines) != 2 || lines[0] != "\x1b[38;2;204;102;102m-old\x1b[39m" || lines[1] != "\x1b[38;2;181;189;104m+new\x1b[39m" {
			t.Fatalf("diff without trailing newline=%q", lines)
		}
	})
	t.Run("keeps cli-highlight default styled scopes mapped to theme styles", func(t *testing.T) {
		if got := HighlightCode("const re = /foo+/gi;", "javascript")[0]; !strings.Contains(got, "\x1b[38;2;206;145;120m/foo+/gi\x1b[39m") {
			t.Fatalf("regexp color=%q", got)
		}
		if got := HighlightCode("@decorator", "python")[0]; got != "\x1b[38;2;128;128;128m@decorator\x1b[39m" {
			t.Fatalf("meta color=%q", got)
		}
		if got := HighlightCode("<div></div>", "html")[0]; !strings.Contains(got, "\x1b[38;2;86;156;214mdiv\x1b[39m") {
			t.Fatalf("tag name color=%q", got)
		}
	})
}

func TestMarkdownSyntaxThemeScopes(t *testing.T) {
	withTrueColor(t, true)
	SetTheme("dark")
	// Markdown renders fenced code through the shared theme highlighter, as packages/tui/src/components/markdown.ts:renderToken does for code tokens.
	for _, tc := range []struct{ language, code, want string }{
		{"diff", "-old\n+new", "\x1b[38;2;204;102;102m-old\x1b[39m"},
		{"diff", "-old\n+new", "\x1b[38;2;181;189;104m+new\x1b[39m"},
		{"python", "@decorator", "\x1b[38;2;128;128;128m@decorator\x1b[39m"},
		{"html", "<div></div>", "\x1b[38;2;86;156;214mdiv\x1b[39m"},
	} {
		t.Run(tc.language, func(t *testing.T) {
			out := strings.Join(NewMarkdown("```"+tc.language+"\n"+tc.code+"\n```").Render(80), "\n")
			if !strings.Contains(out, tc.want) {
				t.Fatalf("fenced code lacks theme scope %q: %q", tc.want, out)
			}
		})
	}
}

func BenchmarkSyntaxThemeScopes(b *testing.B) {
	for _, tc := range []struct{ language, code string }{{"diff", "-old\n+new\n"}, {"python", "@decorator\ndef function(value):\n    return value\n"}, {"html", "<div class=\"content\">text</div>\n"}} {
		b.Run(tc.language, func(b *testing.B) {
			code := strings.Repeat(tc.code, 100)
			b.ReportAllocs()
			for b.Loop() {
				highlightCodeUncached(code, tc.language, ActiveTheme())
			}
		})
	}
}
