package usagetotals

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/core/usage-totals.ts

// combineUsage against pinned Pi: the sum of every counter and cost, with cacheWrite1h and reasoning kept when either side reports them
// (and summed with 0 for the side that does not), and left absent when neither does.
func TestCombineUsageMatchesPi(t *testing.T) {
	n := func(v int) *int { return &v }
	usage := func(in, out, cr, cw int, w1h, reasoning *int, total int, cost [5]float64) ai.Usage {
		return ai.Usage{Input: in, Output: out, CacheRead: cr, CacheWrite: cw, CacheWrite1h: w1h, Reasoning: reasoning, TotalTokens: total,
			Cost: ai.UsageCost{Input: cost[0], Output: cost[1], CacheRead: cost[2], CacheWrite: cost[3], Total: cost[4]}}
	}
	cases := [][2]ai.Usage{
		{usage(1, 2, 3, 4, nil, nil, 10, [5]float64{.1, .2, .3, .4, 1}), usage(10, 20, 30, 40, nil, nil, 100, [5]float64{1, 2, 3, 4, 10})},
		{usage(1, 2, 3, 4, n(5), nil, 10, [5]float64{}), usage(1, 1, 1, 1, nil, nil, 4, [5]float64{})},
		{usage(1, 2, 3, 4, nil, nil, 10, [5]float64{}), usage(1, 1, 1, 1, n(7), n(3), 4, [5]float64{})},
		{usage(0, 0, 0, 0, n(0), n(0), 0, [5]float64{}), usage(0, 0, 0, 0, nil, nil, 0, [5]float64{})},
		{usage(1, 0, 0, 0, nil, n(9), 1, [5]float64{.1, 0, 0, 0, .1}), usage(2, 0, 0, 0, nil, n(1), 2, [5]float64{.2, 0, 0, 0, .2})},
		{{}, {}},
	}
	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/combine_usage.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []ai.Usage
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		if got := CombineUsage(c[0], c[1]); !reflect.DeepEqual(got, expected[i]) {
			t.Errorf("CombineUsage case %d = %+v, Pi %+v", i, got, expected[i])
		}
	}
}
