package ai

// pi: packages/ai/src/model-catalog.ts

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// pinnedCatalogIDs reads providers/data/<provider>.json in document order and returns the model ids per model type in the
// order flattenModelCatalog in packages/ai/src/model-catalog.ts visits them: groups in key order, then models in key order.
func pinnedCatalogIDs(t *testing.T, provider string) map[string][]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("../.upstream/current/packages/ai/src/providers/data", provider+".json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	expect := func(want json.Delim) {
		t.Helper()
		if token, err := decoder.Token(); err != nil || token != want {
			t.Fatalf("%s: token %v, error %v, want %v", provider, token, err, want)
		}
	}
	out := map[string][]string{}
	expect('{')
	for decoder.More() {
		if _, err := decoder.Token(); err != nil { // group key
			t.Fatal(err)
		}
		expect('{')
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				t.Fatal(err)
			}
			var model struct{ ID, Type, Provider string }
			if err := decoder.Decode(&model); err != nil {
				t.Fatal(err)
			}
			kind, id, _ := strings.Cut(key.(string), ":")
			if kind != model.Type || id != model.ID || model.Provider != provider {
				t.Fatalf("%s: key %q does not match its entry", provider, key)
			}
			out[kind] = append(out[kind], id)
		}
		expect('}')
	}
	return out
}

// Each <ID>_MODELS, <ID>_IMAGE_MODELS and <ID>_CLASSIFIER_MODELS export of the pinned providers/*.models.ts is a record of
// the provider's models of one type by id, in data-file order (providers/*.models.ts, model-catalog.ts flattenModelCatalog).
func TestProviderCatalogExportsMatchPinnedData(t *testing.T) {
	if len(providerCatalogCases()) == 0 {
		t.Fatal("no provider catalog cases")
	}
	for _, tc := range providerCatalogCases() {
		t.Run(tc.provider, func(t *testing.T) {
			want := pinnedCatalogIDs(t, tc.provider)
			chat, image, classifier := tc.chat(), tc.image(), tc.classifier()
			if !slices.Equal(chat.IDs(), want["chat"]) {
				t.Errorf("chat ids %v, upstream %v", chat.IDs(), want["chat"])
			}
			if !slices.Equal(image.IDs(), want["image"]) {
				t.Errorf("image ids %v, upstream %v", image.IDs(), want["image"])
			}
			if !slices.Equal(classifier.IDs(), want["classifier"]) {
				t.Errorf("classifier ids %v, upstream %v", classifier.IDs(), want["classifier"])
			}
			if chat.Len() != len(want["chat"]) || image.Len() != len(want["image"]) || classifier.Len() != len(want["classifier"]) {
				t.Errorf("lengths %d/%d/%d, upstream %d/%d/%d", chat.Len(), image.Len(), classifier.Len(), len(want["chat"]), len(want["image"]), len(want["classifier"]))
			}
			for i, id := range chat.IDs() {
				model, ok := chat.Get(id)
				if !ok || model.ID != id || model.ProviderID() != tc.provider || chat.Values()[i] != model {
					t.Errorf("chat %q: %+v", id, model)
				}
			}
			for i, id := range image.IDs() {
				model, ok := image.Get(id)
				if !ok || model.ID != id || model.Provider != tc.provider || image.Values()[i] != model {
					t.Errorf("image %q: %+v", id, model)
				}
			}
			for i, id := range classifier.IDs() {
				model, ok := classifier.Get(id)
				if !ok || model.ID != id || model.Provider != tc.provider || classifier.Values()[i] != model {
					t.Errorf("classifier %q: %+v", id, model)
				}
			}
			if _, ok := chat.Get("no-such-model"); ok {
				t.Error("Get found a model that is not in the catalog")
			}
		})
	}
}

// The catalog is a copy: changing a model or the id list does not change the next call or the generated data.
func TestProviderCatalogExportsReturnCopies(t *testing.T) {
	chat := OpenAIModels()
	if chat.Len() == 0 {
		t.Fatal("openai has no chat models")
	}
	id := chat.IDs()[0]
	model, _ := chat.Get(id)
	model.ProviderMeta.Headers = map[string]string{"x": "y"}
	model.DisplayName = "changed"
	ids := chat.IDs()
	ids[0] = "changed"
	again, _ := OpenAIModels().Get(id)
	if again.DisplayName == "changed" || OpenAIModels().IDs()[0] != id {
		t.Fatal("the catalog shares state with an earlier call")
	}
	var image ImageModelCatalog
	var imageFn func() ImageModelCatalog
	for _, tc := range providerCatalogCases() {
		if image = tc.image(); image.Len() > 0 {
			imageFn = tc.image
			break
		}
	}
	if imageFn == nil {
		t.Fatal("no provider has image models")
	}
	first, _ := image.Get(image.IDs()[0])
	first.Output = append(first.Output, "changed")
	first.Name = "changed"
	if second, _ := imageFn().Get(image.IDs()[0]); second.Name == "changed" || slices.Contains(second.Output, "changed") {
		t.Fatal("the image catalog shares state with an earlier call")
	}
}

// Object.values of a record with a repeated key keeps the first position and the last value.
func TestOrderedCatalogRepeatedKeyKeepsFirstPosition(t *testing.T) {
	catalog := newOrderedCatalog[int](0)
	catalog.add("a", 1)
	catalog.add("b", 2)
	catalog.add("a", 3)
	if got := catalog.IDs(); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("ids %v", got)
	}
	if got := catalog.Values(); !slices.Equal(got, []int{3, 2}) {
		t.Fatalf("values %v", got)
	}
}

// Every pinned packages/ai/src/providers/<id>.models.ts has its three catalog exports in providerCatalogCases.
func TestProviderCatalogCasesCoverEveryModelsFile(t *testing.T) {
	files, err := filepath.Glob("../.upstream/current/packages/ai/src/providers/*.models.ts")
	if err != nil || len(files) == 0 {
		t.Fatalf("pinned models files: %v, %v", files, err)
	}
	var cases []string
	for _, c := range providerCatalogCases() {
		cases = append(cases, c.provider)
	}
	for _, f := range files {
		if id := strings.TrimSuffix(filepath.Base(f), ".models.ts"); !slices.Contains(cases, id) {
			t.Errorf("no catalog exports for %s", id)
		}
	}
	if len(cases) != len(files) {
		t.Errorf("%d catalog cases for %d models files", len(cases), len(files))
	}
}
