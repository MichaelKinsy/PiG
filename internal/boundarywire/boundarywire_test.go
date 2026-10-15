package boundarywire

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

func sourcePreview(n int) extension.BoundaryContextPreview {
	return extension.BoundaryContextPreview{ContextEntries: make([]extension.ProjectedSessionEntry, n)}
}

// A preview's records stand for its sources only while it is the dispatch's latest build: an earlier preview neither encodes nor decodes the later build's records.
func TestCacheSourcesBelongToTheLatestBuildOnly(t *testing.T) {
	_, cache := WithCache(context.Background())
	first := sourcePreview(1)
	cache.Built(first, Key{}, false, []json.RawMessage{json.RawMessage(`{"id":"first"}`)})
	second := sourcePreview(1)
	cache.Built(second, Key{}, false, []json.RawMessage{json.RawMessage(`{"id":"second"}`)})

	if sources, ok := cache.Sources(first); ok {
		t.Fatalf("Sources(earlier preview) = %s, true; want none", sources)
	}
	if err := cache.Deliver(first); err != nil {
		t.Fatal(err)
	}
	if got := first.ContextEntries[0].SourceEntry; got != nil {
		t.Fatalf("Deliver(earlier preview) decoded %v into it; want it untouched", got)
	}
	sources, ok := cache.Sources(second)
	if !ok || len(sources) != 1 || string(sources[0]) != `{"id":"second"}` {
		t.Fatalf("Sources(latest preview) = %s, %v; want its own record", sources, ok)
	}
	if err := cache.Deliver(second); err != nil {
		t.Fatal(err)
	}
	if got, ok := second.ContextEntries[0].SourceEntry.(map[string]any); !ok || got["id"] != "second" {
		t.Fatalf("Deliver(latest preview) SourceEntry = %#v; want the decoded record", second.ContextEntries[0].SourceEntry)
	}
}

// Records that do not pair one to one with the preview's entries are not its sources.
func TestCacheSourcesRequireOneRecordPerEntry(t *testing.T) {
	_, cache := WithCache(context.Background())
	preview := sourcePreview(2)
	cache.Built(preview, Key{}, false, []json.RawMessage{json.RawMessage(`{"id":"only"}`)})
	if sources, ok := cache.Sources(preview); ok {
		t.Fatalf("Sources = %s, true for 1 record and 2 entries; want none", sources)
	}
}

// The session's kept encodings serve only the latest build whose messages are the session's own and whose records pair with its entries, until a handler that reads Go values receives it.
func TestCacheKeptServesOnlyTheLatestSharedUndeliveredBuild(t *testing.T) {
	records := func() []json.RawMessage { return []json.RawMessage{json.RawMessage(`{"id":"a"}`)} }
	build := func(cache *Cache, shared bool) extension.BoundaryContextPreview {
		preview := sourcePreview(1)
		cache.Built(preview, Key{}, true, records())
		if shared {
			cache.Shared()
		}
		return preview
	}
	encodings := &Encodings{}

	_, cache := WithSessionCache(context.Background(), encodings)
	latest := build(cache, true)
	if got, sources, ok := cache.Kept(latest); !ok || got != encodings || len(sources) != 1 {
		t.Fatalf("Kept(latest shared build) = %p, %d records, %v; want the session's encodings and its record", got, len(sources), ok)
	}

	earlier := latest
	build(cache, true)
	if _, _, ok := cache.Kept(earlier); ok {
		t.Fatal("Kept served an earlier build the records of the latest")
	}

	_, cache = WithSessionCache(context.Background(), encodings)
	unshared := build(cache, false)
	if _, _, ok := cache.Kept(unshared); ok {
		t.Fatal("Kept served a build whose messages are not the session's own")
	}

	_, cache = WithSessionCache(context.Background(), encodings)
	delivered := build(cache, true)
	if err := cache.Deliver(delivered); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := cache.Kept(delivered); ok {
		t.Fatal("Kept served a build a Go handler received")
	}

	_, cache = WithCache(context.Background())
	if _, _, ok := cache.Kept(build(cache, true)); ok {
		t.Fatal("Kept served a dispatch without session encodings")
	}
}
