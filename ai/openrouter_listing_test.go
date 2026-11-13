package ai

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"sync"
	"testing"
)

// requireListingIdentifies fails unless listing identifies the entries eager, the list WithOpenRouterRoutingVariants built from models, holds:
// the same number of entries in the same order, each with the listed id and name, and each equal to the model the listing derives it from in
// every other field. A variant must differ from its model in id, name and record only, since a reader of the listing takes everything else from
// the model.
func requireListingIdentifies(t *testing.T, shape string, models, eager []GeneratedModel, listing []ListedOpenRouterModel) {
	t.Helper()
	if listing == nil {
		if len(eager) != len(models) || (len(models) > 0 && &eager[0] != &models[0]) {
			t.Fatalf("%s: no listing, but the list of entries is not the models themselves (%d entries for %d models)", shape, len(eager), len(models))
		}
		return
	}
	if len(listing) != len(eager) {
		t.Fatalf("%s: the listing identifies %d entries, the list holds %d", shape, len(listing), len(eager))
	}
	for i := range eager {
		listed, entry := &listing[i], &eager[i]
		if listed.ID != entry.ID || listed.Name != entry.DisplayName {
			t.Fatalf("%s: entry %d is listed as %q (%q), the list holds %q (%q)", shape, i, listed.ID, listed.Name, entry.ID, entry.DisplayName)
		}
		if int(listed.Base) >= len(models) {
			t.Fatalf("%s: entry %d names model %d of %d", shape, i, listed.Base, len(models))
		}
		model := &models[listed.Base]
		if listed.Variant == 0 {
			if !reflect.DeepEqual(*entry, *model) {
				t.Fatalf("%s: entry %d = %+v, want the model %+v", shape, i, *entry, *model)
			}
			continue
		}
		if want := model.OpenRouterVariant(listed.VariantName()); !reflect.DeepEqual(*entry, want) {
			t.Fatalf("%s: entry %d = %+v, want the %s variant of its model %+v", shape, i, *entry, listed.VariantName(), want)
		}
		rest := *entry
		rest.ID, rest.DisplayName, rest.Record = model.ID, model.DisplayName, model.Record
		if !reflect.DeepEqual(rest, *model) {
			t.Fatalf("%s: the variant %q differs from its model in more than its id, name and record: %+v, model %+v", shape, entry.ID, *entry, *model)
		}
		if entry.Provider != model.Provider || entry.Reasoning != model.Reasoning || !reflect.DeepEqual(entry.Headers, model.Headers) || entry.API != model.API {
			t.Fatalf("%s: the variant %q has another provider, reasoning, headers or API than its model", shape, entry.ID)
		}
	}
}

// D199: the listing by identity is the list of entries, entry for entry, for every shape of catalog the repository ships: the bundled catalog of
// OpenRouter (the held listing), the whole catalog, each provider's list, a list an API strip thinned, and a list that already holds variants.
func TestOpenRouterListingIdentifiesEveryListedEntry(t *testing.T) {
	resetDerivedVariants(t)
	bundled := bundledOpenRouterModels()
	shapes := map[string][]GeneratedModel{"bundled OpenRouter": bundled, "whole catalog": GeneratedModels}
	for _, provider := range ListProviders() {
		shapes["provider "+provider] = ListModels(provider)
	}
	var thinned, relisted []GeneratedModel
	for i := range bundled {
		if i%3 != 0 {
			thinned = append(thinned, bundled[i])
		}
		relisted = append(relisted, bundled[i])
		if i%5 == 0 {
			relisted = append(relisted, bundled[i].OpenRouterVariant("floor"))
		}
		if i%7 == 0 {
			relisted = append(relisted, bundled[i].OpenRouterVariant("nitro"))
		}
	}
	shapes["thinned OpenRouter"], shapes["OpenRouter with listed variants"] = thinned, relisted
	variants := 0
	for shape, models := range shapes {
		listing := OpenRouterListing(models)
		requireListingIdentifies(t, shape, models, WithOpenRouterRoutingVariants(models), listing)
		for i := range listing {
			if listing[i].Variant != 0 {
				variants++
			}
		}
	}
	if variants < 4*len(bundled) {
		t.Fatalf("%d variants identified over every shape of %d OpenRouter models", variants, len(bundled))
	}
	// The listing of the bundled catalog is held: asked again, it is the same listing, and it costs nothing.
	held := OpenRouterListing(bundled)
	if again := OpenRouterListing(bundled); &again[0] != &held[0] || len(again) != len(held) {
		t.Fatal("the listing of the bundled catalog is identified again")
	}
}

