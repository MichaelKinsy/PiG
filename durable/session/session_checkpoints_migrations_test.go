package session_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
)

// Ports packages/durable/test/session-checkpoints-migrations.test.ts

func documentWrites(writes []durable.StorageWrite) []durable.StorageWrite {
	var matched []durable.StorageWrite
	for _, write := range writes {
		switch write.(type) {
		case durable.DocumentCreateWrite, durable.DocumentChangeWrite, durable.DocumentRetireWrite:
			matched = append(matched, write)
		}
	}
	return matched
}

func changeKind(write durable.StorageWrite) string {
	if change, ok := write.(durable.DocumentChangeWrite); ok {
		return string(change.Content.Kind)
	}
	return durable.StorageWriteType(write)
}

func increment(t *testing.T, harness sessiontest.Harness, token durable.AnyDocToken, key string, args ...any) {
	t.Helper()
	commit(t, harness.Session, func(tx durable.Tx) error {
		draft := mustDoc(t, tx, token, args...)
		return draft.Set(key, num(draft.Get(key))+1)
	})
}

func touch(t *testing.T, harness sessiontest.Harness, token durable.AnyDocToken, args ...any) {
	t.Helper()
	commit(t, harness.Session, func(tx durable.Tx) error { _, err := tx.Doc(token, args...); return err })
}

