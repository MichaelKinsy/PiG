package driver_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	coresession "github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Ports of packages/durable/test/session-checkpoints-migrations.test.ts and session-documents.test.ts transaction cases through
// the native driver and the REAL core over SQLite (no scripted core). Pi observes the stored writes through ControlledStorage;
// here the stored document_revisions rows are the same fact: kind (base or delta) and version per revision.

type coreRig struct {
	s  *driver.Session
	db *sqlhost.DB
}

func openCoreDB(t *testing.T) *coreRig { return openCoreDBWith(t, nil) }

// openCoreDBWith opens the rig with a custom task runtime: phases runs the phase and abort handlers of custom tasks.
func openCoreDBWith(t *testing.T, phases func(context.Context, driver.PhaseEffect) error) *coreRig {
	t.Helper()
	return openCoreRig(t, phases, nil)
}

// openCoreRig is openCoreDBWith with a test-only observer of every applied commit: it runs on the owner goroutine after the store
// applied the commit, so blocking in it holds the Session line the way a slow Storage commit does.
func openCoreRig(t *testing.T, phases func(context.Context, driver.PhaseEffect) error, onCommitted func(abi.Commit, []string)) *coreRig {
	t.Helper()
	db, err := sqlhost.OpenDB(t.Context(), filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	uuid := func() string { id, _ := ai.UUIDv7(nil); return id }
	registry := harness.CreateRegistry()
	if phases != nil {
		if err := registry.Install(&durable.Extension{Name: "test.tasks", Tasks: []durable.AnyTask{workTask.Erased()}}); err != nil {
			t.Fatal(err)
		}
	}
	s, err := driver.OpenSession(t.Context(), driver.SessionConfig{
		Core: core.NewSession(uuid), SQL: core.SQLTable(), DB: db, Phases: phases, OnCommitted: onCommitted,
		Harness: harness.HarnessOptions{Models: ai.CreateModels(), Registry: registry},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return &coreRig{s: s, db: db}
}

// revisions lists "kind@version" of every stored revision of the documents of one definition kind, in write order.
func (r *coreRig) revisions(t *testing.T, kind string) []string {
	t.Helper()
	rows, _, err := r.db.All(t.Context(), "SELECT r.kind, r.version FROM document_revisions r JOIN documents d ON d.id = r.document_id WHERE d.kind = ? ORDER BY d.id, r.rowid", []abi.Value{abi.Text(`"` + kind + `"`)})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = fmt.Sprintf("%s@%d", row[0].Bytes, row[1].Int)
	}
	return out
}

// retired asserts that every document of the kind is retired (a retired latest-history document keeps no revisions, so the final
// write Pi persists before the retirement is observable only as the predicate calls and the commit succeeding).
func (r *coreRig) retired(t *testing.T, kind string) {
	t.Helper()
	rows, _, err := r.db.All(t.Context(), "SELECT retired_at IS NOT NULL FROM documents WHERE kind = ?", []abi.Value{abi.Text(`"` + kind + `"`)})
	if err != nil || len(rows) != 1 || rows[0][0].Int != 1 {
		t.Fatalf("%s is retired: %v %v", kind, rows, err)
	}
}

func (r *coreRig) commit(t *testing.T, change func(tx durable.Tx) error) {
	t.Helper()
	if _, err := r.s.Commit(t.Context(), func(tx durable.Tx) (any, error) { return nil, change(tx) }); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

type obj = durable.JsonObject

var sessionScope = durable.DocumentSemantics{Scope: durable.ScopeSession}

type docOption func(*durable.CommonDocDefinition[obj])

func withCheckpoint(f func(value obj, ops []durable.Op, info durable.CheckpointInfo) bool) docOption {
	return func(d *durable.CommonDocDefinition[obj]) { d.CheckpointWhen = f }
}

func withMigrate(f func(value obj, from int) obj) docOption {
	return func(d *durable.CommonDocDefinition[obj]) {
		d.Migrate = func(value obj, from int) (obj, error) { return f(value, from), nil }
	}
}

func defineDoc(kind string, version int, initial func() obj, options ...docOption) durable.DocToken[obj] {
	common := durable.CommonDocDefinition[obj]{Kind: kind, Version: version, CheckpointWhen: func(obj, []durable.Op, durable.CheckpointInfo) bool { return false }}
	for _, option := range options {
		option(&common)
	}
	return durable.DefineDoc(durable.DocDefinition[obj]{CommonDocDefinition: withInitial(common, initial), DocumentSemantics: sessionScope})
}

func eq(t *testing.T, what string, got any, want any) {
	t.Helper()
	if plain(got) != plain(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

func touch(tx durable.Tx, token durable.AnyDocToken) error { _, err := tx.Doc(token); return err }

func increment(token durable.AnyDocToken) func(tx durable.Tx) error {
	return func(tx durable.Tx) error {
		o, err := tx.Doc(token)
		if err != nil {
			return err
		}
		return o.Set("count", toInt(o.Get("count"))+1)
	}
}

// session-checkpoints-migrations.test.ts:42 "selects bases only for nonempty ordinary batches and passes the exact prepared revision and ops".
func TestCoreCheckpointsSelectBasesOnlyForNonemptyOrdinaryBatches(t *testing.T) {
	r := openCoreDB(t)
	var baseCalls, deltaCalls int
	var baseValue obj
	var baseOps int
	baseDoc := defineDoc("checkpoint.base", 1, func() obj { return delta.JsonObjectOf("items", []any{}) }, withCheckpoint(func(v obj, ops []durable.Op, _ durable.CheckpointInfo) bool {
		baseCalls++
		baseValue, baseOps = v, len(ops)
		return true
	}))
	deltaDoc := defineDoc("checkpoint.delta", 1, func() obj { return delta.JsonObjectOf("count", 0) }, withCheckpoint(func(obj, []durable.Op, durable.CheckpointInfo) bool { deltaCalls++; return false }))
	defaultDoc := defineDoc("checkpoint.default", 1, func() obj { return delta.JsonObjectOf("count", 0) })
	r.commit(t, func(tx durable.Tx) error {
		for _, d := range []durable.AnyDocToken{baseDoc, deltaDoc, defaultDoc} {
			if err := touch(tx, d); err != nil {
				return err
			}
		}
		return nil
	})
	eq(t, "predicate calls on creation", []int{baseCalls, deltaCalls}, []int{0, 0})
	r.commit(t, func(tx durable.Tx) error {
		items, err := tx.Doc(baseDoc)
		if err != nil {
			return err
		}
		if err := items.Set("items", []any{"x"}); err != nil {
			return err
		}
		return nil
	})
	r.commit(t, increment(deltaDoc))
	r.commit(t, increment(defaultDoc))
	// A base of a history-"latest" document replaces the revisions before it, so the checkpointed document holds one base.
	eq(t, "base doc revisions", r.revisions(t, "checkpoint.base"), []string{"base@1"})
	eq(t, "delta doc revisions", r.revisions(t, "checkpoint.delta"), []string{"base@1", "delta@1"})
	eq(t, "default doc revisions", r.revisions(t, "checkpoint.default"), []string{"base@1", "delta@1"})
	eq(t, "predicate calls", []int{baseCalls, deltaCalls}, []int{1, 1})
	eq(t, "the predicate sees the prepared revision", baseValue.Value("items"), []any{"x"})
	if baseOps == 0 {
		t.Fatal("the predicate sees the prepared ops")
	}
}

// session-checkpoints-migrations.test.ts:112 "passes the stored delta count since the newest base, including after unload and version bases".
func TestCoreCheckpointsSeeTheStoredDeltaCountAndAVersionBaseResetsIt(t *testing.T) {
	r := openCoreDB(t)
	var seen []int
	v1 := defineDoc("checkpoint.deltas-since-base", 1, func() obj { return delta.JsonObjectOf("count", 0) }, withCheckpoint(func(_ obj, _ []durable.Op, info durable.CheckpointInfo) bool {
		seen = append(seen, info.DeltasSinceBase)
		return info.DeltasSinceBase >= 2
	}))
	v2 := defineDoc("checkpoint.deltas-since-base", 2, func() obj { return delta.JsonObjectOf("count", 0) },
		withMigrate(func(v obj, _ int) obj { return delta.JsonObjectOf("count", v.Value("count")) }),
		withCheckpoint(func(_ obj, _ []durable.Op, info durable.CheckpointInfo) bool {
			seen = append(seen, info.DeltasSinceBase)
			return false
		}))
	r.commit(t, func(tx durable.Tx) error { return touch(tx, v1) })
	for range 4 {
		r.commit(t, increment(v1))
	}
	eq(t, "deltasSinceBase", seen, []int{0, 1, 2, 0})
	eq(t, "kinds", r.revisions(t, "checkpoint.deltas-since-base"), []string{"base@1", "delta@1"})
	// A required version base resets the count without calling the predicate.
	r.commit(t, increment(v2))
	r.commit(t, increment(v2))
	eq(t, "deltasSinceBase after the version base", seen, []int{0, 1, 2, 0, 0})
	got := r.revisions(t, "checkpoint.deltas-since-base")
	eq(t, "the version base", got[len(got)-2:], []string{"base@2", "delta@2"})
}

// session-checkpoints-migrations.test.ts:160 "skips the predicate for empty batches but calls it for nonempty structural no-ops".
func TestCoreCheckpointPredicateSkipsEmptyBatchesButSeesStructuralNoOps(t *testing.T) {
	r := openCoreDB(t)
	calls := 0
	doc := defineDoc("checkpoint.no-op", 1, func() obj { return delta.JsonObjectOf("items", []any{"a", "b"}) }, withCheckpoint(func(obj, []durable.Op, durable.CheckpointInfo) bool { calls++; return false }))
	r.commit(t, func(tx durable.Tx) error { return touch(tx, doc) })
	r.commit(t, func(tx durable.Tx) error {
		o, err := tx.Doc(doc)
		if err != nil {
			return err
		}
		if err := o.Set("items", []any{"a", "b", "x"}); err != nil {
			return err
		}
		return o.Set("items", []any{"a", "b"})
	})
	eq(t, "predicate calls after an empty batch", calls, 0)
	eq(t, "revisions after an empty batch", r.revisions(t, "checkpoint.no-op"), []string{"base@1"})
	r.commit(t, func(tx durable.Tx) error {
		o, err := tx.Doc(doc)
		if err != nil {
			return err
		}
		return o.Set("items", []any{"b", "a"})
	})
	eq(t, "predicate calls after a change", calls, 1)
	eq(t, "revisions after a change", r.revisions(t, "checkpoint.no-op"), []string{"base@1", "delta@1"})
}

// session-checkpoints-migrations.test.ts:306 "uses ordinary checkpoint selection before retirement".
func TestCoreRetirementUsesOrdinaryCheckpointSelection(t *testing.T) {
	r := openCoreDB(t)
	calls := 0
	doc := defineDoc("checkpoint.retire", 1, func() obj { return delta.JsonObjectOf("count", 0) }, withCheckpoint(func(obj, []durable.Op, durable.CheckpointInfo) bool { calls++; return true }))
	r.commit(t, func(tx durable.Tx) error { return touch(tx, doc) })
	r.commit(t, func(tx durable.Tx) error {
		o, err := tx.Doc(doc)
		if err != nil {
			return err
		}
		if err := o.Set("count", 1); err != nil {
			return err
		}
		return tx.RetireDoc(doc)
	})
	eq(t, "predicate calls", calls, 1)
	r.retired(t, "checkpoint.retire")
}

// session-checkpoints-migrations.test.ts:376 "writes the required base on the first successful transaction, then writes deltas".
func TestCoreMigrationWritesTheRequiredBaseOnceThenDeltas(t *testing.T) {
	r := openCoreDB(t)
	checkpoints := 0
	old := defineDoc("migration.transition", 1, func() obj { return delta.JsonObjectOf("count", 4) })
	current := defineDoc("migration.transition", 3, func() obj { return delta.JsonObjectOf("count", 0) },
		withMigrate(func(v obj, from int) obj { return delta.JsonObjectOf("count", toInt(v.Value("count"))+from-1) }),
		withCheckpoint(func(obj, []durable.Op, durable.CheckpointInfo) bool { checkpoints++; return false }))
	r.commit(t, func(tx durable.Tx) error { return touch(tx, old) })
	r.commit(t, func(tx durable.Tx) error { return touch(tx, current) })
	eq(t, "revisions after the migrating transaction", r.revisions(t, "migration.transition"), []string{"base@3"})
	eq(t, "checkpoints", checkpoints, 0)
	rows, _, err := r.db.All(t.Context(), `SELECT r.content FROM document_revisions r JOIN documents d ON d.id = r.document_id WHERE d.kind = '"migration.transition"' ORDER BY r.rowid DESC LIMIT 1`, nil)
	if err != nil || len(rows) != 1 || !strings.Contains(string(rows[0][0].Bytes), `"count":4`) {
		t.Fatalf("the migrated base keeps the value: %v %v", rows, err)
	}
	r.commit(t, func(tx durable.Tx) error {
		o, err := tx.Doc(current)
		if err != nil {
			return err
		}
		return o.Set("count", 7)
	})
	eq(t, "revisions after an edit", r.revisions(t, "migration.transition"), []string{"base@3", "delta@3"})
	eq(t, "checkpoints after an edit", checkpoints, 1)
	var seen any
	r.commit(t, func(tx durable.Tx) error {
		o, err := tx.Doc(current)
		seen = o.Get("count")
		return err
	})
	eq(t, "the stored value", toInt(seen), 7)
}

// session-checkpoints-migrations.test.ts:564 "persists a required migration base before retirement without consulting the checkpoint predicate".
func TestCoreMigrationBaseIsPersistedBeforeRetirementWithoutThePredicate(t *testing.T) {
	r := openCoreDB(t)
	calls := 0
	old := defineDoc("migration.retire", 1, func() obj { return delta.JsonObjectOf("count", 1) })
	current := defineDoc("migration.retire", 2, func() obj { return delta.JsonObjectOf("count", 0) },
		withMigrate(func(v obj, _ int) obj { return delta.JsonObjectOf("count", v.Value("count")) }),
		withCheckpoint(func(obj, []durable.Op, durable.CheckpointInfo) bool { calls++; return true }))
	r.commit(t, func(tx durable.Tx) error { return touch(tx, old) })
	r.commit(t, func(tx durable.Tx) error {
		if err := touch(tx, current); err != nil {
			return err
		}
		return tx.RetireDoc(current)
	})
	r.retired(t, "migration.retire")
	eq(t, "predicate calls", calls, 0)
}

// session-documents.test.ts transaction cases: the records of one commit are written in the order the transaction acquired the
// documents (transaction.ts keeps the drafts in a Map), whatever the documents' kinds sort as.
func TestCoreCommitWritesDocumentsInAcquisitionOrder(t *testing.T) {
	r := openCoreDB(t)
	kinds := []string{"order.f", "order.b", "order.e", "order.a", "order.d", "order.c"}
	tokens := make([]durable.AnyDocToken, len(kinds))
	for i, kind := range kinds {
		tokens[i] = defineDoc(kind, 1, func() obj { return delta.JsonObjectOf("count", 0) })
	}
	r.commit(t, func(tx durable.Tx) error {
		for _, token := range tokens {
			if err := touch(tx, token); err != nil {
				return err
			}
		}
		return nil
	})
	// Acquire in kinds order, then edit in the reverse order: Pi writes the records in the order the documents were acquired
	// (transaction.ts #documents), not the order they were edited.
	for range 3 {
		r.commit(t, func(tx durable.Tx) error {
			objects := make([]*delta.Object, len(tokens))
			for i, token := range tokens {
				o, err := tx.Doc(token)
				if err != nil {
					return err
				}
				objects[i] = o
			}
			for _, object := range slices.Backward(objects) {
				if err := object.Set("count", toInt(object.Get("count"))+1); err != nil {
					return err
				}
			}
			return nil
		})
	}
	rows, _, err := r.db.All(t.Context(), "SELECT d.kind FROM document_revisions r JOIN documents d ON d.id = r.document_id WHERE r.kind = 'delta' ORDER BY r.rowid", nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, row := range rows {
		got = append(got, strings.Trim(string(row[0].Bytes), `"`))
	}
	var want []string
	for range 3 {
		want = append(want, kinds...)
	}
	eq(t, "delta write order", got, want)
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case int64:
		return int(n)
	}
	return -1
}

// session-checkpoints-migrations.test.ts:333 "migrates read-only once per cold load, copies the callback result, and writes nothing":
// a snapshot of an older stored version runs the definition's migrate on the host (NeedMigrate round trip) and stores nothing.
func TestCoreSnapshotMigratesReadOnly(t *testing.T) {
	r := openCoreDB(t)
	calls := 0
	old := defineDoc("migration.snapshot", 1, func() obj { return delta.JsonObjectOf("count", 4) })
	current := defineDoc("migration.snapshot", 3, func() obj { return delta.JsonObjectOf("count", 0) },
		withMigrate(func(v obj, from int) obj {
			calls++
			return delta.JsonObjectOf("count", toInt(v.Value("count"))+from+10)
		}))
	r.commit(t, func(tx durable.Tx) error { return touch(tx, old) })
	before := r.revisions(t, "migration.snapshot")
	got, err := durable.Snapshot[obj](t.Context(), r.s, current)
	if err != nil || got == nil || toInt((*got).Value("count")) != 15 {
		t.Fatalf("snapshot = %v, %v (calls %d)", got, err, calls)
	}
	eq(t, "migrate calls", calls, 1)
	eq(t, "revisions after the read", r.revisions(t, "migration.snapshot"), before)
}

func (r *coreRig) fail(t *testing.T, change func(tx durable.Tx) error) error {
	t.Helper()
	_, err := r.s.Commit(t.Context(), func(tx durable.Tx) (any, error) { return nil, change(tx) })
	if err == nil {
		t.Fatal("the commit must fail")
	}
	return err
}

// session-checkpoints-migrations.test.ts:440 "rolls migration and edits back with the callback, then coalesces later edits into one base".
func TestCoreMigrationAndEditsRollBackWithTheCallback(t *testing.T) {
	r := openCoreDB(t)
	migrations := 0
	old := defineDoc("migration.rollback", 1, func() obj { return delta.JsonObjectOf("count", 1) })
	current := defineDoc("migration.rollback", 2, func() obj { return delta.JsonObjectOf("count", 0, "migrated", false) },
		withMigrate(func(v obj, _ int) obj {
			migrations++
			return delta.JsonObjectOf("count", v.Value("count"), "migrated", true)
		}))
	r.commit(t, func(tx durable.Tx) error { return touch(tx, old) })
	before := r.revisions(t, "migration.rollback")
	err := r.fail(t, func(tx durable.Tx) error {
		o, err := tx.Doc(current)
		if err != nil {
			return err
		}
		if err := o.Set("count", 8); err != nil {
			return err
		}
		return errors.New("rollback")
	})
	if !strings.Contains(err.Error(), "rollback") {
		t.Fatalf("err = %v", err)
	}
	eq(t, "revisions after the rollback", r.revisions(t, "migration.rollback"), before)
	got, err := durable.Snapshot[obj](t.Context(), r.s, current)
	if err != nil || got == nil || toInt((*got).Value("count")) != 1 || (*got).Value("migrated") != true {
		t.Fatalf("snapshot = %v, %v", got, err)
	}
	r.commit(t, func(tx durable.Tx) error {
		o, err := tx.Doc(current)
		if err != nil {
			return err
		}
		if err := o.Set("count", 9); err != nil {
			return err
		}
		return o.Set("migrated", false)
	})
	eq(t, "one required base holds the later edits", r.revisions(t, "migration.rollback"), []string{"base@2"})
	// Pi migrates once per cold load: the rolled-back transaction, the snapshot and the later commit share one migrate call.
	eq(t, "migrate calls", migrations, 1)
}

// session-checkpoints-migrations.test.ts:529 "rejects newer stored versions and older versions without migration for snapshots and transactions".
func TestCoreRejectsNewerStoredVersionsAndOlderVersionsWithoutMigration(t *testing.T) {
	r := openCoreDB(t)
	v2 := defineDoc("migration.compatibility", 2, func() obj { return delta.JsonObjectOf("count", 2) })
	v1 := defineDoc("migration.compatibility", 1, func() obj { return delta.JsonObjectOf("count", 1) })
	v3 := defineDoc("migration.compatibility", 3, func() obj { return delta.JsonObjectOf("count", 3) })
	r.commit(t, func(tx durable.Tx) error { return touch(tx, v2) })
	before := r.revisions(t, "migration.compatibility")
	for _, tc := range []struct {
		name  string
		token durable.DocToken[obj]
		want  string
	}{{"v1", v1, "newer version 2 than 1"}, {"v3", v3, "requires migration from version 2"}} {
		if _, err := durable.Snapshot[obj](t.Context(), r.s, tc.token); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("snapshot %s: %v, want %q", tc.name, err, tc.want)
		}
		err := r.fail(t, func(tx durable.Tx) error { return touch(tx, tc.token) })
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("commit %s: %v, want %q", tc.name, err, tc.want)
		}
	}
	eq(t, "revisions", r.revisions(t, "migration.compatibility"), before)
}

// session-checkpoints-migrations.test.ts:196 "rolls back every prepared document when a checkpoint predicate throws".
func TestCoreCheckpointPredicateFailureRollsBackEveryDocument(t *testing.T) {
	r := openCoreDB(t)
	throwCheckpoint := true
	firstCalls := 0
	first := defineDoc("checkpoint.rollback.first", 1, func() obj { return delta.JsonObjectOf("count", 0) },
		withCheckpoint(func(obj, []durable.Op, durable.CheckpointInfo) bool { firstCalls++; return false }))
	second := defineDoc("checkpoint.rollback.second", 1, func() obj { return delta.JsonObjectOf("count", 0) },
		withCheckpoint(func(obj, []durable.Op, durable.CheckpointInfo) bool {
			if throwCheckpoint {
				panic(errors.New("checkpoint failed"))
			}
			return false
		}))
	r.commit(t, func(tx durable.Tx) error {
		if err := touch(tx, first); err != nil {
			return err
		}
		return touch(tx, second)
	})
	beforeFirst, beforeSecond := r.revisions(t, "checkpoint.rollback.first"), r.revisions(t, "checkpoint.rollback.second")
	err := r.fail(t, func(tx durable.Tx) error {
		o, err := tx.Doc(first)
		if err != nil {
			return err
		}
		if err := o.Set("count", 1); err != nil {
			return err
		}
		o, err = tx.Doc(second)
		if err != nil {
			return err
		}
		return o.Set("count", 2)
	})
	if !strings.Contains(err.Error(), "checkpoint failed") {
		t.Fatalf("err = %v", err)
	}
	eq(t, "first predicate calls", firstCalls, 1)
	eq(t, "first revisions", r.revisions(t, "checkpoint.rollback.first"), beforeFirst)
	eq(t, "second revisions", r.revisions(t, "checkpoint.rollback.second"), beforeSecond)
	throwCheckpoint = false
	r.commit(t, func(tx durable.Tx) error {
		for i, token := range []durable.DocToken[obj]{first, second} {
			o, err := tx.Doc(token)
			if err != nil {
				return err
			}
			if err := o.Set("count", 3+i); err != nil {
				return err
			}
		}
		return nil
	})
	for i, token := range []durable.DocToken[obj]{first, second} {
		got, err := durable.Snapshot[obj](t.Context(), r.s, token)
		if err != nil || got == nil || toInt((*got).Value("count")) != 3+i {
			t.Fatalf("snapshot %d = %v, %v", i, got, err)
		}
	}
}

// session-checkpoints-migrations.test.ts:597 "leaves unaccessed older documents and unavailable definitions untouched".
func TestCoreMigrationLeavesUnaccessedOlderDocumentsUntouched(t *testing.T) {
	r := openCoreDB(t)
	firstV1 := defineDoc("migration.lazy.first", 1, func() obj { return delta.JsonObjectOf("count", 1) })
	secondV1 := defineDoc("migration.lazy.second", 1, func() obj { return delta.JsonObjectOf("count", 2) })
	secondMigrations := 0
	firstV2 := defineDoc("migration.lazy.first", 2, func() obj { return delta.JsonObjectOf("count", 0) }, withMigrate(func(v obj, _ int) obj { return delta.JsonObjectOf("count", v.Value("count")) }))
	_ = defineDoc("migration.lazy.second", 2, func() obj { return delta.JsonObjectOf("count", 0) }, withMigrate(func(v obj, _ int) obj { secondMigrations++; return v }))
	r.commit(t, func(tx durable.Tx) error {
		if err := touch(tx, firstV1); err != nil {
			return err
		}
		return touch(tx, secondV1)
	})
	before := r.revisions(t, "migration.lazy.second")
	got, err := durable.Snapshot[obj](t.Context(), r.s, firstV2)
	if err != nil || got == nil || toInt((*got).Value("count")) != 1 {
		t.Fatalf("snapshot = %v, %v", got, err)
	}
	eq(t, "second migrations", secondMigrations, 0)
	eq(t, "second revisions", r.revisions(t, "migration.lazy.second"), before)
	eq(t, "first revisions", r.revisions(t, "migration.lazy.first"), []string{"base@1"})
}

// A typed Go callback cannot throw, so a panicking migrate or initial rejects the operation like Pi's thrown error does, in every
// path that runs it on the host (transaction doc op, read-only snapshot, watch).
func TestCoreDefinitionCallbackPanicsRejectTheOperation(t *testing.T) {
	r := openCoreDB(t)
	old := defineDoc("callback.panic", 1, func() obj { return delta.JsonObjectOf("count", 1) })
	boom := defineDoc("callback.panic", 2, func() obj { return delta.JsonObjectOf("count", 0) }, withMigrate(func(obj, int) obj { panic(errors.New("migrate failed")) }))
	r.commit(t, func(tx durable.Tx) error { return touch(tx, old) })
	if _, err := durable.Snapshot[obj](t.Context(), r.s, boom); err == nil || !strings.Contains(err.Error(), "migrate failed") {
		t.Fatalf("snapshot: %v", err)
	}
	if err := r.fail(t, func(tx durable.Tx) error { return touch(tx, boom) }); !strings.Contains(err.Error(), "migrate failed") {
		t.Fatalf("commit: %v", err)
	}
	if w, err := r.s.WatchDocErased(t.Context(), boom); err == nil || w != nil || !strings.Contains(err.Error(), "migrate failed") {
		t.Fatalf("watch: %v %v", w, err)
	}
	panicInitial := defineDoc("callback.initial", 1, func() obj { panic(errors.New("initial failed")) })
	if err := r.fail(t, func(tx durable.Tx) error { return touch(tx, panicInitial) }); !strings.Contains(err.Error(), "initial failed") {
		t.Fatalf("initial: %v", err)
	}
}

// A throwing task initial rejects createTask (Pi transaction.ts createTask: initial(input) runs first); the typed Go callback
// panics instead, and the driver reports it as the creation's error.
func TestCoreTaskInitialPanicRejectsCreateTask(t *testing.T) {
	r := openCoreDB(t)
	boom := durable.DefineTask(durable.TaskDefinition[obj, obj, obj, struct{}]{
		Name: "test.initial.panic", Version: 1,
		Initial: func(obj) obj { panic(errors.New("initial failed")) },
		Phases:  map[string]durable.PhaseHandler[obj, obj, obj, struct{}]{},
	})
	err := r.fail(t, func(tx durable.Tx) error {
		_, err := durable.CreateTask(tx, boom, delta.JsonObjectOf(), durable.TaskOptions{})
		return err
	})
	if !strings.Contains(err.Error(), "initial failed") {
		t.Fatalf("err = %v", err)
	}
}

func shapeDocs() (v1, v2 durable.DocToken[obj]) {
	v1 = defineDoc("migration.shape", 1, func() obj { return delta.JsonObjectOf("name", "first") })
	v2 = defineDoc("migration.shape", 2, func() obj { return delta.JsonObjectOf("names", []any{}) },
		withMigrate(func(v obj, _ int) obj { return delta.JsonObjectOf("names", []any{v.Value("name")}) }))
	return v1, v2
}

func snapshotJSON(t *testing.T, r *coreRig, token durable.DocToken[obj]) (string, error) {
	t.Helper()
	got, err := durable.Snapshot[obj](t.Context(), r.s, token)
	if err != nil || got == nil {
		return "", err
	}
	return plain(*got), nil
}

// session-checkpoints-migrations.test.ts:792 "migrates a document cached by an older token without unloading".
func TestCoreNewerTokenMigratesADocumentAnOlderTokenReadFirst(t *testing.T) {
	r := openCoreDB(t)
	v1, v2 := shapeDocs()
	r.commit(t, func(tx durable.Tx) error { return touch(tx, v1) })
	if got, err := snapshotJSON(t, r, v1); err != nil || got != "map[name:first]" {
		t.Fatalf("v1 snapshot = %q, %v", got, err)
	}
	if got, err := snapshotJSON(t, r, v2); err != nil || got != "map[names:[first]]" {
		t.Fatalf("v2 snapshot = %q, %v", got, err)
	}
	r.commit(t, func(tx durable.Tx) error {
		o, err := tx.Doc(v2)
		if err != nil {
			return err
		}
		_, err = o.Array("names").Push("second")
		return err
	})
	eq(t, "the migration base holds the edit", r.revisions(t, "migration.shape"), []string{"base@2"})
	if _, err := snapshotJSON(t, r, v1); err == nil || !strings.Contains(err.Error(), "newer version 2") {
		t.Fatalf("v1 after the v2 write: %v", err)
	}
}

// session-checkpoints-migrations.test.ts:810 "sends observers of an older shape a root replacement after a newer token writes".
func TestCoreObserversOfAnOlderShapeGetARootReplacementAfterANewerTokenWrites(t *testing.T) {
	r := openCoreDB(t)
	v1, v2 := shapeDocs()
	r.commit(t, func(tx durable.Tx) error { return touch(tx, v1) })
	w, err := r.s.WatchDocErased(t.Context(), v1)
	if err != nil || w == nil {
		t.Fatalf("watch: %v %v", w, err)
	}
	frames := make(chan []durable.Op, 4)
	w.Start(func(_ context.Context, _ durable.JsonObject, ops []durable.Op) error { frames <- ops; return nil })
	r.commit(t, func(tx durable.Tx) error {
		o, err := tx.Doc(v2)
		if err != nil {
			return err
		}
		_, err = o.Array("names").Push("second")
		return err
	})
	select {
	case ops := <-frames:
		if len(ops) != 1 || !isRoot(ops[0]) || plain(ops[0][1]) != "map[names:[first second]]" {
			t.Fatalf("frame = %v", ops)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no frame")
	}
	eq(t, "watch value", w.Value(), "map[names:[first second]]")
	_, _ = w.Stop()
}

// session-checkpoints-migrations.test.ts:833 "serves an older token from Storage after a newer token migrated only in memory".
func TestCoreOlderTokenReadsStorageAfterANewerTokenMigratedOnlyInMemory(t *testing.T) {
	r := openCoreDB(t)
	v1, v2 := shapeDocs()
	r.commit(t, func(tx durable.Tx) error { return touch(tx, v1) })
	if got, err := snapshotJSON(t, r, v2); err != nil || got != "map[names:[first]]" {
		t.Fatalf("v2 = %q, %v", got, err)
	}
	if got, err := snapshotJSON(t, r, v1); err != nil || got != "map[name:first]" {
		t.Fatalf("v1 = %q, %v", got, err)
	}
	r.commit(t, func(tx durable.Tx) error {
		o, err := tx.Doc(v1)
		if err != nil {
			return err
		}
		return o.Set("name", "renamed")
	})
	if got, err := snapshotJSON(t, r, v2); err != nil || got != "map[names:[renamed]]" {
		t.Fatalf("v2 after the rename = %q, %v", got, err)
	}
}

// session-checkpoints-migrations.test.ts:252 "persists repeated false decisions as deltas and replays the complete tail".
func TestCoreRepeatedFalseCheckpointDecisionsPersistDeltasAndReplayTheTail(t *testing.T) {
	r := openCoreDB(t)
	calls := 0
	doc := defineDoc("checkpoint.tail", 1, func() obj { return delta.JsonObjectOf("values", []any{}) }, withCheckpoint(func(obj, []durable.Op, durable.CheckpointInfo) bool { calls++; return false }))
	r.commit(t, func(tx durable.Tx) error { return touch(tx, doc) })
	for v := 1; v <= 8; v++ {
		r.commit(t, func(tx durable.Tx) error {
			o, err := tx.Doc(doc)
			if err != nil {
				return err
			}
			_, err = o.Array("values").Push(v)
			return err
		})
	}
	want := []string{"base@1"}
	for range 8 {
		want = append(want, "delta@1")
	}
	eq(t, "revisions", r.revisions(t, "checkpoint.tail"), want)
	eq(t, "predicate calls", calls, 8)
	got, err := durable.Snapshot[obj](t.Context(), r.s, doc)
	if err != nil || got == nil || fmt.Sprint((*got).Value("values")) != "[1 2 3 4 5 6 7 8]" {
		t.Fatalf("replayed tail = %v, %v", got, err)
	}
}

// session-checkpoints-migrations.test.ts:279 "keeps a prepared root replacement as a delta when the predicate is false".
func TestCoreRootReplacementStaysADeltaWhenThePredicateIsFalse(t *testing.T) {
	r := openCoreDB(t)
	initial := delta.JsonObjectOf()
	for i := range 4100 {
		initial.Set(fmt.Sprintf("field%d", i), 0)
	}
	doc := defineDoc("checkpoint.root-replacement", 1, func() obj {
		out := delta.JsonObjectOf()
		for k, v := range initial.All() {
			out.Set(k, v)
		}
		return out
	}, withCheckpoint(func(obj, []durable.Op, durable.CheckpointInfo) bool { return false }))
	r.commit(t, func(tx durable.Tx) error { return touch(tx, doc) })
	r.commit(t, func(tx durable.Tx) error {
		o, err := tx.Doc(doc)
		if err != nil {
			return err
		}
		for i := range 4100 {
			if err := o.Set(fmt.Sprintf("field%d", i), 1); err != nil {
				return err
			}
		}
		return nil
	})
	eq(t, "revisions", r.revisions(t, "checkpoint.root-replacement"), []string{"base@1", "delta@1"})
	rows, _, err := r.db.All(t.Context(), `SELECT r.content FROM document_revisions r JOIN documents d ON d.id = r.document_id WHERE d.kind = '"checkpoint.root-replacement"' AND r.kind = 'delta'`, nil)
	if err != nil || len(rows) != 1 {
		t.Fatalf("delta rows: %v %v", rows, err)
	}
	content := string(rows[0][0].Bytes)
	if !strings.HasPrefix(content, `{"ops":[["r",`) && !strings.HasPrefix(content, `[["r",`) {
		t.Fatalf("the delta is one root replacement, got %.60s", content)
	}
	got, err := durable.Snapshot[obj](t.Context(), r.s, doc)
	if err != nil || got == nil || toInt((*got).Value("field4099")) != 1 {
		t.Fatalf("snapshot field4099 = %v, %v", got, err)
	}
}

// session-checkpoints-migrations.test.ts:648 "migrates current and historical rewindable values independently and follows fork ancestry".
func TestCoreMigratesCurrentAndHistoricalRewindableValuesAndFollowsForkAncestry(t *testing.T) {
	r := openCoreDB(t)
	rewindable := durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryRewindable, Fork: durable.ForkAsOf}
	mk := func(version int, initial func() obj, options ...docOption) durable.DocToken[obj] {
		common := durable.CommonDocDefinition[obj]{Kind: "history.migration", Version: version, CheckpointWhen: func(obj, []durable.Op, durable.CheckpointInfo) bool { return false }}
		for _, o := range options {
			o(&common)
		}
		return durable.DefineDoc(durable.DocDefinition[obj]{CommonDocDefinition: withInitial(common, initial), DocumentSemantics: rewindable})
	}
	v1 := mk(1, func() obj { return delta.JsonObjectOf("count", 0) })
	var migrations []int
	v3 := mk(3, func() obj { return delta.JsonObjectOf("count", 0, "version", 3) }, withMigrate(func(v obj, from int) obj {
		migrations = append(migrations, from)
		return delta.JsonObjectOf("count", v.Value("count"), "version", 3)
	}))
	family := durable.DefineDocFamily(durable.DocFamilyDefinition[obj, string]{
		Kind:              "history.family",
		Version:           1,
		DocumentSemantics: rewindable,
		Initial:           func(seed string) obj { return delta.JsonObjectOf("seed", seed, "count", 0) },
		Family:            true,
		CheckpointWhen:    func(obj, []durable.Op, durable.CheckpointInfo) bool { return false },
	})
	conversationId := r.createConversation(t)
	var first, second, third durable.EntryId
	r.commit(t, func(tx durable.Tx) error {
		e, err := tx.AppendEntry(conversationId, durable.EntryDraft{Kind: "first"})
		if err != nil {
			return err
		}
		first = e.Id
		o, err := tx.Doc(v1, conversationId)
		if err != nil {
			return err
		}
		if err := o.Set("count", 1); err != nil {
			return err
		}
		f, err := tx.Doc(family, conversationId, "member", "seed")
		if err != nil {
			return err
		}
		return f.Set("count", 1)
	})
	r.commit(t, func(tx durable.Tx) error {
		e, err := tx.AppendEntry(conversationId, durable.EntryDraft{Kind: "second"})
		if err != nil {
			return err
		}
		second = e.Id
		o, err := tx.Doc(v1, conversationId)
		if err != nil {
			return err
		}
		return o.Set("count", 2)
	})
	asOf := func(token durable.DocToken[obj], id durable.ConversationId, at durable.EntryId) (string, error) {
		got, err := durable.SnapshotAsOf[obj](t.Context(), r.s, token, at, id)
		if err != nil || got == nil {
			return "", err
		}
		return plain(*got), nil
	}
	if got, err := durable.Snapshot[obj](t.Context(), r.s, v3, conversationId); err != nil || got == nil || plain(*got) != "map[count:2 version:3]" {
		t.Fatalf("current = %v, %v", got, err)
	}
	eq(t, "migrations", migrations, []int{1})
	r.commit(t, func(tx durable.Tx) error {
		e, err := tx.AppendEntry(conversationId, durable.EntryDraft{Kind: "third"})
		if err != nil {
			return err
		}
		third = e.Id
		return touch2(tx, v3, conversationId)
	})
	for _, tc := range []struct {
		at   durable.EntryId
		want string
	}{{first, "map[count:1 version:3]"}, {second, "map[count:2 version:3]"}, {third, "map[count:2 version:3]"}} {
		if got, err := asOf(v3, conversationId, tc.at); err != nil || got != tc.want {
			t.Fatalf("asOf %d = %q, %v; want %q", tc.at, got, err, tc.want)
		}
	}
	eq(t, "migrations after the historical reads", migrations, []int{1, 1, 1})
	if got, err := durable.SnapshotAsOf[obj](t.Context(), r.s, family, first, conversationId, "member"); err != nil || got == nil || plain(*got) != "map[count:1 seed:seed]" {
		t.Fatalf("family asOf = %v, %v", got, err)
	}
	var childId durable.ConversationId
	r.commit(t, func(tx durable.Tx) error {
		child, err := tx.ForkConversation(conversationId, second, durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		childId = child.Id
		return err
	})
	for _, tc := range []struct {
		at   durable.EntryId
		want string
	}{{first, "map[count:1 version:3]"}, {second, "map[count:2 version:3]"}} {
		if got, err := asOf(v3, childId, tc.at); err != nil || got != tc.want {
			t.Fatalf("child asOf %d = %q, %v; want %q", tc.at, got, err, tc.want)
		}
	}
	if _, err := asOf(v3, childId, third); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("Entry %d is not visible", third)) {
		t.Fatalf("child asOf the third entry: %v", err)
	}
}

func touch2(tx durable.Tx, token durable.AnyDocToken, args ...any) error {
	_, err := tx.Doc(token, args...)
	return err
}

// session-checkpoints-migrations.test.ts:735 "selects the incarnation alive at the entry commit across retirement and recreation".
func TestCoreSelectsTheIncarnationAliveAtTheEntryCommitAcrossRetirementAndRecreation(t *testing.T) {
	r := openCoreDB(t)
	doc := durable.DefineDoc(durable.DocDefinition[obj]{
		CommonDocDefinition: durable.CommonDocDefinition[obj]{Kind: "history.incarnation", Version: 1, Initial: func() obj { return delta.JsonObjectOf("value", "initial") }, CheckpointWhen: func(obj, []durable.Op, durable.CheckpointInfo) bool { return false }},
		DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryRewindable, Fork: durable.ForkAsOf},
	})
	conversationId := r.createConversation(t)
	entry := func(kind string, then func(tx durable.Tx) error) durable.EntryId {
		var id durable.EntryId
		r.commit(t, func(tx durable.Tx) error {
			e, err := tx.AppendEntry(conversationId, durable.EntryDraft{Kind: kind})
			if err != nil {
				return err
			}
			id = e.Id
			if then == nil {
				return nil
			}
			if err := then(tx); err != nil {
				t.Logf("entry %q: %v", kind, err)
				return err
			}
			return nil
		})
		return id
	}
	setValue := func(value string) func(tx durable.Tx) error {
		return func(tx durable.Tx) error {
			o, err := tx.Doc(doc, conversationId)
			if err != nil {
				return err
			}
			return o.Set("value", value)
		}
	}
	beforeCreation := entry("before", nil)
	createdAt := entry("create", setValue("old"))
	retiredAt := entry("retire", func(tx durable.Tx) error { return tx.RetireDoc(doc, conversationId) })
	recreatedAt := entry("recreate", setValue("new"))
	for _, tc := range []struct {
		name string
		at   durable.EntryId
		want string
	}{{"before creation", beforeCreation, ""}, {"created", createdAt, "map[value:old]"}, {"retired", retiredAt, ""}, {"recreated", recreatedAt, "map[value:new]"}} {
		got, err := durable.SnapshotAsOf[obj](t.Context(), r.s, doc, tc.at, conversationId)
		text := ""
		if got != nil {
			text = plain(*got)
		}
		if err != nil || text != tc.want {
			t.Fatalf("%s: snapshotAsOf = %q, %v; want %q", tc.name, text, err, tc.want)
		}
	}
	if err := r.s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := durable.SnapshotAsOf[obj](t.Context(), r.s, doc, recreatedAt, conversationId); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("after close: %v", err)
	}
}

var documentsMemberDoc = durable.DefineDocFamily(durable.DocFamilyDefinition[obj, string]{
	Kind:              "test.member",
	Version:           1,
	DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeSession},
	Initial:           func(seed string) obj { return delta.JsonObjectOf("seed", seed, "hits", 0) },
	Family:            true,
	CheckpointWhen:    func(obj, []durable.Op, durable.CheckpointInfo) bool { return false },
})

