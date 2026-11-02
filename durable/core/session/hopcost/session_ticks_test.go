// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package hopcost

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// testdata/session_ticks.json is generated from the reference's Session over each storage profile (durable/contract/tools/gen-hopticks.mjs,
// scenarios/session-ticks.mjs): the microtask passes a Transaction method costs its callback, from the Session's record cache (warm: the Session
// wrote the records) or from Storage (cold: a Session that read nothing), and a whole commit from its call to its caller's resumption. The model
// derives each number from the await structure of transaction.ts, cached-storage.ts, session.ts and storage.ts; this test requires the two to agree.

func hitOf(warm bool) Cache {
	if warm {
		return CacheHit
	}
	return CacheMiss
}

// txModel is the model's ticks of one measured Transaction method, `warm tx.task`'s `tx.task`.
func txModel(t *testing.T, p Profile, name string, warm bool) int {
	t.Helper()
	hit := hitOf(warm)
	switch name {
	case "tx.conversation":
		return TxConversation(p, hit)
	case "tx.task":
		return TxTask(p, false, hit)
	case "tx.task (second)":
		return TxTask(p, true, hit)
	case "tx.submission":
		return TxSubmission(p, hit)
	case "tx.submissionByRequest", "tx.scanTasks":
		return TxRead(p, ReadRow)
	case "tx.entry":
		return TxEntry(p)
	case "tx.scanEntries":
		return TxRead(p, ReadScanEntries)
	case "tx.latestEntryId":
		return LatestEntryID(p, warm, false)
	case "tx.latestHeadMarker":
		return LatestHeadMarker(p, warm, false)
	case "tx.appendEntry":
		return TxAppendEntry(p, false, hit)
	case "tx.appendEntry (conversation created here)":
		return TxAppendEntry(p, true, CacheMiss)
	case "tx.createTask":
		return TxCreateTask(p, false, false, false, CacheMiss, false, hit)
	case "tx.createTask (owned)":
		return TxCreateTask(p, true, false, false, hit, false, hit)
	case "tx.createSubmission":
		return TxCreateSubmission(p, false, hit)
	case "tx.createConversation":
		return TxCreateConversation(p, false, false, false, CacheMiss, 1)
	case "tx.createConversation (owned)":
		return TxCreateConversation(p, true, false, false, hit, 1)
	case "tx.doc (stored)", "tx.doc (task)":
		if warm {
			return TxDoc(p, false, DocCached, 'c', false, false, false, hit)
		}
		return TxDoc(p, false, DocStored, 'c', false, false, false, hit)
	case "tx.doc (second)":
		return TxDoc(p, true, 0, 0, false, false, false, hit)
	case "tx.doc (absent)":
		return TxDoc(p, false, DocAbsent, 'c', false, false, false, hit)
	case "tx.doc (task, absent)":
		return TxDoc(p, false, DocAbsent, 't', false, false, false, hit)
	}
	t.Fatalf("no model for %q", name)
	return 0
}

// span is the ticks from the call of `session.commit` on an idle line to its caller's resumption (session.ts #enqueue, #runCommit, writer.ts):
// the job starts one tick after the call, its work returns Job.Return ticks later, the writer's call to Storage.commit starts there and the
// caller resumes OutcomeAfterPublication + FenceAfterOutcome + Delivery after the part's publication, which is when the writer resumed.
func span(j Job, p Profile, batches []Writes) int {
	if batches == nil {
		return 1 + j.Return() + OutcomeSettled + FenceAfterOutcome + Delivery
	}
	return 1 + j.Return() + Commit(p, batches) + OutcomeAfterPublication + FenceAfterOutcome + Delivery
}

// terminalWrites is the commit of a task going terminal that has one task document (retired with it: a task document keeps only current state).
var terminalWrites = Writes{Tables: 1, DocEdits: 1, Actions: []Action{{Retire: true, CurrentOnly: true}}}

