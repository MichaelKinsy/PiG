package host

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Host halves of packages/chord/test/next/documents/keyed-slot.test.ts (D4 rows): the cases whose assertions are the host's
// (`self.state(...).at` outside the factory, the `object_failed` reports and the failed state of the reference that a
// failing keyed document causes). The documents halves are in chord/next/documents/keyed_slot_test.go. They run on a real
// host through restartable and storeRestartable, as Pi's `restartable` and `storeRestartable`.

var keyedTitleSchema = contracts.Obj(contracts.Prop("title", contracts.Str()))

// keyedDefinition is the keyed `notes.note` (or `notes.memo`) of the upstream file at version.
func keyedDefinition(t testing.TB, name string, version int, persist bool, schema contracts.TSchema, migrate func(*delta.JsonObject, int) (*delta.JsonObject, error), initials ...func() *delta.JsonObject) *documents.StateDefinition {
	t.Helper()
	initial := func() *delta.JsonObject { return jsonObject("title", "") }
	if len(initials) > 0 {
		initial = initials[0]
	}
	definition, err := documents.DefineState(name, documents.StateOptions{
		Schema: schema, Version: version, Keyed: true, Persist: persist, Migrate: migrate,
		Initial:        initial,
		CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
	})
	must(t, err)
	return definition
}

func setTitle(title string) func(draft *delta.Object) (any, error) {
	return func(draft *delta.Object) (any, error) { return nil, draft.Set("title", title) }
}

// handles collects the keyed handles an object's factories made, one per attempt (the factories run in the host's sections).
type handles struct {
	mu   sync.Mutex
	list []*documents.Keyed
}

func (h *handles) add(handle *documents.Keyed) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.list = append(h.list, handle)
}

func (h *handles) at(index int) *documents.Keyed {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.list[index]
}

// seedKeyed writes `entries` (key, title) into store's `notes.note` documents through a host of its own, closes it, and
// returns the definition's handle of that host (Pi's `seed`).
func seedKeyed(t testing.TB, chordStore *store.ChordStore, note *documents.StateDefinition, entries ...[2]string) {
	t.Helper()
	var made handles
	run := storeRestartable(t, func(_ int, self StoreSelf) { made.add(self.State(note).(*documents.Keyed)) }, loadedOptions{Store: chordStore})
	for _, entry := range entries {
		_, err := made.at(0).Change(entry[0], setTitle(entry[1]), background)
		must(t, err)
	}
	closeHosts(t, run.Host)
}

// keyed-slot.test.ts "at(key) › only in the factory".
func TestKeyedSlotAtKeyOnlyInTheFactory(t *testing.T) {
	memo := keyedDefinition(t, "notes.memo", 1, false, keyedTitleSchema, nil)
	var made handles
	run := restartable(t, func(_ int, self Self) { made.add(self.State(memo).(*documents.Keyed)) }, loadedOptions{})
	// The error is the attempt's: the declaration is valid only while the factory runs.
	if got := panicText(func() { _, _ = made.at(0).At("a") }); !hasText(got, "valid only in the factory") {
		t.Fatalf("at(key) outside the factory = %q, want valid only in the factory", got)
	}
	closeHosts(t, run.Host)
}

// keyed-slot.test.ts "migration on access › a newer version migrates each in-memory key when it is read; a key whose
// migrate fails fails the owner then" (the host half: the reports and the state of the reference).
func TestKeyedSlotMigrationOnAccessANewerVersionMigratesEachInMemoryKeyWhenItIsReadAKeyWhoseMigrateFailsFailsTheOwnerThen(t *testing.T) {
	var migrated strs
	memoV1 := keyedDefinition(t, "notes.memo", 1, false, keyedTitleSchema, nil)
	memoV2 := keyedDefinition(t, "notes.memo", 2, false, keyedTitleSchema, func(old *delta.JsonObject, _ int) (*delta.JsonObject, error) {
		title, _ := old.Value("title").(string)
		if title == "bad" {
			return nil, errors.New("cannot migrate")
		}
		migrated.add(title)
		return jsonObject("title", title+"!"), nil
	})
	var memos handles
	run := restartable(t, func(attempt int, self Self) {
		definition := memoV2
		if attempt == 1 {
			definition = memoV1
		}
		memos.add(self.State(definition).(*documents.Keyed))
	}, loadedOptions{})
	for _, key := range []string{"a", "b", "c"} {
		title := key
		if key == "c" {
			title = "bad"
		}
		_, err := memos.at(0).Change(key, setTitle(title), background)
		must(t, err)
	}
	run.Restart()
	// The activation migrated nothing: no key was read.
	if got := migrated.all(); len(got) != 0 {
		t.Fatalf("migrated = %v, want none", got)
	}
	got, err := memos.at(1).Get("a", background)
	must(t, err)
	nexttest.RequireJSONEqual(t, got, jsonObject("title", "a!"))
	if got := migrated.all(); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("migrated = %v, want [a]", got)
	}
	_, err = run.Attempts.at(1).Tx(func(tx *documents.Transaction) (any, error) {
		draft, err := tx.Draft(memos.at(1), "b")
		if err != nil {
			return nil, err
		}
		nexttest.RequireJSONEqual(t, draft.Snapshot(), jsonObject("title", "b!"))
		return nil, nil
	}, background)
	must(t, err)
	if got := migrated.all(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("migrated = %v, want [a b]", got)
	}
	_, err = memos.at(1).Get("c", background)
	if err == nil || !hasText(err.Error(), "cannot migrate") {
		t.Fatalf("get c = %v, want cannot migrate", err)
	}
	// The induced restart and the failing key: two failures of the owner.
	if got := reportKinds(run.Errors.All()); !reflect.DeepEqual(got, []HostErrorKind{ErrObjectFailed, ErrObjectFailed}) {
		t.Fatalf("report kinds = %v, want [object_failed object_failed]", got)
	}
	failed := untilKind(t, run.Reference, RefStateFailed)
	if failed.Cause.Kind != data.FailureBroken {
		t.Fatalf("state = %+v, want failed broken", failed)
	}
	closeHosts(t, run.Host)
}

