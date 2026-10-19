package codingagent

import (
	"math/rand"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// scanNativeModelDefaults is provider-composer.ts findModelDefaults as a scan of the list, the reference the index must agree with.
func scanNativeModelDefaults(models []*ai.Model, id string, api ai.API) *ai.Model {
	for _, model := range models {
		if model.ID == id {
			return model
		}
	}
	if api != "" {
		for _, model := range models {
			if model.ProviderMeta.API == api {
				return model
			}
		}
	}
	for _, model := range models {
		if model.ProviderMeta.API == ai.APIOpenAICompletions {
			return model
		}
	}
	if len(models) > 0 {
		return models[0]
	}
	return nil
}

func testIndexModel(id string, api ai.API) *ai.Model {
	return &ai.Model{ID: id, ProviderMeta: ai.ProviderMetadata{API: api}}
}

// The index answers every lookup as the scan does while models are replaced in place (changing their api) and appended, with duplicate
// ids in the base list and with ids and apis no model has.
func TestNativeModelIndexMatchesScanThroughReplacementsAndAppends(t *testing.T) {
	apis := []ai.API{ai.APIOpenAICompletions, ai.APIAnthropicMessages, ai.APIOpenAIResponses, "custom-api"}
	for seed := range int64(200) {
		rng := rand.New(rand.NewSource(seed))
		var base []*ai.Model
		for range rng.Intn(12) {
			base = append(base, testIndexModel("m"+strconv.Itoa(rng.Intn(8)), apis[rng.Intn(len(apis))]))
		}
		models := append([]*ai.Model(nil), base...)
		index := newNativeModelIndex(models)
		next := 100
		for step := range 60 {
			id := "m" + strconv.Itoa(rng.Intn(12))
			api := ai.API("")
			if rng.Intn(4) != 0 {
				api = apis[rng.Intn(len(apis))]
			}
			if got, want := index.find(id, api), scanNativeModelDefaults(index.models, id, api); got != want {
				t.Fatalf("seed %d step %d: find(%q, %q) = %v, scan = %v", seed, step, id, api, got, want)
			}
			wantIndex := -1
			for i, model := range index.models {
				if model.ID == id {
					wantIndex = i
					break
				}
			}
			if got := index.indexOf(id); got != wantIndex {
				t.Fatalf("seed %d step %d: indexOf(%q) = %d, want %d", seed, step, id, got, wantIndex)
			}
			if wantIndex >= 0 {
				index.replace(wantIndex, testIndexModel(id, apis[rng.Intn(len(apis))]))
			} else {
				next++
				index.add(testIndexModel("m"+strconv.Itoa(next), apis[rng.Intn(len(apis))]))
			}
		}
	}
}