func hit(tx durable.Tx, key, seed string) error {
	o, err := tx.Doc(documentsMemberDoc, key, seed)
	if err != nil {
		return err
	}
	return o.Set("hits", toInt(o.Get("hits"))+1)
}

// Pi 1.2.0 packages/durable/test/session-documents.test.ts:266 "uses the first family seed and ignores seeds for existing members".
func TestCoreFamilyDocumentsUseTheFirstSeedAndIgnoreSeedsForExistingMembers(t *testing.T) {
	r := openCoreDB(t)
	r.commit(t, func(tx durable.Tx) error {
		first, err := tx.Doc(documentsMemberDoc, "k", "first")
		if err != nil {
			return err
		}
		second, err := tx.Doc(documentsMemberDoc, "k", "second")
		if err != nil {
			return err
		}
		if first != second {
			t.Fatal("a second access in one transaction returns the same draft")
		}
		return first.Set("hits", 1)
	})
	r.commit(t, func(tx durable.Tx) error {
		if err := hit(tx, "k", "third"); err != nil {
			return err
		}
		return hit(tx, "other", "fourth")
	})
	for _, tc := range []struct{ key, want string }{{"k", "map[hits:2 seed:first]"}, {"other", "map[hits:1 seed:fourth]"}} {
		got, err := durable.Snapshot[obj](t.Context(), r.s, documentsMemberDoc, tc.key)
		if err != nil || got == nil || plain(*got) != tc.want {
			t.Fatalf("snapshot %s = %v, %v; want %s", tc.key, got, err, tc.want)
		}
	}
}

