package tui

import (
	"regexp"
	"strings"
	"testing"
)

// Ports packages/coding-agent/test/syntax-highlight.test.ts:37-98 through the public highlighter. Upstream drives highlight.js and its HTML renderer; PiG lexes with chroma, which emits tokens rather than HTML. Case :37 keeps only its eager-availability half (chroma registers every lexer statically, so upstream's deferred-loading assertion that "ada" is unsupported before loadAllHighlightLanguages has no Go counterpart). Case :67 (an unscoped nested span such as language-xml inside a string) is designed out: chroma's delegating lexers emit typed tokens, so no unscoped nested span exists.
func TestSyntaxHighlightRendererUpstream(t *testing.T) {
	withTrueColor(t, true)
	SetTheme("dark")
	th := ActiveTheme()
	styled := func(fg, text string) string { return fg + text + SGRFgReset }

	// syntax-highlight.test.ts:37 "loads the twenty most common languages at startup and defers the rest", eager half; "ada" also highlights.
	t.Run("supports the twenty most common languages and the uncommon rest", func(t *testing.T) {
		samples := map[string]string{
			"python": "def f():\n    return 1", "java": "public class A { int x = 1; }", "go": "func main() { return }",
			"javascript": "const x = 1", "cpp": "int main() { return 0; }", "typescript": "const x: number = 1",
			"php": "<?php function f() { return 1; }", "ruby": "def f\n  1\nend", "c": "int main(void) { return 0; }",
			"csharp": "public class A { int x = 1; }", "nix": "let x = 1; in x", "bash": "if true; then echo hi; fi",
			"rust": "fn main() { let x = 1; }", "scala": "def f = 1", "kotlin": "fun f() = 1", "swift": "func f() -> Int { 1 }",
			"dart": "int f() { return 1; }", "groovy": "def x = 1", "perl": "my $x = 1;", "lua": "local x = 1",
			"ada": "procedure Main is begin null; end Main;",
		}
		if len(samples) != 21 {
			t.Fatalf("samples=%d, want the twenty eager languages plus ada", len(samples))
		}
		for language, code := range samples {
			lines := HighlightCode(code, language)
			if !strings.Contains(strings.Join(lines, "\n"), "\x1b[38;2;") {
				t.Errorf("%s: highlighter left the code unstyled: %q", language, lines)
			}
			if got := stripANSI(strings.Join(lines, "\n")); got != code {
				t.Errorf("%s: text changed: %q", language, got)
			}
		}
	})
	// syntax-highlight.test.ts:44 "renders highlighted spans with the provided theme": exact styled output for a keyword followed by plain text.
	t.Run("renders highlighted spans with the theme", func(t *testing.T) {
		if got, want := HighlightCode("const value", "typescript")[0], styled(th.SyntaxKeyword, "const")+" value"; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
	// syntax-highlight.test.ts:51 "decodes HTML entities emitted by highlight.js": markup text and character references reach the output byte-for-byte.
	t.Run("keeps markup and character references as written", func(t *testing.T) {
		const source = `<tag attr="value">&#x41;A</tag>`
		if got := stripANSI(strings.Join(HighlightCode(source, "html"), "\n")); got != source {
			t.Fatalf("text=%q, want %q", got, source)
		}
	})
	// syntax-highlight.test.ts:56 "inherits parent formatting for unmapped nested scopes": an interpolation inside a string inherits the string style as one segment.
	t.Run("inherits parent formatting for unmapped nested scopes", func(t *testing.T) {
		interpolation := "$" + "{x}"
		want := styled(th.SyntaxString, "`a") + styled(th.SyntaxString, interpolation) + styled(th.SyntaxString, "b`")
		if got := HighlightCode("`a"+interpolation+"b`", "javascript")[0]; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		wantNumber := styled(th.SyntaxString, "`a") + styled(th.SyntaxString, "${") + styled(th.SyntaxNumber, "1") + styled(th.SyntaxString, "}") + styled(th.SyntaxString, "b`")
		if got := HighlightCode("`a${1}b`", "javascript")[0]; got != wantNumber {
			t.Fatalf("styled nested token: got %q, want %q", got, wantNumber)
		}
		wantPython := styled(th.SyntaxString, "f") + styled(th.SyntaxString, `"a`) + styled(th.SyntaxString, "{x}") + styled(th.SyntaxString, `b"`)
		if got := HighlightCode(`f"a{x}b"`, "python")[0]; got != wantPython {
			t.Fatalf("python interpolation: got %q, want %q", got, wantPython)
		}
	})
	// syntax-highlight.test.ts:74 "highlights code through highlight.js".
	t.Run("highlights code with the lexer", func(t *testing.T) {
		got := HighlightCode("const value = 1", "typescript")[0]
		if !strings.Contains(got, styled(th.SyntaxKeyword, "const")) || !strings.Contains(got, styled(th.SyntaxNumber, "1")) {
			t.Fatalf("got %q", got)
		}
	})
}

// Interpolation inheritance ends at the string's closing delimiter, as renderHighlightedHtml pops the nested scope at the string's closing span (utils/syntax-highlight.ts:146-198). chroma.Coalesce merges adjacent delimiters ("}${", "}{", "}#{", ")\(", "}}") and some lexers fold a conversion into the closer ("!r}", "=}"), so code after the string must stay unstyled by the string color.
func TestSyntaxHighlightInterpolationEndsWithTheString(t *testing.T) {
	withTrueColor(t, true)
	SetTheme("dark")
	th := ActiveTheme()
	for _, tc := range []struct{ name, lang, code, tail string }{
		{"js adjacent", "javascript", "const s = `${a}${b}`; tailcall(tailarg, tailtwo)", "tailcall tailarg tailtwo"},
		{"ts adjacent", "typescript", "const s = `${a}${b}`; tailcall(tailarg)", "tailcall tailarg"},
		{"js adjacent then comment", "javascript", "const s = `${a}${b}`; // note\ntailcall(tailarg)", "tailcall tailarg"},
		{"python adjacent", "python", `print(f"{x}{y}", tailz)`, "tailz"},
		{"python conversion", "python", `print(f"{x!r}", tailz)`, "tailz"},
		{"python debug", "python", `print(f"{x=}", tailz)`, "tailz"},
		{"ruby adjacent", "ruby", `"#{a}#{b}"; tailfoo tailbar`, "tailfoo tailbar"},
		{"ruby block braces", "ruby", `"#{x.map { |y| y }}" + tailz`, "tailz"},
		{"swift adjacent", "swift", `let s = "\(a)\(b)"; let tailz = tailw`, "tailz tailw"},
		{"scala adjacent", "scala", `val s = s"${a}${b}"; val tailz = tailw`, "tailz tailw"},
		{"nix adjacent", "nix", `{ s = "${a}${b}"; tailt = c; }`, "tailt"},
		{"bash nested default", "bash", `echo "${a:-${b}}" tailfoo tailbar`, "tailfoo tailbar"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := strings.Join(HighlightCode(tc.code, tc.lang), "\n")
			for word := range strings.FieldsSeq(tc.tail) {
				if regexp.MustCompile(regexp.QuoteMeta(th.SyntaxString) + `[^\x1b]*` + word + `[^\x1b]*\x1b\[39m`).MatchString(out) {
					t.Errorf("%q after the string is painted in the string color: %q", word, out)
				}
			}
		})
	}
	nested := HighlightCode(`let s = "\(f(x) + y)"; let tailz = tailw`, "swift")[0]
	if !strings.Contains(nested, th.SyntaxString+`\(f(x)`) {
		t.Errorf("nested parens lost inheritance: %q", nested)
	}
	// The interpolation continues after the nested call closes, so " y)" keeps the string style.
	if !strings.Contains(nested, th.SyntaxString+" y)") {
		t.Errorf("interpolation ended at the nested paren: %q", nested)
	}
	// hljs keeps the string style across a multi-line substitution.
	multi := strings.Join(HighlightCode("`${\n  bodyx\n}`", "javascript"), "\n")
	if !regexp.MustCompile(regexp.QuoteMeta(th.SyntaxString) + `[^\x1b]*bodyx[^\x1b]*\x1b\[39m`).MatchString(multi) {
		t.Errorf("multi-line substitution body not string-colored: %q", multi)
	}
}
