package tui

import (
	"strings"
	"testing"
)

// A scope the theme has no entry for takes the formatter of the scope around it or the theme's default, and so does a scope that names a member of
// Object.prototype: the theme is a Go map and has no inherited members. An entry that holds nil is an own property set to undefined (utils/syntax-highlight.ts
// getScopeFormatter and getActiveFormatter) and is no formatter either.
func TestAScopeNamedLikeAnObjectPrototypeMemberHasNoFormatter(t *testing.T) {
	keyword := func(text string) string { return "[k:" + text + "]" }
	fallback := func(text string) string { return "[d:" + text + "]" }
	for _, scope := range []string{"constructor", "toString", "toString.x", "isPrototypeOf", "isPrototypeOf-y", "toLocaleString", "__proto__", "valueOf", "hasOwnProperty", "propertyIsEnumerable", "__defineGetter__", "__defineSetter__", "__lookupGetter__", "__lookupSetter__", "nosuchscope"} {
		for _, tc := range []struct {
			name, html, want string
			theme            HighlightTheme
		}{
			{"empty theme", `<span class="hljs-` + scope + `">x</span>y`, "xy", HighlightTheme{}},
			{"default formatter", `a<span class="hljs-` + scope + `">x</span>b`, "[d:a][d:x][d:b]", HighlightTheme{"default": fallback}},
			{"enclosing scope", `<span class="hljs-keyword">a<span class="hljs-` + scope + `">x</span>b</span>`, "[k:a][k:x][k:b]", HighlightTheme{"keyword": keyword}},
		} {
			got, err := renderHighlightedHTML(tc.html, tc.theme)
			if err != nil || got != tc.want {
				t.Errorf("scope %s, %s: got %q, error %v; want %q", scope, tc.name, got, err, tc.want)
			}
		}
	}
	if got := RenderHighlightedHtml(`<span class="hljs-toString">x</span>`, HighlightTheme{"toString": nil, "default": fallback}); got != "[d:x]" {
		t.Errorf("own undefined toString: got %q, want %q", got, "[d:x]")
	}
	if got := RenderHighlightedHtml(`<span class="hljs-keyword">x</span>y`, HighlightTheme{"keyword": nil, "default": fallback}); got != "[d:x][d:y]" {
		t.Errorf("own undefined keyword: got %q, want %q", got, "[d:x][d:y]")
	}
}

// highlight.js's reasonml grammar emits the scope `constructor`, the one scope of a shipped grammar that names a member of Object.prototype, so the lookup
// above is reached by a reasonml code block. TestHighlightMatchesPiOracle compares the lines of every grammar's samples with Pi's.
func TestReasonmlEmitsTheScopeConstructor(t *testing.T) {
	if highlightStripped() {
		t.Skip("this build has no syntax highlighting")
	}
	LoadAllHighlightLanguages()
	html, err := highlightRegistry().Highlight("Some(1)", "reasonml", true)
	if err != nil || !strings.Contains(html, `class="hljs-constructor"`) {
		t.Fatalf("reasonml Some(1): html %q, error %v; want a hljs-constructor span", html, err)
	}
	got, err := renderHighlightedHTML(html, HighlightTheme{"default": func(text string) string { return "[d:" + text + "]" }})
	if err != nil || got != "[d:Some(1)]" {
		t.Errorf("reasonml Some(1) with a default formatter: got %q, error %v; want %q", got, err, "[d:Some(1)]")
	}
}

func BenchmarkHighlightThemeFormatter(b *testing.B) {
	SetTheme("dark")
	theme := cliHighlightTheme(ActiveTheme())
	b.ReportAllocs()
	for b.Loop() {
		for _, scope := range [...]string{"keyword", "constructor", "nosuchscope", "title.function", "selector-tag"} {
			getScopeFormatter(scope, theme)
		}
	}
}