// Pi 1.2.0 packages/durable/test/session-documents.test.ts:363 "rejects tokens whose semantics or version disagree with the stored incarnation".
func TestCoreRejectsTokensWhoseSemanticsOrVersionDisagreeWithTheStoredIncarnation(t *testing.T) {
	r := openCoreDB(t)
	conversationId := r.createConversation(t)
	live := func(version int, history durable.DocumentHistory) durable.DocToken[obj] {
		return durable.DefineDoc(durable.DocDefinition[obj]{
			CommonDocDefinition: durable.CommonDocDefinition[obj]{Kind: "test.live", Version: version, Initial: func() obj { return delta.JsonObjectOf("message", "x") }, CheckpointWhen: func(obj, []durable.Op, durable.CheckpointInfo) bool { return false }},
			DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: history, Fork: durable.ForkInitial},
		})
	}
	r.commit(t, func(tx durable.Tx) error { return touch2(tx, live(1, durable.HistoryLatest), conversationId) })
	rewindable := live(1, durable.HistoryRewindable)
	if _, err := durable.Snapshot[obj](t.Context(), r.s, rewindable, conversationId); err == nil || !strings.Contains(err.Error(), "does not match the supplied definition semantics") {
		t.Fatalf("snapshot with other semantics: %v", err)
	}
	err := r.fail(t, func(tx durable.Tx) error { return touch2(tx, rewindable, conversationId) })
	if !strings.Contains(err.Error(), "does not match the supplied definition semantics") {
		t.Fatalf("commit with other semantics: %v", err)
	}
	err = r.fail(t, func(tx durable.Tx) error { return touch2(tx, live(2, durable.HistoryLatest), conversationId) })
	if !strings.Contains(err.Error(), "requires migration from version 1") {
		t.Fatalf("commit with a newer version: %v", err)
	}
}