// persistedOver is Pi's `persisted(store)`: StoreRestartable over store declaring `notes.note` per attempt.
func persistedOver(t testing.TB, chordStore *store.ChordStore, note *documents.StateDefinition, notes *handles) *restartableRunner {
	t.Helper()
	return storeRestartable(t, func(_ int, self StoreSelf) { notes.add(self.State(note).(*documents.Keyed)) }, loadedOptions{Store: chordStore})
}

// failedBroken is the check Pi's tests end with: one `object_failed` report and the reference failed with a broken cause.
func failedBroken(t testing.TB, run *restartableRunner) {
	t.Helper()
	if got := reportKinds(run.Errors.All()); !reflect.DeepEqual(got, []HostErrorKind{ErrObjectFailed}) {
		t.Fatalf("report kinds = %v, want [object_failed]", got)
	}
	failed := untilKind(t, run.Reference, RefStateFailed)
	if failed.Cause.Kind != data.FailureBroken {
		t.Fatalf("state = %+v, want failed broken", failed)
	}
}

// keyed-slot.test.ts "cold keyed loads on the line › tx.retire of a cold key stored at %s fails the owner broken as a read
// would and keeps it (R15, audit)" (the host half).
func TestKeyedSlotColdKeyedLoadsTxRetireOfAColdKeyStoredAtANewerVersionOrAnotherFingerprintFailsTheOwnerBrokenAsAReadWouldAndKeepsIt(t *testing.T) {
	note := keyedDefinition(t, "notes.note", 1, true, keyedTitleSchema, nil)
	otherSchema := contracts.Obj(contracts.Prop("title", contracts.Str()), contracts.Prop("n", contracts.Str()))
	for _, each := range []struct {
		name    string
		version int
		message string
	}{
		{"a newer version", 2, "stored at version 2, newer than the code's 1"},
		{"another fingerprint at its version", 1, "stored with another schema at version 1"},
	} {
		t.Run(each.name, func(t *testing.T) {
			chordStore := memoryStore(t, true)
			// Written by other code: a newer version, or the same version with another schema.
			other := keyedDefinition(t, "notes.note", each.version, true, otherSchema, nil, func() *delta.JsonObject { return jsonObject("title", "", "n", "") })
			var written handles
			writer := storeRestartable(t, func(_ int, self StoreSelf) { written.add(self.State(other).(*documents.Keyed)) }, loadedOptions{Store: chordStore})
			_, err := written.at(0).Change("a", func(draft *delta.Object) (any, error) {
				if err := draft.Set("title", "kept"); err != nil {
					return nil, err
				}
				return nil, draft.Set("n", "1")
			}, background)
			must(t, err)
			closeHosts(t, writer.Host)
			var notes handles
			run := persistedOver(t, chordStore, note, &notes)
			err = notes.at(0).Retire("a", background)
			if err == nil || !hasText(err.Error(), each.message) {
				t.Fatalf("retire = %v, want %q", err, each.message)
			}
			failedBroken(t, run)
			closeHosts(t, run.Host)
			var read handles
			reader := storeRestartable(t, func(_ int, self StoreSelf) { read.add(self.State(other).(*documents.Keyed)) }, loadedOptions{Store: chordStore})
			rows, err := store.Read(chordStore, func(executor sqlite.SqliteExecutor) ([]sqlite.SqliteRow, error) {
				return executor.All("SELECT key FROM chord_documents")
			}, background)
			must(t, err)
			if len(rows) != 1 || rows[0]["key"] != "a" {
				t.Fatalf("rows = %v, want [{key: a}]", rows)
			}
			closeHosts(t, reader.Host)
		})
	}
}

// findHoldDatabase holds the next query that finds a keyed document, after it ran: Pi's `store.read` wrapper of the case
// below. It is what Pi monkeypatches (the store's read); the document is found when the hold is entered.
type findHoldDatabase struct {
	sqlite.SqliteDatabase
	mu      sync.Mutex
	entered chan struct{}
	gate    chan struct{}
}

