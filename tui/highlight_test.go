package tui

import (
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
