package codingagent

import (
	"reflect"
	"strconv"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

var builtinChatModelsSink []*ai.Model

// convertedByEachReader is what the composer and the provider model data built from the listed catalog before the conversion was shared: every listed
// model converted by its own ToModel call, with the capabilities the typed base set on it.
func convertedByEachReader(id string) []*ai.Model {
	listed := builtinChatCatalogListed(id)
	if len(listed) == 0 {
		return nil
	}
	models := make([]*ai.Model, 0, len(listed))
	for _, generated := range listed {
		model := generated.ToModel()
		model.Capabilities = generated.ToCapabilities()
		models = append(models, model)
	}
	return models
}

// The shared snapshot's models are, for every provider and the OpenRouter routing variants, deeply equal to what each reader converted itself.
func TestBuiltinChatModelsEqualTheConversionOfEveryListedModel(t *testing.T) {
	total := 0
	for _, id := range append(ai.ListProviders(), "no-such-provider") {
		want, got := convertedByEachReader(id), builtinChatModels(id)
		if len(want) != len(got) {
			t.Fatalf("builtinChatModels(%q) = %d models, want %d", id, len(got), len(want))
		}
		if want == nil && got != nil {
			t.Fatalf("builtinChatModels(%q) is %v for a provider that lists none, want nil", id, got)
		}
		for i := range want {
			if !reflect.DeepEqual(want[i], got[i]) {
				t.Fatalf("builtinChatModels(%q)[%d] = %+v, want %+v", id, i, got[i], want[i])
			}
		}
		total += len(got)
	}
	if total < 1000 {
		t.Fatalf("%d models converted, the bundled catalog lists more", total)
	}
	if variants := len(builtinChatModels("openrouter")); variants <= len(builtinChatCatalog("openrouter")) {
		t.Fatalf("openrouter snapshot holds %d models, no routing variants beyond its %d bundled models", variants, len(builtinChatCatalog("openrouter")))
	}
}

// Each reader gets its own slice and its own top-level models: replacing a field of one reader's model reaches neither the snapshot nor another reader.
func TestBuiltinChatModelsAreIndependentAtTheTopLevel(t *testing.T) {
	first, second := builtinChatModels("anthropic"), builtinChatModels("anthropic")
	if len(first) == 0 || len(first) != len(second) {
		t.Fatalf("readers got %d and %d models", len(first), len(second))
	}
	if &first[0] == &second[0] {
		t.Fatal("two readers share one slice")
	}
	for i := range first {
		if first[i] == second[i] {
			t.Fatalf("model %d is the same pointer in two readers", i)
		}
	}
	untouched := convertedByEachReader("anthropic")
	first[0].DisplayName, first[0].ProviderMeta.BaseURL, first[0].Capabilities.ContextWindow = "changed", "https://changed.invalid", 1
	first[1] = nil
	if again := builtinChatModels("anthropic"); !reflect.DeepEqual(again, untouched) {
		t.Fatal("a reader's change of its model reached the snapshot")
	}
	if !reflect.DeepEqual(second, untouched) {
		t.Fatal("a reader's change of its model reached another reader")
	}
}

// The conversion happens once: a later reader allocates its slice and one model each, where converting it again costs several allocations a model.
func TestBuiltinChatModelsConvertOnce(t *testing.T) {
	id := "anthropic"
	snapshot := builtinChatModels(id)
	n := len(snapshot)
	if n == 0 {
		t.Fatalf("%s lists no model", id)
	}
	reads := testing.AllocsPerRun(20, func() { builtinChatModelsSink = builtinChatModels(id) })
	if want := float64(n + 1); reads != want {
		t.Fatalf("a reader of the snapshot allocates %v, want %v: its slice and one model each", reads, want)
	}
	converting := testing.AllocsPerRun(20, func() { builtinChatModelsSink = convertedByEachReader(id) })
	if converting < 3*float64(n) {
		t.Fatalf("converting the listed catalog allocates %v for %d models: the comparison no longer measures a conversion", converting, n)
	}
}

// A change of the stripped APIs replaces the snapshot as a whole: the models read afterwards are those of the new set, and a reader never gets a mix. Run under -race.
func TestBuiltinChatModelsFollowTheStrippedAPIsWithoutMixing(t *testing.T) {
	var id string
	var stockLen int
	var api ai.API
	for _, provider := range ai.ListProviders() {
		models := builtinChatCatalogListed(provider)
		apis := map[ai.API]bool{}
		for i := range models {
			apis[models[i].API] = true
		}
		if len(apis) > 1 {
			for candidate := range apis {
				id, stockLen, api = provider, len(models), candidate
			}
			break
		}
	}
	if id == "" {
		t.Skip("no provider with models of two APIs")
	}
	undo := pigstrip.Strip(pigstrip.ListAPIs, string(api))
	strippedLen := len(builtinChatModels(id))
	undo()
	if strippedLen == 0 || strippedLen >= stockLen {
		t.Fatalf("%s: stripping %s leaves %d of %d models", id, api, strippedLen, stockLen)
	}
	if back := len(builtinChatModels(id)); back != stockLen {
		t.Fatalf("%s: after the strip is undone %d models, want %d", id, back, stockLen)
	}

	stop := make(chan struct{})
	var toggler sync.WaitGroup
	toggler.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			pigstrip.Strip(pigstrip.ListAPIs, string(api))()
		}
	})
	var readers sync.WaitGroup
	for range 8 {
		readers.Go(func() {
			for range 300 {
				models := builtinChatModels(id)
				switch len(models) {
				case stockLen:
				case strippedLen:
					for _, model := range models {
						if model.ProviderMeta.API == api {
							t.Errorf("a snapshot of %d models holds a model of the stripped API %s", len(models), api)
							return
						}
					}
				default:
					t.Errorf("a reader got %d models, want %d or %d", len(models), stockLen, strippedLen)
					return
				}
			}
		})
	}
	readers.Wait()
	close(stop)
	toggler.Wait()
}

// A registration committed while the provider's model data is read leaves each reader one registration's view: every model of a result carries one base URL, and the
// bundled models are the whole snapshot. Run under -race.
func TestProviderModelDataIsOneRefreshWhileRegistrationsCommit(t *testing.T) {
	registry := NewModelRegistry(t.TempDir())
	stock := len(builtinChatCatalogListed("anthropic"))
	register := func(i int) {
		if err := registry.RegisterExtensionProvider("anthropic", extension.ProviderConfig{BaseURL: "https://refresh-" + strconv.Itoa(i) + ".test"}); err != nil {
			t.Error(err)
		}
	}
	register(0)
	var writers, readers sync.WaitGroup
	stop := make(chan struct{})
	writers.Go(func() {
		for i := 1; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			register(i)
		}
	})
	for range 4 {
		readers.Go(func() {
			for range 60 {
				models := registry.GetProviderModelData("anthropic")
				if len(models) != stock {
					t.Errorf("a reader got %d models, want %d", len(models), stock)
					return
				}
				for _, model := range models {
					if model.ProviderMeta.BaseURL != models[0].ProviderMeta.BaseURL {
						t.Errorf("a reader got base URLs %q and %q of two refreshes in one result", models[0].ProviderMeta.BaseURL, model.ProviderMeta.BaseURL)
						return
					}
				}
			}
		})
	}
	readers.Wait()
	close(stop)
	writers.Wait()
}
