// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package hopcost

import "testing"

var (
	delta  = Action{Content: Delta}
	base   = Action{Content: Base, CurrentOnly: true}
	create = Action{Create: true, Content: Base, CurrentOnly: true}
)

// The expectations are Pi's measured SqliteStorage.commit durations over the gate's capsql facade
// (handoffs/dcore-trace-hops.md, gate table, the direct-open values: every proxied value is these plus 2).
func TestCommitMatchesPi(t *testing.T) {
	cases := []struct {
		name string
		w    Writes
		want int
	}{
		{"task", Writes{Tables: 1}, 15},
		{"task,task", Writes{Tables: 2}, 20},
		{"entry,task", Writes{Tables: 2}, 20},
		{"entry,submission", Writes{Tables: 2}, 20},
		{"document.change (delta)", Writes{DocEdits: 1, Actions: []Action{delta}}, 17},
		{"document.change (base)", Writes{DocEdits: 1, Actions: []Action{base}}, 17},
		{"task,document.change", Writes{Tables: 1, DocEdits: 1, Actions: []Action{delta}}, 22},
		{"submission,document.change", Writes{Tables: 1, DocEdits: 1, Actions: []Action{delta}}, 22},
		{"entry,task,document.change", Writes{Tables: 2, DocEdits: 1, Actions: []Action{delta}}, 27},
		{"submission,task,document.change", Writes{Tables: 2, DocEdits: 1, Actions: []Action{delta}}, 27},
		{"task,document.create", Writes{Tables: 1, DocCreates: 1, Actions: []Action{create}}, 25},
		{"entry,submission,task,document.change", Writes{Tables: 3, DocEdits: 1, Actions: []Action{delta}}, 32},
		{"entry,task,task,document.change,document.change", Writes{Tables: 3, DocEdits: 2, Actions: []Action{delta, delta}}, 39},
		{"entry,submission,task,document.change,document.change", Writes{Tables: 3, DocEdits: 2, Actions: []Action{delta, delta}}, 39},
		{"entry,entry,task,task,document.change,document.change", Writes{Tables: 4, DocEdits: 2, Actions: []Action{delta, delta}}, 44},
	}
	for _, c := range cases {
		if got := commitBackend(GateCapsql, c.w); got != c.want {
			t.Errorf("%s: Commit = %d, want %d", c.name, got, c.want)
		}
		if got := commitBackend(Gate, c.w); got != c.want+2 {
			t.Errorf("%s: gate Commit = %d, want %d (the lazy proxy adds 2)", c.name, got, c.want+2)
		}
	}
}

// Pi's measured Storage read durations over the gate (dcore-trace-hops.md): direct-open value and the proxied value.
func TestReadMatchesPi(t *testing.T) {
	cases := []struct {
		name         string
		r            Read
		direct, lazy int
	}{
		{"conversation, task, findDocument, submission, scanTasks", ReadRow, 5, 7},
		{"document", ReadDocument, 8, 10},
		{"entry(id)", ReadEntry, 6, 8},
		{"entry(conversation, id), scanEntries, findLatestHeadMarker", ReadEntryIn, 11, 13},
		{"scanEntries", ReadScanEntries, 11, 13},
		{"findLatestHeadMarker", ReadHeadMarker, 11, 13},
		{"mintId", MintID, 1, 4},
	}
	for _, c := range cases {
		if got := readBackend(GateCapsql, c.r); got != c.direct {
			t.Errorf("%s: direct = %d, want %d", c.name, got, c.direct)
		}
		if got := readBackend(Gate, c.r); got != c.lazy {
			t.Errorf("%s: gate = %d, want %d", c.name, got, c.lazy)
		}
	}
}

