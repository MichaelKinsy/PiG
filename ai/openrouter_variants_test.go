package ai

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// piListedModels is the part of models that Pi's catalog lists: it leaves out the OpenRouter routing variants PiG derives (D199).
func piListedModels(models []GeneratedModel) []GeneratedModel {
	listed := make([]GeneratedModel, 0, len(models))
	for i := range models {
		if _, _, variant := OpenRouterRoutingVariant(models[i].Provider, models[i].ID); !variant {
			listed = append(listed, models[i])
		}
	}
	return listed
}

func decodedRecord(t *testing.T, record string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(record), &decoded); err != nil {
		t.Fatalf("record %q: %v", record, err)
	}
	return decoded
}

// OpenRouter's variants page (https://openrouter.ai/docs/guides/routing/model-variants/overview) says every model entry accepts :nitro
// and :floor, and that the metadata of a variant is its base entry's. So the OpenRouter list holds, after each base entry, both
// variants, each a copy of the base but for its id, name and JSON record; no other provider has one; and the bundled catalog stores none.
func TestOpenRouterRoutingVariantsMirrorTheirBaseEntries(t *testing.T) {
	for i := range GeneratedModels {
		if _, _, variant := OpenRouterRoutingVariant(GeneratedModels[i].Provider, GeneratedModels[i].ID); variant {
			t.Errorf("the bundled catalog stores %s/%s", GeneratedModels[i].Provider, GeneratedModels[i].ID)
		}
	}
	bases := piListedModels(ListModels("openrouter"))
	if len(bases) == 0 {
		t.Fatal("the catalog lists no OpenRouter model")
	}
	listed := ListModels("openrouter")
	if want := len(bases) * (1 + len(OpenRouterRoutingVariants)); len(listed) != want {
		t.Fatalf("%d OpenRouter entries, want %d (each of %d bases and its variants)", len(listed), want, len(bases))
	}
	for at, base := range bases {
		position := at * (1 + len(OpenRouterRoutingVariants))
		if listed[position].ID != base.ID {
			t.Fatalf("entry %d is %s, want base %s", position, listed[position].ID, base.ID)
		}
		for offset, variant := range OpenRouterRoutingVariants {
			got := listed[position+1+offset]
			if got.ID != base.ID+":"+variant {
				t.Fatalf("entry %d is %s, want %s:%s", position+1+offset, got.ID, base.ID, variant)
			}
			if want := base.DisplayName + " (" + variant + ")"; got.DisplayName != want {
				t.Errorf("%s: name %q, want %q", got.ID, got.DisplayName, want)
			}
			derived, original := got, base
			derived.ID, derived.DisplayName, derived.Record = "", "", ""
			original.ID, original.DisplayName, original.Record = "", "", ""
			if !reflect.DeepEqual(derived, original) {
				t.Errorf("%s differs from %s beyond its id, name and record", got.ID, base.ID)
			}
			wantRecord := decodedRecord(t, base.Record)
			wantRecord["id"], wantRecord["name"] = got.ID, got.DisplayName
			if !reflect.DeepEqual(decodedRecord(t, got.Record), wantRecord) {
				t.Errorf("%s: record %s is not the base record with its id and name replaced", got.ID, got.Record)
			}
		}
	}
	for _, provider := range ListProviders() {
		if provider != "openrouter" && !reflect.DeepEqual(ListModels(provider), piListedModels(ListModels(provider))) {
			t.Errorf("%s lists a routing variant: only OpenRouter has them", provider)
		}
	}
	if got, want := len(ListModels("")), len(GeneratedModels)+len(listed)-len(bases); got != want {
		t.Errorf("the whole list has %d entries, want %d", got, want)
	}
}

func TestOpenRouterRoutingVariantsAreLookedUpFromTheirBase(t *testing.T) {
	base := piListedModels(ListModels("openrouter"))[0]
	for _, variant := range OpenRouterRoutingVariants {
		spec := "openrouter/" + base.ID + ":" + variant
		want := base.OpenRouterVariant(variant)
		for name, lookup := range map[string]func(string) (*GeneratedModel, bool){"LookupModelExact": LookupModelExact, "LookupModel": LookupModel} {
			got, ok := lookup(spec)
			if !ok || !reflect.DeepEqual(*got, want) {
				t.Errorf("%s(%q) = %+v, %t; want %+v", name, spec, got, ok, want)
			}
			if again, _ := lookup(spec); again != got {
				t.Errorf("%s(%q) returns a new entry on every lookup", name, spec)
			}
		}
	}
	// A variant of a model the catalog lacks, of another provider's model or of a variant is no entry; an unknown suffix is no variant.
	other := piListedModels(ListModels("openai"))[0]
	for _, spec := range []string{
		"openrouter/no-such/model:nitro", "openai/" + other.ID + ":nitro", "openrouter/" + base.ID + ":nitro:nitro",
		"openrouter/" + base.ID + ":turbo", "openrouter/" + base.ID + ":",
	} {
		if got, ok := LookupModelExact(spec); ok {
			t.Errorf("LookupModelExact(%q) = %+v, want no entry", spec, got)
		}
	}
	// A catalog variant (:free) takes the routing variants on top, as the variants page's "free:nitro" example does.
	for _, model := range ListModels("openrouter") {
		if strings.HasSuffix(model.ID, ":free") {
			if got, ok := LookupModelExact("openrouter/" + model.ID + ":nitro"); !ok || got.ID != model.ID+":nitro" {
				t.Errorf("no :nitro variant of %s: %+v, %t", model.ID, got, ok)
			}
			break
		}
	}
}

