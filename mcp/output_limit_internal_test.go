package mcp

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Pi packages/mcp/src/output-limit.ts truncateMiddle: keep the start and the end of the text, half of maxBytes each (the tail takes the odd byte), and replace the middle with a `…N chars truncated…` marker.
// mutation-checked: a head or tail that is not half of maxBytes, an odd byte given to the head, a removed-character count taken in bytes, and a total line count that counts a final newline as another line fail it.
func TestTruncateMiddle(t *testing.T) {
	t.Run("keeps text within the limit, a length equal to the limit included", func(t *testing.T) {
		if got, truncated := truncateMiddle("a\nb\n€", 100); truncated {
			t.Fatalf("truncateMiddle within the limit = %+v", got)
		}
		if got, truncated := truncateMiddle("0123456789", 10); truncated {
			t.Fatalf("a length equal to the limit is kept: %+v", got)
		}
	})
	t.Run("keeps half of maxBytes at each end", func(t *testing.T) {
		got, truncated := truncateMiddle("0123456789ABCDEFGHIJ", 10)
		want := middleTruncation{content: "01234…10 chars truncated…FGHIJ", totalBytes: 20, totalLines: 1}
		if !truncated || got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
		// An odd limit gives the tail the extra byte.
		if got, _ := truncateMiddle("0123456789ABCDEFGHIJ", 7); got.content != "012…13 chars truncated…GHIJ" {
			t.Fatalf("odd limit: %+v", got)
		}
	})
	t.Run("counts lines of the whole text, a final newline ending the last line", func(t *testing.T) {
		if got, _ := truncateMiddle(strings.Repeat("line\n", 100), 20); got.totalLines != 100 || got.totalBytes != 500 {
			t.Fatalf("%+v", got)
		}
		if got, _ := truncateMiddle(strings.Repeat("line\n", 100)+"end", 20); got.totalLines != 101 {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("does not split a four-byte character", func(t *testing.T) {
		got, _ := truncateMiddle(strings.Repeat("😀", 10), 10)
		if got.content != "😀…8 chars truncated…😀" {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("cuts multi-byte text only at character boundaries", func(t *testing.T) {
		text := strings.Repeat("é", 20_000) + "end"
		result, truncated := truncateMiddle(text, 1001)
		if !truncated || strings.ContainsRune(result.content, utf8.RuneError) || !strings.HasSuffix(result.content, "end") {
			t.Fatalf("result = %+v", result)
		}
		head, tail, found := strings.Cut(result.content, "…")
		if !found {
			t.Fatalf("no marker in %.60q", result.content)
		}
		_, tail, _ = strings.Cut(tail, " chars truncated…")
		if len(head) > 500 || len(tail) > 501 {
			t.Fatalf("head %d bytes, tail %d bytes", len(head), len(tail))
		}
	})
}

// Pi's strings never hold a lone continuation byte, but a Go string can, and the boundary walks must stop at the ends of the text instead of indexing outside it.
// mutation-checked: `>=` for `>` in the head walk panics on this input.
func TestTruncateMiddleStopsAtTheEndsOfTextWithoutCharacterStarts(t *testing.T) {
	text := "\x80\x80\x80\x80\x80\x80\x80\x80"
	got, truncated := truncateMiddle(text, 1)
	if !truncated || got.content == "" || got.totalBytes != len(text) {
		t.Errorf("truncateMiddle of continuation bytes = %+v", got)
	}
}
