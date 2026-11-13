package ai

import (
	"reflect"
	"sync"
	"testing"
)

// resetDerivedVariants drops every held variant, as a process that has listed and looked up nothing holds none, and puts back what was held
// when the test ends.
func resetDerivedVariants(t *testing.T) {
	t.Helper()
	derivedVariants.Lock()
	listing, bundle, byKey, bySpec := derivedVariants.listing.Load(), derivedVariants.bundle.Load(), derivedVariants.byKey, derivedVariants.bySpec
	derivedVariants.listing.Store(nil)
	derivedVariants.bundle.Store(nil)
	derivedVariants.byKey, derivedVariants.bySpec = nil, nil
	derivedVariants.Unlock()
	t.Cleanup(func() {
		derivedVariants.Lock()
		derivedVariants.listing.Store(listing)
		derivedVariants.bundle.Store(bundle)
		derivedVariants.byKey, derivedVariants.bySpec = byKey, bySpec
		derivedVariants.Unlock()
	})
}

// oneString reports whether a and b are one string, the same bytes and not equal copies: the mark of one derivation.
func oneString(a, b string) bool {
	return len(a) == len(b) && reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

// sameStrings reports whether a and b hold the very strings of one derivation: the same id, name and record bytes, not equal copies.
func sameStrings(a, b *GeneratedModel) bool {
	return oneString(a.ID, b.ID) && oneString(a.DisplayName, b.DisplayName) && oneString(a.Record, b.Record)
}

// D199: a routing variant of a bundled OpenRouter model is held once. The shared list holds it, both lookups return that element, and every
// other listing copies it: each variant's id, name and record are derived once, however it is read and however often.
func TestOpenRouterVariantsAreHeldOnce(t *testing.T) {
	resetDerivedVariants(t)
	bundled := bundledOpenRouterModels()
	if len(bundled) < 100 {
		t.Fatalf("%d OpenRouter models bundled", len(bundled))
	}
	shared := SharedOpenRouterRoutingVariants(bundled)
	if again := SharedOpenRouterRoutingVariants(bundled); &again[0] != &shared[0] || len(again) != len(shared) {
		t.Fatal("the shared list is built again")
	}
	held := map[string]*GeneratedModel{}
	for i := range shared {
		entry := &shared[i]
		base, variant, ok := OpenRouterRoutingVariant(entry.Provider, entry.ID)
		if !ok {
			continue
		}
		held[entry.ID] = entry
		source, found := LookupModelExact("openrouter/" + base)
		if !found {
			t.Fatalf("no base of %s", entry.ID)
		}
		if want := source.OpenRouterVariant(variant); !reflect.DeepEqual(*entry, want) {
			t.Fatalf("listed %+v, want the variant derived from its base %+v", *entry, want)
		}
		for name, lookup := range map[string]func(string) (*GeneratedModel, bool){"LookupModelExact": LookupModelExact, "LookupModel": LookupModel} {
			if got, ok := lookup("openrouter/" + entry.ID); !ok || got != entry {
				t.Fatalf("%s(openrouter/%s) = %p, %t; want the listed entry %p", name, entry.ID, got, ok, entry)
			}
		}
	}
	if len(held) != len(bundled)*len(OpenRouterRoutingVariants) {
		t.Fatalf("%d variants listed for %d models", len(held), len(bundled))
	}
	copied := append([]GeneratedModel(nil), bundled...)
	for name, list := range map[string][]GeneratedModel{
		"ListModels(openrouter)":                ListModels("openrouter"),
		"ListModels()":                          ListModels(""),
		"WithOpenRouterRoutingVariants":         WithOpenRouterRoutingVariants(bundled),
		"WithOpenRouterRoutingVariants(copied)": WithOpenRouterRoutingVariants(copied),
		"SharedOpenRouterRoutingVariants(copy)": SharedOpenRouterRoutingVariants(copied),
	} {
		variants := 0
		for i := range list {
			if _, _, ok := OpenRouterRoutingVariant(list[i].Provider, list[i].ID); !ok {
				continue
			}
			variants++
			if &list[i] == held[list[i].ID] {
				t.Fatalf("%s hands out the held entry of %s; only the shared list of the bundled catalog may", name, list[i].ID)
			}
			if entry := held[list[i].ID]; entry == nil || !sameStrings(&list[i], entry) || !reflect.DeepEqual(list[i], *entry) {
				t.Fatalf("%s derived %s again", name, list[i].ID)
			}
		}
		if variants == 0 {
			t.Fatalf("%s lists no variant", name)
		}
	}
	derivedVariants.RLock()
	singles := len(derivedVariants.byKey)
	derivedVariants.RUnlock()
	if singles != 0 {
		t.Fatalf("%d variants are held outside the list", singles)
	}
}

// A lookup before the first listing derives the one variant it names. The listing then takes that entry over: it lists the very strings the
// lookup derived, the lookup returns the listed element from then on, and nothing stays held beside the list.
func TestAVariantLookedUpBeforeTheListIsListedOnce(t *testing.T) {
	resetDerivedVariants(t)
	bundled := bundledOpenRouterModels()
	type early struct {
		spec  string
		entry *GeneratedModel
		value GeneratedModel
	}
	var looked []early
	for i := 0; i < len(bundled); i += 7 {
		for n, variant := range OpenRouterRoutingVariants {
			spec := "openrouter/" + bundled[i].ID + ":" + variant
			lookup := LookupModelExact
			if (i+n)%2 == 1 {
				lookup = LookupModel
			}
			entry, ok := lookup(spec)
			if !ok {
				t.Fatalf("no %s", spec)
			}
			looked = append(looked, early{spec, entry, *entry})
		}
	}
	if derivedVariants.bundle.Load() != nil {
		t.Fatal("a lookup built the whole list")
	}
	derivedVariants.RLock()
	singles := len(derivedVariants.byKey)
	derivedVariants.RUnlock()
	if singles != len(looked) {
		t.Fatalf("%d lookups hold %d variants", len(looked), singles)
	}
	shared := SharedOpenRouterRoutingVariants(bundled)
	listed := func(entry *GeneratedModel) bool {
		for i := range shared {
			if &shared[i] == entry {
				return true
			}
		}
		return false
	}
	for _, e := range looked {
		for name, lookup := range map[string]func(string) (*GeneratedModel, bool){"LookupModelExact": LookupModelExact, "LookupModel": LookupModel} {
			got, ok := lookup(e.spec)
			if !ok || !listed(got) {
				t.Fatalf("%s(%s) after the listing is not an element of the list", name, e.spec)
			}
			if !reflect.DeepEqual(*got, e.value) || !sameStrings(got, &e.value) {
				t.Fatalf("%s(%s) = %+v after the listing, %+v before it", name, e.spec, *got, e.value)
			}
		}
	}
	derivedVariants.RLock()
	singles = len(derivedVariants.byKey)
	derivedVariants.RUnlock()
	if singles != 0 {
		t.Fatalf("%d variants stay held beside the list", singles)
	}
}

// First uses from several goroutines, lists and lookups mixed, build one list: every reader gets it, and every lookup's entry equals the listed
// one. Run under -race.
func TestConcurrentFirstUsesBuildOneOpenRouterBundle(t *testing.T) {
	resetDerivedVariants(t)
	bundled := bundledOpenRouterModels()
	const readers = 12
	lists := make([][]GeneratedModel, readers)
	found := make([][]*GeneratedModel, readers)
	var start, done sync.WaitGroup
	start.Add(1)
	for r := range readers {
		done.Go(func() {
			start.Wait()
			switch r % 3 {
			case 0:
				lists[r] = SharedOpenRouterRoutingVariants(bundled)
			case 1:
				lists[r] = ListModels("openrouter")
			default:
				for i := r; i < len(bundled); i += 5 {
					entry, _ := LookupModel("openrouter/" + bundled[i].ID + ":nitro")
					found[r] = append(found[r], entry)
				}
			}
		})
	}
	start.Done()
	done.Wait()
	shared := SharedOpenRouterRoutingVariants(bundled)
	for r := range readers {
		switch r % 3 {
		case 0:
			if &lists[r][0] != &shared[0] || len(lists[r]) != len(shared) {
				t.Fatalf("reader %d got a list of its own", r)
			}
		case 1:
			if !reflect.DeepEqual(lists[r], WithOpenRouterRoutingVariants(bundled)) {
				t.Fatalf("reader %d listed other entries", r)
			}
		default:
			for _, entry := range found[r] {
				now, ok := LookupModelExact("openrouter/" + entry.ID)
				if !ok || !reflect.DeepEqual(*entry, *now) || !sameStrings(entry, now) {
					t.Fatalf("reader %d found %+v, the list holds %+v", r, entry, now)
				}
			}
		}
	}
	derivedVariants.RLock()
	singles := len(derivedVariants.byKey)
	derivedVariants.RUnlock()
	if singles != 0 {
		t.Fatalf("%d variants stay held beside the list", singles)
	}
}