// Pi 1.2.0 packages/durable/test/session-documents.test.ts:479 "retires documents and creates a new incarnation at the same address".
func TestCoreRetiredDocumentsGetANewIncarnationAtTheSameAddress(t *testing.T) {
	r := openCoreDB(t)
	conversationId := r.createConversation(t)
	live := defineConversationDoc("test.live")
	r.commit(t, func(tx durable.Tx) error { return touch2(tx, live, conversationId) })
	r.commit(t, func(tx durable.Tx) error {
		old, err := tx.Doc(live, conversationId)
		if err != nil {
			return err
		}
		if err := old.Set("message", "final"); err != nil {
			return err
		}
		if err := tx.RetireDoc(live, conversationId); err != nil {
			return err
		}
		replacement, err := tx.Doc(live, conversationId)
		if err != nil {
			return err
		}
		if replacement == old {
			t.Fatal("a new incarnation is a different draft")
		}
		return replacement.Set("message", "new")
	})
	if got, err := durable.Snapshot[obj](t.Context(), r.s, live, conversationId); err != nil || got == nil || plain(*got) != "map[items:[] message:new]" {
		t.Fatalf("new incarnation = %v, %v", got, err)
	}
	rows, _, err := r.db.All(t.Context(), `SELECT retired_at IS NOT NULL FROM documents WHERE kind = '"test.live"' ORDER BY id`, nil)
	if err != nil || len(rows) != 2 || rows[0][0].Int != 1 || rows[1][0].Int != 0 {
		t.Fatalf("documents = %v, %v: the old incarnation is retired and the new one is live", rows, err)
	}
	r.commit(t, func(tx durable.Tx) error { return tx.RetireDoc(live, conversationId) })
	if got, err := durable.Snapshot[obj](t.Context(), r.s, live, conversationId); err != nil || got != nil {
		t.Fatalf("retired = %v, %v", got, err)
	}
	// Retiring an absent address is a no-op.
	before := r.revisions(t, "test.live")
	r.commit(t, func(tx durable.Tx) error { return tx.RetireDoc(live, conversationId) })
	eq(t, "revisions after retiring an absent address", r.revisions(t, "test.live"), before)
}

