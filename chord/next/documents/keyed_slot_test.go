package documents

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/test/next/documents/keyed-slot.test.ts
//
// Keyed slots (slice 6a §4, §10, §11): keyed reads with a context, per-key watches and `at(key)` sources with their
// pins, persisted keyed documents loaded on demand on the object's line (one load per key, shared), and their
// eviction.
//
// Go stand-ins for what Pi's tests do to the host and the store (the documents tests reach both through `createHost`):
//   - `vi.spyOn(store, "read")` is ksDB.finds: every store read of one key is one find query on the database the
//     store runs on (Persistence.Read and Persistence.Find each make exactly one).
//   - `holdNextRead` is ksDB.holdNextLoad (a document's read held after its load query returned) or, for a retirement's
//     find, ksDB.holdNextRead (held after the find query returned): the Pi helper holds `store.read` after the backend
//     returned the read's result.
//   - `holdNextCommit` holds the next backend transaction (the commit step's) after its writes applied, until released.
//   - A "consumer's" read through the provider's own token (a reference member) is a *Keyed over the object's Own token.

// ---- helpers -----------------------------------------------------------------------------------------------------

// ksHold is a hold on one database call: entered is closed when the call reached it, open lets it go on.
type ksHold struct {
	entered chan struct{}
	gate    chan struct{}
	once    sync.Once
}

func newKsHold() *ksHold { return &ksHold{entered: make(chan struct{}), gate: make(chan struct{})} }

func (h *ksHold) open() { h.once.Do(func() { close(h.gate) }) }

// ksDB is the database a test store runs on: it counts the find queries and holds the next one or the next commit step.
type ksDB struct {
	sqlite.SqliteDatabase
	mu         sync.Mutex
	finds      int
	readHold   *ksHold
	loadHold   *ksHold
	loading    *ksHold
	commitHold *ksHold
}

func ksIsFind(sql string) bool {
	return strings.Contains(sql, "key = ? AND retired_at IS NULL") && !strings.Contains(sql, "created_at <=")
}

func (d *ksDB) Get(sql string, params ...sqlite.SqliteValue) (sqlite.SqliteRow, error) {
	row, err := d.SqliteDatabase.Get(sql, params...)
	if ksIsFind(sql) {
		d.mu.Lock()
		d.finds++
		hold := d.readHold
		d.readHold = nil
		load := d.loadHold
		// A found document is loaded by the same read: its hold waits for the load's query.
		if load != nil && row != nil {
			d.loading, d.loadHold, load = load, nil, nil
		} else {
			d.loadHold = nil
		}
		d.mu.Unlock()
		if hold == nil {
			hold = load
		}
		if hold != nil {
			close(hold.entered)
			<-hold.gate
		}
	}
	return row, err
}

func (d *ksDB) All(sql string, params ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error) {
	rows, err := d.SqliteDatabase.All(sql, params...)
	if strings.Contains(sql, "document_id = ? AND seq <=") {
		d.mu.Lock()
		hold := d.loading
		d.loading = nil
		d.mu.Unlock()
		if hold != nil {
			close(hold.entered)
			<-hold.gate
		}
	}
	return rows, err
}

func (d *ksDB) Transaction(callback func(transaction sqlite.SqliteExecutor) error) error {
	d.mu.Lock()
	hold := d.commitHold
	d.commitHold = nil
	d.mu.Unlock()
	if hold == nil {
		return d.SqliteDatabase.Transaction(callback)
	}
	return d.SqliteDatabase.Transaction(func(transaction sqlite.SqliteExecutor) error {
		err := callback(transaction)
		close(hold.entered)
		<-hold.gate
		return err
	})
}

func (d *ksDB) findCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.finds
}

func (d *ksDB) holdNextRead() *ksHold {
	hold := newKsHold()
	d.mu.Lock()
	d.readHold = hold
	d.mu.Unlock()
	return hold
}

// holdNextLoad holds the next read of a document after the whole read returned: the find and, for a found document, its
// load (Pi's `holdNextRead` holds `store.read` after the backend returned the read's result).
func (d *ksDB) holdNextLoad() *ksHold {
	hold := newKsHold()
	d.mu.Lock()
	d.loadHold = hold
	d.mu.Unlock()
	return hold
}

func (d *ksDB) holdNextCommit() *ksHold {
	hold := newKsHold()
	d.mu.Lock()
	d.commitHold = hold
	d.mu.Unlock()
	return hold
}

// ksFreshStore is a fresh `:memory:` store (with an outbox), closed after the test, and the database it runs on.
func ksFreshStore(t *testing.T) (*store.ChordStore, *ksDB) {
	t.Helper()
	database, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatalf("open :memory:: %v", err)
	}
	db := &ksDB{SqliteDatabase: database}
	outbox := true
	opened, err := store.OpenStore(store.SqliteBackend(db), background, store.StoreOptions{Outbox: &outbox})
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	chordStore := opened.(*store.ChordStore)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = chordStore.Close(ctx)
	})
	return chordStore, db
}

var ksTitleSchema = contracts.Obj(contracts.Prop("title", contracts.Str()))

func ksInitial() *JsonObject { return obj("title", "") }

func ksNever(*JsonObject, []delta.Op, CheckpointInfo) bool { return false }

func ksDefine(t testing.TB, name string, options StateOptions) *StateDefinition {
	t.Helper()
	definition, err := DefineState(name, options)
	if err != nil {
		t.Fatalf("DefineState(%s): %v", name, err)
	}
	return definition
}

// ksMemo is Pi's `Memo`: keyed, in memory.
func ksMemo(t testing.TB) *StateDefinition {
	return ksDefine(t, "notes.memo", StateOptions{Schema: ksTitleSchema, Version: 1, Initial: ksInitial, Keyed: true})
}

// ksNote is Pi's `Note`: keyed, persisted.
func ksNote(t testing.TB) *StateDefinition {
	return ksDefine(t, "notes.note", StateOptions{Schema: ksTitleSchema, Version: 1, Initial: ksInitial, Keyed: true, Persist: true, CheckpointWhen: ksNever})
}

func ksTitle(value string) func(*delta.Object) (any, error) {
	return func(draft *delta.Object) (any, error) { return nil, draft.Set("title", value) }
}

func ksChange(t testing.TB, handle *Keyed, key, title string) {
	t.Helper()
	if _, err := handle.Change(key, ksTitle(title), background); err != nil {
		t.Fatalf("change(%q): %v", key, err)
	}
}

