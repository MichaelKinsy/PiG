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
	}
	for _, tc := range cases {
		if got := LanguageFromPath(tc.path); got != tc.want {
			t.Errorf("LanguageFromPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