// Pi 1.2.0 packages/durable/test/session-documents.test.ts:519 "retires without acquisition and recreates both existing and absent addresses".
func TestCoreRetiresWithoutAcquisitionAndRecreatesExistingAndAbsentAddresses(t *testing.T) {
	r := openCoreDB(t)
	conversationId := r.createConversation(t)
	live := defineConversationDoc("test.live2")
	r.commit(t, func(tx durable.Tx) error { return touch2(tx, live, conversationId) })
	r.commit(t, func(tx durable.Tx) error {
		if err := tx.RetireDoc(live, conversationId); err != nil {
			return err
		}
		o, err := tx.Doc(live, conversationId)
		if err != nil {
			return err
		}
		return o.Set("message", "replacement")
	})
	if got, err := durable.Snapshot[obj](t.Context(), r.s, live, conversationId); err != nil || got == nil || plain(*got) != "map[items:[] message:replacement]" {
		t.Fatalf("replacement = %v, %v", got, err)
	}
	r.commit(t, func(tx durable.Tx) error {
		if err := tx.RetireDoc(documentsMemberDoc, "absent"); err != nil {
			return err
		}
		o, err := tx.Doc(documentsMemberDoc, "absent", "seed")
		if err != nil {
			return err
		}
		return o.Set("hits", 1)
	})
	if got, err := durable.Snapshot[obj](t.Context(), r.s, documentsMemberDoc, "absent"); err != nil || got == nil || plain(*got) != "map[hits:1 seed:seed]" {
		t.Fatalf("absent recreated = %v, %v", got, err)
	}
}

func defineConversationDoc(kind string) durable.DocToken[obj] {
	return durable.DefineDoc(durable.DocDefinition[obj]{
		CommonDocDefinition: durable.CommonDocDefinition[obj]{Kind: kind, Version: 1, Initial: func() obj { return delta.JsonObjectOf("message", "", "items", []any{}) }, CheckpointWhen: func(obj, []durable.Op, durable.CheckpointInfo) bool { return false }},
		DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkInitial},
	})
}

func panics(use func()) (recovered any) {
	defer func() { recovered = recover() }()
	use()
	return nil
}

// Pi 1.2.0 packages/durable/test/session-documents.test.ts:207 "revokes escaped drafts when the callback settles".
func TestCoreEscapedDraftsAreRevokedWhenTheCallbackSettles(t *testing.T) {
	r := openCoreDB(t)
	conversationId := r.createConversation(t)
	live := defineConversationDoc("test.escape")
	var escaped *delta.Object
	r.commit(t, func(tx durable.Tx) error {
		o, err := tx.Doc(live, conversationId)
		if err != nil {
			return err
		}
		escaped = o
		return o.Set("message", "inside")
	})
	if panics(func() { _ = escaped.Get("message") }) == nil {
		t.Fatal("reading an escaped draft must fail after the callback settles")
	}
	if panics(func() { _ = escaped.Set("message", "outside") }) == nil {
		t.Fatal("writing an escaped draft must fail after the callback settles")
	}
	got, err := durable.Snapshot[obj](t.Context(), r.s, live, conversationId)
	if err != nil || got == nil || (*got).Value("message") != "inside" {
		t.Fatalf("snapshot = %v, %v", got, err)
	}
}

// Pi 1.2.0 packages/durable/test/session-documents.test.ts:228 "aborts every change when the callback fails".
func TestCoreACallbackFailureAbortsEveryChange(t *testing.T) {
	r := openCoreDB(t)
	conversationId := r.createConversation(t)
	live := defineConversationDoc("test.abort.live")
	counter := defineDoc("test.abort.counter", 1, func() obj { return delta.JsonObjectOf("count", 0) })
	r.commit(t, func(tx durable.Tx) error { return touch2(tx, live, conversationId) })
	beforeLive, beforeCounter := r.revisions(t, "test.abort.live"), r.revisions(t, "test.abort.counter")
	err := r.fail(t, func(tx durable.Tx) error {
		l, err := tx.Doc(live, conversationId)
		if err != nil {
			return err
		}
		c, err := tx.Doc(counter)
		if err != nil {
			return err
		}
		if err := l.Set("message", "lost"); err != nil {
			return err
		}
		if err := c.Set("count", 5); err != nil {
			return err
		}
		return errors.New("callback failed")
	})
	if !strings.Contains(err.Error(), "callback failed") {
		t.Fatalf("err = %v", err)
	}
	eq(t, "live revisions", r.revisions(t, "test.abort.live"), beforeLive)
	eq(t, "counter revisions", r.revisions(t, "test.abort.counter"), beforeCounter)
	if got, err := durable.Snapshot[obj](t.Context(), r.s, counter); err != nil || got != nil {
		t.Fatalf("the counter created in the failed transaction does not exist: %v, %v", got, err)
	}
	r.commit(t, func(tx durable.Tx) error {
		l, err := tx.Doc(live, conversationId)
		if err != nil {
			return err
		}
		return l.Set("message", "kept")
	})
	if got, err := durable.Snapshot[obj](t.Context(), r.s, live, conversationId); err != nil || got == nil || (*got).Value("message") != "kept" {
		t.Fatalf("snapshot = %v, %v", got, err)
	}
}

// Pi 1.2.0 packages/durable/test/session-documents.test.ts:352 "rejects Tx use after the callback settles".
func TestCoreTxUseAfterTheCallbackSettlesIsRejected(t *testing.T) {
	r := openCoreDB(t)
	conversationId := r.createConversation(t)
	live := defineConversationDoc("test.settled")
	var captured durable.Tx
	r.commit(t, func(tx durable.Tx) error { captured = tx; return nil })
	if _, err := captured.Doc(live, conversationId); err == nil || !strings.Contains(err.Error(), "Transaction has settled") {
		t.Fatalf("doc after settlement: %v", err)
	}
	if _, err := captured.Conversation(conversationId); err == nil || !strings.Contains(err.Error(), "Transaction has settled") {
		t.Fatalf("conversation after settlement: %v", err)
	}
}

// Pi 1.2.0 packages/durable/test/session-documents.test.ts:380 "rejects non-JSON initializer values and draft placements before Storage admission": the Go forms
// of a value that is not strict JSON are NaN and a channel; nothing reaches the store.
func TestCoreNonJSONPlacementsAreRejectedBeforeAnythingIsStored(t *testing.T) {
	r := openCoreDB(t)
	conversationId := r.createConversation(t)
	live := defineConversationDoc("test.json")
	before := r.revisions(t, "test.json")
	for name, value := range map[string]any{"NaN": math.NaN(), "channel": make(chan int)} {
		err := r.fail(t, func(tx durable.Tx) error {
			o, err := tx.Doc(live, conversationId)
			if err != nil {
				return err
			}
			_, err = o.Array("items").Push(value)
			return err
		})
		t.Logf("%s placement rejected: %v", name, err)
	}
	eq(t, "revisions", r.revisions(t, "test.json"), before)
	if got, err := durable.Snapshot[obj](t.Context(), r.s, live, conversationId); err != nil || got != nil {
		t.Fatalf("the document of the failed transactions does not exist: %v, %v", got, err)
	}
	bad := durable.DefineDoc(durable.DocDefinition[obj]{
		CommonDocDefinition: durable.CommonDocDefinition[obj]{Kind: "test.badinit", Version: 1, Initial: func() obj { return delta.JsonObjectOf("at", math.NaN()) }, CheckpointWhen: func(obj, []durable.Op, durable.CheckpointInfo) bool { return false }},
		DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeSession},
	})
	if err := r.fail(t, func(tx durable.Tx) error { return touch2(tx, bad) }); err == nil {
		t.Fatal("a non-JSON initial value must reject the access")
	}
}

func forkDoc(kind string, history durable.DocumentHistory, fork durable.DocumentFork) durable.DocToken[obj] {
	return durable.DefineDoc(durable.DocDefinition[obj]{
		CommonDocDefinition: durable.CommonDocDefinition[obj]{Kind: kind, Version: 1, Initial: func() obj { return delta.JsonObjectOf("value", "initial") }, CheckpointWhen: func(obj, []durable.Op, durable.CheckpointInfo) bool { return false }},
		DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: history, Fork: fork},
	})
}

func forkFamily(kind string, history durable.DocumentHistory, fork durable.DocumentFork) durable.DocFamilyToken[obj, string] {
	return durable.DefineDocFamily(durable.DocFamilyDefinition[obj, string]{
		Kind:              kind,
		Version:           1,
		DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeConversation, History: history, Fork: fork},
		Initial:           func(seed string) obj { return delta.JsonObjectOf("value", seed) },
		Family:            true,
		CheckpointWhen:    func(obj, []durable.Op, durable.CheckpointInfo) bool { return false },
	})
}

func setDoc(tx durable.Tx, token durable.AnyDocToken, value string, args ...any) error {
	o, err := tx.Doc(token, args...)
	if err != nil {
		return err
	}
	return o.Set("value", value)
}