func (d *findHoldDatabase) hold() (entered <-chan struct{}, release func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.entered, d.gate = make(chan struct{}), make(chan struct{})
	gate := d.gate
	var once sync.Once
	return d.entered, func() { once.Do(func() { close(gate) }) }
}

func (d *findHoldDatabase) Get(sql string, params ...sqlite.SqliteValue) (sqlite.SqliteRow, error) {
	row, err := d.SqliteDatabase.Get(sql, params...)
	if strings.Contains(sql, "key = ? AND retired_at IS NULL") && !strings.Contains(sql, "created_at <=") {
		d.mu.Lock()
		entered, gate := d.entered, d.gate
		d.entered, d.gate = nil, nil
		d.mu.Unlock()
		if gate != nil {
			close(entered)
			<-gate
		}
	}
	return row, err
}

// keyed-slot.test.ts "cold keyed loads on the line › a retirement cut by its attempt's seal while it finds the document
// fails no successor (audit)" (the host half).
func TestKeyedSlotColdKeyedLoadsARetirementCutByItsAttemptsSealWhileItFindsTheDocumentFailsNoSuccessor(t *testing.T) {
	var db *findHoldDatabase
	chordStore := openMemoryStore(t, func(inner sqlite.SqliteDatabase) sqlite.SqliteDatabase {
		db = &findHoldDatabase{SqliteDatabase: inner}
		return db
	}, []bool{true})
	newer := keyedDefinition(t, "notes.note", 2, true, keyedTitleSchema, nil)
	seedKeyed(t, chordStore, newer, [2]string{"a", "kept"})
	note := keyedDefinition(t, "notes.note", 1, true, keyedTitleSchema, nil)
	var notes handles
	run := persistedOver(t, chordStore, note, &notes)
	// The find's read is held past the seal: the retirement resumes on a cut transaction.
	entered, release := db.hold()
	defer release()
	retired := make(chan error, 1)
	go func() { retired <- notes.at(0).Retire("a", background) }()
	receive(t, entered)
	run.Fail()
	run.Retry()
	release()
	err := receive(t, retired)
	if chordErr, ok := data.IsChordErrorCode(err, data.CodeReleased); !ok {
		t.Fatalf("retire = %v (%v), want a released rejection", err, chordErr)
	}
	turn(run.Host)
	if state := untilKind(t, run.Reference, RefStateLive); state.Kind != RefStateLive {
		t.Fatalf("state = %+v, want live", state)
	}
	objectFailed := 0
	for _, report := range run.Errors.All() {
		if report.Kind == ErrObjectFailed {
			objectFailed++
		}
	}
	if objectFailed != 1 {
		t.Fatalf("object_failed reports = %d, want 1", objectFailed)
	}
	closeHosts(t, run.Host)
}

// keyed-slot.test.ts "cold keyed loads on the line › a plugin migrate throwing a ChordError fails its owner like any other
// error" (the host half).
func TestKeyedSlotColdKeyedLoadsAPluginMigrateThrowingAChordErrorFailsItsOwnerLikeAnyOtherError(t *testing.T) {
	chordStore := memoryStore(t, true)
	seedKeyed(t, chordStore, keyedDefinition(t, "notes.note", 1, true, keyedTitleSchema, nil), [2]string{"a", "x"})
	v2 := keyedDefinition(t, "notes.note", 2, true, keyedTitleSchema, func(*delta.JsonObject, int) (*delta.JsonObject, error) {
		return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "plugin says no"}, nil)
	})
	var notes handles
	run := persistedOver(t, chordStore, v2, &notes)
	_, err := notes.at(0).Get("a", background)
	if err == nil || !hasText(err.Error(), "plugin says no") {
		t.Fatalf("get = %v, want plugin says no", err)
	}
	failedBroken(t, run)
	closeHosts(t, run.Host)
}

// keyed-slot.test.ts "cold keyed loads on the line › a key whose migrate fails fails its owner broken; the get rejects with
// the cause" (the host half).
func TestKeyedSlotColdKeyedLoadsAKeyWhoseMigrateFailsFailsItsOwnerBrokenTheGetRejectsWithTheCause(t *testing.T) {
	chordStore := memoryStore(t, true)
	seedKeyed(t, chordStore, keyedDefinition(t, "notes.note", 1, true, keyedTitleSchema, nil), [2]string{"a", "x"})
	v2 := keyedDefinition(t, "notes.note", 2, true, keyedTitleSchema, func(*delta.JsonObject, int) (*delta.JsonObject, error) {
		return nil, errors.New("cannot migrate")
	})
	var notes handles
	run := persistedOver(t, chordStore, v2, &notes)
	_, err := notes.at(0).Get("a", background)
	if _, ok := data.IsChordErrorCode(err, data.CodeUnavailable); !ok || !hasText(err.Error(), "cannot migrate") {
		t.Fatalf("get = %v, want an unavailable rejection containing cannot migrate", err)
	}
	failedBroken(t, run)
	closeHosts(t, run.Host)
}
