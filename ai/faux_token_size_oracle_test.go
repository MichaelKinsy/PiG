package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type fauxTokenSizeProbe struct {
	TokenSize *struct {
		Min *int `json:"min,omitempty"`
		Max *int `json:"max,omitempty"`
	} `json:"tokenSize,omitempty"`
	Runs  int `json:"runs"`
	Chars int `json:"chars"`
}

// RegisterFauxProviderOptions.tokenSize {min?, max?} (faux.ts:128-131, 458-461): minTokenSize = max(1, min(min ?? 3, max ?? 5)), maxTokenSize = max(minTokenSize, max ?? 5),
// and each text delta is tokenSize*4 characters (faux.ts:291-292). A set 0 or negative bound is not the default (`??` only skips undefined). The pinned pi-ai answers each probe
// with the distinct non-final chunk lengths over several runs.
func TestFauxTokenSizeMatchesPi(t *testing.T) {
	size := func(minimum, maximum *int) *struct {
		Min *int `json:"min,omitempty"`
		Max *int `json:"max,omitempty"`
	} {
		return &struct {
			Min *int `json:"min,omitempty"`
			Max *int `json:"max,omitempty"`
		}{minimum, maximum}
	}
	probes := []fauxTokenSizeProbe{
		{Runs: 8, Chars: 400},
		{TokenSize: size(nil, nil), Runs: 8, Chars: 400},
		{TokenSize: size(new(0), nil), Runs: 8, Chars: 400},
		{TokenSize: size(new(0), new(0)), Runs: 1, Chars: 100},
		{TokenSize: size(nil, new(0)), Runs: 1, Chars: 100},
		{TokenSize: size(new(7), new(2)), Runs: 1, Chars: 100},
		{TokenSize: size(nil, new(1)), Runs: 1, Chars: 100},
		{TokenSize: size(new(9), nil), Runs: 1, Chars: 100},
		{TokenSize: size(new(4), new(4)), Runs: 1, Chars: 200},
		{TokenSize: size(new(1), new(3)), Runs: 8, Chars: 400},
		{TokenSize: size(new(-3), nil), Runs: 8, Chars: 400},
		{TokenSize: size(nil, new(6)), Runs: 8, Chars: 400},
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/faux_token_size.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want [][]int
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	for i, probe := range probes {
		config := FauxConfig{API: "faux-probe", ProviderID: "faux-probe"}
		if probe.TokenSize != nil {
			config.TokenSize = &FauxTokenSize{Min: probe.TokenSize.Min, Max: probe.TokenSize.Max}
		}
		lengths := map[int]bool{}
		for range probe.Runs {
			provider := NewFauxProvider(config)
			provider.SetResponses([]FauxResponseStep{FauxAssistantMessage(FauxContentBlocks{FauxText(strings.Repeat("a", probe.Chars))}, FauxAssistantMessageOptions{})})
			stream, err := provider.Stream(t.Context(), TranscriptContext{}, StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			var deltas []int
			for event := range stream.Events(context.Background()) {
				if delta, ok := event.(TextDeltaEvent); ok {
					deltas = append(deltas, len(delta.Delta))
				}
			}
			for _, length := range deltas[:len(deltas)-1] {
				lengths[length] = true
			}
		}
		got := make([]int, 0, len(lengths))
		for length := range lengths {
			got = append(got, length)
		}
		slices.Sort(got)
		if !reflect.DeepEqual(got, want[i]) {
			t.Errorf("probe %d %+v: chunk lengths %v, Pi %v", i, probe.TokenSize, got, want[i])
		}
	}
}