func TestSessionDocumentCheckpoints(t *testing.T) {
	t.Run("calls checkpointWhen with its definition as the receiver", func(t *testing.T) {
		// Upstream reads this.initial(); a Go predicate reaches its definition's initial value by closure.
		initial := func() obj { return obj{"count": 0} }
		document := defineDoc("checkpoint.receiver", 1, sessionScope, initial,
			withCheckpoint(func(value obj, _ []durable.Op, _ durable.CheckpointInfo) bool {
				return num(value["count"]) == num(initial()["count"])+2
			}))
		harness := open()
		for count := 0; count <= 2; count++ {
			commit(t, harness.Session, func(tx durable.Tx) error { return mustDoc(t, tx, document).Set("count", count) })
		}
		var kinds []string
		for _, writes := range harness.Storage.Commits() {
			for _, write := range writes {
				if change, ok := write.(durable.DocumentChangeWrite); ok {
					kinds = append(kinds, string(change.Content.Kind))
				}
			}
		}
		expectEqual(t, kinds, []string{"delta", "base"})
	})

	t.Run("selects bases only for nonempty ordinary batches and passes the exact prepared revision and ops", func(t *testing.T) {
		type call struct {
			value obj
			ops   []durable.Op
		}
		var calls, falseCalls []call
		baseDoc := defineDoc("checkpoint.base", 1, sessionScope, func() obj { return obj{"items": []any{}} },
			withCheckpoint(func(value obj, ops []durable.Op, _ durable.CheckpointInfo) bool {
				calls = append(calls, call{value, ops})
				return true
			}))
		deltaDoc := defineDoc("checkpoint.delta", 1, sessionScope, func() obj { return obj{"count": 0} },
			withCheckpoint(func(value obj, ops []durable.Op, _ durable.CheckpointInfo) bool {
				falseCalls = append(falseCalls, call{value, ops})
				return false
			}))
		defaultDoc := defineDoc("checkpoint.default", 1, sessionScope, func() obj { return obj{"count": 0} })
		harness := open()
		commit(t, harness.Session, func(tx durable.Tx) error {
			for _, token := range []durable.AnyDocToken{baseDoc, deltaDoc, defaultDoc} {
				if _, err := tx.Doc(token); err != nil {
					return err
				}
			}
			return nil
		})
		if len(calls) != 0 || len(falseCalls) != 0 {
			t.Fatal("creation consults no predicate")
		}
		for _, write := range documentWrites(harness.Storage.LastCommit()) {
			if create, ok := write.(durable.DocumentCreateWrite); !ok || create.Content.Kind != durable.ContentBase {
				t.Fatal("creations are bases")
			}
		}
		commit(t, harness.Session, func(tx durable.Tx) error {
			_, err := mustDoc(t, tx, baseDoc).Array("items").Push("x")
			must(t, err)
			for _, token := range []durable.AnyDocToken{deltaDoc, defaultDoc} {
				draft := mustDoc(t, tx, token)
				must(t, draft.Set("count", num(draft.Get("count"))+1))
			}
			return nil
		})
		flush(harness)
		writes := writesOfType(lastAdmitted(harness), "document.change")
		var kinds []string
		for _, write := range writes {
			kinds = append(kinds, changeKind(write))
		}
		expectEqual(t, kinds, []string{"base", "delta", "delta"})
		if len(calls) != 1 || len(falseCalls) != 1 {
			t.Fatal("one predicate call per changed document")
		}
		value := snapshot(t, harness.Session, baseDoc)
		deltaValue := snapshot(t, harness.Session, deltaDoc)
		documents := sessiontest.DocumentChanges(harness.Publications.Last())
		published, publishedDelta := documents[0], documents[1]
		if !same(calls[0].value, value) || !same(calls[0].ops, published.Ops) {
			t.Fatal("the base predicate sees the exact prepared revision and ops")
		}
		if !same(falseCalls[0].value, deltaValue) || !same(falseCalls[0].ops, publishedDelta.Ops) {
			t.Fatal("the delta predicate sees the exact prepared revision and ops")
		}
		if !same(writes[1].(durable.DocumentChangeWrite).Content.Ops, falseCalls[0].ops) {
			t.Fatal("the delta write is the predicate's ops")
		}
		base := writes[0].(durable.DocumentChangeWrite)
		if base.Content.Kind != durable.ContentBase || !same(base.Content.Value, value) {
			t.Fatal("the checkpoint base is the revision")
		}
	})

	t.Run("passes the stored delta count since the newest base, including after unload and version bases", func(t *testing.T) {
		var seen []any
		v1 := defineDoc("checkpoint.deltas-since-base", 1, sessionScope, func() obj { return obj{"count": 0} },
			withCheckpoint(func(_ obj, _ []durable.Op, info durable.CheckpointInfo) bool {
				seen = append(seen, info.DeltasSinceBase)
				return info.DeltasSinceBase >= 2
			}))
		v2 := defineDoc("checkpoint.deltas-since-base", 2, sessionScope, func() obj { return obj{"count": 0} },
			withMigrate(func(value obj, _ int) obj { return obj{"count": value["count"]} }),
			withCheckpoint(func(_ obj, _ []durable.Op, info durable.CheckpointInfo) bool {
				seen = append(seen, info.DeltasSinceBase)
				return false
			}))
		harness := open()
		touch(t, harness, v1)
		increment(t, harness, v1, "count")
		increment(t, harness, v1, "count")
		increment(t, harness, v1, "count")
		must(t, harness.Session.UnloadDocuments())
		increment(t, harness, v1, "count")
		expectEqual(t, seen, []any{0, 1, 2, 0})
		commits := harness.Storage.Commits()
		var kinds []string
		for _, writes := range commits[len(commits)-4:] {
			kinds = append(kinds, changeKind(writes[0]))
		}
		expectEqual(t, kinds, []string{"delta", "delta", "base", "delta"})
		// A required version base resets the count without calling the predicate.
		increment(t, harness, v2, "count")
		increment(t, harness, v2, "count")
		expectEqual(t, seen, []any{0, 1, 2, 0, 0})
		commits = harness.Storage.Commits()
		versionBase := commits[len(commits)-2][0].(durable.DocumentChangeWrite)
		if versionBase.Content.Kind != durable.ContentBase || versionBase.Content.Version != 2 {
			t.Fatal("the version change writes a base")
		}
	})

	t.Run("skips the predicate for empty batches but calls it for nonempty structural no-ops", func(t *testing.T) {
		calls := 0
		document := defineDoc("checkpoint.no-op", 1, sessionScope, func() obj { return obj{"items": []any{"a", "b"}} },
			withCheckpoint(func(obj, []durable.Op, durable.CheckpointInfo) bool { calls++; return false }))
		harness := open()
		touch(t, harness, document)
		commits := len(harness.Storage.Commits())
		commit(t, harness.Session, func(tx durable.Tx) error {
			items := mustDoc(t, tx, document).Array("items")
			_, err := items.Push("x")
			items.Pop()
			return err
		})
		if calls != 0 || len(harness.Storage.Commits()) != commits {
			t.Fatal("an empty batch skips the predicate and writes nothing")
		}
		commit(t, harness.Session, func(tx durable.Tx) error {
			items := mustDoc(t, tx, document).Array("items")
			_, err := items.Unshift(items.Shift())
			return err
		})
		if calls != 1 || changeKind(harness.Storage.LastCommit()[0]) != "delta" {
			t.Fatal("a structural no-op consults the predicate and writes a delta")
		}
	})

	t.Run("rolls back every prepared document when a checkpoint predicate throws", func(t *testing.T) {
		throwCheckpoint := true
		firstCalls := 0
		first := defineDoc("checkpoint.rollback.first", 1, sessionScope, func() obj { return obj{"count": 0} },
			withCheckpoint(func(obj, []durable.Op, durable.CheckpointInfo) bool { firstCalls++; return false }))
		second := defineDoc("checkpoint.rollback.second", 1, sessionScope, func() obj { return obj{"count": 0} },
			withCheckpoint(func(obj, []durable.Op, durable.CheckpointInfo) bool {
				if throwCheckpoint {
					panic(errors.New("checkpoint failed"))
				}
				return false
			}))
		harness := open()
		commit(t, harness.Session, func(tx durable.Tx) error {
			_, err := tx.Doc(first)
			must(t, err)
			_, err = tx.Doc(second)
			return err
		})
		flush(harness)
		firstValue, secondValue := snapshot(t, harness.Session, first), snapshot(t, harness.Session, second)
		commits := len(harness.Storage.Commits())
		published := harness.Publications.Len()
		err := tryCommit(harness.Session, func(tx durable.Tx) error {
			must(t, mustDoc(t, tx, first).Set("count", 1))
			return mustDoc(t, tx, second).Set("count", 2)
		})
		expectErrorContains(t, err, "checkpoint failed")
		flush(harness)
		if firstCalls != 1 || len(harness.Storage.Commits()) != commits || harness.Publications.Len() != published {
			t.Fatal("nothing is admitted or published")
		}
		if !same(snapshot(t, harness.Session, first), firstValue) || !same(snapshot(t, harness.Session, second), secondValue) {
			t.Fatal("every prepared document rolls back")
		}
		throwCheckpoint = false
		commit(t, harness.Session, func(tx durable.Tx) error {
			must(t, mustDoc(t, tx, first).Set("count", 3))
			return mustDoc(t, tx, second).Set("count", 4)
		})
		expectEqual(t, snapshot(t, harness.Session, first), obj{"count": 3})
		expectEqual(t, snapshot(t, harness.Session, second), obj{"count": 4})
	})

	t.Run("persists repeated false decisions as deltas and replays the complete tail", func(t *testing.T) {
		calls := 0
		document := defineDoc("checkpoint.tail", 1, sessionScope, func() obj { return obj{"values": []any{}} },
			withCheckpoint(func(obj, []durable.Op, durable.CheckpointInfo) bool { calls++; return false }))
		harness := open()
		touch(t, harness, document)
		for value := 1; value <= 8; value++ {
			commit(t, harness.Session, func(tx durable.Tx) error {
				_, err := mustDoc(t, tx, document).Array("values").Push(value)
				return err
			})
		}
		var changes []durable.StorageWrite
		for _, writes := range harness.Storage.Commits() {
			changes = append(changes, writesOfType(writes, "document.change")...)
		}
		if len(changes) != 8 || calls != 8 {
			t.Fatalf("changes %d calls %d", len(changes), calls)
		}
		for _, change := range changes {
			if changeKind(change) != "delta" {
				t.Fatal("every change is a delta")
			}
		}
		must(t, harness.Session.UnloadDocuments())
		expectEqual(t, snapshot(t, harness.Session, document), obj{"values": []any{1, 2, 3, 4, 5, 6, 7, 8}})
	})

	t.Run("keeps a prepared root replacement as a delta when the predicate is false", func(t *testing.T) {
		initial := obj{}
		for index := range 4_100 {
			initial[fmt.Sprintf("field%d", index)] = 0
		}
		document := defineDoc("checkpoint.root-replacement", 1, sessionScope, func() obj { return initial },
			withCheckpoint(func(obj, []durable.Op, durable.CheckpointInfo) bool { return false }))
		harness := open()
		touch(t, harness, document)
		commit(t, harness.Session, func(tx durable.Tx) error {
			value := mustDoc(t, tx, document)
			for index := range 4_100 {
				if err := value.Set(fmt.Sprintf("field%d", index), 1); err != nil {
					return err
				}
			}
			return nil
		})
		change, ok := harness.Storage.LastCommit()[0].(durable.DocumentChangeWrite)
		if !ok || change.Content.Kind != durable.ContentDelta || len(change.Content.Ops) != 1 || change.Content.Ops[0][0] != "r" {
			t.Fatal("a root replacement delta")
		}
		must(t, harness.Session.UnloadDocuments())
		expectEqual(t, snapshot(t, harness.Session, document)["field4099"], 1)
	})

	t.Run("uses ordinary checkpoint selection before retirement", func(t *testing.T) {
		calls := 0
		document := defineDoc("checkpoint.retire", 1, sessionScope, func() obj { return obj{"count": 0} },
			withCheckpoint(func(obj, []durable.Op, durable.CheckpointInfo) bool { calls++; return true }))
		harness := open()
		touch(t, harness, document)
		commit(t, harness.Session, func(tx durable.Tx) error {
			must(t, mustDoc(t, tx, document).Set("count", 1))
			return tx.RetireDoc(document)
		})
		writes := documentWrites(harness.Storage.LastCommit())
		if calls != 1 || len(writes) != 2 || changeKind(writes[0]) != "base" || changeKind(writes[1]) != "document.retire" {
			t.Fatal("a base change, then retirement")
		}
	})
}