// Pi's line jobs of E23 (gate-capsql, direct open), from an instrumented run of the reference (its callback-return, settle
// and storage.commit events), before the Session's Storage guard (session.ts #call, 42a3497d). The guard adds guarded's
// ticks to each Storage call of the settle, the commit and the callback: the expectations below are the trace's value
// plus those (2 per awaited method; mintId 1 -> 4, so 3).
func TestJobMatchesPi(t *testing.T) {
	cases := []struct {
		name string
		j    Job
		want int
	}{
		// #reserve (scheduler.ts:761-763): the callback awaits `#loadScopes(false)` (1); one task write whose settle
		// reads the committed task. job+ -> COMMIT +22, job- +27 (jobs 44, 48).
		{"#reserve", Job{Change: 1, Settle: Settle(GateCapsql, 1, 0, 0), Writes: &Writes{Tables: 1}}, 27 + 2 + 2},
		// A #gated custom-task done phase (scheduler.ts:1152-1153): callback settled at +4; the settle reads the
		// committed task and the task's documents (scanDocuments) because the task goes terminal (job 36, job- +35).
		{"#gated terminal task", Job{Change: 4, Settle: Settle(GateCapsql, 2, 0, 0), Writes: &Writes{Tables: 1}}, 35 + 2*2 + 2},
		// The generation prepare (generation.ts:164-186): the callback's scanEntries (11), conversation, mintId and
		// appendEntry awaits settle at +22; entry+task writes (job 56, job- +53). Change is the measured callback span.
		{"#gated prepare", Job{Change: 22 + 7, Settle: Settle(GateCapsql, 1, 0, 0), Writes: &Writes{Tables: 2}}, 53 + 7 + 2 + 2},
	}
	for _, c := range cases {
		if got := c.j.Hops(GateCapsql); got != c.want {
			t.Errorf("%s: Job = %d, want %d", c.name, got, c.want)
		}
	}
}

// The generation prepare callback (generation.ts:164-186) through runtime.commit: tx.scanEntries (11), tx.appendEntry of
// the system entry on a conversation the transaction did not create (requireConversation 5+1, mintId 1, return 1) =
// 19 ticks; runtime.commit adds its wrappers: Pi's callback settles at +22 and `await change` resumes at +23 (job 56,
// dcore-trace-e23-jobs.md).
func TestRuntimeCommitMatchesPi(t *testing.T) {
	u := TxRead(GateCapsql, ReadScanEntries) + TxAppendEntry(GateCapsql, false)
	// The trace measured 19 before the guard; scanEntries and the conversation read cost 2 more each, mintId 3.
	if u != 19+2+2+3 {
		t.Fatalf("callback ticks = %d, want %d", u, 19+2+2+3)
	}
	if got := RuntimeCommit(u, true, 0); got != 22+7 {
		t.Fatalf("Change = %d, want %d (callback settled +22 before the guard)", got, 22+7)
	}
	job := Job{Change: RuntimeCommit(u, true, 0), Settle: Settle(GateCapsql, 1, 0, 0), Writes: &Writes{Tables: 2}}
	if got := job.Hops(GateCapsql); got != 53+7+2+2 {
		t.Fatalf("job 56 span = %d, want %d", got, 53+7+2+2)
	}
	if TxTask(GateCapsql, true) != 1 || TxTask(GateCapsql, false) != 5+2 || TxEntry(GateCapsql) != 7+2 {
		t.Fatalf("tx task/entry reads")
	}
	if got := TxAppendEntry(Gate, true); got != 1+6+1 {
		t.Fatalf("gate appendEntry on a created conversation = %d, want 8", got)
	}
	if GatedChange(0) != 2 || GatedSyncChange != 0 {
		t.Fatalf("gated wrappers")
	}
}

// The request-phase commit (generation.ts:191-196): tx.doc(LiveDoc) from the Session's cache (2), then convertPartial
// (an async function with nothing to append: 1) settle at +3; runtime.commit's callback returns no next state; one
// document delta; the settle has no reads. Pi spans 28 ticks (E23 jobs 62 and 63).
func TestRequestCommitMatchesPi(t *testing.T) {
	u := TxDoc(GateCapsql, false, DocCached, 'c', false, false) + 1
	if u != 3 {
		t.Fatalf("callback ticks = %d, want 3", u)
	}
	j := Job{Change: RuntimeCommit(u, false, 0), Settle: Settle(GateCapsql, 0, 0, 0), Writes: &Writes{DocEdits: 1, Actions: []Action{delta}}}
	if got := j.Hops(GateCapsql); got != 28+2 {
		t.Fatalf("request commit = %d, want %d (the commit's guard adds 2)", got, 28+2)
	}
}

