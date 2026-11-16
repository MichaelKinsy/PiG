package state_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Go-only primitive (no Pi twin): Pi's check and the act that depends on it are consecutive statements of one turn
// (store open-query.ts:206-207). CheckAct runs both in one section of the host: a state change another section makes
// while the caller waits for its section is seen by the check, and nothing separates the check from its act.
func TestBatchCheckActRunsTheCheckAndTheActInOneSection(t *testing.T) {
	batch := state.NewBatch(func(state.BatchError) {}, nil)
	entries, inside := 0, false
	var trace []string
	batch.UseSection(func(work func()) {
		entries++
		inside = true
		work()
		inside = false
	}, func() bool { return inside })

	acted := batch.CheckAct(func() func() {
		trace = append(trace, "check")
		return func() { trace = append(trace, "act") }
	})
	if !acted || entries != 1 || len(trace) != 2 || trace[0] != "check" || trace[1] != "act" {
		t.Fatalf("acted = %v, sections = %d, trace = %v; want true, one section, check then act", acted, entries, trace)
	}

	entries, trace = 0, nil
	acted = batch.CheckAct(func() func() {
		trace = append(trace, "check")
		return nil
	})
	if acted || entries != 1 || len(trace) != 1 {
		t.Fatalf("acted = %v, sections = %d, trace = %v; want a refusal in one section that ran no act", acted, entries, trace)
	}

	// The check reads what the section sees: a change made by a section that ran while the caller waited is in it.
	value := 0
	batch.UseSection(func(work func()) {
		value = 1
		work()
	}, nil)
	var seen int
	batch.CheckAct(func() func() {
		seen = value
		return func() {}
	})
	if seen != 1 {
		t.Fatalf("the check saw %d, want what the section before it left (1)", seen)
	}
}
