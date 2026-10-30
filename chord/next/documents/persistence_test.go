package documents

// Ports packages/chord/test/next/documents/persistence.test.ts
//
// Persisted documents' store side (slice 6a §8, §10): loads with the version and fingerprint checks and `migrate`,
// finds, keys, and the writes one commit stages (creation, base or delta by `checkpointWhen`, retirement).

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

func noteSchema() contracts.TSchema {
	return contracts.Obj(contracts.Prop("title", contracts.Str()), contracts.Prop("n", contracts.Int()))
}

func noteInitial() *JsonObject { return obj("title", "", "n", 0.0) }

func noCheckpoint(*JsonObject, []delta.Op, CheckpointInfo) bool { return false }

func newNote(t *testing.T) *StateDefinition {
	return mustDefine(t, "notes.note", StateOptions{
		Schema: noteSchema(), Version: 1, Initial: noteInitial, Persist: true,
		CheckpointWhen: func(_ *JsonObject, _ []delta.Op, info CheckpointInfo) bool { return info.DeltasSinceBase >= 2 },
	})
}

// persistFixture is the store (with an outbox) and the Persistence of "notes.note" under "app.store"/"chord".
type persistFixture struct {
	t     *testing.T
	store *store.ChordStore
	notes *Persistence
	note  *StateDefinition
}

func newPersistFixture(t *testing.T) *persistFixture {
	t.Helper()
	chordStore := memoryStore(t, true)
	return &persistFixture{t: t, store: chordStore, notes: NewPersistence(chordStore, "app.store", "chord", "notes.note"), note: newNote(t)}
}

// change stages the change of `before` to `after` at `key` and commits it; it returns what is stored afterwards.
func (f *persistFixture) change(key string, before *JsonObject, after func(draft *delta.Object), stored *Stored, definition ...*StateDefinition) *Stored {
	f.t.Helper()
	def := f.note
	if len(definition) > 0 {
		def = definition[0]
	}
	if before == nil {
		var err error
		if before, err = def.Initial(background); err != nil {
			f.t.Fatal(err)
		}
	}
	tracker := delta.Track(before)
	draft := tracker.BeginChange()
	after(draft.State())
	prepared, err := draft.Prepare()
	if err != nil {
		f.t.Fatal(err)
	}
	var writes []store.StagedWrite
	result, err := f.notes.Stage(key, def, stored, DocumentChange{Prepared: prepared}, &writes, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	fence := store.CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64) error { return nil }}
	if _, err := f.store.Commit(writes, fence, background); err != nil {
		f.t.Fatal(err)
	}
	if err := tracker.Adopt(prepared); err != nil {
		f.t.Fatal(err)
	}
	return StoredAfter(result)
}

// load is the document at key read from the store and decoded by definition, nil when absent.
func (f *persistFixture) load(key string, definition *StateDefinition, at context.Context) (*Loaded, error) {
	document, err := f.notes.Read(key, at)
	if err != nil || document == nil {
		return nil, err
	}
	loaded, err := f.notes.Decode(document, definition, at)
	if err != nil {
		return nil, err
	}
	return &loaded, nil
}

func (f *persistFixture) mustLoad(key string, definition *StateDefinition) *Loaded {
	f.t.Helper()
	loaded, err := f.load(key, definition, background)
	if err != nil || loaded == nil {
		f.t.Fatalf("load(%q) = %v, %v", key, loaded, err)
	}
	return loaded
}

// outboxKinds is the kinds of the outbox's writes, per commit.
func (f *persistFixture) outboxKinds() [][]string {
	f.t.Helper()
	commits, err := f.store.Outbox().Read(0, 100, background)
	if err != nil {
		f.t.Fatal(err)
	}
	kinds := make([][]string, len(commits))
	for i, commit := range commits {
		kinds[i] = []string{}
		for _, write := range commit.Writes {
			kind := write.StoreWriteKind()
			if change, ok := write.(*store.DocumentChange); ok {
				kind = "change:" + change.Content.Kind
			}
			kinds[i] = append(kinds[i], kind)
		}
	}
	return kinds
}

