// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package hopcost

import (
	"encoding/json"
	"os"
	"testing"
)

// RuntimeCall is the ticks a task's `runtime.commit` adds to the commit it makes: `await this.#gated(...)` resumes where the Session's caller would
// have, and the async `commit` returns one tick later (scheduler.ts:2661-2698).
const runtimeCall = 1

// testdata/gated_ticks.json is generated from the reference's Harness (scenarios/gated-ticks.mjs): a task's `runtime.commit` from its call to its
// caller's resumption. The model is the `#gated` callback's awaits (RuntimeCommit) around the Session line job (span).
func TestRuntimeCommitMatchesPi(t *testing.T) {
	data, err := os.ReadFile("testdata/gated_ticks.json")
	if err != nil {
		t.Fatal(err)
	}
	var table tickTable
	if err := json.Unmarshal(data, &table); err != nil {
		t.Fatal(err)
	}
	if len(table.Profiles) != len(profileOf) {
		t.Fatalf("the table has %d backends, the model has %d profiles", len(table.Profiles), len(profileOf))
	}
	for backend, calls := range table.Profiles {
		p := profileOf[backend]
		// The task's record is live in the Session's cache (the scheduler committed it), so the settle's read of the committed task hits.
		taskHit := TxTask(p, false, CacheHit)
		model := map[string]int{
			"runtime.commit (async, no writes)": runtimeCall + span(Job{Change: RuntimeCommit(0, false, 0), Settle: Settle(0, 0, 0)}, p, nil),
			"runtime.commit (sync, no writes)":  runtimeCall + span(Job{Change: RuntimeCommit(0, false, 0), Settle: Settle(0, 0, 0)}, p, nil),
			"runtime.commit (entry)": runtimeCall + span(Job{Change: RuntimeCommit(TxAppendEntry(p, false, CacheHit), false, 0), Settle: Settle(0, 0, 0)}, p,
				[]Writes{{Tables: 1}}),
			// A task replaced in place: the scheduler's membership promise resolves at once.
			"runtime.commit (next state)": runtimeCall + span(Job{Change: RuntimeCommit(0, true, 0), Settle: Settle(0, 0, taskHit), Membership: Membership(0, 0)}, p,
				[]Writes{{Tables: 1}}),
			// A child task of the running one: the callback reads the owner (a hit) and the conversation (a hit); the settle awaits the owner's
			// settled read; the membership walks the chain above the new task.
			"runtime.commit (child task)": runtimeCall + span(Job{
				Change:     RuntimeCommit(TxCreateTask(p, true, false, false, CacheHit, false, CacheHit), false, 0),
				Settle:     Settle(CurrentTask(p, false, true, CacheHit), 0, 0),
				Membership: Membership(1, 0),
			}, p, []Writes{{Tables: 1}}),
		}
		if len(calls) != len(model) {
			t.Fatalf("%s: %d measured calls, %d modelled", backend, len(calls), len(model))
		}
		for call, want := range calls {
			if got, ok := model[call]; !ok || got != want {
				t.Errorf("%s %s: model %d ticks (modelled: %v), Pi %d", backend, call, got, ok, want)
			}
		}
	}
}

// GatedChange with a synchronous change is the region's own await.
func TestGatedWrappers(t *testing.T) {
	if GatedChange(0) != GatedRegion+2 || GatedSyncChange != GatedRegion {
		t.Fatalf("gated wrappers: %d, %d", GatedChange(0), GatedSyncChange)
	}
	if RuntimeCommit(5, false, 0) != GatedChange(6) || RuntimeCommit(5, true, 3) != GatedChange(6+1+3) {
		t.Fatalf("runtime.commit arithmetic")
	}
}
