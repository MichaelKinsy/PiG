// SPDX-License-Identifier: MIT

//go:build !race

package core

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// prepareDocActions returns its map, so the map is a heap object natively too: a commit without a document write makes none.
// (Before the map was made at the first document write, every commit allocated one: 1 allocation per commit here.)
func TestPrepareDocActionsAllocatesNothingForACommitWithoutDocuments(t *testing.T) {
	m := newMirror()
	writes := []session.Write{{Kind: session.WriteEntry, Record: recordWithID(8)}, {Kind: session.WriteTask, Record: recordWithID(9)}}
	if n := testing.AllocsPerRun(100, func() {
		if _, _, err := m.prepareDocActions(writes); err != nil {
			t.Fatal(err)
		}
	}); n != 0 {
		t.Fatalf("prepareDocActions allocated %v times for a commit without documents", n)
	}
}
