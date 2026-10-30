package host

import (
	"errors"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports packages/chord/test/next/jobs/run.test.ts: one run's record: its `running` entry per phase, and its handle's
// wait for the outcome.

func entryOf(loop *Loop, run *jobRun, state *delta.JsonObject) *delta.JsonObject {
	var entry *delta.JsonObject
	loop.Run(func() { entry = run.entryFor(state) })
	return entry
}

// run.test.ts: a run has an entry per phase: args and state while running or finishing, neither when blocked, with its reason
func TestARunHasAnEntryPerPhaseArgsAndStateWhileRunningOrFinishingNeitherWhenBlockedWithItsReason(t *testing.T) {
	loop := NewLoop()
	key := "k"
	run := newJobRun(loop, "r1", 2, &key, jsonObject("command", "ls"), phaseRunning, nil)
	nexttest.RequireJSONEqual(t, entryOf(loop, run, jsonObject("output", "")), jsonObject(
		"id", "r1", "version", float64(2), "key", "k", "args", jsonObject("command", "ls"),
		"state", jsonObject("output", ""), "phase", "running"))
	run.phase = phaseFinishing
	other := newJobRun(loop, "r2", 1, nil, jsonObject("a", float64(1)), phaseRunning, nil)
	nexttest.RequireJSONEqual(t, entryOf(loop, other, nil), jsonObject(
		"id", "r2", "version", float64(1), "args", jsonObject("a", float64(1)), "phase", "running"))
	run.phase = phaseBlocked
	finishFailed := BlockedFinishFailed
	run.blocked = &finishFailed
	nexttest.RequireJSONEqual(t, entryOf(loop, run, jsonObject("output", "x")), jsonObject(
		"id", "r1", "version", float64(2), "phase", "blocked", "reason", "finish_failed"))
	version := BlockedVersion
	run.blocked = &version
	entry := entryOf(loop, run, nil)
	if entry.Value("phase") != "blocked" || entry.Value("reason") != "version" {
		t.Fatalf("entry = %v", entry)
	}
	// A persisted run waiting for its reservation, or stopped, runs (again): `running`.
	for _, phase := range []Phase{phasePending, phaseStopped} {
		run.phase = phase
		if got := entryOf(loop, run, nil).Value("phase"); got != "running" {
			t.Fatalf("phase %s shows as %v, want running", phase, got)
		}
	}
}

// run.test.ts: a run gives every waiter the outcome, late ones too; a waiter's context ends only its wait
func TestARunGivesEveryWaiterTheOutcomeLateOnesTooAWaitersContextEndsOnlyItsWait(t *testing.T) {
	loop := NewLoop()
	run := newJobRun(loop, "r1", 1, nil, jsonObject(), phaseRunning, nil)
	waiting, cancel := chordctx.WithCancel(background)
	cancel(errors.New("left"))
	if _, err := run.handle.Outcome(waiting); err == nil || err.Error() != "left" {
		t.Fatalf("a cancelled wait = %v, want left", err)
	}
	outcome := AnyOutcome{ID: "r1", Args: jsonObject(), Kind: OutcomeDone, Result: float64(1)}
	loop.Run(func() { run.resolve(outcome) })
	got, err := run.handle.Outcome(background)
	must(t, err)
	if got != outcome {
		t.Fatalf("outcome = %+v, want %+v", got, outcome)
	}
	rejected := newJobRun(loop, "r2", 1, nil, jsonObject(), phaseRunning, nil)
	loop.Run(func() {
		rejected.reject(data.NewChordError(data.ChordErrorInit{Code: data.CodeAborted, Message: "dropped"}, nil))
	})
	_, err = rejected.handle.Outcome(background)
	if chordErr, ok := data.IsChordError(err); !ok || chordErr.Code != data.CodeAborted {
		t.Fatalf("a rejected run = %v, want aborted", err)
	}
}
