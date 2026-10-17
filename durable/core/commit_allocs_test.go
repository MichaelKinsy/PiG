// SPDX-License-Identifier: MIT

package core

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// writeTable gives the table names checkGlobalIDs reports an ID's owner by. A document change or retire writes no record of its own.
func TestWriteTableNamesTheTableOfEachWriteKind(t *testing.T) {
	for kind, want := range map[session.WriteKind]string{
		session.WriteConversation: "conversation",
		session.WriteEntry:        "entry",
		session.WriteTask:         "task",
		session.WriteSubmission:   "submission",
		session.WriteDocCreate:    "document",
		session.WriteDocCopy:      "document",
		session.WriteDocChange:    "",
		session.WriteDocRetire:    "",
	} {
		if got := writeTable(kind); got != want {
			t.Errorf("writeTable(%d) = %q, want %q", kind, got, want)
		}
	}
}

func recordWithID(id float64) *jv.Object {
	rec := jv.NewObject()
	rec.Set("id", id)
	return rec
}

// A commit without a document write is the common one (a turn commits dozens of times). It must not build the per-document
// action map: on TinyGo each empty map is heap blocks the collector has to sweep, and the core makes one per commit. A commit
// with a document write still gets its action, and an empty batch passes the check.
func TestACommitWithoutDocumentWritesMakesNoActionMap(t *testing.T) {
	m := newMirror()
	plain := []session.Write{{Kind: session.WriteEntry, Record: recordWithID(8)}, {Kind: session.WriteTask, Record: recordWithID(9)}}
	actions, order, err := m.prepareDocActions(plain)
	if err != nil || len(actions) != 0 || len(order) != 0 {
		t.Fatalf("a commit without documents prepared %d actions, order %v, err %v", len(actions), order, err)
	}
	if err := m.checkDocActions(actions, order); err != nil {
		t.Fatal(err)
	}
	m.applyDocActions(actions, order, 1)

	withDoc := []session.Write{{Kind: session.WriteDocCreate, Record: recordWithID(12), Base: true}}
	actions, order, err = m.prepareDocActions(withDoc)
	if err != nil || len(actions) != 1 || len(order) != 1 || order[0] != 12 || actions[12] == nil || actions[12].create == nil {
		t.Fatalf("a document create prepared %d actions, order %v, err %v", len(actions), order, err)
	}
}
