package codingagent

import (
	"reflect"
	"strconv"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

var builtinChatCatalogSink []ai.GeneratedModel

// piListedModels is the part of models that Pi's catalog lists: it leaves out the OpenRouter routing variants PiG derives when it lists models (D199),
// which builtinChatCatalog leaves out and builtinChatCatalogListed adds.
func piListedModels(models []ai.GeneratedModel) []ai.GeneratedModel {
	listed := make([]ai.GeneratedModel, 0, len(models))
	for _, model := range models {
		if _, _, variant := ai.OpenRouterRoutingVariant(model.Provider, model.ID); !variant {
			listed = append(listed, model)
		}
	}
	return listed
}

// checkBuiltinChatCatalog compares builtinChatCatalog with ai.ListModels, less the routing variants it derives, for every provider and returns how many models they list.
func checkBuiltinChatCatalog(t *testing.T, stage string) int {
	t.Helper()
	listed := 0
	for _, id := range append(ai.ListProviders(), "no-such-provider") {
		want, got := piListedModels(ai.ListModels(id)), builtinChatCatalog(id)
		if len(want) != len(got) || (len(want) > 0 && !reflect.DeepEqual(want, got)) {
			t.Fatalf("%s: builtinChatCatalog(%q) = %d models, ai.ListModels = %d, or they differ", stage, id, len(got), len(want))
		}
		listed += len(got)
		if len(got) == 0 {
			continue
		}
		if cap(got) != len(got) {
			t.Fatalf("%s: builtinChatCatalog(%q) has capacity %d for %d models; an append would write into the shared list", stage, id, cap(got), len(got))
		}
		if again := builtinChatCatalog(id); &again[0] != &got[0] {
			t.Fatalf("%s: builtinChatCatalog(%q) copied the list", stage, id)
		}
	}
	if all := len(piListedModels(ai.ListModels(""))); listed != all || listed == 0 {
		t.Fatalf("%s: the providers list %d models, the catalog %d", stage, listed, all)
	}
	return listed
}

// builtinChatCatalog lists what ai.ListModels lists for every provider, in order, shares one clipped list per provider, and follows a change of the stripped APIs (a Piglet's strip list is applied at startup).
func TestBuiltinChatCatalogListsWhatListModelsLists(t *testing.T) {
	all := checkBuiltinChatCatalog(t, "stock")
	if allocs := testing.AllocsPerRun(20, func() { builtinChatCatalogSink = builtinChatCatalog("anthropic") }); allocs != 0 {
		t.Fatalf("builtinChatCatalog = %v allocations, want 0", allocs)
	}
	undo := pigstrip.Strip(pigstrip.ListAPIs, string(ai.APIOpenAICompletions))
	stripped := checkBuiltinChatCatalog(t, "openai-completions stripped")
	undo()
	if stripped >= all {
		t.Fatalf("stripping openai-completions left %d of %d models", stripped, all)
	}
	if restored := checkBuiltinChatCatalog(t, "strip undone"); restored != all {
		t.Fatalf("undoing the strip lists %d models, want %d", restored, all)
	}
	if catalogs := currentBuiltinChatCatalogs.Load(); len(catalogs.byProvider) > len(ai.ListProviders()) {
		t.Fatalf("the catalog keeps %d lists for %d providers", len(catalogs.byProvider), len(ai.ListProviders()))
	}
}

// Callers on several goroutines share the first list of a provider, each time the stripped APIs change.
func TestBuiltinChatCatalogIsSharedAcrossGoroutines(t *testing.T) {
	const callers = 16
	for round := range 50 {
		undo := pigstrip.Strip(pigstrip.ListAPIs, "no-such-api-"+strconv.Itoa(round))
		firsts := make([]*ai.GeneratedModel, callers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range callers {
			wg.Go(func() {
				<-start
				firsts[i] = &builtinChatCatalog("anthropic")[0]
			})
		}
		close(start)
		wg.Wait()
		for _, first := range firsts {
			if first != &builtinChatCatalog("anthropic")[0] {
				undo()
				t.Fatalf("round %d: concurrent callers got different lists", round)
			}
		}
		undo()
	}
}

// windowOfGeneratedModels reports whether list is a window of ai.GeneratedModels rather than a copy.
func windowOfGeneratedModels(list []ai.GeneratedModel) bool {
	for i := range ai.GeneratedModels {
		if &ai.GeneratedModels[i] == &list[0] {
			return true
		}
	}
	return false
}

// A provider whose offered models sit together in the generated catalog gets a window of it, without a copy; a strip that leaves gaps gets a copy of exactly the offered models.
func TestBuiltinChatCatalogSharesTheGeneratedCatalog(t *testing.T) {
	windows := 0
	for _, id := range ai.ListProviders() {
		if got := builtinChatCatalog(id); len(got) > 0 {
			if !windowOfGeneratedModels(got) {
				t.Fatalf("builtinChatCatalog(%q) copied models that sit together in the generated catalog", id)
			}
			windows++
		}
	}
	if windows == 0 {
		t.Fatal("no provider lists a bundled chat model")
	}

	// Stripping an API whose models sit between a provider's other models leaves a gap.
	var gapped, api string
	for i := range ai.GeneratedModels {
		model := ai.GeneratedModels[i]
		kept, gap := 0, false
		for j := range ai.GeneratedModels {
			if other := ai.GeneratedModels[j]; other.Provider == model.Provider && other.API != model.API {
				if kept > 0 && j > 0 && ai.GeneratedModels[j-1].Provider == model.Provider && ai.GeneratedModels[j-1].API == model.API {
					gap = true
				}
				kept++
			}
		}
		if gap {
			gapped, api = model.Provider, string(model.API)
			break
		}
	}
	if gapped == "" {
		t.Fatal("no bundled provider has a model of one API between models of other APIs")
	}
	undo := pigstrip.Strip(pigstrip.ListAPIs, api)
	defer undo()
	checkBuiltinChatCatalog(t, api+" stripped")
	if got := builtinChatCatalog(gapped); len(got) == 0 || windowOfGeneratedModels(got) {
		t.Fatalf("stripping %s from %s: %d models, window %v; want a copy of the rest", api, gapped, len(got), len(got) > 0 && windowOfGeneratedModels(got))
	}
}

// providerOfAPI is the first bundled provider with a model on api.
func providerOfAPI(t *testing.T, api ai.API) string {
	t.Helper()
	for i := range ai.GeneratedModels {
		if ai.GeneratedModels[i].API == api {
			return ai.GeneratedModels[i].Provider
		}
	}
	t.Fatalf("no bundled model on %s", api)
	return ""
}

func catalogHasAPI(models []ai.GeneratedModel, api ai.API) bool {
	for i := range models {
		if models[i].API == api {
			return true
		}
	}
	return false
}

// A Piglet that strips an API (a build tag, or its strip list applied at startup) pays no allocation for a catalog lookup: the catalog compares the strip generation and does not copy the stripped IDs on every call.
func TestBuiltinChatCatalogDoesNotAllocateWhileAnAPIIsStripped(t *testing.T) {
	undo := pigstrip.Strip(pigstrip.ListAPIs, string(ai.APIOpenAICompletions))
	defer undo()
	builtinChatCatalogSink = builtinChatCatalog("anthropic")
	if allocs := testing.AllocsPerRun(20, func() { builtinChatCatalogSink = builtinChatCatalog("anthropic") }); allocs != 0 {
		t.Fatalf("builtinChatCatalog = %v allocations with an API stripped, want 0", allocs)
	}
}

// A strip recorded between two lookups is seen by the next one, and its undo too: the catalog never serves a model of a stripped API.
func TestBuiltinChatCatalogSeesAStripBetweenTwoLookups(t *testing.T) {
	api := ai.APIOpenAICompletions
	provider := providerOfAPI(t, api)
	if !catalogHasAPI(builtinChatCatalog(provider), api) {
		t.Fatalf("%s lists no %s model before the strip", provider, api)
	}
	undo := pigstrip.Strip(pigstrip.ListAPIs, string(api))
	if catalogHasAPI(builtinChatCatalog(provider), api) {
		t.Fatalf("%s still lists a %s model after the strip", provider, api)
	}
	again := pigstrip.Strip(pigstrip.ListAPIs, string(api))
	again()
	if catalogHasAPI(builtinChatCatalog(provider), api) {
		t.Fatalf("a strip that recorded nothing, undone, brought back a %s model of %s", api, provider)
	}
	undo()
	if !catalogHasAPI(builtinChatCatalog(provider), api) {
		t.Fatalf("%s lists no %s model after the strip is undone", provider, api)
	}
}

// Lookups racing a Strip never read a torn catalog, and once the Strip has returned every later lookup omits the API.
func TestBuiltinChatCatalogLookupsRaceAStrip(t *testing.T) {
	api := ai.APIOpenAICompletions
	provider := providerOfAPI(t, api)
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					_ = builtinChatCatalog(provider)
					_ = builtinChatModels(provider)
				}
			}
		})
	}
	for range 50 {
		undo := pigstrip.Strip(pigstrip.ListAPIs, string(api))
		if catalogHasAPI(builtinChatCatalog(provider), api) {
			t.Error("a lookup after the strip returned a model of the stripped API")
		}
		undo()
		if !catalogHasAPI(builtinChatCatalog(provider), api) {
			t.Error("a lookup after the undo omitted the API")
		}
	}
	close(stop)
	readers.Wait()
}