func ksGet(t testing.TB, handle *Keyed, key string) *JsonObject {
	t.Helper()
	value, err := handle.Get(key, background)
	if err != nil {
		t.Fatalf("get(%q): %v", key, err)
	}
	return value
}

// ksRequireDoc expects value to be the document {title}, or absent when absent is set.
func ksRequireDoc(t testing.TB, value *JsonObject, title string) {
	t.Helper()
	if value == nil {
		t.Fatalf("document = absent, want {title: %q}", title)
	}
	nexttest.RequireJSONEqual(t, value, obj("title", title))
}

func ksRequireAbsent(t testing.TB, value *JsonObject) {
	t.Helper()
	if value != nil {
		t.Fatalf("document = %v, want absent", value)
	}
}

func ksKeysOf(t testing.TB, handle *Keyed) []string {
	t.Helper()
	keys, err := ksKeysErr(handle)
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	return keys
}

func ksKeysErr(handle *Keyed) ([]string, error) {
	keys := []string{}
	it := handle.Keys(background)
	for {
		key, ok, err := it.Next(background)
		if err != nil {
			return nil, err
		}
		if !ok {
			return keys, nil
		}
		keys = append(keys, key)
	}
}

func ksRequireStrings(t testing.TB, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

// ksChordError is err as the ChordError it is; the test fails when it is none.
func ksChordError(t testing.TB, err error) *data.ChordError {
	t.Helper()
	if err == nil {
		t.Fatal("expected a rejection")
	}
	chordErr, ok := data.IsChordError(err)
	if !ok {
		t.Fatalf("rejection %v is not a ChordError", err)
	}
	return chordErr
}

func ksRequireCode(t testing.TB, err error, code data.ChordErrorCode) *data.ChordError {
	t.Helper()
	chordErr := ksChordError(t, err)
	if chordErr.Code != code {
		t.Fatalf("code = %q (%s), want %q", chordErr.Code, chordErr.Message, code)
	}
	return chordErr
}

func ksRequireMessage(t testing.TB, err error, part string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a rejection containing %q", part)
	}
	if !strings.Contains(err.Error(), part) {
		t.Fatalf("rejection %q does not contain %q", err.Error(), part)
	}
}

// ksResult is what a call that runs beside the test returned.
type ksResult struct {
	value *JsonObject
	err   error
}

func ksGo(call func() (*JsonObject, error)) <-chan ksResult {
	out := make(chan ksResult, 1)
	go func() {
		value, err := call()
		out <- ksResult{value, err}
	}()
	return out
}

func ksRecv[T any](t testing.TB, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for a result that never came")
		panic("unreachable")
	}
}

func ksWaitClosed(t testing.TB, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for a channel that never closed")
	}
}

