package ai

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// largeStrings is count distinct strings above requestSpliceMinBytes, each marked with tag.
func largeStrings(tag string, count int) []string {
	values := make([]string, count)
	for i := range values {
		values[i] = fmt.Sprintf("%s %d %s", tag, i, strings.Repeat("tool output <line> & \"quoted\"\n", 300))
	}
	return values
}

func spliceInto(t *testing.T, cache *quotedStringCache, payload []string) {
	t.Helper()
	body, err := marshalSpliced(payload, cache, marshalRequestBody)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := marshalRequestBody(payload); !bytes.Equal(body.Bytes(), want) {
		t.Fatal("the spliced body differs from marshalRequestBody")
	}
}

// cachedSources counts the values the cache holds by identity, without using their entries.
func cachedSources(cache *quotedStringCache, values []string) int {
	held := 0
	for _, value := range values {
		if _, ok := cache.bySource[identityOf(value)]; ok {
			held++
		}
	}
	return held
}

// A long session re-sends its history with every request: each history string is encoded once and stays cached however many requests
// follow, while each request's new string joins it.
func TestTheQuotedStringCacheKeepsHistoryEveryRequestSends(t *testing.T) {
	cache := newQuotedStringCache(1 << 30)
	history := largeStrings("history", 20)
	prompts := largeStrings("prompt", 100)
	for request, prompt := range prompts {
		history = append(history, prompt)
		spliceInto(t, cache, history)
		if held := cachedSources(cache, history); held != len(history) {
			t.Fatalf("request %d: %d of %d history strings cached", request, held, len(history))
		}
	}
	if want := len(history); cache.misses != want {
		t.Fatalf("encoded %d strings over 100 requests, want each of the %d once", cache.misses, want)
	}
}

// History the next requests stop sending (a compaction, /new, /resume or a branch switch) is freed once three request builds in a row have
// not used it, instead of staying until the size limit pushes it out; two builds keep it for a session that alternates with another.
func TestTheQuotedStringCacheFreesStringsThreeRequestsDidNotUse(t *testing.T) {
	cache := newQuotedStringCache(1 << 30)
	before, after := largeStrings("before", 10), largeStrings("after", 10)
	spliceInto(t, cache, before)
	for request := 1; request <= 3; request++ {
		spliceInto(t, cache, after)
		want := len(before)
		if request == 3 {
			want = 0
		}
		if held := cachedSources(cache, before); held != want {
			t.Fatalf("after %d requests without them, %d of %d earlier strings are cached, want %d", request, held, len(before), want)
		}
	}
	afterBytes := 0
	for _, value := range after {
		afterBytes += len(value) + len(cache.bySource[identityOf(value)].Value.(*quotedString).quoted)
	}
	if len(cache.entries) != len(after) || len(cache.bySource) != len(after) || len(cache.byData) != len(after) || cache.held != afterBytes {
		t.Fatalf("cache holds %d entries (%d by source, %d by data) and %d bytes, want the %d current strings and %d bytes", len(cache.entries), len(cache.bySource), len(cache.byData), cache.held, len(after), afterBytes)
	}
}
