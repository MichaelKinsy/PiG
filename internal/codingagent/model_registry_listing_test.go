package codingagent

// D199: the lists that identify models (RuntimeModels, GetAllModelDataSources) name OpenRouter's routing variants from the listing by
// identity (builtinChatCatalogListing) and build no variant. These tests hold them to the lists built from the entries themselves
// (builtinChatCatalogListed), for every model and every field a reader gets.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// eagerRuntimeModels is RuntimeModels built from the listed entries: every model of a bundled catalog read from the entry
// builtinChatCatalogListed holds for it, a routing variant included.
func eagerRuntimeModels(r *ModelRegistry) []RuntimeModel {
	var result []RuntimeModel
	for _, id := range r.modelProviderIDs() {
		if r.hasModelDataOverlay(id) {
			for _, model := range r.GetProviderModelData(id) {
				result = append(result, RuntimeModel{Provider: model.ProviderMeta.ProviderID, ID: model.ID, Name: model.DisplayName, Reasoning: model.ProviderMeta.Reasoning || model.Capabilities.MaxThinking != "", Headers: ai.ProviderHeadersFromStrings(model.ProviderMeta.Headers)})
			}
			continue
		}
		for _, model := range builtinChatCatalogListed(id) {
			result = append(result, RuntimeModel{Provider: model.Provider, ID: model.ID, Name: model.DisplayName, Reasoning: model.Reasoning, Headers: ai.ProviderHeadersFromStrings(model.Headers)})
		}
	}
	return result
}

func requireSameRuntimeModels(t *testing.T, shape string, got, want []RuntimeModel) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d models, want %d", shape, len(got), len(want))
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Provider != w.Provider || g.ID != w.ID || g.Name != w.Name || g.Reasoning != w.Reasoning || !reflect.DeepEqual(g.Headers, w.Headers) || (g.NativeModel == nil) != (w.NativeModel == nil) {
			t.Fatalf("%s: model %d = {%s %s %q reasoning=%t headers=%v}, want {%s %s %q reasoning=%t headers=%v}", shape, i, g.Provider, g.ID, g.Name, g.Reasoning, g.Headers, w.Provider, w.ID, w.Name, w.Reasoning, w.Headers)
		}
	}
}