// Pi 1.2.0 packages/durable/test/session-forks.test.ts:29 "copies as-of and current singleton and family bases while leaving initial documents absent".
func TestCoreForkCopiesAsOfAndCurrentSingletonAndFamilyBasesAndLeavesInitialDocumentsAbsent(t *testing.T) {
	r := openCoreDB(t)
	asOf := forkDoc("fork.policies.as-of", durable.HistoryRewindable, durable.ForkAsOf)
	current := forkDoc("fork.policies.current", durable.HistoryLatest, durable.ForkCurrent)
	initial := forkDoc("fork.policies.initial", durable.HistoryLatest, durable.ForkInitial)
	asOfFamily := forkFamily("fork.policies.as-of-family", durable.HistoryRewindable, durable.ForkAsOf)
	currentFamily := forkFamily("fork.policies.current-family", durable.HistoryLatest, durable.ForkCurrent)
	parentId := r.createConversation(t)
	var forkAt durable.EntryId
	r.commit(t, func(tx durable.Tx) error {
		e, err := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "fork-point"})
		if err != nil {
			return err
		}
		forkAt = e.Id
		for _, step := range []func() error{
			func() error { return setDoc(tx, asOf, "as-at-fork", parentId) },
			func() error { return setDoc(tx, current, "current-at-fork", parentId) },
			func() error { return setDoc(tx, initial, "parent-only", parentId) },
			func() error { return setDoc(tx, asOfFamily, "family-as-a", parentId, "a", "unused") },
			func() error { return setDoc(tx, asOfFamily, "family-as-b", parentId, "b", "unused") },
			func() error { return setDoc(tx, currentFamily, "family-current-a", parentId, "a", "unused") },
		} {
			if err := step(); err != nil {
				return err
			}
		}
		return nil
	})
	r.commit(t, func(tx durable.Tx) error {
		for _, step := range []func() error{
			func() error { return setDoc(tx, asOf, "as-after-fork", parentId) },
			func() error { return setDoc(tx, current, "current-when-copied", parentId) },
			func() error { return setDoc(tx, asOfFamily, "family-as-after", parentId, "a", "unused") },
			func() error { return setDoc(tx, currentFamily, "family-current-when-copied", parentId, "a", "unused") },
		} {
			if err := step(); err != nil {
				return err
			}
		}
		return nil
	})
	var child durable.ConversationRecord
	r.commit(t, func(tx durable.Tx) error {
		var err error
		child, err = tx.ForkConversation(parentId, forkAt, durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		return err
	})
	value := func(token durable.AnyDocToken, args ...any) string {
		t.Helper()
		var raw durable.JsonObject
		var err error
		switch tk := token.(type) {
		case durable.DocToken[obj]:
			var got *obj
			got, err = durable.Snapshot[obj](t.Context(), r.s, tk, args...)
			if got != nil {
				raw = *got
			}
		case durable.DocFamilyToken[obj, string]:
			var got *obj
			got, err = durable.Snapshot[obj](t.Context(), r.s, tk, args...)
			if got != nil {
				raw = *got
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		if raw == nil {
			return "<absent>"
		}
		return fmt.Sprint(raw.Value("value"))
	}
	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{"as-of", value(asOf, child.Id), "as-at-fork"},
		{"current", value(current, child.Id), "current-when-copied"},
		{"initial", value(initial, child.Id), "<absent>"},
		{"as-of family a", value(asOfFamily, child.Id, "a"), "family-as-a"},
		{"as-of family b", value(asOfFamily, child.Id, "b"), "family-as-b"},
		{"current family a", value(currentFamily, child.Id, "a"), "family-current-when-copied"},
	} {
		if tc.got != tc.want {
			t.Fatalf("%s child = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	r.commit(t, func(tx durable.Tx) error { return setDoc(tx, asOf, "child-independent", child.Id) })
	if got := value(asOf, parentId); got != "as-after-fork" {
		t.Fatalf("parent after the child's write = %q", got)
	}
	if got := value(asOf, child.Id); got != "child-independent" {
		t.Fatalf("child = %q", got)
	}
	r.commit(t, func(tx durable.Tx) error { return setDoc(tx, initial, "child-created", child.Id) })
	if got := value(initial, child.Id); got != "child-created" {
		t.Fatalf("initial child = %q", got)
	}
}

// Pi 1.2.0 packages/durable/test/session-forks.test.ts:382 "rejects invisible fork points before admission and remains usable".
func TestCoreForkRejectsInvisibleForkPointsAndRemainsUsable(t *testing.T) {
	r := openCoreDB(t)
	rootId := r.createConversation(t)
	var visible, hidden durable.EntryId
	r.commit(t, func(tx durable.Tx) error {
		v, err := tx.AppendEntry(rootId, durable.EntryDraft{Kind: "visible"})
		if err != nil {
			return err
		}
		h, err := tx.AppendEntry(rootId, durable.EntryDraft{Kind: "hidden"})
		visible, hidden = v.Id, h.Id
		return err
	})
	var parent durable.ConversationRecord
	r.commit(t, func(tx durable.Tx) error {
		var err error
		parent, err = tx.ForkConversation(rootId, visible, durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		return err
	})
	err := r.fail(t, func(tx durable.Tx) error {
		_, ferr := tx.ForkConversation(parent.Id, hidden, durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		return ferr
	})
	if want := fmt.Sprintf("Entry %d is not visible", hidden); !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if id := r.createConversation(t); id == 0 {
		t.Fatal("the Session stays usable")
	}
}

// Pi 1.2.0 packages/durable/test/session-forks.test.ts:449 "rejects current and as-of source writes in the fork transaction".
func TestCoreForkRejectsCurrentAndAsOfSourceWritesInTheForkTransaction(t *testing.T) {
	r := openCoreDB(t)
	current := forkDoc("fork.same-transaction-current", durable.HistoryLatest, durable.ForkCurrent)
	asOf := forkDoc("fork.same-transaction-as-of", durable.HistoryRewindable, durable.ForkAsOf)
	parentId := r.createConversation(t)
	var forkAt durable.EntryId
	r.commit(t, func(tx durable.Tx) error {
		e, err := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "point"})
		if err != nil {
			return err
		}
		forkAt = e.Id
		if err := touch2(tx, current, parentId); err != nil {
			return err
		}
		return touch2(tx, asOf, parentId)
	})
	fork := func(tx durable.Tx) error {
		_, err := tx.ForkConversation(parentId, forkAt, durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		return err
	}
	for name, change := range map[string]func(tx durable.Tx) error{
		"current write before the fork": func(tx durable.Tx) error {
			if err := setDoc(tx, current, "before-fork", parentId); err != nil {
				return err
			}
			return fork(tx)
		},
		"current write after the fork": func(tx durable.Tx) error {
			if err := fork(tx); err != nil {
				return err
			}
			return setDoc(tx, current, "after-fork", parentId)
		},
		"as-of write before the fork": func(tx durable.Tx) error {
			if err := setDoc(tx, asOf, "as-of-write", parentId); err != nil {
				return err
			}
			return fork(tx)
		},
	} {
		err := r.fail(t, change)
		if !strings.Contains(err.Error(), "Cannot change fork source document") {
			t.Fatalf("%s: %v", name, err)
		}
	}
	got, err := durable.Snapshot[obj](t.Context(), r.s, current, parentId)
	if err != nil || got == nil || (*got).Value("value") != "initial" {
		t.Fatalf("parent current = %v, %v", got, err)
	}
	var child durable.ConversationRecord
	r.commit(t, func(tx durable.Tx) error {
		var ferr error
		child, ferr = tx.ForkConversation(parentId, forkAt, durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		return ferr
	})
	if got, err := durable.Snapshot[obj](t.Context(), r.s, current, child.Id); err != nil || got == nil || (*got).Value("value") != "initial" {
		t.Fatalf("child current = %v, %v", got, err)
	}
}

// Pi 1.2.0 packages/durable/test/session-forks.test.ts:584 "retires a copied document and can recreate the address in the fork transaction".
func TestCoreForkRetiresACopiedDocumentAndRecreatesTheAddressInTheForkTransaction(t *testing.T) {
	r := openCoreDB(t)
	doc := forkDoc("fork.retire-recreate", durable.HistoryRewindable, durable.ForkAsOf)
	parentId := r.createConversation(t)
	var forkAt durable.EntryId
	r.commit(t, func(tx durable.Tx) error {
		e, err := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "point"})
		if err != nil {
			return err
		}
		forkAt = e.Id
		return setDoc(tx, doc, "copied", parentId)
	})
	var child durable.ConversationRecord
	r.commit(t, func(tx durable.Tx) error {
		var err error
		child, err = tx.ForkConversation(parentId, forkAt, durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		if err != nil {
			return err
		}
		if err := tx.RetireDoc(doc, child.Id); err != nil {
			return err
		}
		return setDoc(tx, doc, "replacement", child.Id)
	})
	if got, err := durable.Snapshot[obj](t.Context(), r.s, doc, child.Id); err != nil || got == nil || (*got).Value("value") != "replacement" {
		t.Fatalf("child = %v, %v", got, err)
	}
	if got, err := durable.Snapshot[obj](t.Context(), r.s, doc, parentId); err != nil || got == nil || (*got).Value("value") != "copied" {
		t.Fatalf("parent = %v, %v", got, err)
	}
}

// Pi 1.2.0 packages/durable/test/session-forks.test.ts:667 "copies every family member across storage scan pages".
func TestCoreForkCopiesEveryFamilyMemberAcrossScanPages(t *testing.T) {
	r := openCoreDB(t)
	family := durable.DefineDocFamily(durable.DocFamilyDefinition[obj, int]{
		Kind:              "fork.pagination",
		Version:           1,
		DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkCurrent},
		Initial:           func(seed int) obj { return delta.JsonObjectOf("value", seed) },
		Family:            true,
		CheckpointWhen:    func(obj, []durable.Op, durable.CheckpointInfo) bool { return false },
	})
	parentId := r.createConversation(t)
	var forkAt durable.EntryId
	r.commit(t, func(tx durable.Tx) error {
		e, err := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "point"})
		forkAt = e.Id
		return err
	})
	const members = 260
	r.commit(t, func(tx durable.Tx) error {
		for i := range members {
			if err := touch2(tx, family, parentId, fmt.Sprintf("member-%d", i), i); err != nil {
				return err
			}
		}
		return nil
	})
	var child durable.ConversationRecord
	r.commit(t, func(tx durable.Tx) error {
		var err error
		child, err = tx.ForkConversation(parentId, forkAt, durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		return err
	})
	for _, i := range []int{0, 1, 99, 100, 101, 199, 200, 259} {
		got, err := durable.Snapshot[obj](t.Context(), r.s, family, child.Id, fmt.Sprintf("member-%d", i))
		if err != nil || got == nil || toInt((*got).Value("value")) != i {
			t.Fatalf("member-%d = %v, %v", i, got, err)
		}
	}
	rows, _, err := r.db.All(t.Context(), `SELECT COUNT(*) FROM documents WHERE kind = '"fork.pagination"' AND owner_id = ?`, []abi.Value{abi.Int(int64(child.Id))})
	if err != nil || len(rows) != 1 || rows[0][0].Int != members {
		t.Fatalf("copied members = %v, %v; want %d", rows, err, members)
	}
}

func versionedFork(kind string, version int, initial func() obj, options ...docOption) durable.DocToken[obj] {
	common := durable.CommonDocDefinition[obj]{Kind: kind, Version: version, CheckpointWhen: func(obj, []durable.Op, durable.CheckpointInfo) bool { return false }}
	for _, o := range options {
		o(&common)
	}
	return durable.DefineDoc(durable.DocDefinition[obj]{
		CommonDocDefinition: withInitial(common, initial),
		DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryRewindable, Fork: durable.ForkAsOf},
	})
}

func (r *coreRig) forkOf(t *testing.T, parentId durable.ConversationId, at durable.EntryId, change func(tx durable.Tx, child durable.ConversationRecord) error) durable.ConversationRecord {
	t.Helper()
	var child durable.ConversationRecord
	r.commit(t, func(tx durable.Tx) error {
		var err error
		child, err = tx.ForkConversation(parentId, at, durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		if err != nil || change == nil {
			return err
		}
		return change(tx, child)
	})
	return child
}

// Pi 1.2.0 packages/durable/test/session-forks.test.ts:230 "copies the stored value and version without consulting migration definitions or migrated caches".
func TestCoreForkCopiesTheStoredVersionWithoutConsultingMigrations(t *testing.T) {
	r := openCoreDB(t)
	migrations := 0
	v1 := versionedFork("fork.stored-version", 1, func() obj { return delta.JsonObjectOf("count", 1) })
	v3 := versionedFork("fork.stored-version", 3, func() obj { return delta.JsonObjectOf("count", 0, "migrated", false) },
		withMigrate(func(v obj, from int) obj {
			if from != 1 {
				t.Errorf("fromVersion = %d, want 1", from)
			}
			migrations++
			return delta.JsonObjectOf("count", v.Value("count"), "migrated", true)
		}))
	parentId := r.createConversation(t)
	var forkAt durable.EntryId
	r.commit(t, func(tx durable.Tx) error {
		e, err := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "point"})
		if err != nil {
			return err
		}
		forkAt = e.Id
		return touch2(tx, v1, parentId)
	})
	if got, err := durable.Snapshot[obj](t.Context(), r.s, v3, parentId); err != nil || got == nil || plain(*got) != "map[count:1 migrated:true]" {
		t.Fatalf("parent = %v, %v", got, err)
	}
	eq(t, "migrations after the parent read", migrations, 1)
	child := r.forkOf(t, parentId, forkAt, nil)
	eq(t, "migrations after the fork", migrations, 1)
	rows, _, err := r.db.All(t.Context(), `SELECT r.version, r.content FROM document_revisions r JOIN documents d ON d.id = r.document_id WHERE d.kind = '"fork.stored-version"' AND d.owner_id = ?`, []abi.Value{abi.Int(int64(child.Id))})
	if err != nil || len(rows) != 1 || rows[0][0].Int != 1 || !strings.Contains(string(rows[0][1].Bytes), `"count":1`) {
		t.Fatalf("the copied base keeps version 1: %v %v", rows, err)
	}
	if got, err := durable.Snapshot[obj](t.Context(), r.s, v3, child.Id); err != nil || got == nil || plain(*got) != "map[count:1 migrated:true]" {
		t.Fatalf("child = %v, %v", got, err)
	}
	eq(t, "migrations after the child read", migrations, 2)
}

// Pi 1.2.0 packages/durable/test/session-forks.test.ts:280 "coalesces a typed migration and override into the copied creation base".
func TestCoreForkCoalescesATypedMigrationAndOverrideIntoTheCopiedCreationBase(t *testing.T) {
	r := openCoreDB(t)
	checkpoints := 0
	v1 := versionedFork("fork.override", 1, func() obj { return delta.JsonObjectOf("count", 2) })
	v2 := versionedFork("fork.override", 2, func() obj { return delta.JsonObjectOf("count", 0, "migrated", false) },
		withMigrate(func(v obj, _ int) obj { return delta.JsonObjectOf("count", v.Value("count"), "migrated", true) }),
		withCheckpoint(func(obj, []durable.Op, durable.CheckpointInfo) bool { checkpoints++; return false }))
	parentId := r.createConversation(t)
	var forkAt durable.EntryId
	r.commit(t, func(tx durable.Tx) error {
		e, err := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "point"})
		if err != nil {
			return err
		}
		forkAt = e.Id
		return touch2(tx, v1, parentId)
	})
	child := r.forkOf(t, parentId, forkAt, func(tx durable.Tx, child durable.ConversationRecord) error {
		o, err := tx.Doc(v2, child.Id)
		if err != nil {
			return err
		}
		return o.Set("count", 9)
	})
	eq(t, "checkpoint predicate calls", checkpoints, 0)
	rows, _, err := r.db.All(t.Context(), `SELECT r.kind, r.version, r.content FROM document_revisions r JOIN documents d ON d.id = r.document_id WHERE d.kind = '"fork.override"' AND d.owner_id = ?`, []abi.Value{abi.Int(int64(child.Id))})
	if err != nil || len(rows) != 1 || string(rows[0][0].Bytes) != "base" || rows[0][1].Int != 2 ||
		!strings.Contains(string(rows[0][2].Bytes), `"count":9`) || !strings.Contains(string(rows[0][2].Bytes), `"migrated":true`) {
		t.Fatalf("one creation base of version 2 with count 9 and migrated true: %v %v", rows, err)
	}
	if got, err := durable.Snapshot[obj](t.Context(), r.s, v2, child.Id); err != nil || got == nil || plain(*got) != "map[count:9 migrated:true]" {
		t.Fatalf("child = %v, %v", got, err)
	}
	prow, _, err := r.db.All(t.Context(), `SELECT r.version FROM document_revisions r JOIN documents d ON d.id = r.document_id WHERE d.kind = '"fork.override"' AND d.owner_id = ?`, []abi.Value{abi.Int(int64(parentId))})
	if err != nil || len(prow) == 0 || prow[len(prow)-1][0].Int != 1 {
		t.Fatalf("the parent stays at version 1: %v %v", prow, err)
	}
}

