package codingagent

// pi: packages/coding-agent/src/core/provider-composer.ts (model overrides 199-207, mergeSamplingParamsByThinkingLevel 164-172).

import (
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A model override's sampling parameters are spread over the model's (`{ ...model.samplingParams, ...override.samplingParams }`): the
// model's members stay in the model's order, an overridden one in its place, and the override's new members follow in the override's
// order. The same holds within each thinking level. An order equal to the sorted one is not recorded.
func TestSamplingParamsMergeKeepsSpreadOrder(t *testing.T) {
	var definition modelDefinition
	var override modelOverrideJSON
	if err := definition.UnmarshalJSON([]byte(`{"id":"m","samplingParams":{"top_p":0.9,"min_p":0.1},"samplingParamsByThinkingLevel":{"off":{"top_k":40,"min_p":0.2},"high":{"a":1,"b":2}}}`)); err != nil {
		t.Fatal(err)
	}
	if err := override.UnmarshalJSON([]byte(`{"samplingParams":{"temperature":0.3,"top_p":0.8,"seed":1},"samplingParamsByThinkingLevel":{"off":{"mirostat":2,"top_k":50},"low":{"z":1,"y":2}}}`)); err != nil {
		t.Fatal(err)
	}
	if want := []string{"top_p", "min_p"}; !slices.Equal(definition.samplingOrder, want) {
		t.Fatalf("the definition's samplingParams order = %v, want %v", definition.samplingOrder, want)
	}
	if want := (map[ai.ModelThinkingLevel][]string{ai.ThinkingOff: {"top_k", "min_p"}}); !reflect.DeepEqual(definition.samplingLevelOrder, want) {
		t.Fatalf("the definition's per-level order = %v, want %v (a sorted level is not recorded)", definition.samplingLevelOrder, want)
	}
	merged, order := mergeSamplingParams(definition.SamplingParams, definition.samplingOrder, override.SamplingParams, override.samplingOrder)
	if want := []string{"top_p", "min_p", "temperature", "seed"}; !slices.Equal(order, want) || merged["top_p"] != 0.8 || len(merged) != 4 {
		t.Errorf("merged samplingParams = %v in order %v, want order %v", merged, order, want)
	}
	levels, levelOrder := mergeSamplingParamsByThinkingLevel(definition.SamplingParamsByThinkingLevel, definition.samplingLevelOrder, override.SamplingParamsByThinkingLevel, override.samplingLevelOrder)
	wantOrder := map[ai.ModelThinkingLevel][]string{ai.ThinkingOff: {"top_k", "min_p", "mirostat"}, ai.ThinkingLow: {"z", "y"}}
	if !reflect.DeepEqual(levelOrder, wantOrder) {
		t.Errorf("merged per-level order = %v, want %v", levelOrder, wantOrder)
	}
	if levels[ai.ThinkingOff]["top_k"] != float64(50) || len(levels[ai.ThinkingHigh]) != 2 || len(levels[ai.ThinkingLow]) != 2 {
		t.Errorf("merged per-level parameters = %v", levels)
	}
	if _, order := mergeSamplingParams(ai.SamplingParams{"b": 1.0}, nil, ai.SamplingParams{"a": 2.0}, nil); !slices.Equal(order, []string{"b", "a"}) {
		t.Errorf("two sorted sources merge in order %v, want base's member then the override's", order)
	}
	if _, order := mergeSamplingParams(ai.SamplingParams{"a": 1.0}, nil, ai.SamplingParams{"b": 2.0}, nil); order != nil {
		t.Errorf("a merge in sorted order records %v", order)
	}
}
