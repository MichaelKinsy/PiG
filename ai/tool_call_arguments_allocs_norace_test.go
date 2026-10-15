//go:build !race

package ai

import "testing"

// jsonParseValueAllocBudget is the allocation count of jsonParseValue on the row below; lower it when the decode gets cheaper. The race detector adds allocations, so the budget is checked without it.
const jsonParseValueAllocBudget = 19

// jsonParseValue runs once per streamed tool-call delta, so a Decoder (which allocates its refill buffer on every call) is only for the out-of-range number Unmarshal refuses. The in-range value must cost no more than Unmarshal's own allocations.
func TestJSONParseValueAvoidsADecoderForInRangeValues(t *testing.T) {
	const text = `{"path":"a/b.go","edits":[{"old":"x","new":"y"}],"n":1.5}`
	var sink any
	allocs := testing.AllocsPerRun(200, func() { sink, _ = jsonParseValue(text) })
	_ = sink
	if allocs > jsonParseValueAllocBudget {
		t.Errorf("jsonParseValue allocates %.0f objects, budget %d", allocs, jsonParseValueAllocBudget)
	}
}