// ksEventually waits until cond holds: the state a test waits for is reached by work that runs beside it.
func ksEventually(t testing.TB, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// ksWaiting waits until the object's line shows n holders that asked and wait.
func ksWaiting(t testing.TB, o *object, n int) {
	t.Helper()
	ksEventually(t, "the line has the holders waiting", func() bool { return o.documents.Line().waitingHolders() == n })
}

// ksProbeCtx is a context that tells when its caller waits on it: a blocking read selects on Done at its wait.
type ksProbeCtx struct {
	context.Context
	reached chan struct{}
	once    sync.Once
}

func newKsProbeCtx(parent context.Context) *ksProbeCtx {
	return &ksProbeCtx{Context: parent, reached: make(chan struct{})}
}

func (c *ksProbeCtx) Done() <-chan struct{} {
	c.once.Do(func() { close(c.reached) })
	return c.Context.Done()
}

// ksPersisted is Pi's `persisted(store, declare)`: an object over store whose attempts declare Note (and what declare
// adds); notes is the latest attempt's handle.
type ksPersisted struct {
	t        *testing.T
	o        *object
	def      *StateDefinition
	declare  func(attempt int, a *attempt, notes *Keyed)
	attempts []*attempt
	handle   *Keyed
}

func ksPersistedOver(t *testing.T, chordStore *store.ChordStore, declare func(attempt int, a *attempt, notes *Keyed)) *ksPersisted {
	t.Helper()
	p := &ksPersisted{t: t, o: newObject(t, objectOptions{store: chordStore}), def: ksNote(t), declare: declare}
	if err := p.start(); err != nil {
		t.Fatalf("activate: %v", err)
	}
	return p
}

// start is a new attempt that ran its factory and activated: the first, or the retry of a failed one.
func (p *ksPersisted) start() error {
	a := p.o.newAttempt()
	p.attempts = append(p.attempts, a)
	p.handle = a.Keyed(p.def)
	if p.declare != nil {
		p.declare(len(p.attempts), a, p.handle)
	}
	return a.Activate()
}

func (p *ksPersisted) notes() *Keyed { return p.handle }

func (p *ksPersisted) current() *attempt { return p.attempts[len(p.attempts)-1] }

// restart is `restart()`: the live attempt fails, the next one activates.
func (p *ksPersisted) restart() error {
	p.current().Fail()
	return p.start()
}

func (p *ksPersisted) close() { p.o.End() }

type ksEntry struct{ key, title string }

// ksSeed writes each entry's title at its key in store's Note documents through a host of its own.
func ksSeed(t *testing.T, chordStore *store.ChordStore, entries ...ksEntry) {
	t.Helper()
	p := ksPersistedOver(t, chordStore, nil)
	for _, entry := range entries {
		ksChange(t, p.notes(), entry.key, entry.title)
	}
	p.close()
}

// ksSource is slot.source(key) for a test, read in a section.
func ksSource(o *object, slot *KeyedSlot, key string) *KeySource {
	var source *KeySource
	o.Do(func() { source = slot.KeySourceOf(key) })
	return source
}

func ksResident(o *object, slot *KeyedSlot, key string) bool {
	var present bool
	o.Do(func() { _, present = slot.residents[key] })
	return present
}

// ksFrames is calls as plain data: [value, ops, retired] each.
func ksFrames(calls []chord.WatchCall[delta.JsonValue]) []any {
	out := make([]any, len(calls))
	for i, call := range calls {
		ops := []any{}
		for _, op := range call.Ops {
			ops = append(ops, []any(op))
		}
		out[i] = []any{string(call.Kind), call.Value, ops, call.Retired}
	}
	return out
}

func ksFrame(value any, ops []any, retired bool) any {
	return []any{string(chord.WatchCallValue), value, ops, retired}
}

func ksRequireFrames(t testing.TB, got []chord.WatchCall[delta.JsonValue], want ...any) {
	t.Helper()
	nexttest.RequireJSONEqual(t, ksFrames(got), append([]any{}, want...))
}

// ---- keyed reads in memory ---------------------------------------------------------------------------------------

func TestKeyedSlotGetAndKeysTakeAContextKeysComeInCodePointOrder(t *testing.T) {
	var memos *Keyed
	o, _ := activated(t, objectOptions{}, func(a *attempt) { memos = a.Keyed(ksMemo(t)) })
	for _, key := range []string{"b", "\U0001F600", "a", "\uFFFF"} {
		ksChange(t, memos, key, key)
	}
	ksRequireDoc(t, ksGet(t, memos, "a"), "a")
	ksRequireAbsent(t, ksGet(t, memos, "z"))
	ksRequireStrings(t, ksKeysOf(t, memos), []string{"a", "b", "\uFFFF", "\U0001F600"})
	o.End()
}

// ---- migration on access -----------------------------------------------------------------------------------------

func TestKeyedSlotANewerVersionMigratesEachInMemoryKeyWhenItIsReadAKeyWhoseMigrateFailsFailsTheOwnerThen(t *testing.T) {
	var migrated []string
	memoV2 := ksDefine(t, "notes.memo", StateOptions{
		Schema: ksTitleSchema, Version: 2, Initial: ksInitial, Keyed: true,
		Migrate: func(old *JsonObject, _ int) (*JsonObject, error) {
			title, _ := old.Value("title").(string)
			if title == "bad" {
				return nil, errorString("cannot migrate")
			}
			migrated = append(migrated, title)
			return obj("title", title+"!"), nil
		},
	})
	var first, second *Keyed
	o, firstAttempt := activated(t, objectOptions{}, func(a *attempt) { first = a.Keyed(ksMemo(t)) })
	for _, key := range []string{"a", "b", "c"} {
		title := key
		if key == "c" {
			title = "bad"
		}
		ksChange(t, first, key, title)
	}
	// restart(): the first attempt fails, the second activates.
	firstAttempt.Fail()
	secondAttempt, err := o.restart(func(a *attempt) { second = a.Keyed(memoV2) })
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	// The activation migrated nothing: no key was read.
	if len(migrated) != 0 {
		t.Fatalf("migrated = %v, want none", migrated)
	}
	ksRequireDoc(t, ksGet(t, second, "a"), "a!")
	ksRequireStrings(t, migrated, []string{"a"})
	_, err = secondAttempt.Tx(func(tx *Transaction) (any, error) {
		draft, err := tx.Draft(second, "b")
		if err != nil {
			return nil, err
		}
		nexttest.RequireJSONEqual(t, draft.Snapshot(), obj("title", "b!"))
		return nil, nil
	}, background)
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
	ksRequireStrings(t, migrated, []string{"a", "b"})
	_, err = second.Get("c", background)
	ksRequireMessage(t, err, "cannot migrate")
	// The owner failed once, broken (the host's `object_failed` for the induced restart and the failed state of its
	// reference are the host's: PENDING(host)).
	failures := o.Failures()
	if len(failures) != 1 {
		t.Fatalf("failures = %d, want 1", len(failures))
	}
	if !strings.Contains(failures[0].Message, "cannot migrate") {
		t.Fatalf("failure = %q", failures[0].Message)
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

// ---- keys and the text rules -------------------------------------------------------------------------------------

func TestKeyedSlotALoneSurrogateInAKeyIsUFFFDAtEveryEntryPointInMemoryAndInTheStoreNULIsRefused(t *testing.T) {
	chordStore, _ := ksFreshStore(t)
	p := ksPersistedOver(t, chordStore, nil)
	lone := jsstring.FromUTF16([]uint16{'x', 0xD800})
	notes := p.notes()
	ksChange(t, notes, lone, "one")
	ksRequireStrings(t, ksKeysOf(t, notes), []string{"x\uFFFD"})
	// One key: the change through either spelling reaches the same document and record.
	ksChange(t, notes, "x\uFFFD", "two")
	_, err := p.attempts[0].Tx(func(tx *Transaction) (any, error) {
		draft, err := tx.Draft(notes, lone)
		if err != nil {
			return nil, err
		}
		return nil, draft.Set("title", draft.Get("title").(string)+"!")
	}, background)
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
	ksRequireDoc(t, ksGet(t, notes, lone), "two!")
	watch, err := notes.Watch(lone, background)
	if err != nil || watch == nil {
		t.Fatalf("watch = %v, %v", watch, err)
	}
	nexttest.RequireJSONEqual(t, watch.Value(), obj("title", "two!"))
	nul := "a\x00b"
	_, changeErr := notes.Change(nul, ksTitle("nul"), background)
	_, getErr := notes.Get(nul, background)
	_, watchErr := notes.Watch(nul, background)
	retireErr := notes.Retire(nul, background)
	for _, use := range []error{changeErr, getErr, watchErr, retireErr} {
		chordErr := ksRequireCode(t, use, data.CodeInvalid)
		if chordErr.Reason != data.ReasonArgs {
			t.Fatalf("reason = %q, want args", chordErr.Reason)
		}
	}
	if err := notes.Retire(lone, background); err != nil {
		t.Fatalf("retire: %v", err)
	}
	ksRequireStrings(t, ksKeysOf(t, notes), []string{})
	p.close()
	// The store has the key as memory had it.
	again := ksPersistedOver(t, chordStore, nil)
	ksRequireAbsent(t, ksGet(t, again.notes(), "x\uFFFD"))
	again.close()
}

func TestKeyedSlotInMemoryTooOneKeyPerSpellingNULRefused(t *testing.T) {
	var memos *Keyed
	o, _ := activated(t, objectOptions{}, func(a *attempt) { memos = a.Keyed(ksMemo(t)) })
	ksChange(t, memos, jsstring.FromUTF16([]uint16{'x', 0xDC00}), "one")
	ksRequireDoc(t, ksGet(t, memos, "x\uFFFD"), "one")
	ksRequireStrings(t, ksKeysOf(t, memos), []string{"x\uFFFD"})
	_, err := memos.Change("\x00", ksTitle("nul"), background)
	_ = ksRequireCode(t, err, data.CodeInvalid)
	o.End()
}

func TestKeyedSlotTheEmptyKeyIsAKeyListedFirstAlsoOnALaterPage(t *testing.T) {
	chordStore, _ := ksFreshStore(t)
	entries := []ksEntry{{"", "empty"}}
	for i := range 120 {
		entries = append(entries, ksEntry{"k" + ksPad3(i), "x"})
	}
	ksSeed(t, chordStore, entries...)
	p := ksPersistedOver(t, chordStore, nil)
	keys := ksKeysOf(t, p.notes())
	if len(keys) != 121 {
		t.Fatalf("keys = %d, want 121", len(keys))
	}
	if keys[0] != "" {
		t.Fatalf("keys[0] = %q, want the empty key", keys[0])
	}
	ksRequireDoc(t, ksGet(t, p.notes(), ""), "empty")
	p.close()
}

func ksPad3(i int) string {
	return string([]byte{byte('0' + i/100%10), byte('0' + i/10%10), byte('0' + i%10)})
}

// ---- keyed watches -----------------------------------------------------------------------------------------------

func TestKeyedSlotWatchKeyIsUndefinedWhenAbsentItFollowsTheAddressThroughARetirementAndARecreation(t *testing.T) {
	var memos *Keyed
	o, first := activated(t, objectOptions{}, func(a *attempt) { memos = a.Keyed(ksMemo(t)) })
	absent, err := memos.Watch("a", background)
	if err != nil || absent != nil {
		t.Fatalf("watch of an absent key = %v, %v, want none", absent, err)
	}
	ksChange(t, memos, "a", "x")
	watch, err := memos.Watch("a", background)
	if err != nil || watch == nil {
		t.Fatalf("watch = %v, %v", watch, err)
	}
	nexttest.RequireJSONEqual(t, watch.Value(), obj("title", "x"))
	changes := nexttest.ChangesOf(watch)
	ksChange(t, memos, "a", "y")
	o.delivered()
	if err := memos.Retire("a", background); err != nil {
		t.Fatalf("retire: %v", err)
	}
	o.delivered()
	ksChange(t, memos, "a", "z")
	o.delivered()
	_, err = first.Tx(func(tx *Transaction) (any, error) {
		if err := tx.Retire(memos, "a"); err != nil {
			return nil, err
		}
		draft, err := tx.Draft(memos, "a")
		if err != nil {
			return nil, err
		}
		return nil, draft.Set("title", "w")
	}, background)
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
	o.delivered()
	ksRequireFrames(t, changes.Items(),
		ksFrame(obj("title", "y"), []any{[]any{"s", []any{"title"}, "y"}}, false),
		ksFrame(obj("title", ""), []any{[]any{"r", obj("title", "")}}, true),
		ksFrame(obj("title", "z"), []any{[]any{"r", obj("title", "z")}}, false),
		ksFrame(obj("title", "w"), []any{[]any{"r", obj("title", "w")}}, true),
	)
	o.End()
	ksWaitClosed(t, watch.Closed())
	if end := watch.End(); end.Kind != chord.WatchEnded {
		t.Fatalf("closed = %v, want ended", end)
	}
}

func TestKeyedSlotAStoppedWatchUnpinsItsKeyItsSourceIsCollected(t *testing.T) {
	var memos *Keyed
	o, _ := activated(t, objectOptions{}, func(a *attempt) { memos = a.Keyed(ksMemo(t)) })
	ksChange(t, memos, "a", "x")
	// The watch's source: the slot's per-key source while pinned.
	source := func() weak.Pointer[KeySource] {
		watch, err := memos.Watch("a", background)
		if err != nil || watch == nil {
			t.Fatalf("watch = %v, %v", watch, err)
		}
		ref := weak.Make(ksSource(o, memos.Slot, "a"))
		if _, err := watch.Stop(); err != nil {
			t.Fatalf("stop: %v", err)
		}
		return ref
	}()
	nexttest.Flushed()
	nexttest.ExpectCollected(t, source)
	o.End()
}

// ---- at(key) -----------------------------------------------------------------------------------------------------

func TestKeyedSlotAtKeyIsInitialWhileAbsentAStateMemberDeriveAndOnSourceRetirementArrivesWithTheFlag(t *testing.T) {
	var memos *Keyed
	var pinned *KeySource
	var upper *state.Derive
	var seenMu sync.Mutex
	type seenCall struct {
		title   string
		retired bool
	}
	var seen []seenCall
	o, _ := activated(t, objectOptions{}, func(a *attempt) {
		memos = a.Keyed(ksMemo(t))
		var err error
		if pinned, err = memos.At("a"); err != nil {
			t.Fatalf("at: %v", err)
		}
		// self.derive([pinned], ([note]) => note.title.toUpperCase())
		upper = state.NewDerive([]any{pinned}, func(values []any) (delta.JsonValue, error) {
			return strings.ToUpper(values[0].(*JsonObject).Value("title").(string)), nil
		}, o_batchOf(a))
		a.object.Do(func() {
			if err := upper.Attach(func(source any, _ int) (state.DeriveInput, error) {
				return source.(state.DeriveInput), nil
			}, func(error) {}); err != nil {
				t.Fatalf("attach: %v", err)
			}
		})
	})
	// self.on(pinned, ({value, retired}) => seen.push(...)): the routine's first call is the value it started at, then
	// one call per frame.
	var onWatch *chord.Watch[delta.JsonValue]
	o.Do(func() {
		var err error
		if onWatch, err = pinned.OpenWatch(background); err != nil {
			t.Errorf("watch: %v", err)
		}
	})
	seen = append(seen, seenCall{onWatch.Value().(*JsonObject).Value("title").(string), false})
	onWatch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		seenMu.Lock()
		defer seenMu.Unlock()
		seen = append(seen, seenCall{call.Value.(*JsonObject).Value("title").(string), call.Retired})
		return nil
	})
	var value delta.JsonValue
	var err error
	read := func() delta.JsonValue {
		o.Do(func() { value, err = pinned.Value() })
		if err != nil {
			t.Fatalf("pinned.value: %v", err)
		}
		return value
	}
	nexttest.RequireJSONEqual(t, read(), obj("title", ""))
	var upperWatch *chord.Watch[delta.JsonValue]
	o.Do(func() {
		var werr error
		if upperWatch, werr = upper.OpenWatch(background); werr != nil {
			t.Errorf("upper watch: %v", werr)
		}
	})
	changes := nexttest.ChangesOf(upperWatch)
	ksChange(t, memos, "a", "x")
	o.delivered()
	nexttest.RequireJSONEqual(t, read(), obj("title", "x"))
	if err := memos.Retire("a", background); err != nil {
		t.Fatalf("retire: %v", err)
	}
	o.delivered()
	nexttest.Flushed()
	ksRequireFrames(t, changes.Items(),
		ksFrame("X", []any{[]any{"r", "X"}}, false),
		ksFrame("", []any{[]any{"r", ""}}, true),
	)
	seenMu.Lock()
	got := append([]seenCall{}, seen...)
	seenMu.Unlock()
	want := []seenCall{{"", false}, {"x", false}, {"", true}}
	if len(got) != len(want) {
		t.Fatalf("seen = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("seen = %v, want %v", got, want)
		}
	}
	o.End()
}

// o_batchOf is the attempt's object's batch (the derive's).
func o_batchOf(a *attempt) *state.Batch { return a.object.batch }

// PENDING(host): "only in the factory": `memos.at("a")` after the factory throws "<label>: ... valid only in the
// factory" from the host's attempt (host/attempt.ts:975) and reference (host/reference.ts:449); the harness's At is
// the host's stand-in and its own check is not a documents behavior.

func TestKeyedSlotAPersistedKeyIsLoadedBeforeTheObjectGoesLiveAndStaysPinnedUntilTheNextFactoryReturns(t *testing.T) {
	chordStore, _ := ksFreshStore(t)
	ksSeed(t, chordStore, ksEntry{"a", "x"}, ksEntry{"b", "y"})
	var pinned *KeySource
	p := ksPersistedOver(t, chordStore, func(attempt int, a *attempt, notes *Keyed) {
		key := "b"
		if attempt == 1 {
			key = "a"
		}
		var err error
		if pinned, err = notes.At(key); err != nil {
			t.Fatalf("at: %v", err)
		}
	})
	read := func() delta.JsonValue {
		var value delta.JsonValue
		var err error
		p.o.Do(func() { value, err = pinned.Value() })
		if err != nil {
			t.Fatalf("pinned.value: %v", err)
		}
		return value
	}
	nexttest.RequireJSONEqual(t, read(), obj("title", "x"))
	slot := p.notes().Slot
	first := weak.Make(ksSource(p.o, slot, "a"))
	if err := p.restart(); err != nil {
		t.Fatalf("restart: %v", err)
	}
	// The old attempt is no one's: what only it held may go.
	p.attempts = []*attempt{p.current()}
	nexttest.RequireJSONEqual(t, read(), obj("title", "y"))
	if source := ksSource(p.o, slot, "a"); source != nil {
		t.Fatal("source(a) is still there")
	}
	nexttest.ExpectCollected(t, first)
	p.close()
}

// ---- cold keyed loads on the line --------------------------------------------------------------------------------

func TestKeyedSlotLoadAColdKeyOnceACachedGetTouchesNoSQLiteAnAbsentKeyIsNotCached(t *testing.T) {
	chordStore, db := ksFreshStore(t)
	ksSeed(t, chordStore, ksEntry{"a", "x"})
	p := ksPersistedOver(t, chordStore, nil)
	base := db.findCount()
	reads := func() int { return db.findCount() - base }
	ksRequireDoc(t, ksGet(t, p.notes(), "a"), "x")
	if reads() != 1 {
		t.Fatalf("reads = %d, want 1", reads())
	}
	ksRequireDoc(t, ksGet(t, p.notes(), "a"), "x")
	if reads() != 1 {
		t.Fatalf("reads = %d, want 1", reads())
	}
	ksRequireAbsent(t, ksGet(t, p.notes(), "z"))
	ksRequireAbsent(t, ksGet(t, p.notes(), "z"))
	if reads() != 3 {
		t.Fatalf("reads = %d, want 3", reads())
	}
	p.close()
}

func TestKeyedSlotTwoReadersShareOneLoadACancelledWaiterRejectsAlone(t *testing.T) {
	chordStore, db := ksFreshStore(t)
	ksSeed(t, chordStore, ksEntry{"a", "x"})
	p := ksPersistedOver(t, chordStore, nil)
	base := db.findCount()
	// Hold the line so that the load waits behind it.
	entered := make(chan struct{})
	holding := make(chan struct{})
	txDone := ksGo(func() (*JsonObject, error) {
		_, err := p.attempts[0].Tx(func(*Transaction) (any, error) {
			close(entered)
			<-holding
			return nil, nil
		}, background)
		return nil, err
	})
	ksWaitClosed(t, entered)
	cancelCtx, cancel := chordctx.WithCancel(background)
	firstCtx := newKsProbeCtx(cancelCtx)
	secondCtx := newKsProbeCtx(background)
	notes := p.notes()
	first := ksGo(func() (*JsonObject, error) { return notes.Get("a", firstCtx) })
	second := ksGo(func() (*JsonObject, error) { return notes.Get("a", secondCtx) })
	// Both callers wait on the load behind the line.
	ksWaitClosed(t, firstCtx.reached)
	ksWaitClosed(t, secondCtx.reached)
	cancel(errorString("gone"))
	got := ksRecv(t, first)
	if got.err == nil || got.err.Error() != "gone" {
		t.Fatalf("first = %v, want the cause gone", got.err)
	}
	close(holding)
	if res := ksRecv(t, txDone); res.err != nil {
		t.Fatalf("tx: %v", res.err)
	}
	res := ksRecv(t, second)
	if res.err != nil {
		t.Fatalf("second: %v", res.err)
	}
	ksRequireDoc(t, res.value, "x")
	if db.findCount()-base != 1 {
		t.Fatalf("reads = %d, want 1", db.findCount()-base)
	}
	p.close()
}

func TestKeyedSlotAColdGetRacingADraftOfTheSameKeyTheLoadInstallsOnlyWhileTheKeyIsAbsent(t *testing.T) {
	chordStore, _ := ksFreshStore(t)
	ksSeed(t, chordStore, ksEntry{"a", "x"})
	p := ksPersistedOver(t, chordStore, nil)
	notes := p.notes()
	var get <-chan ksResult
	_, err := p.attempts[0].Tx(func(tx *Transaction) (any, error) {
		get = ksGo(func() (*JsonObject, error) { return notes.Get("a", background) })
		// The get's load takes its place behind this transaction on the line.
		ksWaiting(t, p.o, 1)
		draft, err := tx.Draft(notes, "a")
		if err != nil {
			return nil, err
		}
		if title := draft.Get("title"); title != "x" {
			t.Errorf("draft.title = %v, want x", title)
		}
		return nil, draft.Set("title", "y")
	}, background)
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
	res := ksRecv(t, get)
	if res.err != nil {
		t.Fatalf("get: %v", res.err)
	}
	ksRequireDoc(t, res.value, "y")
	ksRequireDoc(t, ksGet(t, notes, "a"), "y")
	p.close()
}

func TestKeyedSlotKeysComeFromTheStoreInCodePointOrderAPagePerLineJob(t *testing.T) {
	chordStore, _ := ksFreshStore(t)
	var entries []ksEntry
	var want []string
	for i := range 150 {
		key := "k" + ksPad3(i)
		entries = append(entries, ksEntry{key, "x"})
		want = append(want, key)
	}
	entries = append(entries, ksEntry{"\U0001F600", "x"}, ksEntry{"\uFFFF", "x"})
	want = append(want, "\uFFFF", "\U0001F600")
	ksSeed(t, chordStore, entries...)
	p := ksPersistedOver(t, chordStore, nil)
	ksRequireStrings(t, ksKeysOf(t, p.notes()), want)
	p.close()
}

func TestKeyedSlotTxRetireOfAColdKeyFindsItWithoutLoadingItAnAbsentKeyRetiresNothing(t *testing.T) {
	chordStore, _ := ksFreshStore(t)
	ksSeed(t, chordStore, ksEntry{"a", "x"})
	p := ksPersistedOver(t, chordStore, nil)
	seq := chordStore.LastSeq()
	if err := p.notes().Retire("z", background); err != nil {
		t.Fatalf("retire z: %v", err)
	}
	if chordStore.LastSeq() != seq {
		t.Fatalf("lastSeq = %d, want %d", chordStore.LastSeq(), seq)
	}
	if err := p.notes().Retire("a", background); err != nil {
		t.Fatalf("retire a: %v", err)
	}
	if chordStore.LastSeq() != seq+1 {
		t.Fatalf("lastSeq = %d, want %d", chordStore.LastSeq(), seq+1)
	}
	ksRequireAbsent(t, ksGet(t, p.notes(), "a"))
	p.close()
}

func TestKeyedSlotTxRetireOfAColdKeyStoredAtANewerVersionOrAnotherFingerprintFailsTheOwnerBrokenAsAReadWouldAndKeepsIt(t *testing.T) {
	otherSchema := contracts.Obj(contracts.Prop("title", contracts.Str()), contracts.Prop("n", contracts.Str()))
	for _, c := range []struct {
		name    string
		version int
		message string
	}{
		{"a newer version", 2, "stored at version 2, newer than the code's 1"},
		{"another fingerprint at its version", 1, "stored with another schema at version 1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			chordStore, _ := ksFreshStore(t)
			// Written by other code: a newer version, or the same version with another schema.
			other := ksDefine(t, "notes.note", StateOptions{
				Schema: otherSchema, Version: c.version, Keyed: true, Persist: true, CheckpointWhen: ksNever,
				Initial: func() *JsonObject { return obj("title", "", "n", "") },
			})
			writer, _ := activated(t, objectOptions{store: chordStore}, func(a *attempt) {
				notes := a.Keyed(other)
				if _, err := notes.Change("a", func(draft *delta.Object) (any, error) {
					if err := draft.Set("title", "kept"); err != nil {
						return nil, err
					}
					return nil, draft.Set("n", "1")
				}, background); err != nil {
					t.Fatalf("write: %v", err)
				}
			})
			writer.End()
			p := ksPersistedOver(t, chordStore, nil)
			err := p.notes().Retire("a", background)
			ksRequireMessage(t, err, c.message)
			// Failed once, broken (the host's `object_failed` report and the reference's failed state: PENDING(host)).
			if failures := p.o.Failures(); len(failures) != 1 || !strings.Contains(failures[0].Message, c.message) {
				t.Fatalf("failures = %v", failures)
			}
			p.close()
			reader, _ := activated(t, objectOptions{store: chordStore}, func(a *attempt) { a.Keyed(other) })
			rows, err := store.Read(chordStore, func(executor sqlite.SqliteExecutor) ([]sqlite.SqliteRow, error) {
				return executor.All("SELECT key FROM chord_documents")
			}, background)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if len(rows) != 1 || rows[0]["key"] != "a" {
				t.Fatalf("rows = %v, want [{key: a}]", rows)
			}
			reader.End()
		})
	}
}

