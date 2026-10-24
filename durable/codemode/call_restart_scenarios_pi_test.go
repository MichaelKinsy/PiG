package codemode

// pi: packages/durable/src/codemode/codemode.ts

import (
	"encoding/json"
	"fmt"
	"testing"
)

func awaitJournal(entries int) restartStep {
	return restartStep{Op: "await", What: "journal", Entries: entries}
}

func awaitIssuedAfter(index, value int) restartStep {
	return restartStep{Op: "await", What: "issuedAfter", Index: index, Value: json.RawMessage(fmt.Sprint(value))}
}

func awaitFirstIssued(value int) restartStep {
	return restartStep{Op: "await", What: "firstIssued", Value: json.RawMessage(fmt.Sprint(value))}
}

func releaseSlow(id int) restartStep { return restartStep{Op: "release", ID: id} }

// restartSteps is Pi's restart() with the steps that run before the close and between the close and the reopen's resume.
func restartSteps(name, code string, before, between []restartStep) restartScenario {
	steps := []restartStep{restartOp("start")}
	steps = append(steps, before...)
	steps = append(steps, awaitStep("hold"), observeAt("before the restart"), restartOp("close"), reopen())
	steps = append(steps, between...)
	steps = append(steps, observeAt("reopened"), restartOp("resume"), awaitStep("done"), observeAt("answer"))
	return restartScenario{Name: name, Code: code, Steps: steps}
}

func startsWith(store string) restartStep { return setDoc("run", store, "start", "store") }

// codemode.ts:495-510: a replay that ends within the replayed part went another way than the live run when the live run
// acknowledged that result, or made a call that a later result answers. For the script's first run (during.seq 0), the live
// run went on when the journal has entries or the count of its first run is recorded (:499-500); without either, the script
// simply ends as it did before.
func TestAResumedScriptThatEndsInItsFirstRunDivergesOnlyWhereTheLiveRunWentOn(t *testing.T) {
	// Globals, not tools: the rows of a tool call that settles as the script ends show it ok in Pi and cancelled in PiG when the
	// machine is slow (see the handoff), and the rows are not what this is about.
	const code = `if (load("k")) return "early"; await next(); await tools.hold({});`
	before := []restartStep{awaitJournal(1), awaitFirstIssued(1)}
	early := startsWith(`{"k":true}`)
	withNext := func(scenario restartScenario) restartScenario {
		scenario.Globals = []string{"next"}
		return scenario
	}
	runRestartScenarios(t, []restartScenario{
		withNext(restartSteps("the journal has an entry and the count was recorded", code, before, []restartStep{early})),
		withNext(restartSteps("the journal has an entry and no count was recorded", code, before, []restartStep{early, deleteDoc("journal", "firstIssued")})),
		withNext(restartSteps("the journal has no entries and the count was recorded", code, before, []restartStep{early, truncateDoc("journal", 0, "entries")})),
		withNext(restartSteps("the journal has no entries and no count was recorded", code, before, []restartStep{early, truncateDoc("journal", 0, "entries"), deleteDoc("journal", "firstIssued")})),
		// The replay made the call the entry answers (id 1 of 1), so only the entry itself says the live run went on. A global's
		// call has no row in the details, which show a finished tool call as cancelled in PiG where Pi shows it ok when the
		// script ended before the replay's nested call settled (see the handoff).
		func() restartScenario {
			s := restartSteps("the entry answers a call the replay made, and no count was recorded",
				`const p = next(); if (load("k")) return "early"; await p; await tools.hold({});`,
				before, []restartStep{early, deleteDoc("journal", "firstIssued")})
			s.Globals = []string{"next"}
			return s
		}(),
	})
}

