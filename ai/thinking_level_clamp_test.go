package ai

import (
	"slices"
	"testing"
)

// thinkingLevelTestModels are the shapes of model reasoning data the supported levels are a function of.
func thinkingLevelTestModels() map[string]*Model {
	null := func(levels ...ModelThinkingLevel) ThinkingLevelMap {
		m := ThinkingLevelMap{}
		for _, level := range levels {
			m[level] = nil
		}
		return m
	}
	reasoning := func(levelMap ThinkingLevelMap) *Model {
		return &Model{ProviderMeta: ProviderMetadata{Reasoning: true}, ThinkingLevelMap: levelMap}
	}
	return map[string]*Model{
		"nil":                     nil,
		"reasoning off":           {},
		"reasoning off with map":  {ThinkingLevelMap: ThinkingLevelMap{ThinkingXHigh: new("xhigh")}},
		"reasoning":               reasoning(nil),
		"max thinking":            {Capabilities: ModelCapabilities{MaxThinking: ThinkingLevelHigh}},
		"max thinking with map":   {Capabilities: ModelCapabilities{MaxThinking: ThinkingLevelHigh}, ThinkingLevelMap: ThinkingLevelMap{ThinkingMax: new("max")}},
		"map removes off":         reasoning(null(ThinkingOff)),
		"map removes minimal":     reasoning(null(ThinkingMinimal)),
		"map removes high":        reasoning(null(ThinkingHigh)),
		"map adds xhigh":          reasoning(ThinkingLevelMap{ThinkingXHigh: new("xhigh")}),
		"map adds max":            reasoning(ThinkingLevelMap{ThinkingMax: new("max")}),
		"map adds xhigh and max":  reasoning(ThinkingLevelMap{ThinkingXHigh: new("xhigh"), ThinkingMax: new("max")}),
		"map nulls xhigh":         reasoning(null(ThinkingXHigh)),
		"map keeps only medium":   reasoning(null(ThinkingOff, ThinkingMinimal, ThinkingLow, ThinkingHigh, ThinkingXHigh, ThinkingMax)),
		"map removes every level": reasoning(null(extendedThinkingLevels...)),
	}
}

func thinkingLevelTestLevels() []ModelThinkingLevel {
	return append(slices.Clone(extendedThinkingLevels), "bogus", "")
}

// referenceClampThinkingLevel is ClampThinkingLevel as it read before it asked thinkingLevelSupported first: every call builds the supported levels.
func referenceClampThinkingLevel(m *Model, level ModelThinkingLevel) ModelThinkingLevel {
	available := GetSupportedThinkingLevels(m)
	if slices.Contains(available, level) {
		return level
	}
	requestedIndex := slices.Index(extendedThinkingLevels, level)
	if requestedIndex == -1 {
		if len(available) == 0 {
			return ThinkingOff
		}
		return available[0]
	}
	for i := requestedIndex; i < len(extendedThinkingLevels); i++ {
		if slices.Contains(available, extendedThinkingLevels[i]) {
			return extendedThinkingLevels[i]
		}
	}
	for i := requestedIndex - 1; i >= 0; i-- {
		if slices.Contains(available, extendedThinkingLevels[i]) {
			return extendedThinkingLevels[i]
		}
	}
	if len(available) == 0 {
		return ThinkingOff
	}
	return available[0]
}

// TestClampThinkingLevelKeepsItsValues pins ClampThinkingLevel to the clamp that builds the supported levels on every call, for every model shape and
// every level, a level no model knows included.
func TestClampThinkingLevelKeepsItsValues(t *testing.T) {
	for name, model := range thinkingLevelTestModels() {
		for _, level := range thinkingLevelTestLevels() {
			if got, want := ClampThinkingLevel(model, level), referenceClampThinkingLevel(model, level); got != want {
				t.Errorf("%s: ClampThinkingLevel(%q) = %q, want %q", name, level, got, want)
			}
		}
	}
}

// TestClampThinkingLevelFollowsAModelWhoseReasoningDataIsReplaced pins that nothing about a model's levels is kept between two calls: a registry
// refresh that replaces the reasoning data of a model gives the next call the new levels.
func TestClampThinkingLevelFollowsAModelWhoseReasoningDataIsReplaced(t *testing.T) {
	model := &Model{ProviderMeta: ProviderMetadata{Reasoning: true}}
	if got := ClampThinkingLevel(model, ThinkingHigh); got != ThinkingHigh {
		t.Fatalf("reasoning model: ClampThinkingLevel(high) = %q, want high", got)
	}
	if got := ClampThinkingLevel(model, ThinkingMax); got != ThinkingHigh {
		t.Fatalf("reasoning model: ClampThinkingLevel(max) = %q, want high", got)
	}
	model.ThinkingLevelMap = ThinkingLevelMap{ThinkingMax: new("max"), ThinkingHigh: nil}
	if got := ClampThinkingLevel(model, ThinkingHigh); got != ThinkingMax {
		t.Errorf("after the map removed high and added max: ClampThinkingLevel(high) = %q, want max", got)
	}
	model.ProviderMeta.Reasoning = false
	if got := ClampThinkingLevel(model, ThinkingMax); got != ThinkingOff {
		t.Errorf("after reasoning was switched off: ClampThinkingLevel(max) = %q, want off", got)
	}
	if got := ClampThinkingLevel(model, ThinkingOff); got != ThinkingOff {
		t.Errorf("after reasoning was switched off: ClampThinkingLevel(off) = %q, want off", got)
	}
}

// TestGetSupportedThinkingLevelsResultIsTheCallersToChange pins the contract GetSupportedThinkingLevels keeps for its other callers: the slice is theirs,
// so changing it does not change the levels of the next call.
func TestGetSupportedThinkingLevelsResultIsTheCallersToChange(t *testing.T) {
	for name, model := range thinkingLevelTestModels() {
		first := GetSupportedThinkingLevels(model)
		want := slices.Clone(first)
		for i := range first {
			first[i] = "changed"
		}
		if got := GetSupportedThinkingLevels(model); !slices.Equal(got, want) {
			t.Errorf("%s: levels after the caller changed the first result = %v, want %v", name, got, want)
		}
	}
}

// TestClampThinkingLevelOfASupportedLevelAllocatesNothing pins that a level the model supports is answered without building the supported levels: the
// coding Session asks for every agent run it ends.
func TestClampThinkingLevelOfASupportedLevelAllocatesNothing(t *testing.T) {
	for name, model := range thinkingLevelTestModels() {
		for _, level := range GetSupportedThinkingLevels(model) {
			if got := testing.AllocsPerRun(100, func() { _ = ClampThinkingLevel(model, level) }); got != 0 {
				t.Errorf("%s: ClampThinkingLevel(%q) allocates %v times, want 0", name, level, got)
			}
		}
	}
}
