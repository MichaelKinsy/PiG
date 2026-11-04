// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// Ports of packages/durable/test/session-deferred-commits.test.ts through the native driver and the REAL core over SQLite (the core's own
// cases, with Pi's names, are core/session/held_test.go). Pi observes what Storage got through ControlledStorage; here the stored
// document_revisions rows are the same fact, as in docs_core_test.go: kind (base or delta) and version per revision.

var deferredLatestInitial = durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkInitial}

func deferredTextDoc() durable.DocToken[obj] {
	common := durable.CommonDocDefinition[obj]{Kind: "test.deferred.text", Version: 1, CheckpointWhen: func(obj, []durable.Op, durable.CheckpointInfo) bool { return false }}
	return durable.DefineDoc(durable.DocDefinition[obj]{CommonDocDefinition: withInitial(common, func() obj { return delta.JsonObjectOf("text", "") }), DocumentSemantics: deferredLatestInitial})
}

var deferredCommit = durable.CommitOptions{Persistence: &durable.CommitPersistence{Kind: durable.CommitDeferred}}

func appendText(t *testing.T, r *coreRig, conversation durable.ConversationId, token durable.DocToken[obj], text string, options ...durable.CommitOptions) {
	t.Helper()
	if _, err := r.s.Commit(t.Context(), func(tx durable.Tx) (any, error) {
		doc, err := tx.Doc(token, conversation)
		if err != nil {
			return nil, err
		}
		current, _ := doc.Get("text").(string)
		return nil, doc.Set("text", current+text)
	}, options...); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func memoryText(t *testing.T, r *coreRig, conversation durable.ConversationId, token durable.DocToken[obj]) string {
	t.Helper()
	value, err := durable.Snapshot(t.Context(), r.s, token, conversation)
	if err != nil || value == nil {
		t.Fatalf("snapshot: %v %v", value, err)
	}
	text, _ := (*value).Value("text").(string)
	return text
}

func deferredRig(t *testing.T) (*coreRig, durable.ConversationId, durable.DocToken[obj]) {
	t.Helper()
	r := openCoreDBWith(t, nil)
	conversation, err := r.s.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	token := deferredTextDoc()
	id := conversation.Id()
	r.commit(t, func(tx durable.Tx) error { _, err := tx.Doc(token, id); return err })
	return r, id, token
}

// session-deferred-commits.test.ts "holding a deferred commit adopts a commit that only changes an existing latest, initial-fork
// conversation document in memory, unstored".
func TestCoreHoldingADeferredCommitAdoptsItInMemoryUnstored(t *testing.T) {
	r, conversation, token := deferredRig(t)
	stored := r.revisions(t, "test.deferred.text")
	appendText(t, r, conversation, token, "a", deferredCommit)
	appendText(t, r, conversation, token, "b", deferredCommit)
	if got := memoryText(t, r, conversation, token); got != "ab" {
		t.Fatalf("memory %q", got)
	}
	eq(t, "stored revisions", r.revisions(t, "test.deferred.text"), stored)
}

// "storing held changes stores them first, folded with its own, in a commit that writes the document".
func TestCoreAnImmediateCommitStoresTheHeldChangesFoldedWithItsOwn(t *testing.T) {
	r, conversation, token := deferredRig(t)
	appendText(t, r, conversation, token, "a", deferredCommit)
	appendText(t, r, conversation, token, "b", deferredCommit)
	before := len(r.revisions(t, "test.deferred.text"))
	appendText(t, r, conversation, token, "c")
	revisions := r.revisions(t, "test.deferred.text")
	if len(revisions) != before+1 || revisions[len(revisions)-1] != "delta@1" {
		t.Fatalf("revisions %v: want one delta after the %d before", revisions, before)
	}
	if got := memoryText(t, r, conversation, token); got != "abc" {
		t.Fatalf("memory %q", got)
	}
}

// "stores a deferred commit at once when it writes anything else": an entry next to the document's change.
func TestCoreADeferredCommitThatWritesAnEntryIsStoredAtOnce(t *testing.T) {
	r, conversation, token := deferredRig(t)
	before := len(r.revisions(t, "test.deferred.text"))
	if _, err := r.s.Commit(t.Context(), func(tx durable.Tx) (any, error) {
		doc, err := tx.Doc(token, conversation)
		if err != nil {
			return nil, err
		}
		if err := doc.Set("text", "x"); err != nil {
			return nil, err
		}
		_, err = tx.AppendEntry(conversation, durable.EntryDraft{Kind: "test.note"})
		return nil, err
	}, deferredCommit); err != nil {
		t.Fatal(err)
	}
	if got := len(r.revisions(t, "test.deferred.text")); got != before+1 {
		t.Fatalf("revisions %d, want %d: the commit was held", got, before+1)
	}
}

// "rejects a withinMs that is not a number of milliseconds, 0 or more".
func TestCoreADeferredCommitWithANegativeWindowIsRejected(t *testing.T) {
	r, conversation, token := deferredRig(t)
	negative := -1.0
	_, err := r.s.Commit(t.Context(), func(tx durable.Tx) (any, error) {
		_, err := tx.Doc(token, conversation)
		return nil, err
	}, durable.CommitOptions{Persistence: &durable.CommitPersistence{Kind: durable.CommitDeferred, WithinMs: &negative}})
	if err == nil {
		t.Fatal("a negative window was accepted")
	}
}