func setN(n float64) func(*delta.Object) {
	return func(d *delta.Object) {
		if err := d.Set("n", n); err != nil {
			panic(err)
		}
	}
}

func setTitle(title string) func(*delta.Object) {
	return func(d *delta.Object) {
		if err := d.Set("title", title); err != nil {
			panic(err)
		}
	}
}

func requireErrorContaining(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want one containing %q", err, want)
	}
}

func requireValue(t *testing.T, got *JsonObject, want string) {
	t.Helper()
	if text := nexttest.JSONText(t, got); text != want {
		t.Errorf("value = %s, want %s", text, want)
	}
}

func TestLoadsAnAbsentDocumentAsNilAndAStoredOneWithItsRecord(t *testing.T) {
	f := newPersistFixture(t)
	if loaded, err := f.load("a", f.note, background); loaded != nil || err != nil {
		t.Fatalf("absent = %v, %v", loaded, err)
	}
	stored := f.change("a", nil, setTitle("x"), nil)
	meter := nexttest.Metered(t)
	loaded, err := f.load("a", f.note, meter.Context)
	if err != nil || loaded == nil {
		t.Fatalf("load = %v, %v", loaded, err)
	}
	requireValue(t, loaded.Value, `{"title":"x","n":0}`)
	if want := (Stored{ID: stored.ID, Revision: 1, Version: 1, DeltasSinceBase: 0}); loaded.Stored != want {
		t.Errorf("stored = %+v, want %+v", loaded.Stored, want)
	}
	if got := meter.Counts()["chord.store.loads"]; got != 1 {
		t.Errorf("chord.store.loads = %v, want 1", got)
	}
}

func TestLoadsMigrateAnOlderStoredVersionAndCheckTheResultOnce(t *testing.T) {
	f := newPersistFixture(t)
	v1 := mustDefine(t, "notes.note", StateOptions{
		Schema: contracts.Obj(contracts.Prop("title", contracts.Str())), Version: 1,
		Initial: func() *JsonObject { return obj("title", "") }, Persist: true, CheckpointWhen: noCheckpoint,
	})
	f.change("a", nil, setTitle("x"), nil, v1)
	v2 := mustDefine(t, "notes.note", StateOptions{
		Schema: noteSchema(), Version: 2, Initial: noteInitial,
		Migrate: func(old *JsonObject, _ int) (*JsonObject, error) {
			return obj("title", old.Value("title").(string), "n", 7.0), nil
		},
		Persist: true, CheckpointWhen: noCheckpoint,
	})
	meter := nexttest.Metered(t)
	loaded, err := f.load("a", v2, meter.Context)
	if err != nil || loaded == nil {
		t.Fatalf("load = %v, %v", loaded, err)
	}
	requireValue(t, loaded.Value, `{"title":"x","n":7}`)
	if loaded.Stored.Version != 1 {
		t.Errorf("stored version = %d, want 1", loaded.Stored.Version)
	}
	if got := meter.Counts()["chord.data.validations"]; got != 1 {
		t.Errorf("validations = %v, want 1", got)
	}
	bad := mustDefine(t, "notes.note", StateOptions{
		Schema: noteSchema(), Version: 2, Initial: noteInitial,
		// A migrate returning a wrong shape: the load is refused.
		Migrate: func(old *JsonObject, _ int) (*JsonObject, error) {
			return obj("title", old.Value("title").(string)), nil
		},
		Persist: true, CheckpointWhen: noCheckpoint,
	})
	_, err = f.load("a", bad, background)
	requireErrorContaining(t, err, "migrate() from version 1 does not match")
}

