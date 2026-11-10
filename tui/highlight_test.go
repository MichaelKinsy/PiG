//go:build !pig_strip_syntax_highlight

package tui

import (
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
)

// TestHighlightCodeConcurrentWithThemeSwitchIsRaceFree reproduces the data
// race reported under `go test -race ./internal/codingagent/...`: one
// goroutine builds a viewport / highlights code (reading ActiveTheme via
// HighlightCode) while another switches the active theme concurrently.
// Before the fix, `activeTheme` in theme.go was a plain `*Theme` package
// variable read by ActiveTheme() and written by SetTheme() with no
// synchronization, so `go test -race` reports a write/read race between
// SetTheme (theme.go) and HighlightCode (highlight.go) even though each
// individual call looks correct in isolation. This is deterministic under
// -race: the fix (an atomic.Pointer[Theme]) makes it pass reliably, and the
// pre-fix bare pointer makes it fail on essentially every run.
func TestHighlightCodeConcurrentWithThemeSwitchIsRaceFree(t *testing.T) {
	prev := ActiveTheme().Name
	t.Cleanup(func() { SetTheme(prev) })

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				SetTheme("light")
				SetTheme("dark")
			}
		}
	})

	for range 2000 {
		_ = HighlightCode("func main() {\n\treturn\n}\n", "go")
	}
	close(stop)
	wg.Wait()
}

func TestHighlightCode_GoStringsAndKeywords(t *testing.T) {
	out := HighlightCode(`package main`, "go")
	if len(out) != 1 {
		t.Fatalf("expected 1 line, got %d", len(out))
	}
	// "package" should be wrapped in syntaxKeyword color.
	if !strings.Contains(out[0], ActiveTheme().SyntaxKeyword+"package") {
		t.Errorf("missing keyword color for `package`: %q", out[0])
	}
}

func TestHighlightCode_MultilineString(t *testing.T) {
	out := HighlightCode("func f() {\n    return\n}", "go")
	if len(out) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(out), out)
	}
	if !strings.Contains(stripANSI(out[0]), "func f() {") {
		t.Errorf("line 0 content lost: %q", out[0])
	}
	if !strings.Contains(stripANSI(out[2]), "}") {
		t.Errorf("line 2 content lost: %q", out[2])
	}
}

// Ports packages/coding-agent/test/syntax-highlight.test.ts "colors each line of Python docstrings independently" (1.0.4,
// #10143): a token that spans lines is formatted once per non-empty line, so every line keeps its color and an empty line
// stays empty.
func TestHighlightCode_ColorsEachLineOfPythonDocstringsIndependently(t *testing.T) {
	code := "\"\"\"\nline one\n\nline two\n\"\"\"\nafter"
	ansi := ActiveTheme().SyntaxString
	want := []string{
		ansi + "\"\"\"\x1b[39m",
		ansi + "line one\x1b[39m",
		"",
		ansi + "line two\x1b[39m",
		ansi + "\"\"\"\x1b[39m",
		"after",
	}
	if got := HighlightCode(code, "python"); !slices.Equal(got, want) {
		t.Errorf("HighlightCode = %q, want %q", got, want)
	}
	if got := HighlightCode(code, "python"); !slices.Equal(got, want) {
		t.Errorf("markdown theme highlightCode = %q, want %q", got, want)
	}
}

// Ports the `subst` entry of theme.ts buildCliHighlightTheme (1.0.4): string interpolations take the text color.
func TestCliHighlightThemeColorsSubstitutionsWithTheTextColor(t *testing.T) {
	theme := cliHighlightTheme(ActiveTheme())
	format, ok := theme["subst"]
	if !ok {
		t.Fatal("the highlight theme has no subst entry")
	}
	if got, want := format("${x}"), ActiveTheme().Fg("text", "${x}"); got != want {
		t.Errorf("subst = %q, want %q", got, want)
	}
}

func TestHighlightCode_UnknownLanguageFallsBack(t *testing.T) {
	out := HighlightCode("hello\nworld", "klingon")
	if len(out) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(out))
	}
	// Should be wrapped in MDCodeBlock color (fallback path).
	if !strings.Contains(out[0], ActiveTheme().MDCodeBlock) {
		t.Errorf("fallback should use MDCodeBlock color: %q", out[0])
	}
}

func TestHighlightCode_EmptyLanguageFallsBack(t *testing.T) {
	out := HighlightCode("plain text", "")
	if len(out) != 1 {
		t.Fatalf("expected 1 line, got %d", len(out))
	}
	if !strings.Contains(out[0], "plain text") {
		t.Errorf("content lost: %q", out[0])
	}
}

func TestHighlightCode_PreservesLineCount(t *testing.T) {
	src := "a\nb\nc\n"
	out := HighlightCode(src, "python")
	// strings.Split adds a trailing empty for the final \n, so 4 lines.
	if len(out) != 4 {
		t.Errorf("line count drift: got %d want 4: %q", len(out), out)
	}
}

func TestLanguageFromPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"foo.go", "go"},
		{"foo.py", "python"},
		{"foo.tsx", "typescript"},
		{"foo.md", "markdown"},
		{"foo.unknownext", ""},
		// Pi main c13f6acfb (syntax-highlight.test.ts "maps file extensions of eagerly loaded languages to their language")
		{"lib/main.dart", "dart"},
		{"flake.nix", "nix"},
		{"script.groovy", "groovy"},
		{"build.gradle", "groovy"},
		{"src/Main.kt", "kotlin"},
		{"Makefile", "makefile"},
		{"Dockerfile", "dockerfile"},
		{"FOO.GO", "go"},
		{"a/b/c.rs", "rust"},
		{"", ""},
		// theme.ts getLanguageFromPath reads the text after the last dot of the whole path, so an extensionless file below a directory maps to nothing.
		{"a/b/Makefile", ""},
		{"x.d/Dockerfile", ""},
		{"file.", ""},
		// toLowerCase maps U+0130 to "i\u0307" and U+212A (Kelvin) to "k" (Pi 1.0.0 getLanguageFromPath under Node 24).
		{"Makef\u0130le", ""},
		{"a.V\u0130M", ""},
		{"x.\u212At", "kotlin"},
	}
	for _, tc := range cases {
		if got := LanguageFromPath(tc.path); got != tc.want {
			t.Errorf("LanguageFromPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// renderHighlightedHtml reads theme[scope] on an object literal, so a scope naming an Object.prototype member finds that member and calls it with an undefined receiver, and an own undefined property shadows the member (utils/syntax-highlight.ts getScopeFormatter and getActiveFormatter). Expected strings and TypeError messages are Pi 1.0.0's renderHighlightedHtml output under Node 24.
func TestRenderHighlightedHtmlReadsObjectPrototypeMembers(t *testing.T) {
	const notObject = "Cannot convert undefined or null to object"
	for _, tc := range []struct {
		scope, want, err string
	}{
		{"constructor", "xy", ""},
		{"toString", "[object Undefined]y", ""},
		{"toString.x", "[object Undefined]y", ""},
		{"isPrototypeOf", "falsey", ""},
		{"isPrototypeOf-y", "falsey", ""},
		{"default", "xy", ""},
		{"toLocaleString", "", "Object.prototype.toLocaleString called on null or undefined"},
		{"__proto__", "", "formatter is not a function"},
		{"valueOf", "", notObject},
		{"hasOwnProperty", "", notObject},
		{"propertyIsEnumerable", "", notObject},
		{"__defineGetter__", "", notObject},
		{"__defineSetter__", "", notObject},
		{"__lookupGetter__", "", notObject},
		{"__lookupSetter__", "", notObject},
	} {
		got, err := renderHighlightedHTML(`<span class="hljs-`+tc.scope+`">x</span>y`, HighlightTheme{})
		gotErr := ""
		if err != nil {
			gotErr = err.Error()
		}
		if got != tc.want || gotErr != tc.err {
			t.Errorf("%s: got %q, error %q; want %q, error %q", tc.scope, got, gotErr, tc.want, tc.err)
		}
	}
	defaultFormatter := func(text string) string { return "D" + text }
	if got := RenderHighlightedHtml(`<span class="hljs-toString">x</span>`, HighlightTheme{"toString": nil, "default": defaultFormatter}); got != "Dx" {
		t.Errorf("own undefined toString: got %q, want %q", got, "Dx")
	}
	if got := RenderHighlightedHtml(`<span class="hljs-keyword">x</span>y`, HighlightTheme{"keyword": nil, "default": defaultFormatter}); got != "DxDy" {
		t.Errorf("own undefined keyword: got %q, want %q", got, "DxDy")
	}
}

// theme.ts highlightCode (Pi 0f21a331) wraps the highlight in `try { ... } catch { return code.split("\n").map((line) => theme.fg("mdCodeBlock", line)); }`, and
// getMarkdownTheme().highlightCode is that same function, so a highlight that throws takes the mdCodeBlock color for both. A theme with no color for a syntax token is a
// highlight that throws: the cli-highlight formatter calls theme.fg, and fg throws `Unknown theme color: <token>`.
func TestHighlightCodeThatFailsTakesTheCodeBlockColor(t *testing.T) {
	full := componentThemeOverride(t)
	broken, err := NewThemeFromANSIPalette("broken", maps.Clone(full.fgAnsi), maps.Clone(full.bgAnsi), full.GetColorMode())
	if err != nil {
		t.Fatal(err)
	}
	delete(broken.fgAnsi, "syntaxKeyword")
	code := "const answer = 42;\nreturn answer"
	want := codeBlockLines(code, broken)
	if slices.Equal(want, strings.Split(code, "\n")) {
		t.Fatal("the mdCodeBlock color of the test theme draws nothing, so it cannot tell a styled fallback from an unstyled one")
	}
	if got := HighlightCodeFor(broken, code, "javascript"); !slices.Equal(got, want) {
		t.Errorf("highlightCode of a failing highlight = %q, Pi gives %q", got, want)
	}
	if got := MarkdownThemeFor(broken).HighlightCode(code, "javascript"); !slices.Equal(got, want) {
		t.Errorf("getMarkdownTheme().highlightCode of a failing highlight = %q, Pi gives %q", got, want)
	}
	// The active theme goes through the memo and the resumable highlighter: a failure is not a panic there either.
	previous := activeTheme.Load()
	t.Cleanup(func() { activeTheme.Store(previous) })
	activeTheme.Store(broken)
	if got := HighlightCode(code, "javascript"); !slices.Equal(got, want) {
		t.Errorf("HighlightCode under the active theme = %q, Pi gives %q", got, want)
	}
}

// A .dart, .nix or .groovy file renders highlighted, not as plain text, through the language the path maps to.
func TestHighlightCodeHighlightsDartNixAndGroovy(t *testing.T) {
	for path, code := range map[string]string{
		"main.dart":    "class A { final int x = 1; }",
		"flake.nix":    "{ pkgs }: let x = 1; in x",
		"build.gradle": "def x = 1\nclass A { String s = 'a' }",
	} {
		lang := LanguageFromPath(path)
		if lang == "" {
			t.Fatalf("LanguageFromPath(%q) is empty", path)
		}
		plain := strings.Split(code, "\n")
		got := HighlightCode(code, lang)
		if slices.Equal(got, plain) || !strings.Contains(strings.Join(got, "\n"), "\x1b[") {
			t.Errorf("%s (%s) renders without highlighting: %q", path, lang, got)
		}
	}
}