// codemode.ts:498-502: past the first run, the live run went on when the result's entry records a count, or when a later
// entry answers a call the replay never made (`entry.id > during.issued`); an entry for a call the replay did make does not.
func TestAResumedScriptThatEndsAfterAResultDivergesOnlyWhereALaterEntryAnswersACallItNeverMade(t *testing.T) {
	const twoCalls = `const first = gslow(1); const second = gslow(2);
		await Promise.race([first, second]);
		if (load("k")) return "early";
		await Promise.all([first, second]);
		await tools.hold({});`
	const oneCall = `await next(); if (load("k")) return "early"; await next(); await tools.hold({});`
	withGlobals := func(scenario restartScenario) restartScenario {
		scenario.Globals = []string{"gslow", "next"}
		return scenario
	}
	early := startsWith(`{"k":true}`)
	// Call 1 finishes first, and the script goes on with both calls made (2 issued); call 2's entry follows.
	raced := []restartStep{releaseSlow(1), awaitIssuedAfter(0, 2), releaseSlow(2), awaitJournal(2)}
	runRestartScenarios(t, []restartScenario{
		withGlobals(restartSteps("a later entry is for a call the replay made, and the count was not recorded", twoCalls, raced, []restartStep{early, deleteDoc("journal", "entries", 0, "issuedAfter")})),
		withGlobals(restartSteps("a later entry is for a call the replay made, and the count was recorded", twoCalls, raced, []restartStep{early})),
		withGlobals(restartSteps("a later entry is for a call the replay never made", oneCall, []restartStep{awaitJournal(2)}, []restartStep{early, deleteDoc("journal", "entries", 0, "issuedAfter")})),
		withGlobals(restartSteps("the result's count was recorded", oneCall, []restartStep{awaitJournal(2), awaitIssuedAfter(0, 2)}, []restartStep{early})),
	})
}

// codemode.ts:573-577: a script that only deletes a stored key still writes its store.
func TestAScriptThatOnlyDeletesAStoredKeyRemovesIt(t *testing.T) {
	runRestartScenarios(t, []restartScenario{{
		Name: "delete only",
		Steps: []restartStep{
			{Op: "start", Code: `store("only", 1); return load("only")`}, awaitStep("done"), observeAt("one set"),
			{Op: "start", Code: `store("k", 1); store("other", 2); return load("k")`}, awaitStep("done"),
			{Op: "start", Code: `store("k", undefined); return load("k") ?? null`}, awaitStep("done"), observeAt("deleted"),
			{Op: "start", Code: `store("other", undefined); store("k", 3); return [load("k"), load("other") ?? null]`}, awaitStep("done"), observeAt("deleted and set"),
		},
	}})
}

// codemode.ts:658-666: the journal entry of a global is for a global of that name and those arguments; a tool call, or another
// global's, is a divergence, whatever the other fields say.
func TestAReplayedCallThatDiffersFromAGlobalsEntryDiverges(t *testing.T) {
	early := startsWith(`{"k":true}`)
	before := []restartStep{awaitJournal(1)}
	runRestartScenarios(t, []restartScenario{
		func() restartScenario {
			s := restartSteps("a tool call where the journal has a global of the same name and arguments",
				`if (load("k")) await tools.next([]); else await next(); await tools.hold({});`, before, []restartStep{early})
			s.Globals = []string{"next"}
			return s
		}(),
		func() restartScenario {
			s := restartSteps("another global with the same arguments",
				`if (load("k")) await constant(); else await next(); await tools.hold({});`, before, []restartStep{early})
			s.Globals = []string{"next", "constant"}
			return s
		}(),
		func() restartScenario {
			s := restartSteps("the same global",
				`if (load("k")) await next(); else await next(); await tools.hold({});`, before, []restartStep{early})
			s.Globals = []string{"next"}
			return s
		}(),
	})
}

func awaitCommits(count int) restartStep {
	return restartStep{Op: "await", What: "commits", Count: count}
}

func releaseCommit(message string) restartStep {
	return restartStep{Op: "release-commit", Message: message}
}

func awaitHeldEvents(count int) restartStep {
	return restartStep{Op: "await", What: "held-events", Count: count}
}