func TestWithOpenRouterRoutingVariantsKeepsAListedVariantAndOtherProviders(t *testing.T) {
	models := []GeneratedModel{
		{Provider: "openai", ID: "m", DisplayName: "M", Record: `{"id":"m"}`},
		{Provider: "openrouter", ID: "a/b", DisplayName: "B", Record: `{"id":"a/b","name":"B"}`},
		{Provider: "openrouter", ID: "a/b:nitro", DisplayName: "B nitro", Record: `{"id":"a/b:nitro","name":"B nitro"}`},
	}
	var ids []string
	for _, model := range WithOpenRouterRoutingVariants(models) {
		ids = append(ids, model.Provider+"/"+model.ID)
	}
	if want := []string{"openai/m", "openrouter/a/b", "openrouter/a/b:floor", "openrouter/a/b:nitro"}; !slices.Equal(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	plain := models[:1]
	if got := WithOpenRouterRoutingVariants(plain); &got[0] != &plain[0] {
		t.Error("a list without an OpenRouter model is copied")
	}
}

// rewriteRecordIdentity must agree with a JSON decoder on records whose id and name sit anywhere among members that hold nested
// objects with members of the same names, escapes and characters JSON.stringify leaves alone.
func TestRewriteRecordIdentity(t *testing.T) {
	for _, record := range []string{
		`{"id":"a/b","name":"B","type":"chat"}`,
		`{"type":"chat","id":"a/b","name":"B <&> \"q\" \\ é"}`,
		`{"providers":[{"id":"x","name":"y"}],"compat":{"id":1,"name":[1,{"id":2}]},"id":"a/b","cost":{"input":0.5},"name":"B","reasoning":true,"x":null}`,
		`{"name":"B","limits":{"images":{"max":1}},"n":-1.5e3,"id":"a/b"}`,
	} {
		got := rewriteRecordIdentity(record, "a/b:nitro", "B (nitro) <&> \"z\"\n")
		want := decodedRecord(t, record)
		want["id"], want["name"] = "a/b:nitro", "B (nitro) <&> \"z\"\n"
		if !reflect.DeepEqual(decodedRecord(t, got), want) {
			t.Errorf("%s -> %s", record, got)
		}
		if strings.Contains(got, `\u003c`) {
			t.Errorf("%s escaped HTML characters", got)
		}
	}
	for _, record := range []string{`{"id":"x"}`, `{"name":"x"}`, `[]`, ``, `{"id":"x","name"`} {
		if got := rewriteRecordIdentity(record, "i", "n"); got != record {
			t.Errorf("%q changed to %q", record, got)
		}
	}
}

// The derived variants are held once per variant of each bundled model: listing again, or looking a variant up, builds none and the
// entries it returns are the held ones.
func TestDerivedOpenRouterVariantsAreBuiltOnce(t *testing.T) {
	bases := piListedModels(ListModels("openrouter"))
	count := func() int {
		n := 0
		derivedVariants.Range(func(_, _ any) bool { n++; return true })
		return n
	}
	first := count()
	if first < len(bases)*len(OpenRouterRoutingVariants) {
		t.Fatalf("%d derived variants held after a list of %d bases", first, len(bases))
	}
	allocs := testing.AllocsPerRun(5, func() { _ = ListModels("openrouter") })
	if second := count(); second != first {
		t.Errorf("a second list holds %d derived variants, the first held %d", second, first)
	}
	// Copying held entries costs a few hundred allocations (the cache keys), not the 2,661 deriving every variant took.
	if allocs > 1000 {
		t.Errorf("a list allocates %v times; it should copy held entries, not derive them", allocs)
	}
	spec := "openrouter/" + bases[0].ID + ":nitro"
	got, _ := LookupModelExact(spec)
	held, _ := derivedVariants.Load(derivedVariantKey{bases[0].ID, "nitro"})
	if got != held.(*derivedVariant).entry {
		t.Error("a lookup returns an entry other than the held one")
	}
}