func TestKeyedSlotARetirementCutByItsAttemptsSealWhileItFindsTheDocumentFailsNoSuccessor(t *testing.T) {
	chordStore, db := ksFreshStore(t)
	newer := ksDefine(t, "notes.note", StateOptions{Schema: ksTitleSchema, Version: 2, Initial: ksInitial, Keyed: true, Persist: true, CheckpointWhen: ksNever})
	writer, _ := activated(t, objectOptions{store: chordStore}, func(a *attempt) {
		ksChange(t, a.Keyed(newer), "a", "kept")
	})
	writer.End()
	p := ksPersistedOver(t, chordStore, nil)
	// The find's read is held past the seal: the retirement resumes on a cut transaction.
	gate := db.holdNextRead()
	notes := p.notes()
	retired := ksGo(func() (*JsonObject, error) { return nil, notes.Retire("a", background) })
	ksWaitClosed(t, gate.entered)
	p.current().Fail()
	if err := p.start(); err != nil {
		t.Fatalf("retry: %v", err)
	}
	gate.open()
	res := ksRecv(t, retired)
	_ = ksRequireCode(t, res.err, data.CodeReleased)
	nexttest.Flushed()
	// The cut retirement failed nothing: the successor stays live (the host's `reference.state` and `object_failed`
	// reports: PENDING(host); the documents report no failure).
	if failures := p.o.Failures(); len(failures) != 0 {
		t.Fatalf("failures = %v, want none", failures)
	}
	p.close()
}