func TestLoadsRefuseAStoredVersionNewerThanTheCodesAndTheSameVersionWithAnotherFingerprint(t *testing.T) {
	f := newPersistFixture(t)
	f.change("a", nil, setTitle("x"), nil)
	old := mustDefine(t, "notes.note", StateOptions{
		Schema: noteSchema(), Version: 0, Initial: noteInitial, Persist: true, CheckpointWhen: noCheckpoint,
	})
	_, err := f.load("a", old, background)
	requireErrorContaining(t, err, "stored at version 1, newer than the code's 0")
	edited := mustDefine(t, "notes.note", StateOptions{
		Schema:  contracts.Obj(contracts.Prop("title", contracts.Str()), contracts.Prop("n", contracts.Int()), contracts.Prop("extra", contracts.Str())),
		Version: 1, Initial: func() *JsonObject { return obj("title", "", "n", 0.0, "extra", "") },
		Persist: true, CheckpointWhen: noCheckpoint,
	})
	_, err = f.load("a", edited, background)
	requireErrorContaining(t, err, "stored with another schema at version 1")
}

func TestFindALiveRecordWithoutLoadingItAndListKeysInCodePointOrderByPages(t *testing.T) {
	f := newPersistFixture(t)
	if found, err := f.notes.Find("a", background); found != nil || err != nil {
		t.Fatalf("absent find = %v, %v", found, err)
	}
	for _, key := range []string{"b", "\U0001F600", "a", "\uFFFF"} {
		f.change(key, nil, setN(1), nil)
	}
	found, err := f.notes.Find("a", background)
	if err != nil || found == nil {
		t.Fatalf("find = %v, %v", found, err)
	}
	if found.Stored.Revision != 1 || found.Stored.Version != 1 || found.Fingerprint == "" {
		t.Errorf("found = %+v", found)
	}
	keys, err := f.notes.Keys(nil, 10, background)
	if err != nil || !reflect.DeepEqual(keys, []string{"a", "b", "\uFFFF", "\U0001F600"}) {
		t.Errorf("keys = %q, %v", keys, err)
	}
	after := "b"
	keys, err = f.notes.Keys(&after, 1, background)
	if err != nil || !reflect.DeepEqual(keys, []string{"\uFFFF"}) {
		t.Errorf("keys after b = %q, %v", keys, err)
	}
}

func TestStagedWritesStoreACreationAsABaseChangesAsDeltasUntilCheckpointWhenAsksForABase(t *testing.T) {
	f := newPersistFixture(t)
	stored := f.change("a", nil, setN(1), nil)
	if stored.Revision != 1 || stored.DeltasSinceBase != 0 {
		t.Fatalf("stored = %+v", stored)
	}
	value := f.mustLoad("a", f.note).Value
	for _, n := range []float64{2, 3, 4} {
		stored = f.change("a", value, setN(n), stored)
		value = f.mustLoad("a", f.note).Value
	}
	if stored.Revision != 4 || stored.DeltasSinceBase != 0 {
		t.Errorf("stored = %+v, want revision 4 and 0 deltas", stored)
	}
	requireValue(t, value, `{"title":"","n":4}`)
	want := [][]string{{"document.create"}, {"change:delta"}, {"change:delta"}, {"change:base"}}
	if got := f.outboxKinds(); !reflect.DeepEqual(got, want) {
		t.Errorf("outbox = %v, want %v", got, want)
	}
	if got := f.mustLoad("a", f.note).Stored.DeltasSinceBase; got != 0 {
		t.Errorf("deltasSinceBase = %d, want 0", got)
	}
}

