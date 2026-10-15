package ai

import (
	"container/list"
	"fmt"
	"runtime"
	"slices"
	"testing"
	"time"
)

// cacheLen is the number of encodings the cache holds.
func (c *argumentEncodingCache) cacheLen() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.recency.Len()
}

// holdsMarked counts the kept encodings whose arguments carry marker.
func (c *argumentEncodingCache) holdsMarked(marker string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for element := c.recency.Front(); element != nil; element = element.Next() {
		if element.Value.(*argumentEncoding).snapshot["marker"] == marker {
			n++
		}
	}
	return n
}

// waitUntil collects garbage until done reports true. The runtime runs a cleanup some time after the collection that finds its object
// unreachable, on its own goroutine, so the condition is polled to a deadline.
func waitUntil(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("%s", what)
		}
		runtime.GC()
		time.Sleep(5 * time.Millisecond)
	}
}

// A long session writes a tool call per turn and drops the calls a compaction leaves out of the context. The cache keeps the encoding of an
// arguments object only while that object is reachable: the encodings of dropped calls are what grew the process by about 480 bytes per turn
// until the cache's 16 MiB limit, though nothing could ask for them again.
func TestArgumentEncodingsAreReleasedWithTheirArguments(t *testing.T) {
	const calls = 4000
	cache := &argumentEncodingCache{limit: argumentEncodingCacheBytes, entries: map[uintptr]*list.Element{}}
	live := make([]JsonObject, calls)
	for i := range live {
		live[i] = JsonObject{"marker": "lifetime", "path": fmt.Sprintf("/work/file-%d.txt", i)}
		encoded, err := marshalInRecordedOrder(map[string]any(live[i]), nil)
		if err != nil {
			t.Fatal(err)
		}
		cache.keep(live[i], nil, encoded)
	}
	if got := cache.cacheLen(); got != calls {
		t.Fatalf("cache holds %d encodings of %d reachable arguments", got, calls)
	}
	// The newest half is still reachable, so its encodings stay while the older half's go.
	retained := slices.Clone(live[calls/2:])
	live = nil
	waitUntil(t, "the cache still holds the encodings of arguments that nothing reaches", func() bool { return cache.cacheLen() == len(retained) })
	for _, arguments := range retained {
		if cache.find(arguments, nil) == nil {
			t.Fatalf("the cache lost the encoding of reachable arguments %v", arguments)
		}
	}
	runtime.KeepAlive(retained)
	retained = nil
	waitUntil(t, "the cache holds encodings after every arguments object was dropped", func() bool { return cache.cacheLen() == 0 && cache.held == 0 })
}

// The same holds for the process-wide cache that ArgumentsJSON fills for a call the agent finished streaming.
func TestToolCallArgumentsJSONKeepsNothingForADroppedCall(t *testing.T) {
	marker := fmt.Sprintf("dropped-%d", time.Now().UnixNano())
	const calls = 500
	for i := range calls {
		call := ToolCall{ID: fmt.Sprint(i), Name: "read", Arguments: JsonObject{"marker": marker, "path": fmt.Sprintf("/work/%d", i)}}
		if _, err := call.ArgumentsJSON(); err != nil {
			t.Fatal(err)
		}
	}
	waitUntil(t, "ArgumentsJSON left the encodings of dropped calls in the cache", func() bool { return argumentEncodings.holdsMarked(marker) == 0 })
}
