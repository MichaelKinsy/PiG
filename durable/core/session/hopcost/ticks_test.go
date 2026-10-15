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

// tableWrites is the Tables count of a commit shape named like `commit[entry,task]`: every record write is a table write.
func tableWrites(shape string) int { return strings.Count(shape, ",") + 1 }

var readOf = map[string]Read{
	"conversation": ReadRow, "task": ReadRow, "submission": ReadRow, "scanTasks": ReadRow,
	"entry(id)": ReadEntry, "entry(conversation,id)": ReadEntryIn, "scanEntries": ReadScanEntries,
	"findLatestHeadMarker": ReadHeadMarker, "mintId": MintID,
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
		for call, want := range calls {
			var got int
			switch shape, isCommit := strings.CutPrefix(call, "commit["); {
			case isCommit:
				got = commitBackend(p, Writes{Tables: tableWrites(strings.TrimSuffix(shape, "]"))})
			case strings.HasPrefix(call, "commitTail["):
				got = commitTailBackend(p)
			case readOf[call] != 0:
				got = readBackend(p, readOf[call])
			default:
				t.Fatalf("%s: call %q has no model", backend, call)
			}
			if got != want {
				t.Errorf("%s %s: model %d ticks, adapter %d", backend, call, got, want)
			}
		}
	}
}