func TestStagedWritesStoreABaseWhenTheStoredVersionIsOlderThanTheDefinitions(t *testing.T) {
	f := newPersistFixture(t)
	stored := f.change("a", nil, setN(1), nil)
	v2 := mustDefine(t, "notes.note", StateOptions{
		Schema: noteSchema(), Version: 2, Initial: noteInitial,
		Migrate: func(old *JsonObject, _ int) (*JsonObject, error) { return old, nil },
		Persist: true, CheckpointWhen: noCheckpoint,
	})
	after := f.change("a", obj("title", "", "n", 1.0), setN(2), stored, v2)
	if after.Version != 2 || after.Revision != 2 || after.DeltasSinceBase != 0 {
		t.Errorf("after = %+v", after)
	}
	kinds := f.outboxKinds()
	if got := kinds[len(kinds)-1]; !reflect.DeepEqual(got, []string{"change:base"}) {
		t.Errorf("last commit = %v", got)
	}
	loaded := f.mustLoad("a", v2)
	if loaded.Value.Value("n") != 2.0 || loaded.Stored.Version != 2 {
		t.Errorf("loaded = %s, %+v", nexttest.JSONText(t, loaded.Value), loaded.Stored)
	}
}

func TestStagedWritesStageARetirementBeforeARecreationAndNumberCreationsByTheirOrderInTheCommit(t *testing.T) {
	f := newPersistFixture(t)
	stored := f.change("a", nil, setN(1), nil)
	var writes []store.StagedWrite
	fresh := func(n float64) *delta.Prepared {
		initial, err := f.note.Initial(background)
		if err != nil {
			t.Fatal(err)
		}
		draft := delta.Track(initial).BeginChange()
		setN(n)(draft.State())
		prepared, err := draft.Prepare()
		if err != nil {
			t.Fatal(err)
		}
		return prepared
	}
	stage := func(key string, stored *Stored, change DocumentChange) StoredChange {
		result, err := f.notes.Stage(key, f.note, stored, change, &writes, nil)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := stage("b", nil, DocumentChange{Prepared: fresh(2)})
	again := stage("a", stored, DocumentChange{Prepared: fresh(3), Retires: true})
	gone := stage("c", nil, DocumentChange{Retires: true})
	if first.Kind != StoredCreate || first.Version != 1 || again.Kind != StoredCreate || again.Version != 1 || gone.Kind != StoredRetire {
		t.Errorf("staged = %+v %+v %+v", first, again, gone)
	}
	var ids []int64
	fence := store.CommitFence{Admit: func() bool { return true }, Adopt: func(_ int64, created []int64) error { ids = created; return nil }}
	if _, err := f.store.Commit(writes, fence, background); err != nil {
		t.Fatal(err)
	}
	if want := []int64{stored.ID + 1, stored.ID + 2}; !reflect.DeepEqual(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
	if got, want := *StoredAfter(again), (Stored{ID: stored.ID + 2, Revision: 1, Version: 1, DeltasSinceBase: 0}); got != want {
		t.Errorf("storedAfter = %+v, want %+v", got, want)
	}
	kinds := f.outboxKinds()
	if got, want := kinds[len(kinds)-1], []string{"document.retire", "document.create", "document.create"}; !reflect.DeepEqual(got, want) {
		t.Errorf("last commit = %v, want %v", got, want)
	}
}

func TestStagedWritesSerializeAtStagingAValueJSONCannotEncodeFailsThereNeverInTheStep(t *testing.T) {
	f := newPersistFixture(t)
	// Drafts refuse such values when placed; a prepared change made by hand shows where the text is made. Upstream's
	// `1n` (a BigInt, which JSON.stringify refuses with a TypeError) is a value no JSON text holds: Go's is a channel,
	// in a document that is tracked as it is and prepared unchanged, so its value is the one staged.
	value := obj("title", "", "n", make(chan int))
	change := delta.Track(value).BeginChange()
	prepared, err := change.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	var writes []store.StagedWrite
	_, err = f.notes.Stage("a", f.note, nil, DocumentChange{Prepared: prepared}, &writes, nil)
	if err == nil {
		t.Fatal("Stage accepted a value JSON cannot encode")
	}
	if len(writes) != 0 {
		t.Errorf("writes = %d, want none staged", len(writes))
	}
}
