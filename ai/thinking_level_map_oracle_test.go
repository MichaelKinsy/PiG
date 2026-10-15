package ai

// pi: packages/ai/src/models.ts

import (
	"fmt"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type thinkingLevelMapCase struct {
	Reasoning bool               `json:"reasoning"`
	Map       map[string]*string `json:"map,omitempty"`
	HasMap    bool               `json:"hasMap"`
}

type thinkingLevelMapAnswer struct {
	Supported []string          `json:"supported"`
	Clamped   map[string]string `json:"clamped"`
}

// models.ts getSupportedThinkingLevels and clampThinkingLevel over every combination of absent, null and mapped entries
// for the seven thinking levels, with and without reasoning and without a map: a null entry removes the level, xhigh and
// max need a non-null entry, and clamping goes to the nearest supported level above then below, and to the first
// supported level for a level outside the list.
func TestThinkingLevelMapSupportAndClampMatchPi(t *testing.T) {
	levels := []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
	mapped := "x"
	var cases []thinkingLevelMapCase
	for _, reasoning := range []bool{true, false} {
		cases = append(cases, thinkingLevelMapCase{Reasoning: reasoning})
		for n := range 2187 {
			entries := map[string]*string{}
			for i, level := range levels {
				switch n / pow3(i) % 3 {
				case 1:
					entries[level] = nil
				case 2:
					entries[level] = &mapped
				}
			}
			cases = append(cases, thinkingLevelMapCase{Reasoning: reasoning, Map: entries, HasMap: true})
		}
	}
	var want []thinkingLevelMapAnswer
	pioracle.Run(t, `
const mod = await load("pi-ai/models.js");
const clampLevels = ["off", "minimal", "low", "medium", "high", "xhigh", "max", "bogus"];
emit(input.map((c) => {
	const model = { reasoning: c.reasoning, thinkingLevelMap: c.hasMap ? c.map : undefined };
	const clamped = {};
	for (const level of clampLevels) clamped[level] = mod.clampThinkingLevel(model, level);
	return { supported: mod.getSupportedThinkingLevels(model), clamped };
}));`, cases, &want)
	failures := 0
	for i, c := range cases {
		model := &Model{ProviderMeta: ProviderMetadata{Reasoning: c.Reasoning}}
		if c.HasMap {
			model.ThinkingLevelMap = ThinkingLevelMap{}
			for level, value := range c.Map {
				model.ThinkingLevelMap[ModelThinkingLevel(level)] = value
			}
		}
		got := thinkingLevelMapAnswer{Clamped: map[string]string{}}
		for _, level := range GetSupportedThinkingLevels(model) {
			got.Supported = append(got.Supported, string(level))
		}
		for level := range want[i].Clamped {
			got.Clamped[level] = string(ClampThinkingLevel(model, ModelThinkingLevel(level)))
		}
		if fmt.Sprint(got) != fmt.Sprint(want[i]) || !slices.Equal(got.Supported, want[i].Supported) {
			if failures++; failures <= 6 {
				t.Errorf("reasoning %t map %v:\n  Pig %v\n  Pi  %v", c.Reasoning, describeLevelMap(c.Map), got, want[i])
			}
		}
	}
	if failures > 6 {
		t.Errorf("%d of %d models differ from Pi", failures, len(cases))
	}
}

func pow3(n int) int {
	result := 1
	for range n {
		result *= 3
	}
	return result
}

func describeLevelMap(entries map[string]*string) string {
	text := ""
	for _, level := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
		if value, ok := entries[level]; ok {
			if value == nil {
				text += level + "=null "
			} else {
				text += level + "=" + *value + " "
			}
		}
	}
	return text
}
