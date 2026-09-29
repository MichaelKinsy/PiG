package main

import (
	"encoding/json"
	"maps"
	"strconv"
	"testing"
)

func TestModelDataStructureHashJavaScriptOrdering(t *testing.T) {
	// Oracle: modelDataStructureHash in .upstream/v0.87.1/packages/ai/scripts/model-data.ts:121; exact fixture below.
	structure := ModelDataStructure{"10": {"2": "api2", "10": "api10", "a": "a"}, "2": {"<>&\u2028": "z", "\ue000": "y", "😀": "x"}}
	const want = "fd8b67241b59568ba2d26a5833a9a066f2e5fe55017f38cb8257ed04b615388d"
	if got := ModelDataStructureHash(structure); got != want {
		t.Fatalf("hash = %s, want Pi hash %s", got, want)
	}
}

func TestModelDataDateParseGrammar(t *testing.T) {
	for _, test := range []struct {
		value string
		valid bool
	}{{fixtureGeneratedAt, true}, {"0", true}, {"2026-02-30T10:00:00.000Z", true}, {"July 23, 2026", true}, {"invalid", false}, {"2026-13-01T00:00:00.000Z", false}} {
		raw, err := json.Marshal(test.value)
		if err != nil {
			t.Fatal(err)
		}
		got, err := modelDataTimestampValid(raw)
		if err != nil || got != test.valid {
			t.Fatalf("Date.parse(%q) validity=%v, err=%v", test.value, got, err)
		}
	}
}

func BenchmarkModelDataValidation(b *testing.B) {
	f := newModelDataFixture(b)
	template := f.values["model-a"].(map[string]any)
	for i := range 512 {
		id := "generated-model-" + strconv.Itoa(i)
		model := maps.Clone(template)
		model["id"] = id
		f.values[id] = model
		f.structure["test-provider"][id] = "openai-completions"
	}
	f.write(b, ModelDataSchemaVersion, "openai-completions")
	b.ReportAllocs()
	for b.Loop() {
		if err := ValidateGeneratedModelData(f.root); err != nil {
			b.Fatal(err)
		}
	}
}
