package tui

import (
	"reflect"
	"strings"
	"testing"
)

// retainedHighlightBytes recomputes what the memo holds from its entries, to check the running total against.
func retainedHighlightBytes() (entries, bytes int) {
	hlMu.Lock()
	defer hlMu.Unlock()
	for key, lines := range hlCache {
		bytes += hlEntrySize(key, lines)
	}
	return len(hlCache), bytes
}

// Streaming one code block re-highlights a longer prefix every frame and each prefix is a new memo key that is never requested again. The memo held up to 1,024 of them,
// 402 MB for a 100 KB Go block (finding U3); it now holds at most hlCacheMaxBytes, and its running total matches its entries through every overflow reset.
// mutation-checked: the mutant "no byte budget" (the overflow check ignores hlCacheBytes) fails it.
// mutation-checked: the mutant "reset keeps the byte total" fails it.
func TestHighlightCacheRetainsAtMostItsByteBudgetWhileACodeBlockStreams(t *testing.T) {
	hlMu.Lock()
	resetHighlightCacheLocked()
	hlMu.Unlock()
	t.Cleanup(func() {
		hlMu.Lock()
		resetHighlightCacheLocked()
		hlMu.Unlock()
	})

	line := strings.Repeat("x", 99) + "\n"
	block := strings.Repeat(line, 1000) // 100 KB, streamed in 200 frames of 500 bytes
	peak := 0
	for end := 500; end <= len(block); end += 500 {
		HighlightCode(block[:end], "")
		entries, bytes := retainedHighlightBytes()
		hlMu.Lock()
		running := hlCacheBytes
		hlMu.Unlock()
		if running != bytes {
			t.Fatalf("after the frame ending at %d the running byte total is %d, the entries hold %d", end, running, bytes)
		}
		if bytes > hlCacheMaxBytes || entries > hlCacheMax {
			t.Fatalf("after the frame ending at %d the memo holds %d entries and %d bytes, over its bounds %d and %d", end, entries, bytes, hlCacheMax, hlCacheMaxBytes)
		}
		peak = max(peak, bytes)
	}
	if peak == 0 {
		t.Fatal("the memo never held an entry, so the bound was not exercised")
	}
	if want := highlightCodeUncached(highlightRegistry(), block, "", false, ActiveTheme()); !reflect.DeepEqual(HighlightCode(block, ""), want) {
		t.Fatal("the bounded memo changed the highlighted lines")
	}
}

// An entry that alone exceeds the budget is returned but not retained, so it cannot empty the memo or pass the bound.
// mutation-checked: the mutant "oversized entry cached" (the size guard is dropped) fails it.
func TestHighlightCacheDoesNotRetainAnEntryLargerThanItsBudget(t *testing.T) {
	hlMu.Lock()
	resetHighlightCacheLocked()
	hlMu.Unlock()
	t.Cleanup(func() {
		hlMu.Lock()
		resetHighlightCacheLocked()
		hlMu.Unlock()
	})

	small := "small block\n"
	HighlightCode(small, "")
	huge := strings.Repeat("y", hlCacheMaxBytes)
	got := HighlightCode(huge, "")
	if len(got) != 1 || !strings.Contains(got[0], "y") {
		t.Fatalf("the oversized block was not highlighted: %d lines", len(got))
	}
	entries, bytes := retainedHighlightBytes()
	if bytes > hlCacheMaxBytes {
		t.Fatalf("the memo holds %d bytes after an oversized block, over its bound %d", bytes, hlCacheMaxBytes)
	}
	if entries != 1 {
		t.Fatalf("the memo holds %d entries; want only the small block's (the oversized one must not evict it)", entries)
	}
}
