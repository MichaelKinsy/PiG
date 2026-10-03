package tui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/internal/hljs"
)

// Ports packages/coding-agent/test/syntax-highlight.test.ts:37-98 (the "syntax highlight renderer" cases).
func TestSyntaxHighlightRendererUpstream(t *testing.T) {
	// syntax-highlight.test.ts:37 "loads the twenty most common languages at startup and defers the rest".
	t.Run("loads the twenty most common languages at startup and defers the rest", func(t *testing.T) {
		eagerLanguages := []string{"python", "java", "go", "javascript", "cpp", "typescript", "php", "ruby", "c", "csharp", "nix", "bash", "rust", "scala", "kotlin", "swift", "dart", "groovy", "perl", "lua"}
		startup := hljs.NewRegistry()
		for _, language := range eagerLanguages {
			if !startup.SupportsLanguage(language) {
				t.Errorf("%s is not loaded at startup", language)
			}
		}
		if startup.SupportsLanguage("ada") {
			t.Fatal("ada is loaded at startup")
		}
		startup.LoadAllLanguages()
		if !startup.SupportsLanguage("ada") {
			t.Fatal("ada is not loaded after loading all languages")
		}
		LoadAllHighlightLanguages()
		if !SupportsLanguage("ada") {
			t.Fatal("LoadAllHighlightLanguages did not load ada")
		}
	})
	// syntax-highlight.test.ts:44 "renders highlighted spans with the provided theme".
	t.Run("renders highlighted spans with the provided theme", func(t *testing.T) {
		rendered := RenderHighlightedHtml(`<span class="hljs-keyword">const</span> value`, HighlightTheme{
			"keyword": func(text string) string { return "[keyword:" + text + "]" },
		})
		if rendered != "[keyword:const] value" {
			t.Fatalf("rendered %q", rendered)
		}
	})
	// syntax-highlight.test.ts:51 "decodes HTML entities emitted by highlight.js".
	t.Run("decodes HTML entities emitted by highlight.js", func(t *testing.T) {
		rendered := RenderHighlightedHtml("&lt;tag attr=&quot;value&quot;&gt;&amp;#x41;&#65;&lt;/tag&gt;", nil)
		if rendered != `<tag attr="value">&#x41;A</tag>` {
			t.Fatalf("rendered %q", rendered)
		}
	})
	// syntax-highlight.test.ts:56 "inherits parent formatting for unmapped nested scopes".
	t.Run("inherits parent formatting for unmapped nested scopes", func(t *testing.T) {
		interpolation := "$" + "{x}"
		rendered := RenderHighlightedHtml(`<span class="hljs-string">a<span class="hljs-subst">`+interpolation+`</span>b</span>`, HighlightTheme{
			"string": func(text string) string { return "[string:" + text + "]" },
		})
		if want := "[string:a][string:" + interpolation + "][string:b]"; rendered != want {
			t.Fatalf("rendered %q, want %q", rendered, want)
		}
	})
	// syntax-highlight.test.ts:67 "keeps parent formatting across unscoped nested spans".
	t.Run("keeps parent formatting across unscoped nested spans", func(t *testing.T) {
		rendered := RenderHighlightedHtml(`<span class="hljs-string">a<span class="language-xml">b</span>c</span>`, HighlightTheme{
			"string": func(text string) string { return "[string:" + text + "]" },
		})
		if rendered != "[string:a][string:b][string:c]" {
			t.Fatalf("rendered %q", rendered)
		}
	})
	// syntax-highlight.test.ts:74 "highlights code through highlight.js".
	t.Run("highlights code through highlight.js", func(t *testing.T) {
		if !SupportsLanguage("typescript") {
			t.Fatal("typescript is not supported")
		}
		rendered, err := Highlight("const value = 1", HighlightOptions{
			Language:       "typescript",
			IgnoreIllegals: true,
			Theme: HighlightTheme{
				"keyword": func(text string) string { return "[keyword:" + text + "]" },
				"number":  func(text string) string { return "[number:" + text + "]" },
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(rendered, "[keyword:const]") || !strings.Contains(rendered, "[number:1]") {
			t.Fatalf("rendered %q", rendered)
		}
	})
}

// String interpolations take the string color through renderHighlightedHtml's scope inheritance; a styled nested token keeps its own color. Expected bytes are Pi 1.0.0 highlightCode output for the dark theme with truecolor.
func TestSyntaxHighlightInterpolationInheritsTheString(t *testing.T) {
	withTrueColor(t, true)
	SetTheme("dark")
	styled := func(token, text string) string { return ActiveTheme().FgText(token, text) }
	for _, tc := range []struct{ lang, code, want string }{
		{"javascript", "`a${x}b`", styled("syntaxString", "`a") + styled("syntaxString", "${x}") + styled("syntaxString", "b`")},
		{"javascript", "`a${1}b`", styled("syntaxString", "`a") + styled("syntaxString", "${") + styled("syntaxNumber", "1") + styled("syntaxString", "}") + styled("syntaxString", "b`")},
		{"python", `f"a{x}b"`, styled("syntaxString", `f"a`) + styled("syntaxString", "{x}") + styled("syntaxString", `b"`)},
	} {
		if got := HighlightCode(tc.code, tc.lang)[0]; got != tc.want {
			t.Errorf("%s %q: got %q, want %q", tc.lang, tc.code, got, tc.want)
		}
	}
}

// Interpolation inheritance ends at the string's closing delimiter, as renderHighlightedHtml pops the nested scope at the string's closing span, so code after the string stays out of the string color.
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