// The order of a listing, against ids and names spelled here and not by the rule under test: each model, then its :nitro, then its :floor.
func TestOpenRouterListingOrder(t *testing.T) {
	bundled := bundledOpenRouterModels()
	var ids, names []string
	for i := range bundled {
		ids = append(ids, bundled[i].ID, bundled[i].ID+":nitro", bundled[i].ID+":floor")
		names = append(names, bundled[i].DisplayName, bundled[i].DisplayName+" (nitro)", bundled[i].DisplayName+" (floor)")
	}
	var listedIDs, listedNames, entryIDs, entryNames []string
	for _, listed := range OpenRouterListing(bundled) {
		listedIDs, listedNames = append(listedIDs, listed.ID), append(listedNames, listed.Name)
	}
	for _, entry := range WithOpenRouterRoutingVariants(bundled) {
		entryIDs, entryNames = append(entryIDs, entry.ID), append(entryNames, entry.DisplayName)
	}
	if !slices.Equal(listedIDs, ids) || !slices.Equal(listedNames, names) {
		t.Errorf("the listing by identity is not model, :nitro, :floor for each of %d models (%d entries, want %d)", len(bundled), len(listedIDs), len(ids))
	}
	if !slices.Equal(entryIDs, ids) || !slices.Equal(entryNames, names) {
		t.Errorf("the list of entries is not model, :nitro, :floor for each of %d models (%d entries, want %d)", len(bundled), len(entryIDs), len(ids))
	}
}

// Property: for generated catalogs (random providers, ids, names, records, listed variants, duplicates) under random sets of routing variants,
// the listing by identity is the list of entries.
func TestOpenRouterListingMatchesTheEntriesOfGeneratedCatalogs(t *testing.T) {
	resetDerivedVariants(t)
	shipped := OpenRouterRoutingVariants
	defer func() { OpenRouterRoutingVariants = shipped }()
	random := rand.New(rand.NewPCG(199, 806))
	suffixes := []string{"nitro", "floor", "free", "x", "exacto", "online"}
	providers := []string{"openrouter", "openrouter", "openrouter", "openai", "OpenRouter", ""}
	stems := []string{"a", "a/b", "b", "vendor/model", "m:1", "", "a/b:free"}
	listings, variants := 0, 0
	for round := range 600 {
		rules := slices.Clone(suffixes)
		random.Shuffle(len(rules), func(i, j int) { rules[i], rules[j] = rules[j], rules[i] })
		OpenRouterRoutingVariants = rules[:random.IntN(len(rules)+1)]
		models := make([]GeneratedModel, random.IntN(12))
		for i := range models {
			id := stems[random.IntN(len(stems))]
			for random.IntN(3) == 0 {
				id += ":" + suffixes[random.IntN(len(suffixes))]
			}
			models[i] = GeneratedModel{Provider: providers[random.IntN(len(providers))], ID: id, Reasoning: random.IntN(2) == 0, API: API(fmt.Sprint("api-", random.IntN(3)))}
			if random.IntN(4) != 0 {
				models[i].DisplayName = fmt.Sprint("Name ", random.IntN(5))
			}
			if random.IntN(2) == 0 {
				models[i].Record = fmt.Sprintf(`{"id":%q,"name":%q,"n":%d}`, id, models[i].DisplayName, i)
			}
			if random.IntN(3) == 0 {
				models[i].Headers = map[string]string{"x-round": fmt.Sprint(round)}
			}
		}
		listing := OpenRouterListing(models)
		requireListingIdentifies(t, fmt.Sprintf("round %d, variants %v, models %+v", round, OpenRouterRoutingVariants, models), models, WithOpenRouterRoutingVariants(models), listing)
		if listing != nil {
			listings++
		}
		for i := range listing {
			if listing[i].Variant != 0 {
				variants++
			}
		}
	}
	if listings < 200 || variants < 500 {
		t.Fatalf("the generated catalogs exercised %d listings and %d variants", listings, variants)
	}
}