func (r *coreRig) appendEntry(t *testing.T, conversationId durable.ConversationId, kind string, also func(tx durable.Tx) error) durable.EntryId {
	t.Helper()
	var id durable.EntryId
	r.commit(t, func(tx durable.Tx) error {
		e, err := tx.AppendEntry(conversationId, durable.EntryDraft{Kind: kind})
		if err != nil {
			return err
		}
		id = e.Id
		if also != nil {
			return also(tx)
		}
		return nil
	})
	return id
}

func (r *coreRig) valueOf(t *testing.T, token durable.DocToken[obj], conversationId durable.ConversationId) string {
	t.Helper()
	got, err := durable.Snapshot[obj](t.Context(), r.s, token, conversationId)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		return "<absent>"
	}
	return fmt.Sprint((*got).Value("value"))
}

// Pi 1.2.0 packages/durable/test/session-forks.test.ts:151 "uses final document state from the fork entry commit while excluding later same-commit entries".
func TestCoreForkUsesTheFinalDocumentStateOfTheEntryCommitAndExcludesLaterSameCommitEntries(t *testing.T) {
	r := openCoreDB(t)
	doc := forkDoc("fork.same-commit", durable.HistoryRewindable, durable.ForkAsOf)
	parentId := r.createConversation(t)
	var forkAt, excluded durable.EntryId
	r.commit(t, func(tx durable.Tx) error {
		e, err := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "included"})
		if err != nil {
			return err
		}
		forkAt = e.Id
		if err := setDoc(tx, doc, "final-state-of-commit", parentId); err != nil {
			return err
		}
		x, err := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "excluded"})
		excluded = x.Id
		return err
	})
	child := r.forkOf(t, parentId, forkAt, nil)
	if got := r.valueOf(t, doc, child.Id); got != "final-state-of-commit" {
		t.Fatalf("child = %q", got)
	}
	r.commit(t, func(tx durable.Tx) error {
		page, err := tx.ScanEntries(durable.EntryQuery{ConversationId: child.Id}, 10, nil)
		if err != nil {
			return err
		}
		var ids []durable.EntryId
		for _, e := range page.Items {
			ids = append(ids, e.Id)
		}
		if !slices.Contains(ids, forkAt) || slices.Contains(ids, excluded) {
			t.Errorf("visible entries %v must contain %d and not %d", ids, forkAt, excluded)
		}
		return nil
	})
}

// Pi 1.2.0 packages/durable/test/session-forks.test.ts:179 "selects the entry-owning ancestor for as-of copies and the immediate parent for current copies".
func TestCoreForkSelectsTheEntryOwningAncestorForAsOfAndTheImmediateParentForCurrent(t *testing.T) {
	r := openCoreDB(t)
	asOf := forkDoc("fork.ancestry.as-of", durable.HistoryRewindable, durable.ForkAsOf)
	current := forkDoc("fork.ancestry.current", durable.HistoryLatest, durable.ForkCurrent)
	rootId := r.createConversation(t)
	inherited := r.appendEntry(t, rootId, "root", func(tx durable.Tx) error {
		if err := setDoc(tx, asOf, "root-at-entry", rootId); err != nil {
			return err
		}
		return setDoc(tx, current, "root-current", rootId)
	})
	parent := r.forkOf(t, rootId, inherited, nil)
	parentEntry := r.appendEntry(t, parent.Id, "parent", func(tx durable.Tx) error {
		if err := setDoc(tx, asOf, "parent-at-own-entry", parent.Id); err != nil {
			return err
		}
		return setDoc(tx, current, "parent-current", parent.Id)
	})
	inheritedFork := r.forkOf(t, parent.Id, inherited, nil)
	eq(t, "inherited as-of", r.valueOf(t, asOf, inheritedFork.Id), "root-at-entry")
	eq(t, "inherited current", r.valueOf(t, current, inheritedFork.Id), "parent-current")
	ownFork := r.forkOf(t, parent.Id, parentEntry, nil)
	eq(t, "own-entry as-of", r.valueOf(t, asOf, ownFork.Id), "parent-at-own-entry")
	eq(t, "own-entry current", r.valueOf(t, current, ownFork.Id), "parent-current")
}

// Pi 1.2.0 packages/durable/test/session-forks.test.ts:338 "copies the incarnation alive at the fork point across retirement and recreation".
func TestCoreForkCopiesTheIncarnationAliveAtTheForkPoint(t *testing.T) {
	r := openCoreDB(t)
	doc := forkDoc("fork.incarnations", durable.HistoryRewindable, durable.ForkAsOf)
	parentId := r.createConversation(t)
	oldAt := r.appendEntry(t, parentId, "old", func(tx durable.Tx) error { return setDoc(tx, doc, "old", parentId) })
	retiredAt := r.appendEntry(t, parentId, "retired", func(tx durable.Tx) error { return tx.RetireDoc(doc, parentId) })
	newAt := r.appendEntry(t, parentId, "new", func(tx durable.Tx) error { return setDoc(tx, doc, "new", parentId) })
	oldChild := r.forkOf(t, parentId, oldAt, nil)
	emptyChild := r.forkOf(t, parentId, retiredAt, nil)
	newChild := r.forkOf(t, parentId, newAt, nil)
	eq(t, "old", r.valueOf(t, doc, oldChild.Id), "old")
	eq(t, "retired", r.valueOf(t, doc, emptyChild.Id), "<absent>")
	eq(t, "new", r.valueOf(t, doc, newChild.Id), "new")
}

// Pi 1.2.0 packages/durable/test/session-forks.test.ts:411 "rejects duplicate as-of and current selections for one child address before admission".
func TestCoreForkRejectsDuplicateAsOfAndCurrentSelectionsForOneChildAddress(t *testing.T) {
	r := openCoreDB(t)
	asOf := forkDoc("fork.duplicate-policy", durable.HistoryRewindable, durable.ForkAsOf)
	current := forkDoc("fork.duplicate-policy", durable.HistoryLatest, durable.ForkCurrent)
	parentId := r.createConversation(t)
	oldAt := r.appendEntry(t, parentId, "old", func(tx durable.Tx) error { return touch2(tx, asOf, parentId) })
	r.commit(t, func(tx durable.Tx) error { return tx.RetireDoc(asOf, parentId) })
	r.commit(t, func(tx durable.Tx) error { return touch2(tx, current, parentId) })
	err := r.fail(t, func(tx durable.Tx) error {
		_, ferr := tx.ForkConversation(parentId, oldAt, durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		return ferr
	})
	if !strings.Contains(err.Error(), "Fork selects multiple source documents") {
		t.Fatalf("err = %v", err)
	}
	if id := r.createConversation(t); id == 0 {
		t.Fatal("the Session stays usable")
	}
}

func (r *coreRig) conversationCount(t *testing.T) int64 {
	t.Helper()
	rows, _, err := r.db.All(t.Context(), `SELECT COUNT(*) FROM conversations`, nil)
	if err != nil || len(rows) != 1 {
		t.Fatalf("count conversations: %v %v", rows, err)
	}
	return rows[0][0].Int
}

// Pi 1.2.0 packages/durable/test/session-forks.test.ts:613 "copies only conversation documents, leaving Session and task documents
// in their original scopes" needs task documents (Tx.setTask), which the public Tx does not offer; it is ported against the
// TS-shaped Session in durable/session/session_forks_test.go.
//
// Pi 1.2.0 packages/durable/test/session-forks.test.ts:502 "rolls every copied base back when later pre-admission assembly fails": the checkpoint predicate of a
// later document panics at flush, after the fork op; no conversation, copy, or revision of the transaction survives.
func TestCoreForkRollsEveryCopiedBaseBackWhenLaterAssemblyFails(t *testing.T) {
	r := openCoreDB(t)
	copied := forkDoc("fork.rollback.copied", durable.HistoryRewindable, durable.ForkAsOf)
	failCheckpoint := true
	failure := defineDoc("fork.rollback.failure", 1, func() obj { return delta.JsonObjectOf("count", 0) },
		withCheckpoint(func(obj, []durable.Op, durable.CheckpointInfo) bool {
			if failCheckpoint {
				panic(errors.New("checkpoint failed"))
			}
			return false
		}))
	parentId := r.createConversation(t)
	forkAt := r.appendEntry(t, parentId, "point", func(tx durable.Tx) error {
		if err := touch2(tx, copied, parentId); err != nil {
			return err
		}
		return touch2(tx, failure)
	})
	conversations := r.conversationCount(t)
	revisions := r.revisions(t, "fork.rollback.copied")
	err := r.fail(t, func(tx durable.Tx) error {
		if _, err := tx.ForkConversation(parentId, forkAt, durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}}); err != nil {
			return err
		}
		o, err := tx.Doc(failure)
		if err != nil {
			return err
		}
		return o.Set("count", 1)
	})
	if !strings.Contains(err.Error(), "checkpoint failed") {
		t.Fatalf("err = %v", err)
	}
	eq(t, "conversations", r.conversationCount(t), conversations)
	eq(t, "copied revisions", r.revisions(t, "fork.rollback.copied"), revisions)
	failCheckpoint = false
	r.commit(t, func(tx durable.Tx) error {
		o, err := tx.Doc(failure)
		if err != nil {
			return err
		}
		return o.Set("count", 2)
	})
	if got, err := durable.Snapshot[obj](t.Context(), r.s, failure); err != nil || got == nil || toInt((*got).Value("count")) != 2 {
		t.Fatalf("failure doc = %v, %v", got, err)
	}
	eq(t, "parent copy", r.valueOf(t, copied, parentId), "initial")
}

// failRevisionsOfKind makes the store refuse every revision insert of one document kind, the way a failing disk would refuse the
// commit's writes: the SQL error reaches the core as a failed Storage commit.
func (r *coreRig) failRevisionsOfKind(t *testing.T, kind, message string) {
	t.Helper()
	trigger := fmt.Sprintf(`CREATE TRIGGER fail_%s BEFORE INSERT ON document_revisions WHEN (SELECT kind FROM documents WHERE id = NEW.document_id) = '"%s"' BEGIN SELECT RAISE(ABORT, '%s'); END`,
		strings.NewReplacer(".", "_", "-", "_").Replace(kind), kind, message)
	if _, err := r.db.Run(t.Context(), trigger, nil); err != nil {
		t.Fatal(err)
	}
}

