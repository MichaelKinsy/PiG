package tui

import (
	"strconv"
	"strings"
	"testing"
)

// Streaming a code block highlights every prefix of its code once; the memo keeps only the newest prefix, so it holds about
// one copy of the block rather than every prefix, and a streamed Markdown render still equals the uncached highlight.
func TestHighlightMemoKeepsOnlyTheNewestPrefixOfAStreamedBlock(t *testing.T) {
	hlMu.Lock()
	resetHighlightCacheLocked()
	hlMu.Unlock()
	t.Cleanup(func() {
		hlMu.Lock()
		resetHighlightCacheLocked()
		hlMu.Unlock()
	})
	var code strings.Builder
	md := NewMarkdown("")
	for i := range 150 {
		code.WriteString("func f" + strconv.Itoa(i) + "(x int) int { return x * " + strconv.Itoa(i) + " }\n")
		md.SetText("Text\n\n```go\n" + code.String())
		md.Render(100)
	}
	final := strings.TrimSuffix(code.String(), "\n")
	want := highlightCodeUncached(highlightRegistry(), final, "go", true, ActiveTheme())
	got := highlightMarkdownCode(final, "go")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatal("the memo's highlight differs from the uncached highlight")
	}
	hlMu.Lock()
	held, entries := hlCacheBytes, len(hlCache)
	hlMu.Unlock()
	if limit := 2 * hlEntrySize(hlKey{lang: "go", code: final}, want); held > limit {
		t.Fatalf("the memo holds %d bytes in %d entries after streaming a block of %d bytes, want at most %d", held, entries, len(final), limit)
	}
}
