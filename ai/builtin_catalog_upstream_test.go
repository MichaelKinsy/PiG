package ai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The pinned 1.0.4 provider catalogs are `providers/<id>.models.ts` modules that flatten `providers/data/<id>.json` into
// a chat, an image and a classifier model record per provider. The Go catalogs are generated from the same data: every
// provider module must list the same model keys with the same identity, endpoint, capability and price fields.
func TestBuiltinCatalogsMatchPinnedProviderDataModules(t *testing.T) {
	modules, err := filepath.Glob("../.upstream/current/packages/ai/src/providers/*.models.ts")
	if err != nil || len(modules) == 0 {
		t.Fatalf("upstream provider model modules: %v", err)
	}
	flatten := regexp.MustCompile(`flattenChatModelCatalog\("([^"]+)", values\)`)
	dataImport := regexp.MustCompile(`from "\./data/([^"]+)\.json"`)
	seen := map[string]bool{}
	for _, module := range modules {
		source, err := os.ReadFile(module)
		if err != nil {
			t.Fatal(err)
		}
		provider := flatten.FindStringSubmatch(string(source))
		data := dataImport.FindStringSubmatch(string(source))
		if provider == nil || data == nil {
			t.Fatalf("%s: no catalog flatten call or data import", module)
		}
		seen[provider[1]] = true
		t.Run(provider[1], func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("../.upstream/current/packages/ai/src/providers/data", data[1]+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var groups map[string]map[string]map[string]any
			if err := json.Unmarshal(raw, &groups); err != nil {
				t.Fatal(err)
			}
			wantChat, wantImage, wantClassifier := map[string]map[string]any{}, map[string]map[string]any{}, map[string]map[string]any{}
			for _, models := range groups {
				for key, model := range models {
					kind, id, _ := strings.Cut(key, ":")
					if model["id"] != id || model["type"] != kind || model["provider"] != provider[1] {
						t.Fatalf("%s: key %q does not match its entry", data[1], key)
					}
					switch kind {
					case "chat":
						wantChat[id] = model
					case "image":
						wantImage[id] = model
					case "classifier":
						wantClassifier[id] = model
					default:
						t.Fatalf("%s: unknown model type in key %q", data[1], key)
					}
				}
			}
			checkChatCatalog(t, provider[1], wantChat)
			checkImageCatalog(t, provider[1], wantImage)
			checkClassifierCatalog(t, provider[1], wantClassifier)
		})
	}
	if len(seen) == 0 {
		t.Fatal("no provider catalog compared")
	}
}

func catalogCost(t *testing.T, want map[string]any) ModelCost {
	t.Helper()
	encoded, err := json.Marshal(want["cost"])
	if err != nil {
		t.Fatal(err)
	}
	var cost ModelCost
	if err := json.Unmarshal(encoded, &cost); err != nil {
		t.Fatal(err)
	}
	return cost
}

func catalogStrings(value any) []string {
	var out []string
	items, _ := value.([]any)
	for _, item := range items {
		out = append(out, item.(string))
	}
	return out
}

// sameJSON reports whether a Go value marshals to the same JSON document as the upstream data value.
func sameJSON(t *testing.T, got any, want any) bool {
	t.Helper()
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(decoded, want)
}