// Pi 1.2.0 packages/durable/test/session-documents.test.ts:440 "fails the Session after a Storage failure and publishes nothing". The
// core's handle is fatal after a failed commit (ABI section 4.6); the driver Session reports it as Pi's failed Session: the commit
// that failed returns the store's own error, every later call returns Pi's SessionFailed (a *durable.SessionFailed that also
// matches the core's ErrSessionFailed) whose cause is that failure, and nothing is published.
func TestCoreAnUncertainStorageFailurePoisonsTheSession(t *testing.T) {
	r := openCoreDB(t)
	conversationId := r.createConversation(t)
	live := defineConversationDoc("test.poison")
	r.commit(t, func(tx durable.Tx) error { return touch2(tx, live, conversationId) })
	published := make(chan durable.CommitPublication, 8)
	off := r.s.SubscribeCommits(func(_ context.Context, p durable.CommitPublication) { published <- p })
	defer off()
	r.failRevisionsOfKind(t, "test.poison", "disk vanished")
	err := r.fail(t, func(tx durable.Tx) error {
		o, err := tx.Doc(live, conversationId)
		if err != nil {
			return err
		}
		return o.Set("message", "uncertain")
	})
	if !strings.Contains(err.Error(), "disk vanished") {
		t.Fatalf("the commit that failed returns the store's error: %v", err)
	}
	select {
	case p := <-published:
		t.Fatalf("a failed commit publishes nothing: %+v", p)
	case <-time.After(200 * time.Millisecond):
	}
	const piText = "Session failed after a storage error; close and reopen it"
	poisoned := func(what string, err error) {
		t.Helper()
		var typed *durable.SessionFailed
		if err == nil || !errors.Is(err, coresession.ErrSessionFailed) || !errors.As(err, &typed) || err.Error() != piText {
			t.Fatalf("%s after the failure = %v, want Pi's SessionFailed", what, err)
		}
		if typed.Cause == nil || !strings.Contains(typed.Cause.Error(), "disk vanished") {
			t.Fatalf("%s: the SessionFailed's cause is the store's failure, got %v", what, typed.Cause)
		}
	}
	_, err = durable.Snapshot[obj](t.Context(), r.s, live, conversationId)
	poisoned("snapshot", err)
	poisoned("commit", r.fail(t, func(durable.Tx) error { return nil }))
	_, err = r.s.GetTask(t.Context(), 1)
	poisoned("getTask", err)
}

// Pi 1.2.0 packages/durable/test/session-forks.test.ts:552 "fails the Session on a rejected fork commit, which leaves no effect".
// The native Session has no replaceable Storage, so the rejection is a fault injected below the core's store: a trigger refuses
// the fork's child conversation row. The commit that failed returns the store's error, the next commit gets Pi's SessionFailed
// caused by it, and the database holds nothing of the batch. Pi's `session.closed` outcome is asserted on the TS-shaped
// Session in durable/session/session_forks_test.go; the driver Session exposes no closed outcome.
func TestCoreARejectedForkCommitFailsTheSessionAndLeavesNoEffect(t *testing.T) {
	r := openCoreDB(t)
	doc := forkDoc("fork.storage-rejected", durable.HistoryRewindable, durable.ForkAsOf)
	parentId := r.createConversation(t)
	forkAt := r.appendEntry(t, parentId, "point", func(tx durable.Tx) error { return touch2(tx, doc, parentId) })
	conversations := r.conversationCount(t)
	if _, err := r.db.Run(t.Context(), `CREATE TRIGGER fail_fork BEFORE INSERT ON conversations BEGIN SELECT RAISE(ABORT, 'copy rejected'); END`, nil); err != nil {
		t.Fatal(err)
	}
	err := r.fail(t, func(tx durable.Tx) error {
		_, err := tx.ForkConversation(parentId, forkAt, durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		return err
	})
	if !strings.Contains(err.Error(), "copy rejected") {
		t.Fatalf("the commit that failed returns the store's error: %v", err)
	}
	err = r.fail(t, func(tx durable.Tx) error {
		_, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		return err
	})
	failed, ok := errors.AsType[*durable.SessionFailed](err)
	if !ok || failed.Name() != "SessionFailed" || failed.Cause == nil || !strings.Contains(failed.Cause.Error(), "copy rejected") {
		t.Fatalf("the next commit = %v, want Pi's SessionFailed caused by the rejection", err)
	}
	eq(t, "conversations", r.conversationCount(t), conversations)
}

// commitHold parks the next commit applied after arm in the observer until release.
type commitHold struct {
	mu      sync.Mutex
	armed   bool
	entered chan struct{}
	release chan struct{}
}

func newCommitHold() *commitHold {
	return &commitHold{entered: make(chan struct{}), release: make(chan struct{})}
}

func (h *commitHold) arm() {
	h.mu.Lock()
	h.armed = true
	h.mu.Unlock()
}

func (h *commitHold) observe(abi.Commit, []string) {
	h.mu.Lock()
	armed := h.armed
	h.armed = false
	h.mu.Unlock()
	if armed {
		close(h.entered)
		<-h.release
	}
}

func (r *coreRig) snapshotCopy(t *testing.T, token durable.DocToken[obj], conversationId durable.ConversationId) (obj, string) {
	t.Helper()
	got, err := durable.Snapshot[obj](t.Context(), r.s, token, conversationId)
	if err != nil || got == nil {
		t.Fatalf("snapshot = %v, %v", got, err)
	}
	encoded, err := json.Marshal(*got)
	if err != nil {
		t.Fatal(err)
	}
	return *got, string(encoded)
}

// Pi 1.2.0 packages/durable/test/session-documents.test.ts:459 "keeps the previous revision unchanged through Storage settlement": a snapshot the caller holds
// never changes under a later commit, while the commit is held in Storage and after it settled.
func TestCoreASnapshotStaysUnchangedThroughALaterCommit(t *testing.T) {
	hold := newCommitHold()
	r := openCoreRig(t, nil, hold.observe)
	conversationId := r.createConversation(t)
	live := defineConversationDoc("test.snapshot-stable")
	r.commit(t, func(tx durable.Tx) error {
		o, err := tx.Doc(live, conversationId)
		if err != nil {
			return err
		}
		_, err = o.Array("items").Push("a", "b")
		return err
	})
	before, copyOf := r.snapshotCopy(t, live, conversationId)
	hold.arm()
	committed := make(chan error, 1)
	go func() {
		_, err := r.s.Commit(t.Context(), func(tx durable.Tx) (any, error) {
			o, err := tx.Doc(live, conversationId)
			if err != nil {
				return nil, err
			}
			_, err = o.Array("items").Push("c")
			return nil, err
		})
		committed <- err
	}()
	<-hold.entered
	if encoded, _ := json.Marshal(before); string(encoded) != copyOf {
		t.Fatalf("the held snapshot changed while the commit is in Storage: %s", encoded)
	}
	close(hold.release)
	if err := <-committed; err != nil {
		t.Fatal(err)
	}
	after, _ := r.snapshotCopy(t, live, conversationId)
	if fmt.Sprint(after.Value("items")) != "[a b c]" {
		t.Fatalf("after = %v", after)
	}
	if encoded, _ := json.Marshal(before); string(encoded) != copyOf {
		t.Fatalf("the held snapshot changed after the commit settled: %s", encoded)
	}
}

// Pi 1.2.0 packages/durable/test/session-documents.test.ts:549 "retires the existing incarnation when retirement races a pending acquisition", in the Go form of
// the same sequence: acquire, retire, recreate in one transaction. The recreated draft is a new object and the commit writes
// the old document's retirement and one creation.
func TestCoreRetirementBetweenTwoAcquisitionsCreatesAFreshIncarnation(t *testing.T) {
	r := openCoreDB(t)
	conversationId := r.createConversation(t)
	live := defineConversationDoc("test.race")
	r.commit(t, func(tx durable.Tx) error { return setDoc(tx, live, "second", conversationId) })
	r.commit(t, func(tx durable.Tx) error {
		acquired, err := tx.Doc(live, conversationId)
		if err != nil {
			return err
		}
		if err := tx.RetireDoc(live, conversationId); err != nil {
			return err
		}
		fresh, err := tx.Doc(live, conversationId)
		if err != nil {
			return err
		}
		if fresh == acquired {
			t.Error("the recreated draft is a new object")
		}
		return fresh.Set("message", "third")
	})
	got, err := durable.Snapshot[obj](t.Context(), r.s, live, conversationId)
	if err != nil || got == nil || (*got).Value("message") != "third" {
		t.Fatalf("snapshot = %v, %v", got, err)
	}
}

// Pi 1.2.0 packages/durable/test/session-documents.test.ts:622 "publishes close synchronously and supports unsubscription".
func TestCoreCloseListenersRunOnceAndSupportUnsubscription(t *testing.T) {
	r := openCoreDB(t)
	var mu sync.Mutex
	var calls []string
	record := func(name string) func() {
		return func() {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, name)
		}
	}
	r.s.SubscribeClose(record("active"))
	unsubscribe := r.s.SubscribeClose(record("removed"))
	unsubscribe()
	if err := r.s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	eq(t, "close calls", calls, []string{"active"})
}

// Pi 1.2.0 packages/durable/test/session-documents.test.ts:632 "settles admitted commits before close and rejects later admission": a commit held in Storage and
// one queued behind it settle, a snapshot queued before the close answers, and anything admitted after Close began is
// rejected as closed.
func TestCoreCloseSettlesAdmittedCommitsAndRejectsLaterAdmission(t *testing.T) {
	hold := newCommitHold()
	r := openCoreRig(t, nil, hold.observe)
	conversationId := r.createConversation(t)
	live := defineConversationDoc("test.close-admission")
	member := defineDoc("test.close-absent", 1, func() obj { return delta.JsonObjectOf() })
	r.commit(t, func(tx durable.Tx) error { return touch2(tx, live, conversationId) })
	hold.arm()
	commitAs := func(message string) chan error {
		done := make(chan error, 1)
		go func() {
			_, err := r.s.Commit(t.Context(), func(tx durable.Tx) (any, error) { return nil, setDoc(tx, live, message, conversationId) })
			done <- err
		}()
		return done
	}
	admitted := commitAs("admitted")
	<-hold.entered
	queued := commitAs("queued")
	snapshot := make(chan error, 1)
	go func() {
		got, err := durable.Snapshot[obj](t.Context(), r.s, member)
		if err == nil && got != nil {
			err = errors.New("the absent member must not exist")
		}
		snapshot <- err
	}()
	// The held commit, the queued commit and the snapshot are all admitted before Close begins.
	waitFor(t, "three calls admitted", func() bool { return r.s.Admitted() == 3 })
	began := make(chan struct{})
	r.s.SubscribeClose(func() { close(began) })
	closed := make(chan error, 1)
	go func() { closed <- r.s.Close(context.Background()) }()
	select {
	case <-began:
	case <-time.After(15 * time.Second):
		t.Fatal("Close never began")
	}
	if _, err := r.s.Commit(t.Context(), func(durable.Tx) (any, error) { return nil, nil }); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("a commit after Close began = %v", err)
	}
	if _, err := durable.Snapshot[obj](t.Context(), r.s, live, conversationId); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("a snapshot after Close began = %v", err)
	}
	close(hold.release)
	for name, done := range map[string]chan error{"admitted": admitted, "queued": queued, "snapshot": snapshot, "close": closed} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		case <-time.After(15 * time.Second):
			t.Fatalf("%s never settled", name)
		}
	}
}

// withInitial is common with its Initial set.
func withInitial[T any](common durable.CommonDocDefinition[T], initial func() T) durable.CommonDocDefinition[T] {
	common.Initial = initial
	return common
}

// plain prints a JSON value with each ordered object as a Go map, so the expectation does not depend on key order.
func plain(value any) string { return fmt.Sprint(plainValue(value)) }

func plainValue(value any) any {
	switch typed := value.(type) {
	case *delta.JsonObject:
		if typed == nil {
			return map[string]any(nil)
		}
		out := make(map[string]any, typed.Len())
		for key, member := range typed.All() {
			out[key] = plainValue(member)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, member := range typed {
			out[i] = plainValue(member)
		}
		return out
	}
	return value
}
