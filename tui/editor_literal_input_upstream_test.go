package tui

import (
	"strings"
	"testing"
)

// These cases retain the original input events, not one combined insertion.
func TestUpstreamEditorLiteralInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []string
		want string
	}{
		// .upstream/v0.87.1/packages/tui/test/editor.test.ts:320.
		{"inserts backslash immediately (no buffering)", []string{"\\"}, "\\"},
		// .upstream/v0.87.1/packages/tui/test/editor.test.ts:329.
		{"converts standalone backslash to newline on Enter", []string{"\\", "\r"}, "\n"},
		// .upstream/v0.87.1/packages/tui/test/editor.test.ts:338.
		{"inserts backslash normally when followed by other characters", []string{"\\", "x"}, "\\x"},
		// .upstream/v0.87.1/packages/tui/test/editor.test.ts:378.
		{"ignores printable CSI-u sequences with unsupported modifiers", []string{"\x1b[99;9u"}, ""},
		// .upstream/v0.87.1/packages/tui/test/editor.test.ts:386.
		{"inserts shifted CSI-u letters as text", []string{"\x1b[69;2u"}, "E"},
		// .upstream/v0.87.1/packages/tui/test/editor.test.ts:394.
		{"inserts shifted xterm modifyOtherKeys letters as text", []string{"\x1b[27;2;69~"}, "E"},
		// .upstream/v0.87.1/packages/tui/test/editor.test.ts:404.
		{"inserts mixed ASCII, umlauts, and emojis as literal text", []string{"H", "e", "l", "l", "o", " ", "ä", "ö", "ü", " ", "😀"}, "Hello äöü 😀"},
		// .upstream/v0.87.1/packages/tui/test/editor.test.ts:423.
		{"deletes single-code-unit unicode characters (umlauts) with Backspace", []string{"ä", "ö", "ü", "\x7f"}, "äö"},
		// .upstream/v0.87.1/packages/tui/test/editor.test.ts:437.
		{"deletes multi-code-unit emojis with single Backspace", []string{"😀", "👍", "\x7f"}, "😀"},
		// .upstream/v0.87.1/packages/tui/test/editor.test.ts:450.
		{"inserts characters at the correct position after cursor movement over umlauts", []string{"ä", "ö", "ü", "\x1b[D", "\x1b[D", "x"}, "äxöü"},
		// .upstream/v0.87.1/packages/tui/test/editor.test.ts:468.
		{"moves cursor across multi-code-unit emojis with single arrow key", []string{"😀", "👍", "🎉", "\x1b[D", "\x1b[D", "x"}, "😀x👍🎉"},
		// .upstream/v0.87.1/packages/tui/test/editor.test.ts:488.
		{"preserves umlauts across line breaks", []string{"ä", "ö", "ü", "\n", "Ä", "Ö", "Ü"}, "äöü\nÄÖÜ"},
		// .upstream/v0.87.1/packages/tui/test/editor.test.ts:513.
		{"moves cursor to document start on Ctrl+A and inserts at the beginning", []string{"a", "b", "\x01", "x"}, "xab"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEditor()
			for _, key := range tc.keys {
				e.HandleInput(key)
			}
			assertEditorText(t, e, tc.want)
		})
	}

	// .upstream/v0.87.1/packages/tui/test/editor.test.ts:347.
	t.Run("does not trigger newline when backslash is not immediately before cursor", func(t *testing.T) {
		e := NewEditor()
		submitted := false
		e.OnSubmit = func(string) { submitted = true }
		e.HandleInput("\\")
		e.HandleInput("x")
		e.HandleInput("\r")
		if !submitted {
			t.Fatal("Enter did not submit")
		}
	})

	// .upstream/v0.87.1/packages/tui/test/editor.test.ts:363.
	t.Run("only removes one backslash when multiple are present", func(t *testing.T) {
		e := NewEditor()
		e.HandleInput("\\")
		e.HandleInput("\\")
		e.HandleInput("\\")
		assertEditorText(t, e, "\\\\\\")
		e.HandleInput("\r")
		assertEditorText(t, e, "\\\\\n")
	})

	// .upstream/v0.87.1/packages/tui/test/editor.test.ts:503.
	t.Run("replaces the entire document with unicode text via setText (paste simulation)", func(t *testing.T) {
		e := NewEditor()
		e.SetText("Hällö Wörld! 😀 äöüÄÖÜß")
		assertEditorText(t, e, "Hällö Wörld! 😀 äöüÄÖÜß")
	})
}

// Pi components/editor.ts:440-450,1359-1375 uses String.trim for both history and submission.
func TestEditorHistoryAndSubmissionUseJavaScriptTrim(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
	}{
		{"empty", "", ""},
		{"ordinary", " \t ordinary prompt \n", "ordinary prompt"},
		{"BOM edges", "\ufeffprompt\ufeff", "prompt"},
		{"BOM only", "\ufeff", ""},
		{"NEL edges", "\u0085prompt\u0085", "\u0085prompt\u0085"},
		{"NEL only", "\u0085", "\u0085"},
		{"mixed edges", "\ufeff \u0085prompt\u0085 \ufeff", "\u0085prompt\u0085"},
		{"large prompt", "\ufeff" + strings.Repeat("ä😀 ", 1024) + "\u0085\ufeff", strings.Repeat("ä😀 ", 1024) + "\u0085"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("history", func(t *testing.T) {
				e := NewEditor()
				e.AddToHistory("older")
				e.AddToHistory(tc.input)
				e.HandleInput(historyUp)
				want := tc.want
				if want == "" {
					want = "older"
				}
				assertEditorText(t, e, want)
			})
			t.Run("submit", func(t *testing.T) {
				e := NewEditor()
				var calls []string
				e.OnSubmit = func(text string) { calls = append(calls, text) }
				e.SetText(tc.input)
				e.HandleInput("\r")
				if len(calls) != 1 || calls[0] != tc.want {
					t.Fatalf("submitted=%q, want one call with %q", calls, tc.want)
				}
				assertEditorText(t, e, "")
			})
		})
	}
}

func TestEditorHistoryDeduplicatesAfterJavaScriptTrim(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("older")
	e.AddToHistory("prompt")
	e.AddToHistory("\ufeffprompt\ufeff")
	e.HandleInput(historyUp)
	assertEditorText(t, e, "prompt")
	e.HandleInput(historyUp)
	assertEditorText(t, e, "older")
}

func BenchmarkEditorHistorySubmissionWhitespace(b *testing.B) {
	text := "\ufeff" + strings.Repeat("ä😀 ", 1024) + "\u0085\ufeff"
	e := NewEditor()
	e.OnSubmit = e.AddToHistory
	b.ReportAllocs()
	b.SetBytes(int64(len(text)))
	for b.Loop() {
		e.SetText(text)
		e.HandleInput("\r")
		e.HandleInput(historyUp)
	}
}
