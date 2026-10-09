package session_test

// pi: packages/durable/src/session/forks.ts

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
)

// Ports packages/durable/test/session-forks.test.ts

func valueDoc(kind string, semantics durable.DocumentSemantics, initial string, options ...docOption) durable.DocToken[obj] {
	return defineDoc(kind, 1, semantics, func() obj { return delta.JsonObjectOf("value", initial) }, options...)
}

func appendPoint(t *testing.T, tx durable.Tx, conversationId durable.ConversationId, kind string) durable.EntryId {
	t.Helper()
	entry, err := tx.AppendEntry(conversationId, durable.EntryDraft{Kind: kind})
	must(t, err)
	return entry.Id
}

func fork(t *testing.T, harness sessiontest.Harness, parentId durable.ConversationId, at durable.EntryId) durable.ConversationRecord {
	t.Helper()
	child, err := tryFork(harness, parentId, at)
	must(t, err)
	return child
}

func tryFork(harness sessiontest.Harness, parentId durable.ConversationId, at durable.EntryId) (durable.ConversationRecord, error) {
	return durable.Commit(ctx, harness.Session, func(tx durable.Tx) (durable.ConversationRecord, error) {
		return tx.ForkConversation(parentId, at, ownerless())
	})
}

func setValue(t *testing.T, tx durable.Tx, token durable.AnyDocToken, value any, args ...any) {
	t.Helper()
	must(t, mustDoc(t, tx, token, args...).Set("value", value))
}

func findDocument(t *testing.T, harness sessiontest.Harness, kind string, conversationId durable.ConversationId) *durable.DocumentRecord {
	t.Helper()
	record, err := harness.Storage.FindDocument(ctx, durable.DocumentAddress{Kind: kind, Scope: durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: conversationId}}, durable.CurrentPoint)
	must(t, err)
	return record
}