func checkChatCatalog(t *testing.T, provider string, want map[string]map[string]any) {
	t.Helper()
	got := ListModels(provider)
	if len(got) != len(want) {
		t.Errorf("%s chat models: %d, upstream %d", provider, len(got), len(want))
	}
	for _, model := range got {
		upstream, ok := want[model.ID]
		if !ok {
			t.Errorf("%s: Go chat model %q is not in the upstream data", provider, model.ID)
			continue
		}
		cost := catalogCost(t, upstream)
		if model.DisplayName != upstream["name"] || string(model.API) != upstream["api"] || model.BaseURL != upstream["baseUrl"] ||
			model.Reasoning != upstream["reasoning"] || float64(model.ContextWindow) != upstream["contextWindow"] || float64(model.MaxOutputTokens) != upstream["maxTokens"] {
			t.Errorf("%s/%s: identity or capability differs: %+v vs %v", provider, model.ID, model, upstream)
		}
		if model.InputCostPerMTokens != cost.Input || model.OutputCostPerMTokens != cost.Output || model.CacheReadCost != cost.CacheRead || model.CacheWriteCost != cost.CacheWrite || !reflect.DeepEqual(model.Tiers, cost.Tiers) && (len(model.Tiers) != 0 || len(cost.Tiers) != 0) {
			t.Errorf("%s/%s: cost differs: %+v vs %+v", provider, model.ID, model, cost)
		}
		if !slices.Equal(model.Capabilities, catalogStrings(upstream["input"])) {
			t.Errorf("%s/%s: input %v vs %v", provider, model.ID, model.Capabilities, upstream["input"])
		}
		if upstreamLimits, has := upstream["inputLimits"]; has != (model.InputLimits != nil) || has && !sameJSON(t, model.InputLimits, upstreamLimits) {
			t.Errorf("%s/%s: inputLimits %+v vs %v", provider, model.ID, model.InputLimits, upstreamLimits)
		}
		if upstreamHeaders, has := upstream["headers"]; has != (len(model.Headers) != 0) || has && !sameJSON(t, model.Headers, upstreamHeaders) {
			t.Errorf("%s/%s: headers %v vs %v", provider, model.ID, model.Headers, upstreamHeaders)
		}
	}
}

func checkImageCatalog(t *testing.T, provider string, want map[string]map[string]any) {
	t.Helper()
	got := GetImageModels(BuiltinImageProvider(provider))
	if len(got) != len(want) {
		t.Errorf("%s image models: %d, upstream %d", provider, len(got), len(want))
	}
	for _, model := range got {
		upstream, ok := want[model.ID]
		if !ok {
			t.Errorf("%s: Go image model %q is not in the upstream data", provider, model.ID)
			continue
		}
		if model.Name != upstream["name"] || string(model.API) != upstream["api"] || model.BaseURL != upstream["baseUrl"] ||
			!slices.Equal(model.Input, catalogStrings(upstream["input"])) || !slices.Equal(model.Output, catalogStrings(upstream["output"])) {
			t.Errorf("%s/%s: image identity differs: %+v vs %v", provider, model.ID, model, upstream)
		}
		cost := catalogCost(t, upstream)
		if model.Cost.Input != cost.Input || model.Cost.Output != cost.Output || model.Cost.CacheRead != cost.CacheRead || model.Cost.CacheWrite != cost.CacheWrite {
			t.Errorf("%s/%s: image cost %+v vs %+v", provider, model.ID, model.Cost, cost)
		}
		if upstreamLimits, has := upstream["inputLimits"]; has != (model.InputLimits != nil) || has && !sameJSON(t, model.InputLimits, upstreamLimits) {
			t.Errorf("%s/%s: image inputLimits %+v vs %v", provider, model.ID, model.InputLimits, upstreamLimits)
		}
	}
}

func checkClassifierCatalog(t *testing.T, provider string, want map[string]map[string]any) {
	t.Helper()
	got := GetBuiltinClassifierModels(provider)
	if len(got) != len(want) {
		t.Errorf("%s classifier models: %d, upstream %d", provider, len(got), len(want))
	}
	for _, model := range got {
		upstream, ok := want[model.ID]
		if !ok {
			t.Errorf("%s: Go classifier model %q is not in the upstream data", provider, model.ID)
			continue
		}
		cost := catalogCost(t, upstream)
		if model.Name != upstream["name"] || string(model.API) != upstream["api"] || model.BaseURL != upstream["baseUrl"] || float64(model.ContextWindow) != upstream["contextWindow"] ||
			!slices.Equal(model.Input, catalogStrings(upstream["input"])) || model.Cost.Input != cost.Input || model.Cost.Output != cost.Output || model.Cost.CacheRead != cost.CacheRead || model.Cost.CacheWrite != cost.CacheWrite {
			t.Errorf("%s/%s: classifier differs: %+v vs %v", provider, model.ID, model, upstream)
		}
	}
}