func TestSessionDocumentMigrations(t *testing.T) {
	t.Run("migrates read-only once per cold load, copies the callback result, and writes nothing", func(t *testing.T) {
		old := defineDoc("migration.read-only", 1, sessionScope, func() obj { return obj{"count": 2} })
		calls := 0
		var retained obj
		current := defineDoc("migration.read-only", 3, sessionScope, func() obj { return obj{"count": 0, "labels": []any{}} },
			withMigrate(func(value obj, fromVersion int) obj {
				if fromVersion != 1 {
					t.Errorf("fromVersion %d", fromVersion)
				}
				calls++
				retained = obj{"count": value["count"], "labels": []any{"migrated"}}
				return retained
			}))
		harness := open()
		touch(t, harness, old)
		must(t, harness.Session.UnloadDocuments())
		commits := len(harness.Storage.Commits())
		first := snapshot(t, harness.Session, current)
		if !same(snapshot(t, harness.Session, current), first) || calls != 1 || len(harness.Storage.Commits()) != commits {
			t.Fatal("one read-only migration")
		}
		retained["count"] = 99
		retained["labels"] = append(retained["labels"].([]any), "mutated")
		expectEqual(t, first, obj{"count": 2, "labels": []any{"migrated"}})
		must(t, harness.Session.UnloadDocuments())
		second := snapshot(t, harness.Session, current)
		if same(second, first) || !equal(second, first) || calls != 2 || len(harness.Storage.Commits()) != commits {
			t.Fatal("each cold load migrates again without writing")
		}
	})

	t.Run("writes the required base on the first successful transaction, then writes deltas", func(t *testing.T) {
		old := defineDoc("migration.transition", 1, sessionScope, func() obj { return obj{"count": 4} })
		checkpoints := 0
		current := defineDoc("migration.transition", 3, sessionScope, func() obj { return obj{"count": 0} },
			withMigrate(func(value obj, fromVersion int) obj {
				return obj{"count": num(value["count"]) + float64(fromVersion) - 1}
			}),
			withCheckpoint(func(obj, []durable.Op, durable.CheckpointInfo) bool { checkpoints++; return false }))
		harness := open()
		touch(t, harness, old)
		must(t, harness.Session.UnloadDocuments())
		// An observer of the older shape.
		watch := watchDoc(t, harness, ctx, old)
		frames := &watchRecorder{}
		watch.Start(func(_ ctxT, value obj, ops []durable.Op) error { frames.record(value, ops); return nil })
		expectEqual(t, snapshot(t, harness.Session, current), obj{"count": 4})
		value := snapshot(t, harness.Session, current)
		// An observer of the new shape: the migration changes nothing it sees.
		currentWatch := watchDoc(t, harness, ctx, current)
		currentFrames := &watchRecorder{}
		currentWatch.Start(func(_ ctxT, value obj, ops []durable.Op) error { currentFrames.record(value, ops); return nil })
		touch(t, harness, current)
		flush(harness)
		base := harness.Storage.LastCommit()[0].(durable.DocumentChangeWrite)
		if base.Content.Kind != durable.ContentBase || base.Content.Version != 3 {
			t.Fatal("the version base")
		}
		expectEqual(t, base.Content.Value, obj{"count": 4})
		if checkpoints != 0 {
			t.Fatal("a version base consults no predicate")
		}
		// The migration-only base is published, so the older-shape watch receives the new value as a root replacement.
		documents := sessiontest.DocumentChanges(harness.Publications.Last())
		if len(documents) != 1 || *documents[0].Version != 3 || len(documents[0].Ops) != 0 {
			t.Fatal("one migration-only publication")
		}
		expectEqual(t, documents[0].Value, obj{"count": 4})
		received := frames.all()
		if len(received) != 1 {
			t.Fatalf("frames %d", len(received))
		}
		expectEqual(t, received[0].value, obj{"count": 4})
		expectEqual(t, received[0].ops, []any{[]any{"r", obj{"count": 4}}})
		if len(currentFrames.all()) != 0 {
			t.Fatal("the new-shape watch receives nothing")
		}
		_, _ = watch.Stop()
		_, _ = currentWatch.Stop()
		if !same(snapshot(t, harness.Session, current), value) {
			t.Fatal("the version base keeps the revision")
		}
		commit(t, harness.Session, func(tx durable.Tx) error { return mustDoc(t, tx, current).Set("count", 7) })
		next := harness.Storage.LastCommit()[0].(durable.DocumentChangeWrite)
		if next.Content.Kind != durable.ContentDelta || next.Content.Version != 3 || checkpoints != 1 {
			t.Fatal("later edits are deltas")
		}
		must(t, harness.Session.UnloadDocuments())
		expectEqual(t, snapshot(t, harness.Session, current), obj{"count": 7})
	})

	t.Run("rolls migration and edits back with the callback, then coalesces later edits into one base", func(t *testing.T) {
		old := defineDoc("migration.rollback", 1, sessionScope, func() obj { return obj{"count": 1} })
		migrations := 0
		current := defineDoc("migration.rollback", 2, sessionScope, func() obj { return obj{"count": 0, "migrated": false} },
			withMigrate(func(value obj, _ int) obj { migrations++; return obj{"count": value["count"], "migrated": true} }))
		harness := open()
		touch(t, harness, old)
		must(t, harness.Session.UnloadDocuments())
		commits := len(harness.Storage.Commits())
		err := tryCommit(harness.Session, func(tx durable.Tx) error {
			must(t, mustDoc(t, tx, current).Set("count", 8))
			return errors.New("rollback")
		})
		expectErrorContains(t, err, "rollback")
		if len(harness.Storage.Commits()) != commits {
			t.Fatal("nothing is written")
		}
		expectEqual(t, snapshot(t, harness.Session, current), obj{"count": 1, "migrated": true})
		if migrations != 1 {
			t.Fatal("one migration")
		}
		commit(t, harness.Session, func(tx durable.Tx) error {
			value := mustDoc(t, tx, current)
			must(t, value.Set("count", 9))
			return value.Set("migrated", false)
		})
		writes := documentWrites(harness.Storage.LastCommit())
		if len(writes) != 1 {
			t.Fatalf("writes %d", len(writes))
		}
		base := writes[0].(durable.DocumentChangeWrite)
		if base.Content.Kind != durable.ContentBase || base.Content.Version != 2 {
			t.Fatal("one coalesced base")
		}
		expectEqual(t, base.Content.Value, obj{"count": 9, "migrated": false})
		flush(harness)
		published := lastPublishedDocument(harness)
		admitted := lastAdmitted(harness)[0].(durable.DocumentChangeWrite)
		if !same(published.Value, snapshot(t, harness.Session, current)) || !same(published.Value, admitted.Content.Value) || len(published.Ops) == 0 || migrations != 1 {
			t.Fatal("the published base is the revision with its ops")
		}
	})

	t.Run("strict-checks migration results before tracker ownership and remains usable after rejection", func(t *testing.T) {
		old := defineDoc("migration.invalid", 1, sessionScope, func() obj { return obj{"count": 1} })
		// Upstream returns a Date; the Go counterpart is a kind with no JSON form.
		invalid := defineDoc("migration.invalid", 2, sessionScope, func() obj { return obj{} },
			withMigrate(func(obj, int) obj { return obj{"invalid": make(chan int)} }))
		other := defineDoc("migration.invalid.other", 1, sessionScope, func() obj { return obj{"ok": true} })
		harness := open()
		touch(t, harness, old)
		must(t, harness.Session.UnloadDocuments())
		commits := len(harness.Storage.Commits())
		_, err := harness.Session.SnapshotErased(ctx, invalid)
		expectStrictJSON(t, err)
		expectStrictJSON(t, tryCommit(harness.Session, func(tx durable.Tx) error { _, err := tx.Doc(invalid); return err }))
		if len(harness.Storage.Commits()) != commits {
			t.Fatal("nothing is written")
		}
		touch(t, harness, other)
		expectEqual(t, snapshot(t, harness.Session, other), obj{"ok": true})
	})

	t.Run("rejects newer stored versions and older versions without migration for snapshots and transactions", func(t *testing.T) {
		v2 := defineDoc("migration.compatibility", 2, sessionScope, func() obj { return obj{"count": 2} })
		v1 := defineDoc("migration.compatibility", 1, sessionScope, func() obj { return obj{"count": 1} })
		v3 := defineDoc("migration.compatibility", 3, sessionScope, func() obj { return obj{"count": 3} })
		harness := open()
		touch(t, harness, v2)
		must(t, harness.Session.UnloadDocuments())
		commits := len(harness.Storage.Commits())
		_, err := harness.Session.SnapshotErased(ctx, v1)
		expectErrorContains(t, err, "newer version 2 than 1")
		expectErrorContains(t, tryCommit(harness.Session, func(tx durable.Tx) error { _, err := tx.Doc(v1); return err }), "newer version 2 than 1")
		_, err = harness.Session.SnapshotErased(ctx, v3)
		expectErrorContains(t, err, "requires migration from version 2")
		expectErrorContains(t, tryCommit(harness.Session, func(tx durable.Tx) error { _, err := tx.Doc(v3); return err }), "requires migration from version 2")
		if len(harness.Storage.Commits()) != commits {
			t.Fatal("nothing is written")
		}
	})

	t.Run("persists a required migration base before retirement without consulting the checkpoint predicate", func(t *testing.T) {
		old := defineDoc("migration.retire", 1, sessionScope, func() obj { return obj{"count": 1} })
		current := defineDoc("migration.retire", 2, sessionScope, func() obj { return obj{"count": 0} },
			withMigrate(func(value obj, _ int) obj { return obj{"count": value["count"]} }),
			withCheckpoint(func(obj, []durable.Op, durable.CheckpointInfo) bool { panic(errors.New("must not run")) }))
		harness := open()
		touch(t, harness, old)
		must(t, harness.Session.UnloadDocuments())
		commit(t, harness.Session, func(tx durable.Tx) error {
			_, err := tx.Doc(current)
			must(t, err)
			return tx.RetireDoc(current)
		})
		writes := documentWrites(harness.Storage.LastCommit())
		if len(writes) != 2 || changeKind(writes[1]) != "document.retire" {
			t.Fatal("a base, then retirement")
		}
		base := writes[0].(durable.DocumentChangeWrite)
		if base.Content.Kind != durable.ContentBase || base.Content.Version != 2 {
			t.Fatal("the migration base")
		}
		expectEqual(t, base.Content.Value, obj{"count": 1})
	})

	t.Run("leaves unaccessed older documents and unavailable definitions untouched", func(t *testing.T) {
		firstV1 := defineDoc("migration.lazy.first", 1, sessionScope, func() obj { return obj{"count": 1} })
		secondV1 := defineDoc("migration.lazy.second", 1, sessionScope, func() obj { return obj{"count": 2} })
		secondMigrations := 0
		firstV2 := defineDoc("migration.lazy.first", 2, sessionScope, func() obj { return obj{"count": 0} },
			withMigrate(func(value obj, _ int) obj { return obj{"count": value["count"]} }))
		defineDoc("migration.lazy.second", 2, sessionScope, func() obj { return obj{"count": 0} },
			withMigrate(func(value obj, _ int) obj { secondMigrations++; return obj{"count": value["count"]} }))
		harness := open()
		commit(t, harness.Session, func(tx durable.Tx) error {
			_, err := tx.Doc(firstV1)
			must(t, err)
			_, err = tx.Doc(secondV1)
			return err
		})
		must(t, harness.Session.UnloadDocuments())
		commits := len(harness.Storage.Commits())
		expectEqual(t, snapshot(t, harness.Session, firstV2), obj{"count": 1})
		if len(harness.Storage.Commits()) != commits || secondMigrations != 0 {
			t.Fatal("unaccessed documents stay untouched")
		}
		record, err := harness.Storage.FindDocument(ctx, durable.DocumentAddress{Kind: "migration.lazy.second", Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}}, durable.CurrentPoint)
		must(t, err)
		stored, err := harness.Storage.Document(ctx, record.Id, durable.CurrentPoint)
		must(t, err)
		if stored.Version != 1 {
			t.Fatal("the unaccessed document keeps version 1")
		}
	})
}