// A variant built after the listing, alone (HeldOpenRouterVariant) or with the whole list, is the variant an eager list holds, for every
// variant; it takes its id and name from the listing; and building one builds only that one.
func TestLazilyBuiltOpenRouterVariantEqualsTheListedOne(t *testing.T) {
	bundled := bundledOpenRouterModels()
	resetDerivedVariants(t)
	eager := slices.Clone(SharedOpenRouterRoutingVariants(bundled))

	for _, order := range []string{"each variant alone, then the list", "the list, then each variant", "half alone, the list, the rest"} {
		derivedVariants.Lock()
		derivedVariants.listing.Store(nil)
		derivedVariants.bundle.Store(nil)
		derivedVariants.byKey, derivedVariants.bySpec = nil, nil
		derivedVariants.Unlock()
		listing := OpenRouterListing(bundled)
		if derivedVariants.bundle.Load() != nil || len(derivedVariants.byKey) != 0 {
			t.Fatalf("%s: identifying the listing built a variant", order)
		}
		if order == "the list, then each variant" {
			SharedOpenRouterRoutingVariants(bundled)
		}
		built := 0
		for i := range listing {
			listed := &listing[i]
			if listed.Variant == 0 {
				continue
			}
			if order == "half alone, the list, the rest" && i == len(listing)/2 {
				SharedOpenRouterRoutingVariants(bundled)
			}
			lazy := HeldOpenRouterVariant(&bundled[listed.Base], listed.VariantName())
			if !reflect.DeepEqual(*lazy, eager[i]) {
				t.Fatalf("%s: the lazily built %s = %+v, the eager list holds %+v", order, listed.ID, *lazy, eager[i])
			}
			if !oneString(lazy.ID, listed.ID) || !oneString(lazy.DisplayName, listed.Name) {
				t.Fatalf("%s: the lazily built %s spells its id and name again", order, listed.ID)
			}
			if again := HeldOpenRouterVariant(&bundled[listed.Base], listed.VariantName()); again != lazy {
				t.Fatalf("%s: %s is built twice", order, listed.ID)
			}
			if exact, ok := LookupModelExact("openrouter/" + listed.ID); !ok || exact != lazy {
				t.Fatalf("%s: LookupModelExact(%s) is not the lazily built entry", order, listed.ID)
			}
			built++
			if derivedVariants.bundle.Load() == nil {
				derivedVariants.RLock()
				held := len(derivedVariants.byKey)
				derivedVariants.RUnlock()
				if held != built {
					t.Fatalf("%s: %d variants asked for, %d built", order, built, held)
				}
			}
		}
		if listed := SharedOpenRouterRoutingVariants(bundled); !reflect.DeepEqual(listed, eager) {
			t.Fatalf("%s: the list built at the end is not the eager list", order)
		}
	}
}

// First uses from several goroutines, listings by identity, lists of entries, single variants and lookups mixed, identify one listing and build
// one list, and every reader sees the same variants. Run under -race.
func TestConcurrentFirstUsesIdentifyOneOpenRouterListing(t *testing.T) {
	resetDerivedVariants(t)
	bundled := bundledOpenRouterModels()
	const readers = 16
	listings := make([][]ListedOpenRouterModel, readers)
	found := make([][]*GeneratedModel, readers)
	var start, done sync.WaitGroup
	start.Add(1)
	for r := range readers {
		done.Go(func() {
			start.Wait()
			switch r % 4 {
			case 0:
				listings[r] = OpenRouterListing(bundled)
			case 1:
				for i := r; i < len(bundled); i += 3 {
					found[r] = append(found[r], HeldOpenRouterVariant(&bundled[i], "floor"))
				}
			case 2:
				for i := r; i < len(bundled); i += 3 {
					entry, _ := LookupModel("openrouter/" + bundled[i].ID + ":floor")
					found[r] = append(found[r], entry)
				}
			default:
				_ = SharedOpenRouterRoutingVariants(bundled)
			}
		})
	}
	start.Done()
	done.Wait()
	listing := OpenRouterListing(bundled)
	entries := SharedOpenRouterRoutingVariants(bundled)
	requireListingIdentifies(t, "after concurrent first uses", bundled, entries, listing)
	for r := range readers {
		if r%4 == 0 && (&listings[r][0] != &listing[0] || len(listings[r]) != len(listing)) {
			t.Fatalf("reader %d identified a listing of its own", r)
		}
		for _, entry := range found[r] {
			now, ok := LookupModelExact("openrouter/" + entry.ID)
			if !ok || !reflect.DeepEqual(*entry, *now) {
				t.Fatalf("reader %d found %+v, the list holds %+v", r, entry, now)
			}
		}
	}
}