// Pi source: packages/durable/src/storage/memory.ts
// mutation-checked: zeroing the results of MemoryStorage.Conversation fails it
func TestSessionConversationDocumentForks(t *testing.T) {
	t.Run("copies as-of and current singleton and family bases while leaving initial documents absent", func(t *testing.T) {
		asOf := valueDoc("fork.policies.as-of", rewindableScope(durable.ForkAsOf), "as-initial")
		current := valueDoc("fork.policies.current", latestScope(durable.ForkCurrent), "current-initial")
		initial := valueDoc("fork.policies.initial", latestScope(durable.ForkInitial), "fresh")
		seeded := func(seed string) obj { return delta.JsonObjectOf("value", seed) }
		asOfFamily := defineFamily("fork.policies.as-of-family", 1, rewindableScope(durable.ForkAsOf), seeded)
		currentFamily := defineFamily("fork.policies.current-family", 1, latestScope(durable.ForkCurrent), seeded)
		harness := open()
		parentId := createConversation(t, harness)
		var forkAt durable.EntryId
		commit(t, harness.Session, func(tx durable.Tx) error {
			forkAt = appendPoint(t, tx, parentId, "fork-point")
			setValue(t, tx, asOf, "as-at-fork", parentId)
			setValue(t, tx, current, "current-at-fork", parentId)
			setValue(t, tx, initial, "parent-only", parentId)
			setValue(t, tx, asOfFamily, "family-as-a", parentId, "a", "unused")
			setValue(t, tx, asOfFamily, "family-as-b", parentId, "b", "unused")
			setValue(t, tx, currentFamily, "family-current-a", parentId, "a", "unused")
			return nil
		})
		commit(t, harness.Session, func(tx durable.Tx) error {
			setValue(t, tx, asOf, "as-after-fork", parentId)
			setValue(t, tx, current, "current-when-copied", parentId)
			setValue(t, tx, asOfFamily, "family-as-after", parentId, "a", "unused")
			setValue(t, tx, currentFamily, "family-current-when-copied", parentId, "a", "unused")
			return nil
		})
		flush(harness)
		documentReads := harness.Storage.DocumentReadCount()
		child := fork(t, harness, parentId, forkAt)
		flush(harness)
		if harness.Storage.DocumentReadCount() != documentReads {
			t.Fatal("a fork copies without reading document content")
		}
		expectEqual(t, snapshot(t, harness.Session, asOf, child.Id), delta.JsonObjectOf("value", "as-at-fork"))
		expectEqual(t, snapshot(t, harness.Session, current, child.Id), delta.JsonObjectOf("value", "current-when-copied"))
		if snapshot(t, harness.Session, initial, child.Id) != nil {
			t.Fatal("initial-policy documents stay absent")
		}
		expectEqual(t, snapshot(t, harness.Session, asOfFamily, child.Id, "a"), delta.JsonObjectOf("value", "family-as-a"))
		expectEqual(t, snapshot(t, harness.Session, asOfFamily, child.Id, "b"), delta.JsonObjectOf("value", "family-as-b"))
		expectEqual(t, snapshot(t, harness.Session, currentFamily, child.Id, "a"), delta.JsonObjectOf("value", "family-current-when-copied"))

		copies := writesOfType(lastAdmitted(harness), "document.copy")
		if len(copies) != 5 {
			t.Fatalf("copies %d", len(copies))
		}
		for _, write := range copies {
			scope := write.(durable.DocumentCopyWrite).Record.Scope
			if scope.Kind != durable.ScopeConversation || scope.ConversationId != child.Id {
				t.Fatal("copies belong to the child")
			}
		}
		publication := harness.Publications.Last()
		copied := sessiontest.DocumentCopyChanges(publication)
		if len(copied) != 5 {
			t.Fatalf("copy changes %d", len(copied))
		}
		for _, change := range copied {
			if change.Record.CreatedAt != publication.Seq || change.ConversationId != child.Id {
				t.Fatal("copy publication stamps")
			}
			for _, write := range copies {
				if copyWrite := write.(durable.DocumentCopyWrite); copyWrite.Record.Id == change.Record.Id {
					expectEqual(t, change.Source, copyWrite.Source)
				}
			}
		}
		if findDocument(t, harness, "fork.policies.as-of", child.Id).Id == findDocument(t, harness, "fork.policies.as-of", parentId).Id {
			t.Fatal("the child has its own incarnation")
		}
		commit(t, harness.Session, func(tx durable.Tx) error {
			setValue(t, tx, asOf, "child-independent", child.Id)
			return nil
		})
		expectEqual(t, snapshot(t, harness.Session, asOf, parentId), delta.JsonObjectOf("value", "as-after-fork"))
		expectEqual(t, snapshot(t, harness.Session, asOf, child.Id), delta.JsonObjectOf("value", "child-independent"))
		commit(t, harness.Session, func(tx durable.Tx) error {
			setValue(t, tx, initial, "child-created", child.Id)
			return nil
		})
		expectEqual(t, snapshot(t, harness.Session, initial, child.Id), delta.JsonObjectOf("value", "child-created"))
	})

	t.Run("uses final document state from the fork entry commit while excluding later same-commit entries", func(t *testing.T) {
		document := valueDoc("fork.same-commit", rewindableScope(durable.ForkAsOf), "initial")
		harness := open()
		parentId := createConversation(t, harness)
		var forkAt, excluded durable.EntryId
		commit(t, harness.Session, func(tx durable.Tx) error {
			forkAt = appendPoint(t, tx, parentId, "included")
			setValue(t, tx, document, "final-state-of-commit", parentId)
			excluded = appendPoint(t, tx, parentId, "excluded")
			return nil
		})
		child := fork(t, harness, parentId, forkAt)
		expectEqual(t, snapshot(t, harness.Session, document, child.Id), delta.JsonObjectOf("value", "final-state-of-commit"))
		visible, err := durable.Commit(ctx, harness.Session, func(tx durable.Tx) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
			return tx.ScanEntries(durable.EntryQuery{ConversationId: child.Id}, 10, nil)
		})
		must(t, err)
		ids := map[durable.EntryId]bool{}
		for _, entry := range visible.Items {
			ids[entry.Id] = true
		}
		if !ids[forkAt] || ids[excluded] {
			t.Fatal("the fork includes its entry and excludes later same-commit entries")
		}
	})

	t.Run("selects the entry-owning ancestor for as-of copies and the immediate parent for current copies", func(t *testing.T) {
		asOf := valueDoc("fork.ancestry.as-of", rewindableScope(durable.ForkAsOf), "initial")
		current := valueDoc("fork.ancestry.current", latestScope(durable.ForkCurrent), "initial")
		harness := open()
		rootId := createConversation(t, harness)
		var inherited durable.EntryId
		commit(t, harness.Session, func(tx durable.Tx) error {
			inherited = appendPoint(t, tx, rootId, "root")
			setValue(t, tx, asOf, "root-at-entry", rootId)
			setValue(t, tx, current, "root-current", rootId)
			return nil
		})
		parent := fork(t, harness, rootId, inherited)
		var parentEntry durable.EntryId
		commit(t, harness.Session, func(tx durable.Tx) error {
			parentEntry = appendPoint(t, tx, parent.Id, "parent")
			setValue(t, tx, asOf, "parent-at-own-entry", parent.Id)
			setValue(t, tx, current, "parent-current", parent.Id)
			return nil
		})
		inheritedFork := fork(t, harness, parent.Id, inherited)
		expectEqual(t, snapshot(t, harness.Session, asOf, inheritedFork.Id), delta.JsonObjectOf("value", "root-at-entry"))
		expectEqual(t, snapshot(t, harness.Session, current, inheritedFork.Id), delta.JsonObjectOf("value", "parent-current"))
		ownEntryFork := fork(t, harness, parent.Id, parentEntry)
		expectEqual(t, snapshot(t, harness.Session, asOf, ownEntryFork.Id), delta.JsonObjectOf("value", "parent-at-own-entry"))
		expectEqual(t, snapshot(t, harness.Session, current, ownEntryFork.Id), delta.JsonObjectOf("value", "parent-current"))
	})

	t.Run("copies the stored value and version without consulting migration definitions or migrated caches", func(t *testing.T) {
		v1 := defineDoc("fork.stored-version", 1, rewindableScope(durable.ForkAsOf), func() obj { return delta.JsonObjectOf("count", 1) })
		var mu sync.Mutex
		migrations := 0
		v3 := defineDoc("fork.stored-version", 3, rewindableScope(durable.ForkAsOf), func() obj { return delta.JsonObjectOf("count", 0, "migrated", false) },
			withMigrate(func(value obj, fromVersion int) obj {
				if fromVersion != 1 {
					t.Errorf("fromVersion %d", fromVersion)
				}
				mu.Lock()
				migrations++
				mu.Unlock()
				return delta.JsonObjectOf("count", value.Value("count"), "migrated", true)
			}))
		harness := open()
		parentId := createConversation(t, harness)
		var forkAt durable.EntryId
		commit(t, harness.Session, func(tx durable.Tx) error {
			forkAt = appendPoint(t, tx, parentId, "point")
			_, err := tx.Doc(v1, parentId)
			return err
		})
		must(t, harness.Session.UnloadDocuments())
		expectEqual(t, snapshot(t, harness.Session, v3, parentId), delta.JsonObjectOf("count", 1, "migrated", true))
		if migrations != 1 {
			t.Fatal("one migration")
		}
		child := fork(t, harness, parentId, forkAt)
		if migrations != 1 {
			t.Fatal("forking does not migrate")
		}
		record := findDocument(t, harness, "fork.stored-version", child.Id)
		stored, err := harness.Storage.Document(ctx, record.Id, durable.CurrentPoint)
		must(t, err)
		if stored.Version != 1 {
			t.Fatalf("stored version %d", stored.Version)
		}
		expectEqual(t, stored.Value, delta.JsonObjectOf("count", 1))
		expectEqual(t, snapshot(t, harness.Session, v3, child.Id), delta.JsonObjectOf("count", 1, "migrated", true))
		if migrations != 2 {
			t.Fatal("the copy migrates on its own load")
		}
	})

	t.Run("coalesces a typed migration and override into the copied creation base", func(t *testing.T) {
		v1 := defineDoc("fork.override", 1, rewindableScope(durable.ForkAsOf), func() obj { return delta.JsonObjectOf("count", 2) })
		checkpoints := 0
		v2 := defineDoc("fork.override", 2, rewindableScope(durable.ForkAsOf), func() obj { return delta.JsonObjectOf("count", 0, "migrated", false) },
			withMigrate(func(value obj, _ int) obj { return delta.JsonObjectOf("count", value.Value("count"), "migrated", true) }),
			withCheckpoint(func(obj, []durable.Op, durable.CheckpointInfo) bool { checkpoints++; return false }))
		harness := open()
		parentId := createConversation(t, harness)
		var forkAt durable.EntryId
		commit(t, harness.Session, func(tx durable.Tx) error {
			forkAt = appendPoint(t, tx, parentId, "point")
			_, err := tx.Doc(v1, parentId)
			return err
		})
		documentReads := harness.Storage.DocumentReadCount()
		var child durable.ConversationRecord
		commit(t, harness.Session, func(tx durable.Tx) error {
			var err error
			child, err = tx.ForkConversation(parentId, forkAt, ownerless())
			must(t, err)
			return mustDoc(t, tx, v2, child.Id).Set("count", 9)
		})
		flush(harness)
		if harness.Storage.DocumentReadCount() != documentReads+1 {
			t.Fatal("typed access reads the copy source once")
		}
		creates := writesOfType(lastAdmitted(harness), "document.create")
		if len(creates) != 1 {
			t.Fatalf("creates %d", len(creates))
		}
		content := creates[0].(durable.DocumentCreateWrite).Content
		if content.Kind != durable.ContentBase || content.Version != 2 {
			t.Fatalf("content %+v", content)
		}
		expectEqual(t, content.Value, delta.JsonObjectOf("count", 9, "migrated", true))
		if checkpoints != 0 {
			t.Fatal("a creation base consults no checkpoint predicate")
		}
		documents := sessiontest.DocumentChanges(harness.Publications.Last())
		if len(documents) != 1 || *documents[0].Version != 2 {
			t.Fatal("one version-2 creation publication")
		}
		expectEqual(t, snapshot(t, harness.Session, v2, child.Id), delta.JsonObjectOf("count", 9, "migrated", true))
		parentRecord := findDocument(t, harness, "fork.override", parentId)
		parentStored, err := harness.Storage.Document(ctx, parentRecord.Id, durable.CurrentPoint)
		must(t, err)
		if parentStored.Version != 1 {
			t.Fatal("the parent keeps its stored version")
		}
	})

	t.Run("copies the incarnation alive at the fork point across retirement and recreation", func(t *testing.T) {
		document := valueDoc("fork.incarnations", rewindableScope(durable.ForkAsOf), "initial")
		harness := open()
		parentId := createConversation(t, harness)
		var oldAt, retiredAt, newAt durable.EntryId
		commit(t, harness.Session, func(tx durable.Tx) error {
			oldAt = appendPoint(t, tx, parentId, "old")
			setValue(t, tx, document, "old", parentId)
			return nil
		})
		commit(t, harness.Session, func(tx durable.Tx) error {
			retiredAt = appendPoint(t, tx, parentId, "retired")
			return tx.RetireDoc(document, parentId)
		})
		commit(t, harness.Session, func(tx durable.Tx) error {
			newAt = appendPoint(t, tx, parentId, "new")
			setValue(t, tx, document, "new", parentId)
			return nil
		})
		oldChild := fork(t, harness, parentId, oldAt)
		emptyChild := fork(t, harness, parentId, retiredAt)
		newChild := fork(t, harness, parentId, newAt)
		expectEqual(t, snapshot(t, harness.Session, document, oldChild.Id), delta.JsonObjectOf("value", "old"))
		if snapshot(t, harness.Session, document, emptyChild.Id) != nil {
			t.Fatal("no incarnation was alive at the retirement entry")
		}
		expectEqual(t, snapshot(t, harness.Session, document, newChild.Id), delta.JsonObjectOf("value", "new"))
	})

	t.Run("rejects invisible fork points before admission and remains usable", func(t *testing.T) {
		harness := open()
		rootId := createConversation(t, harness)
		var visible, hidden durable.EntryId
		commit(t, harness.Session, func(tx durable.Tx) error {
			visible = appendPoint(t, tx, rootId, "visible")
			hidden = appendPoint(t, tx, rootId, "hidden")
			return nil
		})
		parent := fork(t, harness, rootId, visible)
		flush(harness)
		commits := len(harness.Storage.Commits())
		published := harness.Publications.Len()
		_, err := tryFork(harness, parent.Id, hidden)
		expectErrorContains(t, err, fmt.Sprintf("Entry %d is not visible", hidden))
		flush(harness)
		if len(harness.Storage.Commits()) != commits || harness.Publications.Len() != published {
			t.Fatal("nothing is admitted or published")
		}
		independent, err := durable.Commit(ctx, harness.Session, func(tx durable.Tx) (durable.ConversationRecord, error) {
			return tx.CreateConversation(ownerless())
		})
		must(t, err)
		stored, err := harness.Storage.Conversation(ctx, independent.Id)
		must(t, err)
		expectEqual(t, *stored, independent)
	})

	t.Run("rejects duplicate as-of and current selections for one child address before admission", func(t *testing.T) {
		asOf := valueDoc("fork.duplicate-policy", rewindableScope(durable.ForkAsOf), "as-of")
		current := valueDoc("fork.duplicate-policy", latestScope(durable.ForkCurrent), "current")
		harness := open()
		parentId := createConversation(t, harness)
		var oldAt durable.EntryId
		commit(t, harness.Session, func(tx durable.Tx) error {
			oldAt = appendPoint(t, tx, parentId, "old")
			_, err := tx.Doc(asOf, parentId)
			return err
		})
		commit(t, harness.Session, func(tx durable.Tx) error { return tx.RetireDoc(asOf, parentId) })
		commit(t, harness.Session, func(tx durable.Tx) error { _, err := tx.Doc(current, parentId); return err })
		commits := len(harness.Storage.Commits())
		_, err := tryFork(harness, parentId, oldAt)
		expectErrorContains(t, err, "Fork selects multiple source documents")
		if len(harness.Storage.Commits()) != commits {
			t.Fatal("nothing is admitted")
		}
		independent, err := durable.Commit(ctx, harness.Session, func(tx durable.Tx) (durable.ConversationRecord, error) {
			return tx.CreateConversation(ownerless())
		})
		must(t, err)
		stored, err := harness.Storage.Conversation(ctx, independent.Id)
		must(t, err)
		expectEqual(t, *stored, independent)
	})

	t.Run("rejects current and as-of source writes in the fork transaction", func(t *testing.T) {
		current := valueDoc("fork.same-transaction-current", latestScope(durable.ForkCurrent), "committed")
		asOf := valueDoc("fork.same-transaction-as-of", rewindableScope(durable.ForkAsOf), "committed")
		harness := open()
		parentId := createConversation(t, harness)
		var forkAt durable.EntryId
		commit(t, harness.Session, func(tx durable.Tx) error {
			forkAt = appendPoint(t, tx, parentId, "point")
			_, err := tx.Doc(current, parentId)
			must(t, err)
			_, err = tx.Doc(asOf, parentId)
			return err
		})
		commits := len(harness.Storage.Commits())
		err := tryCommit(harness.Session, func(tx durable.Tx) error {
			setValue(t, tx, current, "before-fork", parentId)
			_, err := tx.ForkConversation(parentId, forkAt, ownerless())
			return err
		})
		expectErrorContains(t, err, "Cannot change fork source document")
		err = tryCommit(harness.Session, func(tx durable.Tx) error {
			_, err := tx.ForkConversation(parentId, forkAt, ownerless())
			must(t, err)
			setValue(t, tx, current, "after-fork", parentId)
			return nil
		})
		expectErrorContains(t, err, "Cannot change fork source document")
		err = tryCommit(harness.Session, func(tx durable.Tx) error {
			setValue(t, tx, asOf, "as-of-write", parentId)
			_, err := tx.ForkConversation(parentId, forkAt, ownerless())
			return err
		})
		expectErrorContains(t, err, "Cannot change fork source document")
		if len(harness.Storage.Commits()) != commits {
			t.Fatal("nothing is admitted")
		}
		expectEqual(t, snapshot(t, harness.Session, current, parentId), delta.JsonObjectOf("value", "committed"))
		child := fork(t, harness, parentId, forkAt)
		expectEqual(t, snapshot(t, harness.Session, current, child.Id), delta.JsonObjectOf("value", "committed"))
	})

	t.Run("rolls every copied base back when later pre-admission assembly fails", func(t *testing.T) {
		copied := valueDoc("fork.rollback.copied", rewindableScope(durable.ForkAsOf), "copied")
		failCheckpoint := true
		failure := defineDoc("fork.rollback.failure", 1, sessionScope, func() obj { return delta.JsonObjectOf("count", 0) },
			withCheckpoint(func(obj, []durable.Op, durable.CheckpointInfo) bool {
				if failCheckpoint {
					panic(errors.New("checkpoint failed"))
				}
				return false
			}))
		harness := open()
		parentId := createConversation(t, harness)
		var forkAt durable.EntryId
		commit(t, harness.Session, func(tx durable.Tx) error {
			forkAt = appendPoint(t, tx, parentId, "point")
			_, err := tx.Doc(copied, parentId)
			must(t, err)
			_, err = tx.Doc(failure)
			return err
		})
		flush(harness)
		commits := len(harness.Storage.Commits())
		published := harness.Publications.Len()
		var childId durable.ConversationId
		err := tryCommit(harness.Session, func(tx durable.Tx) error {
			child, err := tx.ForkConversation(parentId, forkAt, ownerless())
			must(t, err)
			childId = child.Id
			return mustDoc(t, tx, failure).Set("count", 1)
		})
		expectErrorContains(t, err, "checkpoint failed")
		flush(harness)
		if len(harness.Storage.Commits()) != commits || harness.Publications.Len() != published {
			t.Fatal("nothing is admitted or published")
		}
		if conversation, _ := harness.Storage.Conversation(ctx, childId); conversation != nil {
			t.Fatal("the child is not persisted")
		}
		failCheckpoint = false
		commit(t, harness.Session, func(tx durable.Tx) error { return mustDoc(t, tx, failure).Set("count", 2) })
		expectEqual(t, snapshot(t, harness.Session, failure), delta.JsonObjectOf("count", 2))
		expectEqual(t, snapshot(t, harness.Session, copied, parentId), delta.JsonObjectOf("value", "copied"))
	})

	t.Run("rolls back a guaranteed Storage rejection without poisoning the Session", func(t *testing.T) {
		document := valueDoc("fork.storage-rejected", rewindableScope(durable.ForkAsOf), "source")
		harness := open()
		parentId := createConversation(t, harness)
		var forkAt durable.EntryId
		commit(t, harness.Session, func(tx durable.Tx) error {
			forkAt = appendPoint(t, tx, parentId, "point")
			_, err := tx.Doc(document, parentId)
			return err
		})
		harness.Storage.FailNextCommit(durable.NewStorageRejected("copy rejected", nil))
		var rejectedChildId durable.ConversationId
		err := tryCommit(harness.Session, func(tx durable.Tx) error {
			child, err := tx.ForkConversation(parentId, forkAt, ownerless())
			rejectedChildId = child.Id
			return err
		})
		expectErrorContains(t, err, "copy rejected")
		if conversation, _ := harness.Storage.Conversation(ctx, rejectedChildId); conversation != nil {
			t.Fatal("the rejected child is not persisted")
		}
		next, err := durable.Commit(ctx, harness.Session, func(tx durable.Tx) (durable.ConversationRecord, error) {
			return tx.CreateConversation(ownerless())
		})
		must(t, err)
		stored, err := harness.Storage.Conversation(ctx, next.Id)
		must(t, err)
		expectEqual(t, *stored, next)
	})

	t.Run("retires a copied document and can recreate the address in the fork transaction", func(t *testing.T) {
		document := valueDoc("fork.retire-recreate", rewindableScope(durable.ForkAsOf), "fresh")
		harness := open()
		parentId := createConversation(t, harness)
		var forkAt durable.EntryId
		commit(t, harness.Session, func(tx durable.Tx) error {
			forkAt = appendPoint(t, tx, parentId, "point")
			setValue(t, tx, document, "copied", parentId)
			return nil
		})
		var child durable.ConversationRecord
		commit(t, harness.Session, func(tx durable.Tx) error {
			var err error
			child, err = tx.ForkConversation(parentId, forkAt, ownerless())
			must(t, err)
			must(t, tx.RetireDoc(document, child.Id))
			setValue(t, tx, document, "replacement", child.Id)
			return nil
		})
		writes := harness.Storage.LastCommit()
		if len(writesOfType(writes, "document.copy")) != 1 || len(writesOfType(writes, "document.create")) != 1 || len(writesOfType(writes, "document.retire")) != 1 {
			t.Fatal("one copy, one creation, one retirement")
		}
		expectEqual(t, snapshot(t, harness.Session, document, child.Id), delta.JsonObjectOf("value", "replacement"))
	})

	t.Run("copies only conversation documents, leaving Session and task documents in their original scopes", func(t *testing.T) {
		copied := valueDoc("fork.scope.conversation", latestScope(durable.ForkCurrent), "conversation")
		sessionOnly := valueDoc("fork.scope.session", sessionScope, "session")
		taskOnly := valueDoc("fork.scope.task", taskScope, "task")
		work := durable.DefineTask(durable.TaskDefinition[any, obj, any, workHooks]{
			Name:    "fork.scope.work",
			Version: 1,
			Initial: func(any) obj { return delta.JsonObjectOf("phase", "start") },
			Phases: map[string]durable.PhaseHandler[any, obj, any, workHooks]{
				"start": func(ctxT, durable.RunningTask[any, obj, any], durable.TaskRuntime[any, obj, any, workHooks]) error {
					return nil
				},
			},
			Abort: func(ctxT, durable.RunningTask[any, obj, any], durable.TaskRuntime[any, obj, any, workHooks]) error {
				return nil
			},
		})
		harness := open()
		parentId := createConversation(t, harness)
		var forkAt durable.EntryId
		var taskId durable.TaskId
		commit(t, harness.Session, func(tx durable.Tx) error {
			forkAt = appendPoint(t, tx, parentId, "point")
			var err error
			taskId, err = durable.CreateTask(tx, work, nil, conversationOwned(parentId))
			must(t, err)
			_, err = tx.Doc(copied, parentId)
			must(t, err)
			_, err = tx.Doc(sessionOnly)
			must(t, err)
			_, err = tx.Doc(taskOnly, taskId)
			return err
		})
		child := fork(t, harness, parentId, forkAt)
		flush(harness)
		copies := writesOfType(harness.Storage.LastCommit(), "document.copy")
		if len(copies) != 1 || copies[0].(durable.DocumentCopyWrite).Record.Kind != "fork.scope.conversation" {
			t.Fatal("only the conversation document is copied")
		}
		changes := sessiontest.DocumentCopyChanges(harness.Publications.Last())
		if len(changes) != 1 || changes[0].Record.Kind != "fork.scope.conversation" {
			t.Fatal("only the conversation copy is published")
		}
		expectEqual(t, snapshot(t, harness.Session, copied, child.Id), delta.JsonObjectOf("value", "conversation"))
		expectEqual(t, snapshot(t, harness.Session, sessionOnly), delta.JsonObjectOf("value", "session"))
		expectEqual(t, snapshot(t, harness.Session, taskOnly, taskId), delta.JsonObjectOf("value", "task"))
	})

	t.Run("copies every family member across storage scan pages", func(t *testing.T) {
		family := defineFamily("fork.pagination", 1, latestScope(durable.ForkCurrent), func(seed float64) obj { return delta.JsonObjectOf("value", seed) })
		harness := open()
		parentId := createConversation(t, harness)
		forkAt, err := durable.Commit(ctx, harness.Session, func(tx durable.Tx) (durable.EntryId, error) {
			return appendPoint(t, tx, parentId, "point"), nil
		})
		must(t, err)
		commit(t, harness.Session, func(tx durable.Tx) error {
			var group sync.WaitGroup
			errs := make([]error, 260)
			for index := range 260 {
				group.Go(func() {
					_, errs[index] = tx.Doc(family, parentId, fmt.Sprintf("member-%d", index), index)
				})
			}
			group.Wait()
			return errors.Join(errs...)
		})
		child := fork(t, harness, parentId, forkAt)
		if copies := writesOfType(harness.Storage.LastCommit(), "document.copy"); len(copies) != 260 {
			t.Fatalf("copies %d", len(copies))
		}
		expectEqual(t, snapshot(t, harness.Session, family, child.Id, "member-0"), delta.JsonObjectOf("value", 0))
		expectEqual(t, snapshot(t, harness.Session, family, child.Id, "member-259"), delta.JsonObjectOf("value", 259))
	})
}
