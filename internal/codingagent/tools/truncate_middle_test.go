package tools

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// truncate.ts:278-314 (v0.99.1) truncateMiddle.
func TestTruncateMiddle(t *testing.T) {
	t.Run("returns content within the limit unchanged", func(t *testing.T) {
		got := TruncateMiddle("a\nb\n€", 100)
		want := MiddleTruncationResult{Content: "a\nb\n€", TotalBytes: 7, TotalLines: 3}
		if got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
		if got := TruncateMiddle("0123456789", 10); got.Truncated || got.Content != "0123456789" {
			t.Fatalf("a length equal to the limit is kept: %+v", got)
		}
	})
	t.Run("keeps half of maxBytes at each end", func(t *testing.T) {
		got := TruncateMiddle("0123456789ABCDEFGHIJ", 10)
		want := MiddleTruncationResult{Content: "01234…10 chars truncated…FGHIJ", Truncated: true, RemovedChars: 10, TotalBytes: 20, TotalLines: 1}
		if got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
		// An odd limit gives the tail the extra byte.
		if got := TruncateMiddle("0123456789ABCDEFGHIJ", 7); got.Content != "012…13 chars truncated…GHIJ" || got.RemovedChars != 13 {
			t.Fatalf("odd limit: %+v", got)
		}
	})
	t.Run("counts lines of the whole content", func(t *testing.T) {
		if got := TruncateMiddle(strings.Repeat("line\n", 100), 20); got.TotalLines != 100 || got.TotalBytes != 500 {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("does not split a four-byte character", func(t *testing.T) {
		got := TruncateMiddle(strings.Repeat("😀", 10), 10)
		if got.Content != "😀…8 chars truncated…😀" || got.RemovedChars != 8 {
			t.Fatalf("%+v", got)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/mcp-extension.test.ts:289 (family 8 owns the rest of that file).
	t.Run("cuts multi-byte text only at character boundaries", func(t *testing.T) {
		text := strings.Repeat("é", 20_000) + "end"
		result := TruncateMiddle(text, 1001)
		if !result.Truncated || strings.ContainsRune(result.Content, utf8.RuneError) || !strings.HasSuffix(result.Content, "end") {
			t.Fatalf("result = %+v", result)
		}
		marker := "…" + itoa(result.RemovedChars) + " chars truncated…"
		head, tail, found := strings.Cut(result.Content, marker)
		if !found {
			t.Fatalf("no marker in %.60q", result.Content)
		}
		if len(head) > 500 || len(tail) > 501 {
			t.Fatalf("head %d bytes, tail %d bytes", len(head), len(tail))
		}
		if utf8.RuneCountInString(head)+utf8.RuneCountInString(tail)+result.RemovedChars != utf8.RuneCountInString(text) {
			t.Fatal("kept and removed characters do not add up")
		}
	})
}