func TestKeyedSlotASealedHandleReadsCachedDocumentsAColdReadIsRefusedReleased(t *testing.T) {
	chordStore, _ := ksFreshStore(t)
	ksSeed(t, chordStore, ksEntry{"a", "x"}, ksEntry{"b", "y"})
	p := ksPersistedOver(t, chordStore, nil)
	old := p.notes()
	ksGet(t, old, "a")
	if err := p.restart(); err != nil {
		t.Fatalf("restart: %v", err)
	}
	ksRequireDoc(t, ksGet(t, old, "a"), "x")
	_, err := old.Get("b", background)
	_ = ksRequireCode(t, err, data.CodeReleased)
	p.close()
}

func TestKeyedSlotASealedHandleJoiningAColdLoadInFlightIsRefusedReleasedRule4(t *testing.T) {
	chordStore, db := ksFreshStore(t)
	ksSeed(t, chordStore, ksEntry{"b", "y"})
	p := ksPersistedOver(t, chordStore, nil)
	gate := db.holdNextLoad()
	old := p.notes()
	first := ksGo(func() (*JsonObject, error) { return old.Get("b", background) })
	ksWaitClosed(t, gate.entered)
	// restart(): the attempt fails (its token is sealed), the next one activates behind the load on the line.
	p.current().Fail()
	restarting := ksGo(func() (*JsonObject, error) { return nil, p.start() })
	_, err := old.Get("b", background)
	_ = ksRequireCode(t, err, data.CodeReleased)
	gate.open()
	res := ksRecv(t, first)
	if res.err != nil {
		t.Fatalf("first: %v", res.err)
	}
	ksRequireDoc(t, res.value, "y")
	if res := ksRecv(t, restarting); res.err != nil {
		t.Fatalf("restart: %v", res.err)
	}
	p.close()
}

