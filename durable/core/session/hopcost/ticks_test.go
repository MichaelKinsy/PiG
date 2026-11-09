// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package hopcost

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// testdata/ticks.json is generated from the reference's adapters (durable/contract/tools/gen-hopticks.mjs, scenarios/storage-ticks.mjs):
// the microtask passes each Storage call spends on an idle queue, per backend. The model derives the same numbers from the adapters'
// await structure (before the Session's guard, which guarded adds); this test requires the two to agree for every call of every backend, so the model cannot drift from the adapters
// it describes (`node tools/gen-hopticks.mjs --check` fails when the reference moves under the table).
type tickTable struct {
	Profiles map[string]map[string]int `json:"profiles"`
}

// profileOf names the model's profile of a measured backend.
var profileOf = map[string]Profile{"gate": Gate, "gate-capsql": GateCapsql, "node": Node}

var readOf = map[string]Read{
	"conversation": ReadRow, "task": ReadRow, "submission": ReadRow, "scanTasks": ReadRow,
	"entry(id)": ReadEntry, "entry(conversation,id)": ReadEntryIn, "scanEntries": ReadScanEntries,
	"findLatestHeadMarker": ReadHeadMarker, "mintId": MintID,
}

// parseShape reads the shape of a measured commit, `commit[a,b;c]`'s `a,b;c`: batches separated by `;`, writes by `,`. A write is a table
// kind (conversation, entry, task, submission) or a document write: `doc.create:base[:latest]`, `doc.change:delta`, `doc.change:base[:latest]`,
// `doc.retire[:latest]`, `doc.copy`. A `doc.retire` right after a `doc.create` of its batch retires that document (one merged action); the
// other document writes name documents of their own (`@a` marks an address the commit reads in bulk, which the model does not tell apart).
// A write marked `!taken` is the one Durable's checks refuse.
func parseShape(t *testing.T, shape string) []Writes {
	t.Helper()
	var batches []Writes
	var docs int64
	for batch := range strings.SplitSeq(shape, ";") {
		var w Writes
		for i, write := range strings.Split(batch, ",") {
			if before, ok := strings.CutSuffix(write, "!taken"); ok {
				write, w.Refused = before, true
			}
			address := false
			if before, _, ok := strings.Cut(write, "@"); ok {
				write, address = before, true
			}
			parts := strings.Split(write, ":")
			latest := len(parts) > 1 && parts[len(parts)-1] == "latest"
			docs++
			a := Action{Doc: docs, CurrentOnly: latest}
			switch kind := parts[0]; kind {
			case "conversation":
				w.Conversations++
			case "entry":
				w.Entries++
			case "task":
				w.Tasks++
			case "submission":
				w.Submissions++
			case "doc.create":
				a.Create, a.Content = true, Base
				w.Actions = append(w.Actions, a)
			case "doc.copy":
				a.Create, a.Copy, a.Content = true, true, Base
				w.Actions = append(w.Actions, a)
			case "doc.change":
				a.Content = Base
				if parts[1] == "delta" {
					a.Content = Delta
				}
				w.Actions = append(w.Actions, a)
			case "doc.retire":
				if last := len(w.Actions) - 1; i > 0 && last >= 0 && w.Actions[last].Create && !address {
					w.Actions[last].Retire = true
					break
				}
				a.Retire = true
				w.Actions = append(w.Actions, a)
			default:
				t.Fatalf("shape %q: unknown write %q", shape, write)
			}
		}
		batches = append(batches, w)
	}
	return batches
}

func TestModelMatchesTheGeneratedBackendTables(t *testing.T) {
	data, err := os.ReadFile("testdata/ticks.json")
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
		p, ok := profileOf[backend]
		if !ok {
			t.Fatalf("backend %q has no profile in the model", backend)
		}
		if len(calls) < 10 {
			t.Fatalf("backend %q measures only %d calls", backend, len(calls))
		}
		commits := 0
		for call, want := range calls {
			var got int
			switch shape, isCommit := strings.CutPrefix(call, "commit["); {
			case isCommit:
				commits++
				got = commitBackend(p, parseShape(t, strings.TrimSuffix(shape, "]")))
			case strings.HasPrefix(call, "commitTail["):
				got = commitTailBackend(p)
			case strings.HasPrefix(call, "receiveTail["):
				got = receiveTailBackend(p)
			case readOf[call] != 0:
				got = readBackend(p, readOf[call])
			default:
				t.Fatalf("%s: call %q has no model", backend, call)
			}
			if got != want {
				t.Errorf("%s %s: model %d ticks, adapter %d", backend, call, got, want)
			}
		}
		if commits < 25 {
			t.Errorf("backend %q measures only %d commit shapes", backend, commits)
		}
	}
}

// The Memory profile stores in the tick of the call and resumes MemoryCommitResume ticks later, whatever the batches.
func TestCommitCallMemory(t *testing.T) {
	for _, batches := range [][]Writes{{{Entries: 1}}, {{Entries: 2}, {Tasks: 1}}} {
		if receive, resume := CommitCall(Memory, batches); receive != 0 || resume != MemoryCommitResume {
			t.Errorf("CommitCall(Memory, %d batches) = %d, %d, want 0, %d", len(batches), receive, resume, MemoryCommitResume)
		}
	}
}

// A call's COMMIT is CommitTail ticks before the writer resumes and the receipts ReceiveTail ticks before it: the receipts follow the COMMIT.
func TestCommitCall(t *testing.T) {
	for _, p := range []Profile{GateCapsql, Gate, Node} {
		batches := []Writes{{Entries: 1}, {Tasks: 2}}
		receive, resume := CommitCall(p, batches)
		if resume != Commit(p, batches) || receive != resume-CommitTail(p) {
			t.Errorf("%s: CommitCall = %d, %d", p, receive, resume)
		}
		if ReceiveTail(p) >= CommitTail(p) {
			t.Errorf("%s: the receipts (%d) come after the COMMIT (%d ticks before the resumption)", p, ReceiveTail(p), CommitTail(p))
		}
	}
}
