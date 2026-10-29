package store

import (
	"bytes"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/test/next/store/text.test.ts: text rules (slice 6a §5): code point order, stored text (lone
// surrogates replaced, NUL refused), prefix bounds.

func TestCompareText(t *testing.T) {
	t.Run("orders by code point: a supplementary character after every BMP character, U+E000 to U+FFFF included", func(t *testing.T) {
		sorted := []string{"\U00010000", "\uFFFF", "\uE000", "\uD7FF", "z", "a", "", "ab"}
		slices.SortFunc(sorted, CompareText)
		want := []string{"", "a", "ab", "z", "\uD7FF", "\uE000", "\uFFFF", "\U00010000"}
		if !slices.Equal(sorted, want) {
			t.Fatalf("sorted = %q, want %q", sorted, want)
		}
		if CompareText("a", "a") != 0 {
			t.Fatal("CompareText(a, a) != 0")
		}
		if CompareText("\U0001F600", "\U0001F601") >= 0 {
			t.Fatal("CompareText(U+1F600, U+1F601) is not negative")
		}
	})

	t.Run("agrees with UTF-8 byte order", func(t *testing.T) {
		texts := []string{"\U0001F600", "\uFF5E", "\u00E9", "e", "\U0010FFFF", "\uE000", "aa", "a"}
		byBytes := slices.Clone(texts)
		slices.SortFunc(byBytes, func(a, b string) int { return bytes.Compare([]byte(a), []byte(b)) })
		byText := slices.Clone(texts)
		slices.SortFunc(byText, CompareText)
		if !slices.Equal(byText, byBytes) {
			t.Fatalf("by text = %q, by bytes = %q", byText, byBytes)
		}
	})
}

func TestStoredText(t *testing.T) {
	t.Run("keeps well-formed text as it is and replaces a lone surrogate with U+FFFD", func(t *testing.T) {
		text := "a\U0001F600b"
		got, err := StoredText(text, "v")
		if err != nil || got != text {
			t.Fatalf("StoredText(%q) = %q, %v", text, got, err)
		}
		lone := jsstring.FromUTF16([]uint16{'a', 0xD800, 'b'})
		got, err = StoredText(lone, "v")
		if err != nil || got != "a\uFFFDb" {
			t.Fatalf("StoredText(lone) = %q, %v", got, err)
		}
	})

	t.Run("refuses NUL with invalid/args", func(t *testing.T) {
		_, err := StoredText("a\x00b", "edits.path")
		rejected(t, err, data.CodeInvalid, data.ReasonArgs)
	})
}

func TestPrefixUpperBound(t *testing.T) {
	bound := func(prefix string) (string, bool) { return PrefixUpperBound(prefix) }

	t.Run("is the next code point after the last one", func(t *testing.T) {
		for prefix, want := range map[string]string{"a/": "a0", "a\U0001F600": "a\U0001F601", "\uFFFF": "\U00010000"} {
			if got, ok := bound(prefix); !ok || got != want {
				t.Fatalf("PrefixUpperBound(%q) = %q, %v; want %q", prefix, got, ok, want)
			}
		}
	})

	t.Run("skips the surrogates and carries past U+10FFFF", func(t *testing.T) {
		if got, ok := bound("\uD7FF"); !ok || got != "\uE000" {
			t.Fatalf("PrefixUpperBound(U+D7FF) = %q, %v", got, ok)
		}
		if got, ok := bound("a\U0010FFFF"); !ok || got != "b" {
			t.Fatalf("PrefixUpperBound(a U+10FFFF) = %q, %v", got, ok)
		}
		if got, ok := bound("\U0010FFFF\U0010FFFF"); ok {
			t.Fatalf("PrefixUpperBound(U+10FFFF U+10FFFF) = %q, want none", got)
		}
		if got, ok := bound(""); ok {
			t.Fatalf("PrefixUpperBound(\"\") = %q, want none", got)
		}
	})

	t.Run("bounds exactly the strings starting with the prefix, in code point order", func(t *testing.T) {
		texts := []string{"a", "a/", "a/x", "a/\U0001F600", "a/\uFFFF", "a0", "a.", "b"}
		for _, prefix := range []string{"a/", "a", "a/\uFFFF"} {
			upper, _ := bound(prefix)
			var inRange, starting []string
			for _, text := range texts {
				if CompareText(text, prefix) >= 0 && CompareText(text, upper) < 0 {
					inRange = append(inRange, text)
				}
				if len(text) >= len(prefix) && text[:len(prefix)] == prefix {
					starting = append(starting, text)
				}
			}
			if !slices.Equal(inRange, starting) {
				t.Fatalf("prefix %q: in range %q, starting with it %q", prefix, inRange, starting)
			}
		}
	})
}
