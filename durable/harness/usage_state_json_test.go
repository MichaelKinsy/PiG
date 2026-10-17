package harness

// pi: packages/durable/src/harness/usage.ts

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// usage.ts:7-12 types both ledgers as Record<string, JsonRepresentation<Usage>>: UsageState.Models and .Tools hold the ai.Usage itself, so the
// stored JSON of a ledger decodes to it and encodes back to the same JSON, keyed `provider/modelId` and by tool name.
func TestUsageStateIsTheStoredUsageLedger(t *testing.T) {
	const stored = `{"models":{"faux/faux-1":{"input":10,"output":20,"cacheRead":3,"cacheWrite":4,"totalTokens":37,"cost":{"input":0.1,"output":0.2,"cacheRead":0.03,"cacheWrite":0.04,"total":0.37}}},"tools":{"read":{"input":1,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":1,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}}`
	var state UsageState
	if err := json.Unmarshal([]byte(stored), &state); err != nil {
		t.Fatal(err)
	}
	if got := state.Models["faux/faux-1"]; got.Input != 10 || got.CacheWrite != 4 || got.TotalTokens != 37 || got.Cost.Total != 0.37 {
		t.Fatalf("model usage decoded to %+v", got)
	}
	if got := state.Tools["read"]; got.Input != 1 || got.TotalTokens != 1 {
		t.Fatalf("tool usage decoded to %+v", got)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var want, got map[string]any
	if err := json.Unmarshal([]byte(stored), &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("encoded %s, want the stored ledger %s", encoded, stored)
	}
	empty, err := json.Marshal(NewUsageState())
	if err != nil || string(empty) != `{"models":{},"tools":{}}` {
		t.Fatalf("an empty ledger encodes as %s (%v), want both records present", empty, err)
	}
}

// Pi: packages/durable/src/harness/usage.ts:7-12. UsageState keeps each bucket's totals as JsonRepresentation<Usage>: the stored
// pi.usage document holds Usage's JSON members, and reading the document back gives those members as the Usage value. The Go
// ledger decodes the stored JSON into ai.Usage, so a recorded usage with optional counters and a cost breakdown survives the
// commit, the addition of a second record and the read as the same totals.
func TestUsageStateIsTheStoredJSONOfUsageTotals(t *testing.T) {
	harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
	id := root.Id()
	reasoning := 3
	first := ai.Usage{Input: 10, Output: 4, CacheRead: 2, CacheWrite: 1, Reasoning: &reasoning, TotalTokens: 17, Cost: ai.UsageCost{Input: 0.5, Output: 0.25, Total: 0.75}}
	second := ai.Usage{Input: 5, Output: 1, CacheRead: 1, CacheWrite: 2, Reasoning: &reasoning, TotalTokens: 9, Cost: ai.UsageCost{Input: 0.25, CacheRead: 0.125, Total: 0.375}}
	for _, usage := range []ai.Usage{first, second} {
		commitValue(t, root, func(tx durable.Tx) (struct{}, error) {
			return struct{}{}, RecordUsage(tx, id, UsageModels, "faux/faux-1", usage)
		})
	}
	state := must(durable.Snapshot(testContext, harness, UsageDoc, id))
	got, ok := state.Models["faux/faux-1"]
	if !ok || got.Input != 15 || got.Output != 5 || got.CacheRead != 3 || got.CacheWrite != 3 || got.TotalTokens != 26 || got.Cost.CacheRead != 0.125 || got.Cost.Total != 1.125 || got.Reasoning == nil || *got.Reasoning != 6 {
		t.Fatalf("models[faux/faux-1] = %+v, want the two records added member by member", got)
	}
	if len(state.Tools) != 0 {
		t.Fatalf("tools = %+v, want the empty bucket Pi's initial state has", state.Tools)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]map[string]any
	if err := json.Unmarshal(encoded, &shape); err != nil {
		t.Fatal(err)
	}
	if len(shape) != 2 || shape["tools"] == nil || shape["models"]["faux/faux-1"] == nil {
		t.Fatalf("state JSON = %s, want Pi's { models, tools } record of usage objects", encoded)
	}
	closeHarness(t, harness)
}

// Pi: packages/durable/src/harness/usage.ts:36 stores a new total as copyJson(usage, { omitUndefinedProperties: true }), which keeps
// the usage object's own key order, so the pi.usage document lists a model's counters as the message usage does (input, output,
// cacheRead, cacheWrite, cacheWrite1h, reasoning, totalTokens, cost), not sorted. The document is the view and replication value.
func TestRecordUsageStoresTheUsageInItsOwnKeyOrder(t *testing.T) {
	harness, root := openChat(t, storage.NewMemoryStorage(), chatSetup(t))
	id := root.Id()
	oneHour, reasoning := 2, 3
	usage := ai.Usage{Input: 10, Output: 4, CacheRead: 2, CacheWrite: 5, CacheWrite1h: &oneHour, Reasoning: &reasoning, TotalTokens: 21, Cost: ai.UsageCost{Input: 0.5, Output: 0.25, CacheRead: 0.125, CacheWrite: 0.0625, Total: 0.9375}}
	commitValue(t, root, func(tx durable.Tx) (struct{}, error) {
		return struct{}{}, RecordUsage(tx, id, UsageModels, "faux/faux-1", usage)
	})
	document := must(harness.SnapshotErased(testContext, UsageDoc, id))
	got, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"models":{"faux/faux-1":{"input":10,"output":4,"cacheRead":2,"cacheWrite":5,"cacheWrite1h":2,"reasoning":3,"totalTokens":21,"cost":{"input":0.5,"output":0.25,"cacheRead":0.125,"cacheWrite":0.0625,"total":0.9375}}},"tools":{}}`
	if string(got) != want {
		t.Fatalf("pi.usage = %s\nwant      %s", got, want)
	}
	closeHarness(t, harness)
}
