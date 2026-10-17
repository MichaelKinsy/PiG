package ai

import (
	"slices"
	"testing"
)

// models.ts getSupportedThinkingLevels / clampThinkingLevel (1223-1258) read Model.thinkingLevelMap: a null entry removes a level (types.ts:1142), xhigh and max
// exist only when the map has a non-null entry for them, and clamp searches upward from the requested level before downward. Each row is worked out from those
// lines; the ones that decide the downward search start at the level just below the request.
//
// mutation-checked: starting the downward search one level lower than the neighbour fails the max-to-xhigh row.
func TestThinkingLevelMapDecidesSupportedAndClampedLevels(t *testing.T) {
	reasoning := func(levels ThinkingLevelMap) *Model {
		return &Model{ProviderMeta: ProviderMetadata{Reasoning: true}, ThinkingLevelMap: levels}
	}
	text := func(value string) *string { return &value }
	base := []ModelThinkingLevel{ThinkingOff, ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh}
	for _, tc := range []struct {
		name      string
		model     *Model
		supported []ModelThinkingLevel
		clamp     map[ModelThinkingLevel]ModelThinkingLevel
	}{
		{"no map: xhigh and max need an entry", reasoning(nil), base, map[ModelThinkingLevel]ModelThinkingLevel{ThinkingXHigh: ThinkingHigh, ThinkingMax: ThinkingHigh}},
		{"xhigh entry: max falls back to the level just below it", reasoning(ThinkingLevelMap{ThinkingXHigh: text("x")}),
			append(slices.Clone(base), ThinkingXHigh), map[ModelThinkingLevel]ModelThinkingLevel{ThinkingMax: ThinkingXHigh}},
		{"max entry: xhigh climbs to max", reasoning(ThinkingLevelMap{ThinkingMax: text("m")}),
			append(slices.Clone(base), ThinkingMax), map[ModelThinkingLevel]ModelThinkingLevel{ThinkingXHigh: ThinkingMax}},
		{"null xhigh with a max entry: the null level is skipped upward", reasoning(ThinkingLevelMap{ThinkingXHigh: nil, ThinkingMax: text("m")}),
			append(slices.Clone(base), ThinkingMax), map[ModelThinkingLevel]ModelThinkingLevel{ThinkingXHigh: ThinkingMax}},
		{"null low and minimal: minimal climbs to medium", reasoning(ThinkingLevelMap{ThinkingMinimal: nil, ThinkingLow: nil}),
			[]ModelThinkingLevel{ThinkingOff, ThinkingMedium, ThinkingHigh}, map[ModelThinkingLevel]ModelThinkingLevel{ThinkingMinimal: ThinkingMedium, ThinkingLow: ThinkingMedium}},
		{"null off: off climbs to minimal", reasoning(ThinkingLevelMap{ThinkingOff: nil}),
			base[1:], map[ModelThinkingLevel]ModelThinkingLevel{ThinkingOff: ThinkingMinimal}},
		{"nothing above medium: high and max fall to medium", reasoning(ThinkingLevelMap{ThinkingHigh: nil}),
			[]ModelThinkingLevel{ThinkingOff, ThinkingMinimal, ThinkingLow, ThinkingMedium}, map[ModelThinkingLevel]ModelThinkingLevel{ThinkingHigh: ThinkingMedium, ThinkingMax: ThinkingMedium}},
		{"an unknown level takes the first supported one", reasoning(ThinkingLevelMap{ThinkingOff: nil}),
			base[1:], map[ModelThinkingLevel]ModelThinkingLevel{"turbo": ThinkingMinimal}},
		{"a model without reasoning supports only off", &Model{ThinkingLevelMap: ThinkingLevelMap{ThinkingHigh: text("h")}},
			[]ModelThinkingLevel{ThinkingOff}, map[ModelThinkingLevel]ModelThinkingLevel{ThinkingHigh: ThinkingOff, ThinkingMax: ThinkingOff}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := GetSupportedThinkingLevels(tc.model); !slices.Equal(got, tc.supported) {
				t.Fatalf("supported levels %v, want %v", got, tc.supported)
			}
			for requested, want := range tc.clamp {
				if got := ClampThinkingLevel(tc.model, requested); got != want {
					t.Errorf("ClampThinkingLevel(%q) = %q, want %q", requested, got, want)
				}
			}
		})
	}
}
