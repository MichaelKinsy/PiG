package host

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// The host halves of packages/chord/test/next/documents/keyed-slot.test.ts (the documents package ports the rest): the
// cases whose assertions are the host's (`self.state(...).at` outside the factory, `object_failed` reports, a reference's
// failed state) run on a restartable object of a whole host.

// ksDefine is a keyed state "notes.note"/"notes.memo" of version over schema.
func ksDefine(name string, version int, schema contracts.TSchema, persist bool, migrate func(*delta.JsonObject, int) (*delta.JsonObject, error)) *documents.StateDefinition {
	options := documents.StateOptions{
		Schema: schema, Version: version, Keyed: true, Persist: persist, Migrate: migrate,
		Initial: func() *delta.JsonObject {
			if schema.Node == ksTwo.Node {
				return jsonObject("title", "", "n", "")
			}
			return jsonObject("title", "")
		},
	}
	if persist {
		options.CheckpointWhen = func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false }
	}
	definition, err := documents.DefineState(name, options)
	if err != nil {
		panic(err)
	}
	return definition
}

var (
	ksTitle = contracts.Obj(contracts.Prop("title", contracts.Str()))
	// ksTwo is the schema other code stored the documents with.
	ksTwo = contracts.Obj(contracts.Prop("title", contracts.Str()), contracts.Prop("n", contracts.Str()))
)

// ksPersisted is `persisted(store)`: StoreRestartable declaring definition per attempt; notes is the latest attempt's handle.
type ksPersisted struct {
	*restartableRunner
	handle atomic.Pointer[documents.Keyed]
}

func (p *ksPersisted) notes() *documents.Keyed { return p.handle.Load() }

func ksPersistedOver(t *testing.T, st store.Store, definition *documents.StateDefinition) *ksPersisted {
	t.Helper()
	p := &ksPersisted{}
	p.restartableRunner = storeRestartable(t, func(_ int, self StoreSelf) {
		p.handle.Store(self.State(definition).(*documents.Keyed))
	}, loadedOptions{Store: st})
	return p
}

// ksSet changes the title of key.
func ksSet(t testing.TB, notes *documents.Keyed, key, title string) {
	t.Helper()
	_, err := notes.Change(key, func(draft *delta.Object) (any, error) { return nil, draft.Set("title", title) }, background)
	must(t, err)
}

// ksSeed writes `entries` into st's `notes.note` documents through a host of its own.
func ksSeed(t *testing.T, st store.Store, definition *documents.StateDefinition, entries map[string]string) {
	t.Helper()
	p := ksPersistedOver(t, st, definition)
	for key, title := range entries {
		ksSet(t, p.notes(), key, title)
	}
	must(t, p.Host.Close(background))
}

// ksObjectFailed asserts the host reported exactly count object_failed errors and the reference is failed broken.
func ksObjectFailed(t *testing.T, errs *hostErrors, reference *Reference, count int) {
	t.Helper()
	var kinds []HostErrorKind
	for _, each := range errs.All() {
		kinds = append(kinds, each.Kind)
	}
	if len(kinds) != count {
		t.Fatalf("host errors = %v, want %d object_failed", kinds, count)
	}
	for _, kind := range kinds {
		if kind != ErrObjectFailed {
			t.Fatalf("host errors = %v, want only object_failed", kinds)
		}
	}
	if got := reference.State().Value(); got.Kind != RefStateFailed || got.Cause.Kind != data.FailureBroken {
		t.Fatalf("reference state = %+v, want failed broken", got)
	}
}

// keyed-slot.test.ts › at(key) › only in the factory.
func TestKeyedSlotAtKeyOnlyInTheFactory(t *testing.T) {
	memoDefinition := ksDefine("notes.memo", 1, ksTitle, false, nil)
	var memos atomic.Pointer[documents.Keyed]
	runner := restartable(t, func(_ int, self Self) {
		memos.Store(self.State(memoDefinition).(*documents.Keyed))
	}, loadedOptions{})
	func() {
		defer func() {
			recovered := recover()
			if recovered == nil || !strings.Contains(fmtAny(recovered), "valid only in the factory") {
				t.Fatalf("at(key) outside the factory = %v, want it to throw valid only in the factory", recovered)
			}
		}()
		_, _ = memos.Load().At("a")
	}()
	must(t, runner.Host.Close(background))
}

func fmtAny(value any) string {
	if err, ok := value.(error); ok {
		return err.Error()
	}
	if text, ok := value.(string); ok {
		return text
	}
	return "unknown panic"
}

