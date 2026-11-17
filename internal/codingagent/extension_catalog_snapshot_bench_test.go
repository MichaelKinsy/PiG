package codingagent

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestCloneCatalogInputDoesNotAllocateInlineStructs(t *testing.T) {
	input := reflect.ValueOf(struct {
		ID     string
		Limits struct{ Tokens int }
	}{ID: "model"})
	allocations := testing.AllocsPerRun(100, func() {
		if _, ok := cloneCatalogInput(input); !ok {
			panic("scalar snapshot rejected")
		}
	})
	// One owned root holds both the outer fields and its inline scalar-only child.
	if allocations != 1 {
		t.Fatalf("snapshot allocated %.0f objects, want its one owned root", allocations)
	}
}

// catalogCloneFixturePath is the fixed set of models BenchmarkCloneCatalogInput clones: a models store entry under the data-only testdata directory,
// read by path. It is chosen from the built-in catalog of 2026-11-16 so that every shape of model the clone copies is in it (see
// TestCatalogCloneFixtureSetsEveryModelField), and it does not change when the catalog is refreshed: the benchmark measures the clone, not the catalog.
const catalogCloneFixturePath = "testdata/catalog-clone-fixture.json"

func catalogCloneFixture(tb testing.TB) []*ai.Model {
	tb.Helper()
	data, err := os.ReadFile(filepath.FromSlash(catalogCloneFixturePath))
	if err != nil {
		tb.Fatal(err)
	}
	var entry ai.ModelsStoreEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		tb.Fatalf("%s: %v", catalogCloneFixturePath, err)
	}
	models := make([]*ai.Model, len(entry.Models))
	for i, model := range entry.Models {
		typed, ok := model.(*ai.Model)
		if !ok {
			tb.Fatalf("%s: model %d is a %T, want a chat model", catalogCloneFixturePath, i, model)
		}
		// A caller that decodes a model fills the order of its sampling parameters (ai.Model.SamplingParamsOrder).
		typed.SamplingParamsOrder = slices.Sorted(maps.Keys(typed.SamplingParams))
		if len(typed.SamplingParamsByThinkingLevel) > 0 {
			typed.SamplingParamsByThinkingLevelOrder = map[ai.ModelThinkingLevel][]string{}
			for level, params := range typed.SamplingParamsByThinkingLevel {
				typed.SamplingParamsByThinkingLevelOrder[level] = slices.Sorted(maps.Keys(params))
			}
		}
		models[i] = typed
	}
	return models
}

// BenchmarkCloneCatalogInput is the allocation cost of the snapshot an extension's catalog is built from, on the fixed fixture catalog: its count changes
// only when the clone does.
func BenchmarkCloneCatalogInput(b *testing.B) {
	value := reflect.ValueOf(catalogCloneFixture(b))
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := cloneCatalogInput(value); !ok {
			b.Fatal("catalog snapshot rejected")
		}
	}
}

// BenchmarkCloneCatalogInputLiveCatalog is the same clone over every model of the built-in catalog. It is informational: its count follows the catalog data
// and moves at every catalog refresh, so allocbase does not gate it (its skipped map). allocs/model is the figure to compare across refreshes.
func BenchmarkCloneCatalogInputLiveCatalog(b *testing.B) {
	models := make([]*ai.Model, len(ai.GeneratedModels))
	for i := range ai.GeneratedModels {
		models[i] = ai.GeneratedModels[i].ToModel()
	}
	value := reflect.ValueOf(models)
	b.ReportAllocs()
	var allocs float64
	for b.Loop() {
		allocs = testing.AllocsPerRun(1, func() {
			if _, ok := cloneCatalogInput(value); !ok {
				b.Fatal("catalog snapshot rejected")
			}
		})
	}
	b.ReportMetric(float64(len(models)), "models")
	b.ReportMetric(allocs/float64(len(models)), "allocs/model")
}

// catalogCloneFixtureExempt are the members of ai.Model that no fixture model sets, each with its reason.
var catalogCloneFixtureExempt = map[string]string{
	"Model.ProviderMeta.Compat.Extra":    "raw JSON members: the snapshot clone declines a value with a marshaler by design (copyCatalogInput), so such a model never reaches it",
	"Model.Provider":                     "the runtime provider a registry attaches; a stored or built-in model has none",
	"Model.Capabilities.SupportsImages":  "derived from Input when a built-in model is converted (GeneratedModel.ToCapabilities); a model decoded from a store entry leaves it unset",
	"Model.Capabilities.SupportsToolUse": "derived when a built-in model is converted (GeneratedModel.ToCapabilities); a model decoded from a store entry leaves it unset",
}

// TestCatalogCloneFixtureSetsEveryModelField fails for a member of ai.Model (and of the types it holds) that no fixture model sets, so a field added to the model
// cannot leave the benchmark cloning less than the clone copies: add a model that sets it to the fixture, or exempt it here with its reason.
func TestCatalogCloneFixtureSetsEveryModelField(t *testing.T) {
	models := catalogCloneFixture(t)
	if len(models) < 10 || len(models) > 100 {
		t.Fatalf("fixture holds %d models, want tens", len(models))
	}
	values := make([]reflect.Value, len(models))
	for i, model := range models {
		values[i] = reflect.ValueOf(model).Elem()
	}
	seenExempt := map[string]bool{}
	var walk func(path string, typ reflect.Type, values []reflect.Value)
	walk = func(path string, typ reflect.Type, values []reflect.Value) {
		if _, exempt := catalogCloneFixtureExempt[path]; exempt {
			seenExempt[path] = true
			return
		}
		switch typ.Kind() {
		case reflect.Struct:
			for i := range typ.NumField() {
				field := typ.Field(i)
				if !field.IsExported() {
					continue
				}
				sub := make([]reflect.Value, len(values))
				for j, v := range values {
					sub[j] = v.Field(i)
				}
				walk(path+"."+field.Name, field.Type, sub)
			}
		case reflect.Pointer:
			// A pointer to a scalar (a compat flag) is set by any non-nil value, true or false.
			var elems []reflect.Value
			for _, v := range values {
				if !v.IsNil() {
					elems = append(elems, v.Elem())
				}
			}
			switch {
			case len(elems) == 0:
				t.Errorf("%s: no fixture model sets it", path)
			case typ.Elem().Kind() != reflect.Struct:
			default:
				walk(path, typ.Elem(), elems)
			}
		case reflect.Slice:
			var elems []reflect.Value
			for _, v := range values {
				for i := range v.Len() {
					elems = append(elems, v.Index(i))
				}
			}
			if len(elems) == 0 {
				t.Errorf("%s: no fixture model sets it", path)
				return
			}
			walk(path+"[]", typ.Elem(), elems)
		case reflect.Map:
			var elems []reflect.Value
			for _, v := range values {
				for _, key := range v.MapKeys() {
					elems = append(elems, v.MapIndex(key))
				}
			}
			if len(elems) == 0 {
				t.Errorf("%s: no fixture model sets it", path)
				return
			}
			walk(path+"{}", typ.Elem(), elems)
		case reflect.Interface:
			set := false
			for _, v := range values {
				set = set || !v.IsNil()
			}
			if !set {
				t.Errorf("%s: no fixture model sets it", path)
			}
		default:
			set := false
			for _, v := range values {
				set = set || !v.IsZero()
			}
			if !set {
				t.Errorf("%s: no fixture model sets it", path)
			}
		}
	}
	walk("Model", reflect.TypeFor[ai.Model](), values)
	for path := range catalogCloneFixtureExempt {
		if !seenExempt[path] {
			t.Errorf("%s is exempt and is not a member of ai.Model", path)
		}
	}
}