func TestSessionHistoricalDocumentSnapshots(t *testing.T) {
	t.Run("migrates current and historical rewindable values independently and follows fork ancestry", func(t *testing.T) {
		v1 := defineDoc("history.migration", 1, rewindableScope(durable.ForkAsOf), func() obj { return obj{"count": 0} })
		family := defineFamily("history.family", 1, rewindableScope(durable.ForkAsOf), func(seed string) obj { return obj{"seed": seed, "count": 0} })
		var mu sync.Mutex
		var migrations []any
		v3 := defineDoc("history.migration", 3, rewindableScope(durable.ForkAsOf), func() obj { return obj{"count": 0, "version": 3} },
			withMigrate(func(value obj, fromVersion int) obj {
				mu.Lock()
				migrations = append(migrations, fromVersion)
				mu.Unlock()
				return obj{"count": value["count"], "version": 3}
			}))
		harness := open()
		conversationId := createConversation(t, harness)
		var firstEntry, secondEntry, thirdEntry durable.EntryId
		commit(t, harness.Session, func(tx durable.Tx) error {
			firstEntry = appendPoint(t, tx, conversationId, "first")
			must(t, mustDoc(t, tx, v1, conversationId).Set("count", 1))
			return mustDoc(t, tx, family, conversationId, "member", "seed").Set("count", 1)
		})
		commit(t, harness.Session, func(tx durable.Tx) error {
			secondEntry = appendPoint(t, tx, conversationId, "second")
			return mustDoc(t, tx, v1, conversationId).Set("count", 2)
		})
		must(t, harness.Session.UnloadDocuments())
		commits := len(harness.Storage.Commits())
		expectEqual(t, snapshot(t, harness.Session, v3, conversationId), obj{"count": 2, "version": 3})
		expectEqual(t, migrations, []any{1})
		if len(harness.Storage.Commits()) != commits {
			t.Fatal("nothing is written")
		}
		commit(t, harness.Session, func(tx durable.Tx) error {
			thirdEntry = appendPoint(t, tx, conversationId, "third")
			_, err := tx.Doc(v3, conversationId)
			return err
		})
		hasBase := false
		for _, write := range harness.Storage.LastCommit() {
			if change, ok := write.(durable.DocumentChangeWrite); ok && change.Content.Kind == durable.ContentBase && change.Content.Version == 3 {
				hasBase = true
			}
		}
		if !hasBase {
			t.Fatal("the version base is written")
		}
		asOf := func(token durable.AnyDocToken, at durable.EntryId, args ...any) obj {
			t.Helper()
			value, err := harness.Session.SnapshotAsOfErased(ctx, token, at, args...)
			must(t, err)
			return value
		}
		expectEqual(t, asOf(v3, firstEntry, conversationId), obj{"count": 1, "version": 3})
		expectEqual(t, asOf(v3, secondEntry, conversationId), obj{"count": 2, "version": 3})
		expectEqual(t, asOf(v3, thirdEntry, conversationId), obj{"count": 2, "version": 3})
		expectEqual(t, migrations, []any{1, 1, 1})
		expectEqual(t, asOf(family, firstEntry, conversationId, "member"), obj{"seed": "seed", "count": 1})
		child := fork(t, harness, conversationId, secondEntry)
		expectEqual(t, asOf(v3, firstEntry, child.Id), obj{"count": 1, "version": 3})
		expectEqual(t, asOf(v3, secondEntry, child.Id), obj{"count": 2, "version": 3})
		expectEqual(t, asOf(family, firstEntry, child.Id, "member"), obj{"seed": "seed", "count": 1})
		_, err := harness.Session.SnapshotAsOfErased(ctx, v3, thirdEntry, child.Id)
		expectErrorContains(t, err, fmt.Sprintf("Entry %d is not visible", thirdEntry))
	})

	t.Run("selects the incarnation alive at the entry commit across retirement and recreation", func(t *testing.T) {
		document := valueDoc("history.incarnation", rewindableScope(durable.ForkAsOf), "initial")
		harness := open()
		conversationId := createConversation(t, harness)
		var beforeCreation, createdAt, retiredAt, recreatedAt durable.EntryId
		commit(t, harness.Session, func(tx durable.Tx) error {
			beforeCreation = appendPoint(t, tx, conversationId, "before")
			return nil
		})
		commit(t, harness.Session, func(tx durable.Tx) error {
			createdAt = appendPoint(t, tx, conversationId, "create")
			setValue(t, tx, document, "old", conversationId)
			return nil
		})
		commit(t, harness.Session, func(tx durable.Tx) error {
			retiredAt = appendPoint(t, tx, conversationId, "retire")
			return tx.RetireDoc(document, conversationId)
		})
		commit(t, harness.Session, func(tx durable.Tx) error {
			recreatedAt = appendPoint(t, tx, conversationId, "recreate")
			setValue(t, tx, document, "new", conversationId)
			return nil
		})
		asOf := func(at durable.EntryId) obj {
			t.Helper()
			value, err := harness.Session.SnapshotAsOfErased(ctx, document, at, conversationId)
			must(t, err)
			return value
		}
		if asOf(beforeCreation) != nil || asOf(retiredAt) != nil {
			t.Fatal("no incarnation was alive")
		}
		expectEqual(t, asOf(createdAt), obj{"value": "old"})
		expectEqual(t, asOf(recreatedAt), obj{"value": "new"})
		must(t, harness.Session.Close(ctx))
		_, err := harness.Session.SnapshotAsOfErased(ctx, document, recreatedAt, conversationId)
		expectErrorContains(t, err, "closed")
	})
}