// codemode.ts:543-547: a failure that follows another is not recorded: the call ends with the first. The VM keeps running
// after the call asked to stop it, as a VM that takes a while to stop does, so that the second failure arrives while the
// call still listens. Pi's Session fails every commit after a storage error with a message of its own: the commit of the
// script's first count, queued behind the failing one, is the second failure.
func TestTheFirstFailureOfACallIsTheOneItEndsWith(t *testing.T) {
	runRestartScenarios(t, []restartScenario{{
		Name: "a commit fails and the commit queued behind it fails with the Session's own error",
		Code: `await tools.echo({ text: "a" }); await tools.hold({});`,
		Steps: []restartStep{
			restartOp("open"), restartOp("hold-abort"), restartOp("hold-events"), restartOp("start"), awaitHeldEvents(3),
			restartOp("queue-commits"), restartOp("release-events"),
			// The fingerprint's commit is the first; the count of the script's first run is queued behind it.
			awaitCommits(1), releaseCommit("first failure"),
			awaitStep("abort-requested"),
			restartOp("release-abort"), restartOp("unqueue-commits"), awaitStep("done"), {Op: "observe", Label: "ended", Light: true},
		},
	}})
}

func awaitGlobalsFinished(count int) restartStep {
	return restartStep{Op: "await", What: "globals-finished", Count: count}
}

// codemode.ts:966-1006: the journal's commit is one at a time: what finishes while a commit is in flight waits for it and goes
// into the next, together.
func TestResultsThatFinishWhileAJournalCommitIsInFlightJoinTheNextCommit(t *testing.T) {
	runRestartScenarios(t, []restartScenario{{
		Name:    "three results, the first alone",
		Code:    `const all = await Promise.all([gslow(1), gslow(2), gslow(3)]); await tools.hold({}); return all;`,
		Globals: []string{"gslow"},
		Steps: []restartStep{
			restartOp("open"), restartOp("start"), awaitFirstIssued(3),
			restartOp("queue-commits"), releaseSlow(1), awaitCommits(1),
			releaseSlow(2), awaitGlobalsFinished(2), releaseSlow(3), awaitGlobalsFinished(3),
			releaseCommit(""), restartOp("unqueue-commits"), awaitStep("hold"), awaitIssuedAfter(2, 4), observeAt("journaled"),
			restartOp("close"), reopen(), restartOp("resume"), awaitStep("done"), observeAt("answer"),
		},
	}})
}

// codemode.ts:678-683 and 812-818: a nested call that fails while the call's own signal is aborted fails the call, which a
// later attempt resumes; it is not the script taking another path. The VM keeps running after the call asked to stop it, so
// that the failure reaches the call before the VM's end does.
func TestANestedCallThatFailsBecauseTheHostClosedLeavesTheScriptToALaterAttempt(t *testing.T) {
	const code = `await tools.echo({ text: "a" }); await tools.hold({});`
	closeWhileHeld := []restartStep{
		restartOp("close-begin"), awaitStep("abort-requested"), restartOp("release-abort"), restartOp("close-end"),
	}
	live := []restartStep{restartOp("open"), restartOp("hold-abort"), restartOp("start"), awaitStep("hold")}
	live = append(live, closeWhileHeld...)
	live = append(live, restartStep{Op: "open", Unblock: true, Rearm: true}, restartOp("resume"), awaitStep("done"), observeAt("answer"))
	runRestartScenarios(t, []restartScenario{
		{Name: "a call the script launched", Code: code, Steps: live},
	})
}