// keyed-slot.test.ts › migration on access › a newer version migrates each in-memory key when it is read; a key whose
// migrate fails fails the owner then.
func TestKeyedSlotMigrationOnAccessANewerVersionMigratesEachInMemoryKeyWhenItIsReadAKeyWhoseMigrateFailsFailsTheOwnerThen(t *testing.T) {
	var mu sync.Mutex
	var migrated []string
	memoV1 := ksDefine("notes.memo", 1, ksTitle, false, nil)
	memoV2 := ksDefine("notes.memo", 2, ksTitle, false, func(old *delta.JsonObject, _ int) (*delta.JsonObject, error) {
		title := old.Value("title").(string)
		if title == "bad" {
			return nil, errors.New("cannot migrate")
		}
		mu.Lock()
		migrated = append(migrated, title)
		mu.Unlock()
		return jsonObject("title", title+"!"), nil
	})
	var memos []*documents.Keyed
	runner := restartable(t, func(attempt int, self Self) {
		definition := memoV2
		if attempt == 1 {
			definition = memoV1
		}
		mu.Lock()
		memos = append(memos, self.State(definition).(*documents.Keyed))
		mu.Unlock()
	}, loadedOptions{})
	handle := func(index int) *documents.Keyed {
		mu.Lock()
		defer mu.Unlock()
		return memos[index]
	}
	seen := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), migrated...)
	}
	for _, key := range []string{"a", "b", "c"} {
		title := key
		if key == "c" {
			title = "bad"
		}
		ksSet(t, handle(0), key, title)
	}
	runner.Restart()
	// The activation migrated nothing: no key was read.
	nexttest.RequireJSONEqual(t, seen(), []string(nil))
	value, err := handle(1).Get("a", background)
	must(t, err)
	nexttest.RequireJSONEqual(t, value, jsonObject("title", "a!"))
	if got := seen(); len(got) != 1 || got[0] != "a" {
		t.Fatalf("migrated = %v, want [a]", got)
	}
	_, err = runner.Attempts.at(1).Tx(func(tx *documents.Transaction) (any, error) {
		draft, err := tx.Draft(handle(1), "b")
		if err != nil {
			return nil, err
		}
		nexttest.RequireJSONEqual(t, draft.Snapshot(), jsonObject("title", "b!"))
		return nil, nil
	}, background)
	must(t, err)
	if got := seen(); len(got) != 2 || got[1] != "b" {
		t.Fatalf("migrated = %v, want [a b]", got)
	}
	if _, err := handle(1).Get("c", background); err == nil || !strings.Contains(err.Error(), "cannot migrate") {
		t.Fatalf("get c = %v, want it to reject with cannot migrate", err)
	}
	ksObjectFailed(t, runner.Errors, runner.Reference, 2)
}

// keyed-slot.test.ts › cold keyed loads on the line › tx.retire of a cold key stored at a newer version / at another
// fingerprint of its version fails the owner broken as a read would and keeps it (R15, audit).
func TestKeyedSlotColdKeyedLoadsOnTheLineTxRetireOfAColdKeyStoredAtAFailsTheOwnerBrokenAsAReadWouldAndKeepsItR15Audit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version int
		message string
	}{
		{"a newer version", 2, "stored at version 2, newer than the code's 1"},
		{"another fingerprint at its version", 1, "stored with another schema at version 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := memoryStore(t, true)
			// Written by other code: a newer version, or the same version with another schema.
			other := ksDefine("notes.note", tc.version, ksTwo, true, nil)
			var written atomic.Pointer[documents.Keyed]
			writer := storeRestartable(t, func(_ int, self StoreSelf) {
				written.Store(self.State(other).(*documents.Keyed))
			}, loadedOptions{Store: st})
			_, err := written.Load().Change("a", func(draft *delta.Object) (any, error) {
				if err := draft.Set("title", "kept"); err != nil {
					return nil, err
				}
				return nil, draft.Set("n", "1")
			}, background)
			must(t, err)
			must(t, writer.Host.Close(background))
			p := ksPersistedOver(t, st, ksDefine("notes.note", 1, ksTitle, true, nil))
			if err := p.notes().Retire("a", background); err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("retire = %v, want it to reject with %q", err, tc.message)
			}
			ksObjectFailed(t, p.Errors, p.Reference, 1)
			must(t, p.Host.Close(background))
			reader := storeRestartable(t, func(_ int, self StoreSelf) { self.State(other) }, loadedOptions{Store: st})
			rows, err := store.Read(st, func(executor sqlite.SqliteExecutor) ([]sqlite.SqliteRow, error) {
				return executor.All("SELECT key FROM chord_documents")
			}, background)
			must(t, err)
			if len(rows) != 1 || rows[0]["key"] != "a" {
				t.Fatalf("rows = %v, want [{key: a}]", rows)
			}
			must(t, reader.Host.Close(background))
		})
	}
}

