package harness

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// A read of a cached call returns the caller's own copy: changing what one read returned changes neither the cache
// nor another read.
func TestAssistantCallCacheHandsEachReadItsOwnCopy(t *testing.T) {
	var cache assistantCalls
	key := assistantCallKey{conversationId: 1, entryId: 2}
	call := ai.ToolCall{ID: "c1", Name: "echo", Arguments: ai.JsonObject{"text": "x", "nested": map[string]any{"k": "v"}}}
	cache.put(key, map[string]ai.ToolCall{"c1": call})

	first, found, cached := cache.get(key, "c1")
	if !found || !cached {
		t.Fatalf("get = found %v, cached %v", found, cached)
	}
	first.Arguments["text"] = "changed"
	first.Arguments["nested"].(map[string]any)["k"] = "changed"
	second, _, _ := cache.get(key, "c1")
	if second.Arguments["text"] != "x" || second.Arguments["nested"].(map[string]any)["k"] != "v" {
		t.Fatalf("a later read saw the earlier read's change: %v", second.Arguments)
	}
	if _, found, cached := cache.get(key, "absent"); found || !cached {
		t.Fatalf("an absent call of a cached entry: found %v, cached %v", found, cached)
	}
	if _, _, cached := cache.get(assistantCallKey{conversationId: 1, entryId: 3}, "c1"); cached {
		t.Fatal("another entry was served from the cache")
	}
	for id := range assistantCallCacheSize + 1 {
		cache.put(assistantCallKey{conversationId: 9, entryId: durable.EntryId(100 + id)}, map[string]ai.ToolCall{})
	}
	if _, _, cached := cache.get(key, "c1"); cached {
		t.Fatalf("the cache kept more than %d entries", assistantCallCacheSize)
	}
}
