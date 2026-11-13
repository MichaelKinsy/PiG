package codingagent

import (
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// D199: a routing variant of a bundled OpenRouter model is held once: the entry of the list the registry reads is the entry ai's lookups
// return, not a copy of it.
func TestListedRoutingVariantsAreTheEntriesLookupsReturn(t *testing.T) {
	listed := builtinChatCatalogListed("openrouter")
	variants := 0
	for i := range listed {
		if _, _, ok := ai.OpenRouterRoutingVariant(listed[i].Provider, listed[i].ID); !ok {
			continue
		}
		variants++
		for name, lookup := range map[string]func(string) (*ai.GeneratedModel, bool){"LookupModelExact": ai.LookupModelExact, "LookupModel": ai.LookupModel} {
			if got, ok := lookup("openrouter/" + listed[i].ID); !ok || got != &listed[i] {
				t.Fatalf("%s(openrouter/%s) = %p, %t; want the listed entry %p: the variant is held twice", name, listed[i].ID, got, ok, &listed[i])
			}
		}
	}
	if variants < 100 {
		t.Fatalf("%d routing variants listed", variants)
	}
}

// Two first readers of the listed catalog get one list. Run under -race.
func TestConcurrentFirstReadersOfTheListedCatalogShareOneList(t *testing.T) {
	held := currentBuiltinChatCatalogs.Swap(nil)
	t.Cleanup(func() { currentBuiltinChatCatalogs.Store(held) })
	const readers = 8
	lists := make([][]ai.GeneratedModel, readers)
	var start, done sync.WaitGroup
	start.Add(1)
	for r := range readers {
		done.Go(func() {
			start.Wait()
			lists[r] = builtinChatCatalogListed("openrouter")
		})
	}
	start.Done()
	done.Wait()
	for r := range readers {
		if len(lists[r]) == 0 || &lists[r][0] != &lists[0][0] || len(lists[r]) != len(lists[0]) {
			t.Fatalf("reader %d got a list of its own", r)
		}
	}
}