// codemode.ts:966-968 and 1000: once the run has its outcome, nothing is journaled: a result that finished while a journal
// commit was in flight stays out of the journal when the script ended meanwhile (here, by its timeout).
func TestAResultThatFinishesAfterTheScriptEndedIsNotJournaled(t *testing.T) {
	runRestartScenarios(t, []restartScenario{{
		Name:    "the script times out while a commit is in flight and another result waits for it",
		Code:    "// @options: {\"timeout_ms\": 1500}\nconst a = gslow(1); const b = gslow(2); await a; await new Promise(() => {});",
		Globals: []string{"gslow"},
		Steps: []restartStep{
			restartOp("open"), restartOp("start"), awaitFirstIssued(2),
			restartOp("queue-commits"), releaseSlow(1), awaitCommits(1),
			releaseSlow(2), awaitGlobalsFinished(2),
			awaitStep("vm-ended"), releaseCommit(""), restartOp("unqueue-commits"), awaitStep("done"), observeAt("answer"),
		},
	}})
}

func beginEdit(step restartStep) restartStep {
	step.Op = "edit-begin"
	return step
}

// codemode.ts:979-980: a count the journal already records for a result stays; the replay's acknowledgment is written only
// where the entry has none. The entry's count changes after the replay read the journal, while the replay's commits wait.
func TestAReplayLeavesTheCountAJournalEntryAlreadyRecords(t *testing.T) {
	const code = `await tools.echo({ text: "a" }); await tools.echo({ text: "b" }); await tools.hold({});`
	runRestartScenarios(t, []restartScenario{{
		Name: "an entry's count changes after the replay read it",
		Code: code,
		Steps: []restartStep{
			restartOp("open"), restartOp("start"), awaitJournal(2), awaitIssuedAfter(1, 3), awaitStep("hold"), restartOp("close"),
			{Op: "open", Rearm: true}, restartOp("hold-events"), restartOp("resume"), awaitHeldEvents(3),
			restartOp("queue-commits"),
			beginEdit(setDoc("journal", "9", "entries", 0, "issuedAfter")), awaitCommits(1),
			beginEdit(deleteDoc("journal", "entries", 1, "issuedAfter")),
			restartOp("release-events"), releaseCommit(""), awaitCommits(2), releaseCommit(""),
			restartOp("unqueue-commits"), restartOp("edit-end"), awaitStep("hold"), awaitIssuedAfter(1, 3), observeAt("replayed"),
		},
	}})
}

// codemode.ts:938-: a rerun whose journal is empty releases the calls it held at the script's first acknowledgment, after
// checking them against the earlier attempt's, and goes on live; the call the earlier attempt finished is not run again.
func TestARerunWithoutJournalEntriesGoesOnLiveAfterItsFirstRun(t *testing.T) {
	runRestartScenarios(t, []restartScenario{
		restartSteps("the count of the first run is recorded", `await tools.echo({ text: "a" }); await tools.hold({});`,
			[]restartStep{awaitJournal(1), awaitFirstIssued(1)}, []restartStep{truncateDoc("journal", 0, "entries")}),
		restartSteps("no count is recorded", `await tools.echo({ text: "a" }); await tools.hold({});`,
			[]restartStep{awaitJournal(1), awaitFirstIssued(1)}, []restartStep{truncateDoc("journal", 0, "entries"), deleteDoc("journal", "firstIssued")}),
	})
}

func uninstall(tool string) restartStep { return restartStep{Op: "uninstall-tool", Tool: tool} }

// codemode.ts:678-683 and 812-818: a nested call that fails while the call's signal is live names another tool or other
// arguments than the earlier attempt's: the script took another path. Here the tool is gone after the restart.
func TestANestedCallThatFailsWhileTheCallIsLiveEndsTheScriptWithItsError(t *testing.T) {
	runRestartScenarios(t, []restartScenario{
		restartSteps("a call the script launches", `await tools.echo({ text: "a" }); await tools.hold({}); await tools.next({});`,
			[]restartStep{awaitJournal(1)}, []restartStep{uninstall("next")}),
		restartSteps("a call the journal has", `await tools.next({}); await tools.hold({});`,
			[]restartStep{awaitJournal(1)}, []restartStep{uninstall("next")}),
	})
}
