package ai

import (
	"context"
	"slices"
	"testing"
)

type stubProvider struct{ id string }

func (s stubProvider) ID() string { return s.id }
func (stubProvider) Stream(_ context.Context, _ TranscriptContext, _ StreamOptions) (*AssistantMessageEventStream, error) {
	return NewAssistantMessageEventStream(), nil
}
func (stubProvider) Close() error { return nil }

func TestGetSupportedThinkingLevels(t *testing.T) {
	ptr := func(v string) *string { return &v }
	cases := []struct {
		name string
		m    *Model
		want []ThinkingLevel
	}{
		{
			name: "nil model",
			m:    nil,
			want: []ThinkingLevel{ThinkingOff},
		},
		{
			name: "reasoning without explicit map",
			m: &Model{
				Capabilities: ModelCapabilities{MaxThinking: ThinkingHigh},
			},
			want: []ThinkingLevel{ThinkingOff, ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh},
		},
		{
			name: "map removes off and enables xhigh",
			m: &Model{
				Capabilities:     ModelCapabilities{MaxThinking: ThinkingXHigh},
				ThinkingLevelMap: ThinkingLevelMap{ThinkingOff: nil, ThinkingXHigh: ptr("xhigh")},
			},
			want: []ThinkingLevel{ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh},
		},
		{
			name: "map enables max and excludes xhigh (opus-4-6 shape)",
			m: &Model{
				Capabilities:     ModelCapabilities{MaxThinking: ThinkingMax},
				ThinkingLevelMap: ThinkingLevelMap{ThinkingMax: ptr("max")},
			},
			want: []ThinkingLevel{ThinkingOff, ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingMax},
		},
		{
			name: "non reasoning model",
			m:    &Model{},
			want: []ThinkingLevel{ThinkingOff},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := GetSupportedThinkingLevels(tc.m)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("GetSupportedThinkingLevels() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClampThinkingLevel(t *testing.T) {
	ptr := func(v string) *string { return &v }
	model := &Model{
		Capabilities: ModelCapabilities{MaxThinking: ThinkingXHigh},
		ThinkingLevelMap: ThinkingLevelMap{
			ThinkingOff:    nil,
			ThinkingMedium: nil,
			ThinkingXHigh:  ptr("xhigh"),
		},
	}
	cases := []struct {
		name  string
		level ThinkingLevel
		want  ThinkingLevel
	}{
		{name: "available level passthrough", level: ThinkingLow, want: ThinkingLow},
		{name: "off clamps upward when disabled", level: ThinkingOff, want: ThinkingMinimal},
		{name: "medium clamps upward first", level: ThinkingMedium, want: ThinkingHigh},
		{name: "unknown falls back to first available", level: ThinkingLevel("mystery"), want: ThinkingMinimal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClampThinkingLevel(model, tc.level)
			if got != tc.want {
				t.Fatalf("ClampThinkingLevel(%q) = %q, want %q", tc.level, got, tc.want)
			}
		})
	}
}

func TestCloneThinkingLevelMap(t *testing.T) {
	in := ThinkingLevelMap{ThinkingOff: nil, ThinkingXHigh: new("max")}
	out := cloneThinkingLevelMap(in)
	if out == nil {
		t.Fatal("cloneThinkingLevelMap returned nil")
	}
	if _, ok := out[ThinkingOff]; !ok || out[ThinkingOff] != nil {
		t.Fatalf("off mapping = %v, want explicit nil", out[ThinkingOff])
	}
	if out[ThinkingXHigh] == nil || *out[ThinkingXHigh] != "max" {
		t.Fatalf("xhigh mapping = %v, want max", out[ThinkingXHigh])
	}
	*out[ThinkingXHigh] = "changed"
	if *in[ThinkingXHigh] != "max" {
		t.Fatal("cloneThinkingLevelMap did not deep copy values")
	}
}

func TestModelsAreEqual(t *testing.T) {
	a := &Model{ID: "gpt-4o", Provider: stubProvider{id: "openai"}}
	b := &Model{ID: "gpt-4o", Provider: stubProvider{id: "openai"}}
	c := &Model{ID: "gpt-4o-mini", Provider: stubProvider{id: "openai"}}
	d := &Model{ID: "gpt-4o", Provider: stubProvider{id: "openrouter"}}

	if !ModelsAreEqual(a, b) {
		t.Fatal("expected equal models")
	}
	if ModelsAreEqual(a, c) {
		t.Fatal("different IDs must not compare equal")
	}
	if ModelsAreEqual(a, d) {
		t.Fatal("different providers must not compare equal")
	}
	if ModelsAreEqual(nil, b) || ModelsAreEqual(a, nil) || ModelsAreEqual(nil, nil) {
		t.Fatal("nil inputs must not compare equal")
	}
}

// TestThinkingMaxLevel verifies the 0.81 "max" thinking level: it is derived
// as the ceiling when a model maps it, maps to the native "max" effort for
// such models, and clamps to "high" for models that do not map it.
func TestThinkingMaxLevel(t *testing.T) {
	ptr := func(v string) *string { return &v }
	maxMap := ThinkingLevelMap{ThinkingMax: ptr("max")} // opus-4-6 shape

	if got := thinkingMaxLevel(true, maxMap); got != ThinkingMax {
		t.Fatalf("thinkingMaxLevel = %q, want max", got)
	}

	model := &Model{
		Capabilities:     ModelCapabilities{MaxThinking: ThinkingMax},
		ThinkingLevelMap: maxMap,
	}
	if got := mapThinkingLevelToEffort(model, ThinkingMax); got != "max" {
		t.Fatalf("mapThinkingLevelToEffort(max) = %q, want max", got)
	}
	if got := ClampThinkingLevel(model, ThinkingMax); got != ThinkingMax {
		t.Fatalf("ClampThinkingLevel(max) = %q, want max", got)
	}

	// A model that does not map max clamps the effort down to high.
	plain := &Model{ThinkingLevelMap: ThinkingLevelMap{ThinkingXHigh: ptr("xhigh")}}
	if got := mapThinkingLevelToEffort(plain, ThinkingMax); got != "high" {
		t.Fatalf("mapThinkingLevelToEffort(max) unmapped = %q, want high", got)
	}
}

// TestCalculateCost verifies the upstream cost model: separate cache-read/write
// rates, 1h cache writes billed at 2x the base input rate, and request-wide
// tier selection by total input tokens. CalculateCost fills usage.Cost.
func TestCalculateCost(t *testing.T) {
	intPtr := func(value int) *int { return &value }
	base := &Model{Capabilities: ModelCapabilities{
		InputCostPer1M: 3, OutputCostPer1M: 15,
		CacheReadCostPer1M: 0.3, CacheWriteCostPer1M: 3.75,
	}}
	tiered := &Model{Capabilities: ModelCapabilities{
		InputCostPer1M: 3, OutputCostPer1M: 15,
		CacheReadCostPer1M: 0.3, CacheWriteCostPer1M: 3.75,
		CostTiers: []CostTier{
			{InputTokensAbove: 100_000, InputCostPer1M: 4, OutputCostPer1M: 20, CacheReadCostPer1M: 0.4, CacheWriteCostPer1M: 5},
			{InputTokensAbove: 500_000, InputCostPer1M: 6, OutputCostPer1M: 30, CacheReadCostPer1M: 0.6, CacheWriteCostPer1M: 7.5},
		},
	}}
	const eps = 1e-9
	cases := []struct {
		name string
		m    *Model
		u    *Usage
		want float64
	}{
		{"nil model", nil, &Usage{Input: 1_000_000}, 0},
		{"input+output base rates", base, &Usage{Input: 1_000_000, Output: 1_000_000}, 18},
		{"separate cache rates", base, &Usage{CacheRead: 1_000_000, CacheWrite: 1_000_000}, 4.05},
		{"1h write at 2x input rate", base, &Usage{CacheWrite: 1_000_000, CacheWrite1h: intPtr(1_000_000)}, 6.0},
		{"mixed short/long write", base, &Usage{CacheWrite: 1_000_000, CacheWrite1h: intPtr(400_000)}, 4.65},
		{"below tier uses base", tiered, &Usage{Input: 50_000}, 0.15},
		{"above tier uses tier rate", tiered, &Usage{Input: 300_000}, 1.2},
		{"highest matching tier wins", tiered, &Usage{Input: 600_000}, 3.6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CalculateCost(tc.m, tc.u)
			if got.Total < tc.want-eps || got.Total > tc.want+eps {
				t.Fatalf("CalculateCost().Total = %v, want %v", got.Total, tc.want)
			}
			if tc.u.Cost != got {
				t.Fatalf("usage.Cost = %#v, want the returned %#v", tc.u.Cost, got)
			}
		})
	}
	if got := CalculateCost(base, nil); got != (UsageCost{}) {
		t.Fatalf("CalculateCost(nil usage) = %#v", got)
	}
}

// TestCalculateCostTierBoundaryMatchesUpstream pins Pi 0.87.1 calculateCost
// values for openai/gpt-5.5: a prompt exactly at the tier threshold keeps the
// base rates; one token more switches every rate to the tier.
func TestCalculateCostTierBoundaryMatchesUpstream(t *testing.T) {
	generated, ok := LookupModelExact("openai/gpt-5.5")
	if !ok {
		t.Fatal("catalog model missing")
	}
	model := &Model{Capabilities: generated.ToCapabilities()}
	for _, tc := range []struct {
		input int
		want  UsageCost
	}{
		{272_000, UsageCost{Input: 1.36, Output: 0.030000000000000002, Total: 1.3900000000000001}},
		{272_001, UsageCost{Input: 2.7200100000000003, Output: 0.045000000000000005, Total: 2.76501}},
	} {
		usage := &Usage{Input: tc.input, Output: 1000}
		if got := CalculateCost(model, usage); got != tc.want {
			t.Fatalf("input %d: cost = %#v, want %#v", tc.input, got, tc.want)
		}
	}
}
