package mcp

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// truncateMiddle of packages/mcp/src/output-limit.ts (Pi 76c040a2a; before it, coding-agent's truncate.ts truncateMiddle). These cases were PiG's tests of that
// function in the tools package; the Go function moved with the code, so they moved too.
func TestTruncateMiddle(t *testing.T) {
	t.Run("returns content within the limit unchanged", func(t *testing.T) {
		if _, truncated := truncateMiddle("a\nb\n€", 100); truncated {
			t.Fatal("content within the limit was truncated")
		}
		if _, truncated := truncateMiddle("0123456789", 10); truncated {
			t.Fatal("a length equal to the limit is kept")
		}
	})
	t.Run("keeps half of maxBytes at each end", func(t *testing.T) {
		got, _ := truncateMiddle("0123456789ABCDEFGHIJ", 10)
		if got != (middleTruncation{content: "01234…10 chars truncated…FGHIJ", totalBytes: 20, totalLines: 1}) {
			t.Fatalf("got %+v", got)
		}
		// An odd limit gives the tail the extra byte.
		if got, _ := truncateMiddle("0123456789ABCDEFGHIJ", 7); got.content != "012…13 chars truncated…GHIJ" {
			t.Fatalf("odd limit: %+v", got)
		}
	})
	t.Run("counts lines of the whole content, a final newline ending the last line", func(t *testing.T) {
		if got, _ := truncateMiddle(strings.Repeat("line\n", 100), 20); got.totalLines != 100 || got.totalBytes != 500 {
			t.Fatalf("%+v", got)
		}
		if got, _ := truncateMiddle(strings.Repeat("line\n", 100)+"x", 20); got.totalLines != 101 {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("does not split a four-byte character", func(t *testing.T) {
		if got, _ := truncateMiddle(strings.Repeat("😀", 10), 10); got.content != "😀…8 chars truncated…😀" {
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
		marker, tail, _ := strings.Cut(tail, "chars truncated…")
		removed := 0
		for _, c := range strings.TrimSpace(marker) {
			removed = removed*10 + int(c-'0')
		}
		if len(head) > 500 || len(tail) > 501 || utf8.RuneCountInString(head)+utf8.RuneCountInString(tail)+removed != utf8.RuneCountInString(text) {
			t.Fatalf("head %d bytes, tail %d bytes, removed %d", len(head), len(tail), removed)
		}
	})
	// Pi's strings never hold a lone continuation byte, but a Go string can, and the boundary walks must stop at the ends of the content instead of indexing
	// outside it.
	t.Run("stops at the ends of content without character starts", func(t *testing.T) {
		content := "\x80\x80\x80\x80\x80\x80\x80\x80"
		if got, _ := truncateMiddle(content, 1); got.content == "" || got.totalBytes != len(content) {
			t.Fatalf("truncateMiddle of continuation bytes = %+v", got)
		}
	})
}
