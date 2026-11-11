// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/store"
)

// Each Storage call is one SQL transaction (Pi's Storage.commit runs its batches in one), and two calls are two: a commit of a call is
// joined only to an earlier batch of the same call (ABI.md section 5.5), not to the last commit of the call before it in the step. Without a
// Loop the writer stores a part as it is admitted, so two commits in one step are two calls (writer.ts #send).
func TestTwoStorageCallsInOneStepAreTwoTransactions(t *testing.T) {
	h := newHandler(abi.NewBuilder(), store.New(store.Options{EmptyNew: true}), func() string { return "00000000-0000-7000-8000-000000000000" }).(*handler)
	h.mir = newMirror()
	s := session.New(&adapter{h: h}, 1, func() float64 { return 0 })
	for range 2 {
		if _, err := s.Commit(func(tx *session.Tx) error {
			_, err := tx.CreateConversation(session.Ownership{})
			return err
		}, session.TxScope{}); err != nil {
			t.Fatal(err)
		}
	}
	commits := h.b.Step().Commits
	if len(commits) != 2 {
		t.Fatalf("%d commits in the step, want one per call", len(commits))
	}
	if commits[1].Joined {
		t.Fatal("the second call's commit is joined to the first call's: the host applies both in one SQL transaction")
	}
}

// The batches of one Storage call are one transaction: a commit after BeginCall's first one is joined to the one before it.
func TestTheBatchesOfOneStorageCallAreJoined(t *testing.T) {
	h := newHandler(abi.NewBuilder(), store.New(store.Options{EmptyNew: true}), func() string { return "00000000-0000-7000-8000-000000000000" }).(*handler)
	h.mir = newMirror()
	a := &adapter{h: h}
	commit := func(id float64) {
		t.Helper()
		if _, err := a.Commit([]session.Write{{Kind: session.WriteConversation, Record: recordWithID(id)}}, session.ID(id)+1); err != nil {
			t.Fatal(err)
		}
	}
	a.BeginCall()
	commit(1)
	commit(2)
	a.BeginCall()
	commit(3)
	var joined []bool
	for _, c := range h.b.Step().Commits {
		joined = append(joined, c.Joined)
	}
	if want := []bool{false, true, false}; !slices.Equal(joined, want) {
		t.Fatalf("commits joined %v, want %v: the second batch of a call joins the first, and the next call starts a transaction", joined, want)
	}
}
