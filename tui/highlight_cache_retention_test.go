package tui

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// liveHeap is the bytes of live heap after a collection.
func liveHeap() uint64 {
	var stats runtime.MemStats
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// A finished message that is rendered whole is lexed in one pass, whose strings are copied into chunks of up to 16 KiB; a memo entry must not keep the chunk its code was copied into, or one short snippet in a long answer pins the answer's chunk and the memo holds far more than hlEntrySize counts (hlCacheMaxBytes).
// Bound: after a session of 1000 finished messages of 9 KB prose with one Go block each, the memo retains about what it accounts for.
func TestHighlightMemoRetainsWhatItAccountsFor(t *testing.T) {
	hlMu.Lock()
	resetHighlightCacheLocked()
	hlMu.Unlock()
	t.Cleanup(func() {
		hlMu.Lock()
		resetHighlightCacheLocked()
		hlMu.Unlock()
	})
	const messages = 1000
	half := strings.Repeat("Some prose that explains the change in a sentence of ordinary length.\n\n", 62)
	theme := GetMarkdownTheme()
	kept := make([]*Markdown, 0, messages)
	for i := range messages {
		n := strconv.Itoa(i)
		text := half + "```go\nfunc f" + n + "(x int) int { return x * " + n + " }\n```\n\n" + half
		md := NewMarkdownWithOptions(text, 0, 0, &theme, nil, nil)
		md.Render(100)
		md.FinishStreaming()
		kept = append(kept, md)
	}
	hlMu.Lock()
	accounted, entries := hlCacheBytes, len(hlCache)
	hlMu.Unlock()
	if entries < messages/2 {
		t.Fatalf("the memo holds %d entries after %d messages with a code block each", entries, messages)
	}
	withMemo := liveHeap()
	hlMu.Lock()
	resetHighlightCacheLocked()
	hlMu.Unlock()
	withoutMemo := liveHeap()
	runtime.KeepAlive(kept)
	retained := int64(withMemo) - int64(withoutMemo)
	t.Logf("entries %d accounted %d retained %d", entries, accounted, retained)
	// The entries, their keys and lines are what is accounted; the map and slice headers are not.
	if limit := int64(2*accounted + 1<<20); retained > limit {
		t.Fatalf("the memo retains %d bytes in %d entries but accounts for %d, want at most %d: its keys pin the buffers the lexer copied the messages' strings to", retained, entries, accounted, limit)
	}
}
