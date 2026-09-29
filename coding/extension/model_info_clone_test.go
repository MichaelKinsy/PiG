package extension

import (
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func jsonCompatCopy(value *ai.ModelCompat) *ai.ModelCompat {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var out ai.ModelCompat
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	return &out
}

func TestModelInfoScalarCompatibilityCopyAvoidsJSON(t *testing.T) {
	value := &ai.ModelCompat{SupportsStore: new(false), SupportsReasoningEffort: new(true), MaxTokensField: "max_tokens"}
	legacy := testing.AllocsPerRun(100, func() { _ = jsonCompatCopy(value) })
	got := testing.AllocsPerRun(100, func() { _ = cloneCompat(value) })
	if got >= legacy {
		t.Fatalf("compatibility copy allocates %.0f, JSON reference %.0f", got, legacy)
	}
	for _, model := range ai.GeneratedModels {
		got, want := cloneCompat(model.Compat), jsonCompatCopy(model.Compat)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("compatibility copy changed %s/%s", model.Provider, model.ID)
		}
	}
}

func TestModelInfoCyclesReachJSONError(t *testing.T) {
	if os.Getenv("PIG_TEST_MODEL_INFO_CYCLE") != "1" {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestModelInfoCyclesReachJSONError$")
		cmd.Env = append(os.Environ(), "PIG_TEST_MODEL_INFO_CYCLE=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cycle projection must reach the JSON error boundary: %v\n%s", err, output)
		}
		return
	}
	// Bound an accidental recursive copy in the test child, not in production.
	debug.SetMaxStack(8 << 20)
	object := map[string]any{}
	object["self"] = object
	list := make([]any, 1)
	list[0] = list
	for _, data := range []map[string]any{object, {"list": list}} {
		info := ModelInfo(&ai.Model{SamplingParams: data})
		if _, err := json.Marshal(info); err == nil || !strings.Contains(err.Error(), "cycle") {
			t.Fatalf("cycle error = %v", err)
		}
	}
}

func TestModelInfoSharedSubgraphsKeepCopySemantics(t *testing.T) {
	shared := map[string]any{"value": "initial"}
	info := ModelInfo(&ai.Model{SamplingParams: map[string]any{"first": shared, "second": shared}})
	data := info["samplingParams"].(map[string]any)
	data["first"].(map[string]any)["value"] = "changed"
	if shared["value"] != "initial" || data["second"].(map[string]any)["value"] != "initial" {
		t.Fatal("shared acyclic input changed the existing independent-copy behavior")
	}
}

func BenchmarkModelInfoCatalog(b *testing.B) {
	models := make([]*ai.Model, len(ai.GeneratedModels))
	for i := range ai.GeneratedModels {
		models[i] = ai.GeneratedModels[i].ToModel()
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, model := range models {
			_ = ModelInfo(model)
		}
	}
}
