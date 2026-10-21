package session_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/codemode"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Ports packages/durable/test/codemode-checkpoints.test.ts (Pi 5c1736f8): the checkpoint policies of the built-in documents.
func TestBuiltInDocumentCheckpointPolicies(t *testing.T) {
	t.Run("keeps task-scoped documents as deltas until their task retires", func(t *testing.T) {
		info := durable.CheckpointInfo{DeltasSinceBase: 1000}
		for name, definition := range map[string]*durable.AnyDocDefinition{
			"NestedCallDoc":      harness.NestedCallDoc.AnyDefinition(),
			"CodemodeRunDoc":     codemode.RunDoc.AnyDefinition(),
			"CodemodeJournalDoc": codemode.JournalDoc.AnyDefinition(),
		} {
			base, err := definition.CheckpointWhen(delta.NewJsonObject(0), nil, info)
			if err != nil || base {
				t.Errorf("%s.CheckpointWhen = %v, %v, want false", name, base, err)
			}
		}
	})

	t.Run("checkpoints the codemode store after 64 deltas without changing historical reads or forks", func(t *testing.T) {
		store := codemode.StoreDoc
		stored := open()
		conversationId := createConversation(t, stored)
		setCounter := func(tx durable.Tx, counter int) {
			t.Helper()
			must(t, mustDoc(t, tx, store, conversationId).Object("values").Set("counter", counter))
		}
		appendCounter := func(counter int) durable.EntryId {
			t.Helper()
			entry, err := durable.Commit(ctx, stored.Session, func(tx durable.Tx) (durable.EntryId, error) {
				setCounter(tx, counter)
				return appendPoint(t, tx, conversationId, "test.counter"), nil
			})
			must(t, err)
			return entry
		}
		cutoff := appendCounter(0)
		record, err := stored.Storage.FindDocument(ctx, durable.DocumentAddress{Kind: store.AnyDefinition().Kind, Scope: durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: conversationId}}, durable.CurrentPoint)
		must(t, err)
		if record == nil {
			t.Fatal("Expected codemode store")
		}
		deltasSinceBase := func() int {
			t.Helper()
			document, err := stored.Storage.Document(ctx, record.Id, durable.CurrentPoint)
			must(t, err)
			return document.DeltasSinceBase
		}
		for counter := 1; counter <= 64; counter++ {
			if entry := appendCounter(counter); counter == 32 {
				cutoff = entry
			}
		}
		expectEqual(t, deltasSinceBase(), 64)
		commit(t, stored.Session, func(tx durable.Tx) error { setCounter(tx, 65); return nil })
		expectEqual(t, deltasSinceBase(), 0)
		var bases []durable.DocumentChangeWrite
		for _, write := range stored.Storage.LastCommit() {
			if change, ok := write.(durable.DocumentChangeWrite); ok && change.Id == record.Id {
				bases = append(bases, change)
			}
		}
		if len(bases) != 1 || bases[0].Content.Kind != durable.ContentBase || bases[0].Content.Version != 1 {
			t.Fatalf("last commit changes = %+v, want one base of version 1", bases)
		}
		expectEqual(t, bases[0].Content.Value, delta.JsonObjectOf("values", delta.JsonObjectOf("counter", 65)))
		asOf, err := stored.Session.SnapshotAsOfErased(ctx, store, cutoff, conversationId)
		must(t, err)
		expectEqual(t, asOf, delta.JsonObjectOf("values", delta.JsonObjectOf("counter", 32)))
		child := fork(t, stored, conversationId, cutoff)
		expectEqual(t, snapshot(t, stored.Session, store, child.Id), delta.JsonObjectOf("values", delta.JsonObjectOf("counter", 32)))
		commit(t, stored.Session, func(tx durable.Tx) error { setCounter(tx, 66); return nil })
		expectEqual(t, deltasSinceBase(), 1)
	})
}

// Not in Pi, whose types reject the omission at compile time: a Go definition without CheckpointWhen or CheckpointWhenRaw
// panics where it is defined, as a bad version does, and either predicate satisfies the requirement.
func TestDefinitionsRequireACheckpointPolicy(t *testing.T) {
	initial := func() obj { return delta.JsonObjectOf("count", 0) }
	t.Run("defineDoc", func(t *testing.T) {
		expectPanic(t, "Document t.no-policy checkpointWhen is required", func() {
			durable.DefineDoc(durable.DocDefinition[obj]{CommonDocDefinition: durable.CommonDocDefinition[obj]{Kind: "t.no-policy", Version: 1, Initial: initial}, DocumentSemantics: sessionScope})
		})
	})
	t.Run("defineDocFamily", func(t *testing.T) {
		expectPanic(t, "Document t.no-policy-family checkpointWhen is required", func() {
			durable.DefineDocFamily(durable.DocFamilyDefinition[obj, string]{Kind: "t.no-policy-family", Version: 1, Family: true, DocumentSemantics: sessionScope, Initial: func(string) obj { return initial() }})
		})
	})
	t.Run("CheckpointWhenRaw alone satisfies it", func(t *testing.T) {
		token := durable.DefineDoc(durable.DocDefinition[obj]{CommonDocDefinition: durable.CommonDocDefinition[obj]{Kind: "t.raw-policy", Version: 1, Initial: initial,
			CheckpointWhenRaw: func(durable.JsonObject, []durable.Op, durable.CheckpointInfo) bool { return true }}, DocumentSemantics: sessionScope})
		if token.AnyDefinition().CheckpointWhen == nil {
			t.Fatal("the erased definition has no predicate")
		}
	})
}
