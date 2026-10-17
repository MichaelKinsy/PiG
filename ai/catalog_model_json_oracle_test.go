package ai

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"slices"
	"sync"
	"testing"
)

// Pi's RPC model objects (get_available_models, get_state, set_model, cycle_model) are the catalog's own model objects,
// so their JSON is JSON.stringify of the MODELS entry: each entry keeps its own member order ("type" last in most
// entries, first in others; inputLimits.images as the entry spells it). Every built-in chat model must encode to the
// same bytes.
func TestCatalogModelJSONMatchesPiForEveryBuiltinModel(t *testing.T) {
	const script = `
const [root] = process.argv.slice(1);
const url = require('node:url').pathToFileURL(require('node:path').join(root, 'node_modules/@earendil-works/pi-ai/dist/models.generated.js')).href;
import(url).then(({ MODELS }) => {
  const out = {};
  for (const [provider, models] of Object.entries(MODELS)) {
    for (const model of Object.values(models)) out[provider + "/" + model.id] = JSON.stringify(model);
  }
  process.stdout.write(JSON.stringify(out));
});`
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, stderr.String())
	}
	var want map[string]string
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(GeneratedModels) {
		t.Fatalf("Pi has %d chat models, the Go catalog %d", len(want), len(GeneratedModels))
	}
	mismatches := 0
	for i := range GeneratedModels {
		generated := &GeneratedModels[i]
		key := generated.Provider + "/" + generated.ID
		got, ok, err := CatalogModelJSON(generated.ToModel())
		if err != nil || !ok {
			t.Fatalf("%s: CatalogModelJSON ok=%t err=%v", key, ok, err)
		}
		if string(got) != want[key] {
			mismatches++
			if mismatches <= 5 {
				t.Errorf("%s:\n got %s\nwant %s", key, got, want[key])
			}
		}
	}
	if mismatches > 0 {
		t.Fatalf("%d of %d built-in models encode differently from Pi", mismatches, len(GeneratedModels))
	}
}

// A model with no catalog record (a models.json or extension model) has no catalog JSON; its encoder keeps its own order.
func TestCatalogModelJSONIsAbsentWithoutARecord(t *testing.T) {
	if _, ok, err := CatalogModelJSON(&Model{ID: "custom", Type: ModelTypeChat}); ok || err != nil {
		t.Fatalf("CatalogModelJSON(custom) ok=%t err=%v, want no catalog JSON", ok, err)
	}
}

// Encodings share one decoded shape per catalog record. Concurrent encodings of the same models, whether they decode a
// record first or reuse its shape, give the bytes a serial encoding gives.
func TestCatalogModelJSONIsSafeForConcurrentEncodings(t *testing.T) {
	models := make([]*Model, 0, 64)
	for i := range min(64, len(GeneratedModels)) {
		models = append(models, GeneratedModels[i].ToModel())
	}
	results := make([][]string, 8)
	var wg sync.WaitGroup
	for worker := range results {
		wg.Go(func() {
			for _, model := range models {
				data, ok, err := CatalogModelJSON(model)
				if err != nil || !ok {
					t.Errorf("%s: ok %t, error %v", model.ID, ok, err)
					return
				}
				results[worker] = append(results[worker], string(data))
			}
		})
	}
	wg.Wait()
	for worker := range results {
		if !slices.Equal(results[worker], results[0]) {
			t.Fatalf("worker %d encoded differently from worker 0", worker)
		}
	}
	for i, model := range models {
		if want := models[i].catalogRecord; results[0][i] != want {
			t.Errorf("%s: got %s, want its record %s", model.ID, results[0][i], want)
		}
	}
}