// commitModel is the model's ticks of one measured commit. The callbacks are `async (tx) => {...}` over the awaits named.
func commitModel(t *testing.T, p Profile, name string) int {
	t.Helper()
	appendEntry := func(hit Cache) int { return TxAppendEntry(p, false, hit) }
	createTask := func(hit Cache) int { return TxCreateTask(p, false, false, false, CacheMiss, false, hit) }
	ownedTask := func(first bool) int {
		// The second owned task finds the owner's committed read settled (CurrentTask: 1) and the conversation cached.
		return TxCreateTask(p, true, false, !first, CacheHit, false, CacheHit)
	}
	delta := []Writes{{DocEdits: 1, Actions: []Action{{Content: Delta}}}}
	settle := Settle(0, 0, 0)
	owner := CurrentTask(p, false, true, CacheHit) // validateOwners awaits the owner of a created owned task: its read is settled already
	switch name {
	case "commit warm (no write)":
		return span(Job{Settle: settle}, p, nil)
	case "commit warm (entry)":
		return span(Job{Change: appendEntry(CacheHit), Settle: settle}, p, []Writes{{Tables: 1}})
	case "commit warm (task)":
		return span(Job{Change: createTask(CacheHit), Settle: settle}, p, []Writes{{Tables: 1}})
	case "commit warm (entry, task)":
		return span(Job{Change: appendEntry(CacheHit) + createTask(CacheHit), Settle: settle}, p, []Writes{{Tables: 2}})
	case "commit warm (task owned)":
		return span(Job{Change: ownedTask(true), Settle: Settle(owner, 0, 0)}, p, []Writes{{Tables: 1}})
	case "commit warm (2 tasks owned)":
		return span(Job{Change: ownedTask(true) + ownedTask(false), Settle: Settle(2*owner, 0, 0)}, p, []Writes{{Tables: 2}})
	case "commit warm (doc)":
		return span(Job{Change: TxDoc(p, false, DocCached, 'c', false, false, false, CacheHit), Settle: settle}, p, delta)
	case "commit warm (task terminal, documents known)":
		// The callback reads the task (cache hit) and stages the record; the settle awaits the settled read and the staged task's `#currentTask`
		// for the publication of its retired document; the task's documents are the Session's, so nothing is scanned.
		return span(Job{Change: TxTask(p, false, CacheHit), Settle: Settle(0, 1, CurrentTask(p, true, false, CacheMiss))}, p, []Writes{terminalWrites})
	case "commit cold (entry)":
		return span(Job{Change: appendEntry(CacheMiss), Settle: settle}, p, []Writes{{Tables: 1}})
	case "commit cold (doc)":
		return span(Job{Change: TxDoc(p, false, DocStored, 'c', false, false, false, CacheMiss), Settle: settle}, p, delta)
	case "commit cold (task terminal)":
		// The Session did not create the task: its documents are scanned, one page.
		return span(Job{Change: TxTask(p, false, CacheMiss), Settle: Settle(0, 1, ReadHops(p, ReadRow))}, p, []Writes{{Tables: 1}})
	}
	t.Fatalf("no model for %q", name)
	return 0
}

func TestSessionModelMatchesTheMeasuredSession(t *testing.T) {
	data, err := os.ReadFile("testdata/session_ticks.json")
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
		if len(calls) < 50 {
			t.Fatalf("backend %q measures only %d calls", backend, len(calls))
		}
		for call, want := range calls {
			var got int
			switch {
			case strings.HasPrefix(call, "commit pair "):
				continue
			case strings.HasPrefix(call, "commit "):
				got = commitModel(t, p, call)
			case strings.HasPrefix(call, "warm "):
				got = txModel(t, p, strings.TrimPrefix(call, "warm "), true)
			case strings.HasPrefix(call, "cold "):
				got = txModel(t, p, strings.TrimPrefix(call, "cold "), false)
			default:
				t.Fatalf("%s: call %q has no model", backend, call)
			}
			if got != want {
				t.Errorf("%s %s: model %d ticks, Pi %d", backend, call, got, want)
			}
		}
	}
}

// Two jobs queued together (the writer's group commit): the second job's callback starts LineRelease after the first one's work returned, its
// work returns, and its part waits for the call in flight, which the writer pumps again WriterRepump ticks after its publication.
func TestPairedCommitsMatchPi(t *testing.T) {
	data, err := os.ReadFile("testdata/session_ticks.json")
	if err != nil {
		t.Fatal(err)
	}
	var table tickTable
	if err := json.Unmarshal(data, &table); err != nil {
		t.Fatal(err)
	}
	for backend, calls := range table.Profiles {
		p := profileOf[backend]
		job := Job{Change: TxAppendEntry(p, false, CacheHit), Settle: Settle(0, 0, 0)}
		batches := []Writes{{Tables: 1}}
		admitted := 1 + job.Return()
		resumed := admitted + Commit(p, batches)
		first := resumed + OutcomeAfterPublication + FenceAfterOutcome + Delivery
		second := max(admitted+LineRelease+job.Return(), resumed+WriterRepump) + Commit(p, batches) + OutcomeAfterPublication + FenceAfterOutcome + Delivery
		if got := calls["commit pair first"]; got != first {
			t.Errorf("%s: first = %d, Pi %d", backend, first, got)
		}
		if got := calls["commit pair second"]; got != second {
			t.Errorf("%s: second = %d, Pi %d", backend, second, got)
		}
	}
}