var (
	cacheV1Doc = defineDoc("cache.versioned", 1, sessionScope, func() obj { return obj{"name": "first"} })
	cacheV2Doc = defineDoc("cache.versioned", 2, sessionScope, func() obj { return obj{"names": []any{}} },
		withMigrate(func(value obj, _ int) obj { return obj{"names": []any{value["name"]}} }))
)

func TestSessionTrackerCacheAcrossDefinitionVersions(t *testing.T) {
	t.Run("migrates a document cached by an older token without unloading", func(t *testing.T) {
		harness := open()
		touch(t, harness, cacheV1Doc)
		expectEqual(t, snapshot(t, harness.Session, cacheV1Doc), obj{"name": "first"})
		// Reloaded extension code accesses the still-cached document with a newer token.
		expectEqual(t, snapshot(t, harness.Session, cacheV2Doc), obj{"names": []any{"first"}})
		commit(t, harness.Session, func(tx durable.Tx) error {
			_, err := mustDoc(t, tx, cacheV2Doc).Array("names").Push("second")
			return err
		})
		write := documentWrites(harness.Storage.LastCommit())[0].(durable.DocumentChangeWrite)
		if write.Content.Kind != durable.ContentBase || write.Content.Version != 2 {
			t.Fatal("a version base")
		}
		expectEqual(t, write.Content.Value, obj{"names": []any{"first", "second"}})
		_, err := harness.Session.SnapshotErased(ctx, cacheV1Doc)
		expectErrorContains(t, err, "newer version 2")
	})

	t.Run("sends observers of an older shape a root replacement after a newer token writes", func(t *testing.T) {
		harness := open()
		touch(t, harness, cacheV1Doc)
		state := documentState(t, harness, cacheV1Doc)
		watch := watchDoc(t, harness, ctx, cacheV1Doc)
		delivered := make(chan []durable.Op, 1)
		watch.Start(func(_ ctxT, _ obj, ops []durable.Op) error { delivered <- ops; return nil })
		commit(t, harness.Session, func(tx durable.Tx) error {
			_, err := mustDoc(t, tx, cacheV2Doc).Array("names").Push("second")
			return err
		})
		frames := <-delivered
		flush(harness)
		expectEqual(t, state.Value(), obj{"names": []any{"first", "second"}})
		expectEqual(t, watch.Value(), obj{"names": []any{"first", "second"}})
		expectEqual(t, frames, []any{[]any{"r", obj{"names": []any{"first", "second"}}}})
		state.Dispose()
		_, _ = watch.Stop()
	})

	t.Run("serves an older token from Storage after a newer token migrated only in memory", func(t *testing.T) {
		harness := open()
		touch(t, harness, cacheV1Doc)
		must(t, harness.Session.UnloadDocuments())
		expectEqual(t, snapshot(t, harness.Session, cacheV2Doc), obj{"names": []any{"first"}})
		expectEqual(t, snapshot(t, harness.Session, cacheV1Doc), obj{"name": "first"})
		commit(t, harness.Session, func(tx durable.Tx) error { return mustDoc(t, tx, cacheV1Doc).Set("name", "renamed") })
		expectEqual(t, snapshot(t, harness.Session, cacheV2Doc), obj{"names": []any{"renamed"}})
	})
}