// keyed-slot.test.ts › cold keyed loads on the line › a retirement cut by its attempt's seal while it finds the document
// fails no successor (audit). Pi replaces `store.read` for one call; Go holds the first read of the store once armed.
func TestKeyedSlotColdKeyedLoadsOnTheLineARetirementCutByItsAttemptsSealWhileItFindsTheDocumentFailsNoSuccessorAudit(t *testing.T) {
	gate := newReadGate()
	st := openMemoryStore(t, func(db sqlite.SqliteDatabase) sqlite.SqliteDatabase {
		return &readGateDatabase{SqliteDatabase: db, gate: gate}
	}, []bool{true})
	t.Cleanup(gate.Release)
	newer := ksDefine("notes.note", 2, ksTitle, true, nil)
	var written atomic.Pointer[documents.Keyed]
	writer := storeRestartable(t, func(_ int, self StoreSelf) { written.Store(self.State(newer).(*documents.Keyed)) }, loadedOptions{Store: st})
	ksSet(t, written.Load(), "a", "kept")
	must(t, writer.Host.Close(background))
	p := ksPersistedOver(t, st, ksDefine("notes.note", 1, ksTitle, true, nil))
	// The find's read is held past the seal: the retirement resumes on a cut transaction.
	gate.armed.Store(true)
	retired := make(chan error, 1)
	go func() { retired <- p.notes().Retire("a", background) }()
	<-gate.entered
	p.Fail()
	p.Retry()
	gate.Release()
	err := <-retired
	if chordErr, ok := data.IsChordErrorCode(err, data.CodeReleased); !ok || chordErr == nil {
		t.Fatalf("retire = %v, want it to reject released", err)
	}
	must(t, p.Host.Close(background))
}

// keyed-slot.test.ts › cold keyed loads on the line › a plugin migrate throwing a ChordError fails its owner like any other
// error.
func TestKeyedSlotColdKeyedLoadsOnTheLineAPluginMigrateThrowingAChordErrorFailsItsOwnerLikeAnyOtherError(t *testing.T) {
	st := memoryStore(t, true)
	ksSeed(t, st, ksDefine("notes.note", 1, ksTitle, true, nil), map[string]string{"a": "x"})
	v2 := ksDefine("notes.note", 2, ksTitle, true, func(*delta.JsonObject, int) (*delta.JsonObject, error) {
		return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "plugin says no"}, nil)
	})
	p := ksPersistedOver(t, st, v2)
	if _, err := p.notes().Get("a", background); err == nil || err.Error() != "plugin says no" {
		t.Fatalf("get = %v, want plugin says no", err)
	}
	ksObjectFailed(t, p.Errors, p.Reference, 1)
	must(t, p.Host.Close(background))
}

// keyed-slot.test.ts › cold keyed loads on the line › a key whose migrate fails fails its owner broken; the get rejects with
// the cause.
func TestKeyedSlotColdKeyedLoadsOnTheLineAKeyWhoseMigrateFailsFailsItsOwnerBrokenTheGetRejectsWithTheCause(t *testing.T) {
	st := memoryStore(t, true)
	ksSeed(t, st, ksDefine("notes.note", 1, ksTitle, true, nil), map[string]string{"a": "x"})
	v2 := ksDefine("notes.note", 2, ksTitle, true, func(*delta.JsonObject, int) (*delta.JsonObject, error) {
		return nil, errors.New("cannot migrate")
	})
	p := ksPersistedOver(t, st, v2)
	_, err := p.notes().Get("a", background)
	chordErr, ok := data.IsChordErrorCode(err, data.CodeUnavailable)
	if !ok || chordErr == nil || !strings.Contains(chordErr.Message, "cannot migrate") {
		t.Fatalf("get = %v, want unavailable with cannot migrate", err)
	}
	ksObjectFailed(t, p.Errors, p.Reference, 1)
	must(t, p.Host.Close(background))
}