func TestKeyedSlotColdReadsQueuedOnTheLineBeforeTheObjectsEndAreRefusedReleasedWhenTheyRunAfterIt(t *testing.T) {
	chordStore, db := ksFreshStore(t)
	ksSeed(t, chordStore, ksEntry{"a", "x"}, ksEntry{"b", "y"}, ksEntry{"c", "z"})
	p := ksPersistedOver(t, chordStore, nil)
	gate := db.holdNextLoad()
	notes := p.notes()
	first := ksGo(func() (*JsonObject, error) { return notes.Get("a", background) })
	ksWaitClosed(t, gate.entered)
	get := ksGo(func() (*JsonObject, error) { return notes.Get("b", background) })
	watch := ksGo(func() (*JsonObject, error) {
		w, err := notes.Watch("c", background)
		if w != nil {
			t.Error("a watch was opened")
		}
		return nil, err
	})
	// Both loads queue behind the one holding the line.
	ksWaiting(t, p.o, 2)
	// close(): the object's documents seal at once; the end follows once the line settled.
	p.o.Do(func() { p.o.documents.Seal() })
	closing := ksGo(func() (*JsonObject, error) { p.o.End(); return nil, nil })
	gate.open()
	_ = ksRequireCode(t, ksRecv(t, get).err, data.CodeReleased)
	_ = ksRequireCode(t, ksRecv(t, watch).err, data.CodeReleased)
	// The load holding the line before the end finishes.
	res := ksRecv(t, first)
	if res.err != nil {
		t.Fatalf("first: %v", res.err)
	}
	ksRequireDoc(t, res.value, "x")
	ksRecv(t, closing)
}

