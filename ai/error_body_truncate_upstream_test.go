package ai

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func heapInUse() uint64 {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// Ports packages/ai/test/error-body.test.ts:241 ("keeps an excerpt that does not hold the whole body alive", Pi #9265). Go substrings
// share their backing array like a V8 slice does, so the 6 MB body stays reachable if the excerpt is a plain s[:n]. The omitted count
// is in UTF-16 units; every unit here is one byte.
func TestTruncateErrorTextKeepsAnExcerptThatDoesNotHoldTheWholeBodyAlive(t *testing.T) {
	before := heapInUse()
	kept := func() string {
		body := strings.Repeat("<html>", 1_000_000)
		return TruncateErrorText(body, MaxProviderErrorBodyChars)
	}()
	after := heapInUse()
	if !strings.HasPrefix(kept, "<html><html>") {
		t.Fatalf("excerpt starts %q", kept[:20])
	}
	if want := fmt.Sprintf("[truncated %d chars]", 6_000_000-MaxProviderErrorBodyChars); !strings.HasSuffix(kept, want) {
		t.Fatalf("excerpt ends %q, want suffix %q", kept[len(kept)-40:], want)
	}
	// A slice would keep the 6 MB body alive with the excerpt.
	if grown := int64(after) - int64(before); grown >= 1_000_000 {
		t.Fatalf("heap grew by %d bytes with the excerpt alive, want < 1000000", grown)
	}
	runtime.KeepAlive(kept)
}