// registryShapes are the catalogs a registry composes: the bundled catalogs alone; models.json with a custom OpenAI-compatible provider whose
// ids look like routing variants, and with an override that makes OpenRouter's catalog a composed one; a saved OpenRouter list in place of the
// bundled one; and the bundled catalogs with an API stripped, which thins OpenRouter's list.
func registryShapes(t *testing.T) map[string]*ModelRegistry {
	t.Helper()
	shapes := map[string]*ModelRegistry{}
	plain := NewModelRegistry(t.TempDir())
	t.Cleanup(plain.CloseModelTasks)
	shapes["bundled"] = plain

	custom := t.TempDir()
	config := `{"providers":{
		"custom-gateway":{"baseUrl":"http://127.0.0.1:9/v1","api":"openai-completions","apiKey":"k","models":[{"id":"vendor/model:nitro","name":"Looks like a variant"},{"id":"vendor/model"},{"id":"openai/gpt-4o:floor"}]},
		"openai":{"modelOverrides":{"gpt-4o":{"name":"Renamed 4o"}}}}}`
	if err := os.WriteFile(filepath.Join(custom, "models.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	withCustom := NewModelRegistry(custom)
	t.Cleanup(withCustom.CloseModelTasks)
	shapes["custom provider"] = withCustom

	overridden := t.TempDir()
	if err := os.WriteFile(filepath.Join(overridden, "models.json"), []byte(`{"providers":{"openrouter":{"modelOverrides":{"openai/gpt-4o":{"name":"Overridden 4o"}}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	withOverride := NewModelRegistry(overridden)
	t.Cleanup(withOverride.CloseModelTasks)
	shapes["composed OpenRouter"] = withOverride

	saved := t.TempDir()
	store := ai.NewFileModelsStore(filepath.Join(saved, "models-store.json"))
	if err := store.Write(context.Background(), openRouterProviderID, ai.ModelsStoreEntry{Models: []ai.AnyModel{openRouterRefreshedModel("vendor/refreshed"), openRouterRefreshedModel("vendor/other")}, CheckedAt: new(float64(1))}); err != nil {
		t.Fatal(err)
	}
	refreshed := NewModelRegistry(saved)
	t.Cleanup(refreshed.CloseModelTasks)
	refreshed.SetModelsStore(store)
	if result := refreshed.RefreshCatalogs(context.Background(), CatalogRefreshOptions{AllowNetwork: false, Providers: []string{openRouterProviderID}}); len(result.Errors) != 0 || result.Aborted {
		t.Fatalf("restore = %+v", result)
	}
	shapes["saved OpenRouter list"] = refreshed
	return shapes
}

// RuntimeModels is the list built from the entries, model for model and field for field, for every shape of catalog.
func TestRuntimeModelsEqualTheListOfEntriesForEveryCatalogShape(t *testing.T) {
	variants := 0
	check := func(shape string, registry *ModelRegistry) {
		got := registry.RuntimeModels()
		requireSameRuntimeModels(t, shape, got, eagerRuntimeModels(registry))
		var model RuntimeModel
		for _, listed := range got {
			_, variant, ok := ai.OpenRouterRoutingVariant(listed.Provider, listed.ID)
			if !ok {
				model = listed
				continue
			}
			variants++
			// Where nothing renames a model, a variant is spelled here from the model before it, not by the rule under test.
			if shape != "bundled" && shape != "stripped API" {
				continue
			}
			if listed.ID != model.ID+":"+variant || listed.Name != model.Name+" ("+variant+")" || listed.Reasoning != model.Reasoning || !reflect.DeepEqual(listed.Headers, model.Headers) {
				t.Fatalf("%s: the variant {%s %q reasoning=%t headers=%v} does not follow from the model before it {%s %q reasoning=%t headers=%v}", shape, listed.ID, listed.Name, listed.Reasoning, listed.Headers, model.ID, model.Name, model.Reasoning, model.Headers)
			}
		}
	}
	for shape, registry := range registryShapes(t) {
		check(shape, registry)
	}
	undo := pigstrip.Strip(pigstrip.ListAPIs, "anthropic-messages")
	defer undo()
	stripped := NewModelRegistry(t.TempDir())
	t.Cleanup(stripped.CloseModelTasks)
	before := variants
	check("stripped API", stripped)
	if listed := builtinChatCatalogListed(openRouterProviderID); variants-before == 0 || variants-before >= 2*len(ai.ListModels(""))/3 || len(listed) == 0 {
		t.Fatalf("the stripped catalog lists %d routing variants", variants-before)
	}
	if variants < 2000 {
		t.Fatalf("%d routing variants compared over every shape", variants)
	}
}

// GetAllModelDataSources names the models GetAllModelData lists: the same models in the same order, each source's id and name the entry's, and
// each source's entry, built when it is asked for, the entry builtinChatCatalogListed holds, for every routing variant.
func TestModelDataSourcesEqualTheEntriesForEveryCatalogShape(t *testing.T) {
	variants := 0
	check := func(shape string, registry *ModelRegistry) {
		var want []ModelDataSource
		for _, id := range registry.modelProviderIDs() {
			if plain, ok := registry.plainGeneratedModels(id); ok {
				for _, generated := range ai.OfferedGeneratedModels(plain) {
					want = append(want, ModelDataSource{Generated: generated})
				}
				continue
			}
			for _, model := range registry.GetProviderModelData(id) {
				want = append(want, ModelDataSource{Model: model})
			}
		}
		got := registry.GetAllModelDataSources()
		all := registry.GetAllModelData()
		if len(got) != len(want) || len(got) != len(all) {
			t.Fatalf("%s: %d sources, want %d, for %d models", shape, len(got), len(want), len(all))
		}
		for i := range want {
			if want[i].Generated == nil {
				if got[i].Generated != nil || !reflect.DeepEqual(got[i].Model, want[i].Model) {
					t.Fatalf("%s: source %d = %+v, want the model %+v", shape, i, got[i], want[i].Model)
				}
				continue
			}
			entry := want[i].Generated
			if got[i].Generated == nil || got[i].Model != nil || got[i].ID() != entry.ID || got[i].Name() != entry.DisplayName {
				t.Fatalf("%s: source %d is %+v, want the entry %s (%q)", shape, i, got[i], entry.ID, entry.DisplayName)
			}
			if got[i].Generated.Provider != entry.Provider || got[i].Generated.Reasoning != entry.Reasoning || !reflect.DeepEqual(got[i].Generated.Headers, entry.Headers) {
				t.Fatalf("%s: source %d reads its provider, reasoning or headers from %+v, the entry is %+v", shape, i, got[i].Generated, entry)
			}
			if built := got[i].Entry(); !reflect.DeepEqual(*built, *entry) || !reflect.DeepEqual(built.ToModel(), all[i]) {
				t.Fatalf("%s: source %d builds %+v, want the entry %+v", shape, i, *built, *entry)
			}
			if got[i].Listed != nil {
				variants++
			}
		}
	}
	for shape, registry := range registryShapes(t) {
		check(shape, registry)
	}
	undo := pigstrip.Strip(pigstrip.ListAPIs, "anthropic-messages")
	defer undo()
	stripped := NewModelRegistry(t.TempDir())
	t.Cleanup(stripped.CloseModelTasks)
	check("stripped API", stripped)
	if variants < 2000 {
		t.Fatalf("%d routing variants compared over every shape", variants)
	}
}

// Pi's default model of a provider (defaultModelPerProvider as ported) is never a routing variant, and in every bundled provider's listing a
// variant comes after the model it is derived from, so the first model of a provider is never a variant. The model a custom id borrows from
// (buildFallbackModel: the default, else the first) is therefore always a model Pi lists.
func TestDefaultModelsAndFirstModelsAreNeverRoutingVariants(t *testing.T) {
	if len(DefaultModelPerProviderOrder) < 10 {
		t.Fatalf("%d provider defaults", len(DefaultModelPerProviderOrder))
	}
	for _, entry := range DefaultModelPerProviderOrder {
		if base, variant, ok := ai.OpenRouterRoutingVariant(entry.Provider, entry.ModelID); ok {
			t.Errorf("the default model of %s, %s, is the %s routing variant of %s", entry.Provider, entry.ModelID, variant, base)
		}
	}
	variants := 0
	for _, provider := range ai.ListProviders() {
		catalog, listing := builtinChatCatalogListing(provider)
		listed := builtinChatCatalogListed(provider)
		if listing == nil {
			for i := range listed {
				if _, _, ok := ai.OpenRouterRoutingVariant(listed[i].Provider, listed[i].ID); ok {
					t.Errorf("%s lists the routing variant %s without a listing by identity", provider, listed[i].ID)
				}
			}
			continue
		}
		model := ""
		for i := range listed {
			base, _, ok := ai.OpenRouterRoutingVariant(listed[i].Provider, listed[i].ID)
			if !ok {
				model = listed[i].ID
				continue
			}
			variants++
			if i == 0 || base != model || catalog[listing[i].Base].ID != model {
				t.Errorf("%s lists the routing variant %s at %d, not after its model (the model before it is %q)", provider, listed[i].ID, i, model)
			}
		}
	}
	if variants < 100 {
		t.Fatalf("%d routing variants listed", variants)
	}
}

type fixedResolverRuntime struct {
	models []RuntimeModel
	usable map[string]bool
}

func (r fixedResolverRuntime) GetModels(string) []RuntimeModel {
	return append([]RuntimeModel(nil), r.models...)
}
func (r fixedResolverRuntime) HasConfiguredAuth(provider string) bool { return r.usable[provider] }

func renderCliResult(result ResolveCliModelResult) string {
	if result.Error != "" {
		return "error: " + result.Error
	}
	if result.Model == nil {
		return "no model"
	}
	return fmt.Sprintf("%s/%s (%s) reasoning=%t thinking=%s warning=%s", result.Model.Provider, result.Model.ID, result.Model.Name, result.Model.Reasoning, result.ThinkingLevel, result.Warning)
}

// --model resolution over the routing variants: each case resolves to the same result, message and candidate list included, against the list of
// identities and against the list built from the entries, under every auth shape (none, an OAuth provider, an API key provider, the custom
// OpenAI-compatible provider, all of them, and an inline --api-key); the cases whose result follows from the resolver's rules alone are spelled out.
func TestCliModelResolutionOverRoutingVariants(t *testing.T) {
	registry := registryShapes(t)["custom provider"]
	listed, eager := registry.RuntimeModels(), eagerRuntimeModels(registry)
	requireSameRuntimeModels(t, "custom provider", listed, eager)
	cases := []struct{ provider, model, thinking, want string }{
		// the exact provider/id of a variant
		{"", "openrouter/openai/gpt-4o:nitro", "", "openrouter/openai/gpt-4o:nitro (OpenAI: GPT-4o (nitro)) reasoning=false thinking= warning="},
		{"openrouter", "openai/gpt-4o:floor", "", "openrouter/openai/gpt-4o:floor (OpenAI: GPT-4o (floor)) reasoning=false thinking= warning="},
		{"", "OpenRouter/OpenAI/GPT-4o:NITRO", "", "openrouter/openai/gpt-4o:nitro (OpenAI: GPT-4o (nitro)) reasoning=false thinking= warning="},
		// a variant of a catalog variant
		{"", "openrouter/openai/gpt-4o:batch:nitro", "", "openrouter/openai/gpt-4o:batch:nitro (OpenAI: GPT-4o (batch) (nitro)) reasoning=false thinking= warning="},
		// a bare id that is a variant's id, and one that is also a custom provider's model id
		{"", "openai/gpt-4o:nitro", "", "openrouter/openai/gpt-4o:nitro (OpenAI: GPT-4o (nitro)) reasoning=false thinking= warning="},
		{"", "openai/gpt-4o:floor", "", ""},
		{"", "vendor/model:nitro", "", "custom-gateway/vendor/model:nitro (Looks like a variant) reasoning=false thinking= warning="},
		{"", "custom-gateway/vendor/model:nitro", "", "custom-gateway/vendor/model:nitro (Looks like a variant) reasoning=false thinking= warning="},
		// a text that matches a model and its variants, on several providers
		{"", "gpt-4o", "", ""},
		{"", "openai/gpt-4o", "", ""},
		{"openrouter", "gpt-4o", "", ""},
		{"", "kimi-k2.7-code", "", ""},
		{"", "GPT-4o (nitro)", "", `error: Model "GPT-4o (nitro)" not found. Use --list-models to see available models.`},
		// a text that matches only variants
		{"", "gpt-4o:nitro", "", "openrouter/openai/gpt-4o:nitro (OpenAI: GPT-4o (nitro)) reasoning=false thinking= warning="},
		{"", "4o:floor", "", ""},
		{"openrouter", "kimi-k2.7-code:nitro", "", "openrouter/moonshotai/kimi-k2.7-code:nitro (MoonshotAI: Kimi K2.7 Code (nitro)) reasoning=true thinking= warning="},
		{"", ":nitro", "", ""},
		{"", "nitro", "", ""},
		{"", "(floor)", "", `error: Model "(floor)" not found. Use --list-models to see available models.`},
		// thinking-level suffixes
		{"", "openrouter/openai/gpt-4o:nitro:high", "", "openrouter/openai/gpt-4o:nitro (OpenAI: GPT-4o (nitro)) reasoning=false thinking=high warning="},
		{"", "openrouter/openai/gpt-4o:high", "", "openrouter/openai/gpt-4o (OpenAI: GPT-4o) reasoning=false thinking=high warning="},
		{"", "openai/gpt-4o:floor:low", "", ""},
		{"", "gpt-4o:nitro:max", "", "openrouter/openai/gpt-4o:nitro (OpenAI: GPT-4o (nitro)) reasoning=false thinking=max warning="},
		{"", "openrouter/openai/gpt-4o:nitro:bogus", "", `openrouter/openai/gpt-4o:nitro:bogus (openai/gpt-4o:nitro:bogus) reasoning=true thinking= warning=Model "openai/gpt-4o:nitro:bogus" not found for provider "openrouter". Using custom model id.`},
		{"", "openrouter/openai/gpt-4o:nitro:high", "low", "openrouter/openai/gpt-4o:nitro (OpenAI: GPT-4o (nitro)) reasoning=false thinking=high warning="},
		{"", "custom-gateway/vendor/model:nitro:medium", "", "custom-gateway/vendor/model:nitro (Looks like a variant) reasoning=false thinking=medium warning="},
		{"", "vendor/model:floor", "", `error: Model "vendor/model:floor" not found. Use --list-models to see available models.`},
		// texts that name no model
		{"", "openrouter/no/such-model:nitro", "", `openrouter/no/such-model:nitro (no/such-model:nitro) reasoning=true thinking= warning=Model "no/such-model:nitro" not found for provider "openrouter". Using custom model id.`},
		{"no-such-provider", "openai/gpt-4o:nitro", "", `error: Unknown provider "no-such-provider". Use --list-models to see available providers/models.`},
		{"", "no-such-model:floor", "", `error: Model "no-such-model:floor" not found. Use --list-models to see available models.`},
	}
	auths := map[string]map[string]bool{
		"no auth":         {},
		"OAuth provider":  {"github-copilot": true},
		"API key":         {"openrouter": true},
		"custom base URL": {"custom-gateway": true},
		"all":             {"github-copilot": true, "openrouter": true, "custom-gateway": true, "openai": true},
	}
	results := map[string]bool{}
	for name, usable := range auths {
		for _, inline := range []bool{false, true} {
			for _, c := range cases {
				got := renderCliResult(ResolveCliModel(c.provider, c.model, c.thinking, inline, fixedResolverRuntime{listed, usable}))
				want := renderCliResult(ResolveCliModel(c.provider, c.model, c.thinking, inline, fixedResolverRuntime{eager, usable}))
				if got != want {
					t.Errorf("%s, inline key %t: --provider %q --model %q --thinking %q\n got  %s\n want %s", name, inline, c.provider, c.model, c.thinking, got, want)
				}
				if c.want != "" && got != c.want {
					t.Errorf("%s, inline key %t: --provider %q --model %q resolves to\n %s, want\n %s", name, inline, c.provider, c.model, got, c.want)
				}
				results[strings.SplitN(got, " ", 2)[0]] = true
				if name == "no auth" && !inline {
					t.Logf("--provider %q --model %q --thinking %q: %s", c.provider, c.model, c.thinking, got)
				}
			}
		}
	}
	if !results["error:"] || len(results) < 6 {
		t.Fatalf("the cases reached %d kinds of result: %v", len(results), results)
	}
}

// Scoped-model patterns over the routing variants resolve to the same scope, order and diagnostics against both lists.
func TestModelScopePatternsOverRoutingVariants(t *testing.T) {
	registry := registryShapes(t)["custom provider"]
	listed, eager := registry.RuntimeModels(), eagerRuntimeModels(registry)
	render := func(result ResolveModelScopeResult) string {
		var out strings.Builder
		for _, scoped := range result.ScopedModels {
			fmt.Fprintf(&out, "%s/%s (%s) reasoning=%t thinking=%s\n", scoped.Model.Provider, scoped.Model.ID, scoped.Model.Name, scoped.Model.Reasoning, scoped.ThinkingLevel)
		}
		for _, diagnostic := range result.Diagnostics {
			fmt.Fprintf(&out, "diagnostic %+v\n", diagnostic)
		}
		return out.String()
	}
	scoped := 0
	for _, patterns := range [][]string{
		{"openrouter/openai/gpt-4o*"}, {"*:nitro"}, {"*:floor:high"}, {"openrouter/*:nitro", "custom-gateway/*"}, {"openai/gpt-4o:nitro"}, {"openrouter/openai/gpt-4o:floor:low"},
		{"*/gpt-4o:*"}, {"vendor/model:nitro"}, {"*4o*"}, {"gpt-4o"}, {"kimi-k2.7-code:nitro", "openrouter/moonshotai/*"}, {"[openai/gpt-4o:nitro]"}, {"no-such:nitro"}, {"*"},
	} {
		got, want := render(ResolveModelScopeFromModels(patterns, listed)), render(ResolveModelScopeFromModels(patterns, eager))
		if got != want {
			t.Errorf("patterns %v\n got  %s\n want %s", patterns, got, want)
		}
		scoped += strings.Count(got, "\n")
	}
	if scoped < 500 {
		t.Fatalf("the patterns scoped %d models", scoped)
	}
}

// A process that has only identified models (RuntimeModels, GetAllModelDataSources, a --model resolution) has built no routing variant: the
// list of entries is still to build when it is first read, and a model that is kept builds one variant. Run in a process of its own, since any
// other test of the package may have read the list, and with its collector off (GOGC=off): runtime.MemStats counts the whole process,
// the collector's own allocations included (its workers' sudogs, the threads started for them), and under -race and load those crossed
// the bound on keeping one variant.
func TestIdentifyingModelsBuildsNoRoutingVariant(t *testing.T) {
	if os.Getenv("PIG_TEST_IDENTIFY_ONLY") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestIdentifyingModelsBuildsNoRoutingVariant$", "-test.count=1")
		cmd.Env = append(os.Environ(), "PIG_TEST_IDENTIFY_ONLY=1", "GOGC=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("the fresh process failed: %v\n%s", err, out)
		}
		return
	}
	if percent := debug.SetGCPercent(-1); percent != -1 {
		t.Fatalf("the measuring process collects at GOGC=%d, want GOGC=off", percent)
	}
	mallocs := func(run func()) uint64 {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		run()
		runtime.ReadMemStats(&after)
		return after.Mallocs - before.Mallocs
	}
	registry := NewModelRegistry(t.TempDir())
	t.Cleanup(registry.CloseModelTasks)
	models := registry.RuntimeModels()
	sources := registry.GetAllModelDataSources()
	resolved := ResolveCliModel("", "openrouter/openai/gpt-4o:nitro", "", false, fixedResolverRuntime{models, nil})
	if resolved.Model == nil || resolved.Model.ID != "openai/gpt-4o:nitro" {
		t.Fatalf("resolved %+v", resolved)
	}
	var variant *ModelDataSource
	variants := 0
	for i := range sources {
		if sources[i].Listed != nil {
			variants++
			if sources[i].ID() == resolved.Model.ID {
				variant = &sources[i]
			}
		}
	}
	if variants < 100 || variant == nil {
		t.Fatalf("%d routing variants identified", variants)
	}
	var kept *ai.GeneratedModel
	if one := mallocs(func() { kept = variant.Entry() }); one > 20 || kept.ID != resolved.Model.ID {
		t.Fatalf("keeping one routing variant allocates %d times", one)
	}
	if want := variant.Generated.OpenRouterVariant("nitro"); !reflect.DeepEqual(*kept, want) || kept.DisplayName != variant.Name() || kept.DisplayName != resolved.Model.Name {
		t.Fatalf("the kept variant is %+v (resolved as %q, listed as %q), want the variant derived from its model %+v", *kept, resolved.Model.Name, variant.Name(), want)
	}
	// The kept variant is not built again: a later read is the same entry holding the same record string. Every build derives the
	// variant's record from its model's (ai's openRouterVariantNamed: derived.Record = deriveCatalogRecord(m.Record, id, name)), and
	// deriveCatalogRecord makes a new string, by concatenation for a bundled row's reference and in a strings.Builder for JSON, also when
	// the build is written over the held entry; the kept record is in use, so a new string cannot be at its address. The one build this
	// would not see reuses the model's own record (rewriteRecordIdentity returns its argument for a record without an id and a name),
	// so the kept record must be a derived one.
	//
	// An allocation count of the read cannot hold this: the count is the whole process's, so it is not exact for one call.
	record, base := unsafe.StringData(kept.Record), unsafe.StringData(variant.Generated.Record) //nolint:gosec // G103: the test compares the data addresses of strings
	if kept.Record == "" || record == base {
		t.Fatalf("the kept variant %q has no record of its own to tell a second build by (%d bytes, its model's: %t)", kept.ID, len(kept.Record), record == base)
	}
	again := variant.Entry()
	if read := unsafe.StringData(again.Record); again != kept || read != record { //nolint:gosec // G103: the test compares the data address of a string that must not be derived again
		t.Fatalf("the kept variant is built again (entry %p with its record at %p, kept %p with its record at %p)", again, read, kept, record)
	}
	var listed []ai.GeneratedModel
	if built := mallocs(func() { listed = builtinChatCatalogListed(openRouterProviderID) }); built < uint64(variants) {
		t.Fatalf("the first read of the list of entries allocates %d times for %d variants: the variants were built before it", built, variants)
	}
	if exact, ok := ai.LookupModelExact("openrouter/" + kept.ID); !ok || !reflect.DeepEqual(*exact, *kept) {
		t.Fatalf("the kept variant %+v is not the listed one %+v", kept, exact)
	}
	if len(listed) != len(sources)-len(models)+len(listed) || len(listed) == 0 {
		t.Fatalf("%d sources for %d models; %d entries listed", len(sources), len(models), len(listed))
	}
}

// A catalog refresh while lists are read: every list read is a whole list of one catalog, the bundled one or the refreshed one, and after the
// refresh no list names a variant of the bundled catalog. Run under -race.
func TestRefreshWhileIdentifyingModelsListsOneCatalog(t *testing.T) {
	dir := t.TempDir()
	store := ai.NewFileModelsStore(filepath.Join(dir, "models-store.json"))
	if err := store.Write(context.Background(), openRouterProviderID, ai.ModelsStoreEntry{Models: []ai.AnyModel{openRouterRefreshedModel("vendor/refreshed")}, CheckedAt: new(float64(1))}); err != nil {
		t.Fatal(err)
	}
	held := currentBuiltinChatCatalogs.Swap(nil)
	t.Cleanup(func() { currentBuiltinChatCatalogs.Store(held) })
	registry := NewModelRegistry(dir)
	t.Cleanup(registry.CloseModelTasks)
	registry.SetModelsStore(store)
	bundled := len(builtinChatCatalog(openRouterProviderID)) * (1 + len(ai.OpenRouterRoutingVariants))
	openRouter := func(models []RuntimeModel) []string {
		var ids []string
		for _, model := range models {
			if model.Provider == openRouterProviderID {
				ids = append(ids, model.ID)
			}
		}
		return ids
	}
	var readers sync.WaitGroup
	stop := make(chan struct{})
	failures := make(chan string, 64)
	for reader := range 6 {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				var ids []string
				if reader%2 == 0 {
					ids = openRouter(registry.RuntimeModels())
				} else {
					for _, source := range registry.GetAllModelDataSources() {
						if source.Generated != nil && source.Generated.Provider == openRouterProviderID {
							ids = append(ids, source.ID())
						} else if source.Model != nil && source.Model.ProviderMeta.ProviderID == openRouterProviderID {
							ids = append(ids, source.Model.ID)
						}
					}
				}
				if len(ids) != bundled && !reflect.DeepEqual(ids, refreshedOpenRouterIDs) {
					select {
					case failures <- fmt.Sprintf("a list of %d OpenRouter models: neither the bundled %d nor the refreshed %v", len(ids), bundled, refreshedOpenRouterIDs):
					default:
					}
				}
			}
		})
	}
	if result := registry.RefreshCatalogs(context.Background(), CatalogRefreshOptions{AllowNetwork: false, Providers: []string{openRouterProviderID}}); len(result.Errors) != 0 || result.Aborted {
		t.Fatalf("restore = %+v", result)
	}
	close(stop)
	readers.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	requireIDs(t, "RuntimeModels after the refresh", openRouter(registry.RuntimeModels()), refreshedOpenRouterIDs)
	var sources []string
	for _, source := range registry.GetAllModelDataSources() {
		if source.Model != nil && source.Model.ProviderMeta.ProviderID == openRouterProviderID {
			sources = append(sources, source.Model.ID)
		} else if source.Generated != nil && source.Generated.Provider == openRouterProviderID {
			sources = append(sources, "bundled "+source.ID())
		}
	}
	requireIDs(t, "GetAllModelDataSources after the refresh", sources, refreshedOpenRouterIDs)
}