func TestKeyedSlotColdReadsQueuedBehindAnAdmittedStepAreRefusedReleasedWhenTheirAttemptFailedLiveMeanwhile(t *testing.T) {
	chordStore, db := ksFreshStore(t)
	ksSeed(t, chordStore, ksEntry{"a", "x"})
	pDefinition := ksDefine(t, "notes.p", StateOptions{Schema: ksTitleSchema, Version: 1, Initial: ksInitial, Persist: true, CheckpointWhen: ksNever})
	var single *Singleton
	p := ksPersistedOver(t, chordStore, func(_ int, a *attempt, _ *Keyed) { single = a.Singleton(pDefinition) })
	held := db.holdNextCommit()
	change := ksGo(func() (*JsonObject, error) {
		_, err := single.Change(ksTitle("p"), background)
		return nil, err
	})
	ksWaitClosed(t, held.entered)
	notes := p.notes()
	getCtx := newKsProbeCtx(background)
	watchCtx := newKsProbeCtx(background)
	get := ksGo(func() (*JsonObject, error) { return notes.Get("a", getCtx) })
	watch := ksGo(func() (*JsonObject, error) {
		w, err := notes.Watch("a", watchCtx)
		if w != nil {
			t.Error("a watch was opened")
		}
		return nil, err
	})
	keys := ksGo(func() (*JsonObject, error) { _, err := ksKeysErr(notes); return nil, err })
	// The get and the watch share one load, the keys' page is another: both wait behind the step on the line.
	ksWaitClosed(t, getCtx.reached)
	ksWaitClosed(t, watchCtx.reached)
	ksWaiting(t, p.o, 2)
	// The attempt is sealed; the object stays open.
	p.current().Fail()
	held.open()
	if res := ksRecv(t, change); res.err != nil {
		t.Fatalf("change: %v", res.err)
	}
	_ = ksRequireCode(t, ksRecv(t, get).err, data.CodeReleased)
	_ = ksRequireCode(t, ksRecv(t, watch).err, data.CodeReleased)
	_ = ksRequireCode(t, ksRecv(t, keys).err, data.CodeReleased)
	p.close()
}

func TestKeyedSlotAStillValidWaiterSharingThatLoadGetsTheDocumentOnlyTheSealedHandlesReadIsRefused(t *testing.T) {
	chordStore, db := ksFreshStore(t)
	ksSeed(t, chordStore, ksEntry{"a", "x"})
	var notes *Keyed
	o, a := activated(t, objectOptions{store: chordStore}, func(a *attempt) { notes = a.Keyed(ksNote(t)) })
	// A consumer (a reference's member) reads as the provider's own code may: the object's own token.
	consumer := NewKeyed(notes.Slot, o.documents.Own(), notes.Version, nil)
	held := db.holdNextCommit()
	change := ksGo(func() (*JsonObject, error) {
		_, err := notes.Change("b", ksTitle("y"), background)
		return nil, err
	})
	ksWaitClosed(t, held.entered)
	sealedCtx := newKsProbeCtx(background)
	sharedCtx := newKsProbeCtx(background)
	sealed := ksGo(func() (*JsonObject, error) { return notes.Get("a", sealedCtx) })
	// A consumer joins the same load.
	ksWaitClosed(t, sealedCtx.reached)
	shared := ksGo(func() (*JsonObject, error) { return consumer.Get("a", sharedCtx) })
	ksWaitClosed(t, sharedCtx.reached)
	nexttest.Flushed()
	a.Fail()
	held.open()
	if res := ksRecv(t, change); res.err != nil {
		t.Fatalf("change: %v", res.err)
	}
	_ = ksRequireCode(t, ksRecv(t, sealed).err, data.CodeReleased)
	res := ksRecv(t, shared)
	if res.err != nil {
		t.Fatalf("shared: %v", res.err)
	}
	ksRequireDoc(t, res.value, "x")
	o.End()
}