func TestTxCreateCosts(t *testing.T) {
	if got := TxCreateSubmission(GateCapsql, false); got != (7+1)+4+1 {
		t.Errorf("createSubmission = %d, want 13", got)
	}
	if got := TxCreateTask(Gate, true, true, false, true); got != 1+1+6+1 {
		t.Errorf("gate createTask with a staged owner on a created conversation = %d, want 9", got)
	}
	if got := TxDoc(GateCapsql, false, DocAbsent, 'c', true, false); got != (7+1)+4+1+1 {
		t.Errorf("new conversation document = %d, want 14", got)
	}
	if got := TxDoc(GateCapsql, false, DocStored, 's', false, false); got != 7+10+1+1 {
		t.Errorf("stored document = %d, want 19", got)
	}
	if got := TxCreateConversation(GateCapsql, false, false, false, 1); got != 4+1+1 {
		t.Errorf("createConversation = %d, want 6", got)
	}
}

// Jobs 42 and 46 of E23 (dcore-trace-e23-jobs.md) with the callback span measured (their callbacks are scheduler and
// generation code whose awaits are derived at their call sites): the settle, the commit and the return are derived.
// Job 42 (Submissions.submit): settle without a committed-task read (validateOwners and assemble's return: 2), an
// entry, submission, task and document change commit (32). Job 46 (finishToolRound): settle reads the committed task
// and the task's documents (12), two task writes and a document change (27).
func TestJobsWithMeasuredCallbacks(t *testing.T) {
	j42 := Job{Change: 46, Settle: Settle(GateCapsql, 0, 0, 0), Writes: &Writes{Tables: 3, DocEdits: 1, Actions: []Action{delta}}}
	if got := j42.Hops(GateCapsql); got != 84+2 {
		t.Errorf("job 42 = %d, want 86 (the commit's guard adds 2)", got)
	}
	j46 := Job{Change: 30, Settle: Settle(GateCapsql, 2, 0, 0), Writes: &Writes{Tables: 2, DocEdits: 1, Actions: []Action{delta}}}
	if got := j46.Hops(GateCapsql); got != 73+2*2+2 {
		t.Errorf("job 46 = %d, want 79 (two settle reads and the commit)", got)
	}
}

// guarded is the Session guard's ticks around a backend call that takes d: the async arrow adopts the backend's promise (never
// before tick 2), `await call` adds one and the async #call's return one more (session.ts #call). It is the viaProxy arithmetic,
// and the client-ticks rows (F-ticks-node/-gate: every client call spends 2 more per Storage call than before 42a3497d) hold it
// end to end.
func TestTheSessionGuardWrapsEveryStorageCall(t *testing.T) {
	for d, want := range map[int]int{1: 4, 2: 4, 3: 5, 5: 7, 11: 13} {
		if got := guarded(d); got != want {
			t.Errorf("guarded(%d) = %d, want %d", d, got, want)
		}
	}
	for _, p := range []Profile{GateCapsql, Gate, Memory, Node} {
		for r := ReadRow; r <= MintID; r++ {
			if got, want := ReadHops(p, r), guarded(readBackend(p, r)); got != want {
				t.Errorf("%s read %d = %d, want %d", p, r, got, want)
			}
		}
		w := Writes{Tables: 2}
		if got, want := Commit(p, w), guarded(commitBackend(p, w)); got != want {
			t.Errorf("%s commit = %d, want %d", p, got, want)
		}
	}
}

// The settle of the commit that creates k tool tasks (F-ticks-tools, instrumented Pi session.ts #runCommit): from `rc changed`
// to `rc settled` it takes 5, 6 and 7 ticks for k = 1, 2, 3 in the node and the gate profiles alike. validateOwners awaits each
// created owned task (k) and its own return (1); the replaced generation task's committed read is already settled (1); the
// assemble return (1); and `await tx.settleSuccess()` (1).
func TestSettleOwnersMatchPi(t *testing.T) {
	for _, p := range []Profile{Node, GateCapsql} {
		for k, want := range map[int]int{1: 5, 2: 6, 3: 7} {
			if got := Settle(p, 0, 1, k) + 1; got != want {
				t.Errorf("profile %v, %d created owned tasks: settle = %d, want %d", p, k, got, want)
			}
		}
	}
	// No owned task created: validateOwners is an async function without awaits (1).
	if got, want := Settle(Node, 1, 0, 0), 1+ReadHops(Node, ReadRow)+1; got != want {
		t.Errorf("no owners: settle = %d, want %d", got, want)
	}
}