func TestKeyedSlotALoadCutByTheSealNeverInstallsASuccessorsRetirementStands(t *testing.T) {
	chordStore, db := ksFreshStore(t)
	ksSeed(t, chordStore, ksEntry{"a", "x"})
	p := ksPersistedOver(t, chordStore, nil)
	gate := db.holdNextLoad()
	old := p.notes()
	first := p.attempts[0]
	cut := ksGo(func() (*JsonObject, error) {
		_, err := first.Tx(func(tx *Transaction) (any, error) {
			draft, err := tx.Draft(old, "a")
			if err != nil {
				return nil, err
			}
			return nil, draft.Set("title", "z")
		}, background)
		return nil, err
	})
	ksWaitClosed(t, gate.entered)
	// The seal cuts the transaction and releases the line; the successor retires the key.
	if err := p.restart(); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if err := p.notes().Retire("a", background); err != nil {
		t.Fatalf("retire: %v", err)
	}
	gate.open()
	_ = ksRequireCode(t, ksRecv(t, cut).err, data.CodeReleased)
	ksRequireAbsent(t, ksGet(t, p.notes(), "a"))
	p.close()
}

func TestKeyedSlotAPluginMigrateThrowingAChordErrorFailsItsOwnerLikeAnyOtherError(t *testing.T) {
	chordStore, _ := ksFreshStore(t)
	ksSeed(t, chordStore, ksEntry{"a", "x"})
	v2 := ksDefine(t, "notes.note", StateOptions{
		Schema: ksTitleSchema, Version: 2, Initial: ksInitial, Keyed: true, Persist: true, CheckpointWhen: ksNever,
		Migrate: func(*JsonObject, int) (*JsonObject, error) {
			return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "plugin says no"}, nil)
		},
	})
	var notes *Keyed
	o, _ := activated(t, objectOptions{store: chordStore}, func(a *attempt) { notes = a.Keyed(v2) })
	_, err := notes.Get("a", background)
	if chordErr := ksChordError(t, err); chordErr.Message != "plugin says no" {
		t.Fatalf("message = %q, want plugin says no", chordErr.Message)
	}
	// Failed once, broken (the host's `object_failed` report and the reference's failed state: PENDING(host)).
	if failures := o.Failures(); len(failures) != 1 || failures[0].Message != "plugin says no" {
		t.Fatalf("failures = %v", failures)
	}
	o.End()
}

func TestKeyedSlotAKeyWhoseMigrateFailsFailsItsOwnerBrokenTheGetRejectsWithTheCause(t *testing.T) {
	chordStore, _ := ksFreshStore(t)
	ksSeed(t, chordStore, ksEntry{"a", "x"})
	v2 := ksDefine(t, "notes.note", StateOptions{
		Schema: ksTitleSchema, Version: 2, Initial: ksInitial, Keyed: true, Persist: true, CheckpointWhen: ksNever,
		Migrate: func(*JsonObject, int) (*JsonObject, error) { return nil, errorString("cannot migrate") },
	})
	var notes *Keyed
	o, _ := activated(t, objectOptions{store: chordStore}, func(a *attempt) { notes = a.Keyed(v2) })
	_, err := notes.Get("a", background)
	_ = ksRequireCode(t, err, data.CodeUnavailable)
	ksRequireMessage(t, err, "cannot migrate")
	// Failed once, broken (the host's `object_failed` report and the reference's failed state: PENDING(host)).
	if failures := o.Failures(); len(failures) != 1 || !strings.Contains(failures[0].Message, "cannot migrate") {
		t.Fatalf("failures = %v", failures)
	}
	o.End()
}

// ---- eviction (fake timers) --------------------------------------------------------------------------------------

func TestKeyedSlotAnUnpinnedKeyIdleForTheRetentionLeavesMemoryAPinnedOneStays(t *testing.T) {
	chordStore, db := ksFreshStore(t)
	ksSeed(t, chordStore, ksEntry{"a", "x"}, ksEntry{"b", "y"})
	p := ksPersistedOver(t, chordStore, nil)
	slot := p.notes().Slot
	base := db.findCount()
	reads := func() int { return db.findCount() - base }
	ksGet(t, p.notes(), "a")
	watch, err := p.notes().Watch("b", background)
	if err != nil || watch == nil {
		t.Fatalf("watch = %v, %v", watch, err)
	}
	if reads() != 2 {
		t.Fatalf("reads = %d, want 2", reads())
	}
	p.o.clock.Advance(RetentionMs + 1_000)
	// The sweep runs on the line: a leaves memory, the pinned b stays.
	ksEventually(t, "a was evicted", func() bool { return !ksResident(p.o, slot, "a") })
	ksGet(t, p.notes(), "a")
	ksGet(t, p.notes(), "b")
	if reads() != 3 {
		t.Fatalf("reads = %d, want 3", reads())
	}
	if _, err := watch.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	// The stop releases the pin; b is idle from then.
	ksEventually(t, "b is unpinned", func() bool { return ksSource(p.o, slot, "b") == nil })
	p.o.clock.Advance(RetentionMs + 1_000)
	ksEventually(t, "b was evicted", func() bool { return !ksResident(p.o, slot, "b") })
	ksGet(t, p.notes(), "b")
	if reads() != 4 {
		t.Fatalf("reads = %d, want 4", reads())
	}
	p.close()
}

func TestKeyedSlotAnEvictedDocumentIsCollected(t *testing.T) {
	chordStore, _ := ksFreshStore(t)
	ksSeed(t, chordStore, ksEntry{"a", "x"})
	p := ksPersistedOver(t, chordStore, nil)
	slot := p.notes().Slot
	collected := func() weak.Pointer[JsonObject] {
		value := ksGet(t, p.notes(), "a")
		return weak.Make(value)
	}()
	p.o.clock.Advance(RetentionMs + 1_000)
	ksEventually(t, "a was evicted", func() bool { return !ksResident(p.o, slot, "a") })
	nexttest.Flushed()
	nexttest.ExpectCollected(t, collected)
	ksRequireDoc(t, ksGet(t, p.notes(), "a"), "x")
	p.close()
}
