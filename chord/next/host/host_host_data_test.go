package host

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// pi: packages/chord/src/host/host-data.ts
// Ports packages/chord/test/host/host-data.test.ts: host data operations on a live host (decision "Documents and table rows
// can belong to a host"; spec `docs/host-keyed/spec.md` §5): the gate between operations and activations' load phases; an
// import or delete reaching the live objects that hold the address's data (pinned and resident keys, live queries) in one
// batch, waiting for their transactions (owners of tables without slots or queries too); cancellation before admission
// writing nothing; an owner that cannot read an imported document failing alone; older tables migrated through their
// owner's history, newer ones refused; a host without a store refusing.
//
// Forced differences: a Promise Pi leaves pending while the test goes on is a goroutine whose result arrives on a channel
// (hdGo); `await delivered()` between steps is a wait for what the step reaches (hdEventually), never a duration. The
// handler that holds a transaction open (`await between`) waits with chordctx.Receive, which suspends the handler as
// DESIGN-go-layout §10 asks.

var (
	// hdMeta is the board's keyed documents: one per session.
	hdMeta = hdMetaOf(1, nil)
	// hdMetaV2 is `hdMeta` at version 2: an archive of it cannot be read by version 1's code.
	hdMetaV2 = hdMetaOf(2, func(old *delta.JsonObject, _ int) (*delta.JsonObject, error) { return old, nil })
	hdLabels = hdTable("labels", store.Col("session", store.Host(Session)), store.Col("label", store.Str()))
	// hdLabelsV2 is `hdLabels` at version 2: a color per label, set by a `rows` step.
	hdLabelsV2 = hdEvolved(hdLabels)
	hdEntries  = hdTable("entries", store.Col("session", store.Host(Session)), store.Col("n", store.Int()))
	hdRow      = exObj(exProp("session", exObj(exProp("kind", exStr()), exProp("params", exObj(exProp("id", exStr()))), exProp("key", exStr()))), exProp("label", exStr()))
	// hdBoard is S1's title and labels as live members and a derive over both; any session's title read on demand.
	hdBoard = exLocal("test.board", contracts.Params{},
		contracts.Mem("meta", exObj(exProp("title", exStr()))),
		contracts.Mem("metas", contracts.Keyed(exObj(exProp("title", exStr())), Session)),
		contracts.Mem("labels", exArr(hdRow)),
		contracts.Mem("summary", exStr()),
		contracts.Mem("write", contracts.NewMethod(exObj(exProp("id", exStr()), exProp("title", exStr()), exProp("labels", exArr(exStr()))))),
		contracts.Mem("title", contracts.NewMethod(exStr(), exStr())))
	// hdView is one session's title as the board holds it: a view activated on demand (it has params).
	hdView = exLocal("test.view", contracts.Params{exProp("id", exStr())}, contracts.Mem("meta", exObj(exProp("title", exStr()))))
	// hdBoardV2 is the board's owner name with version 2 of its table: its labels' colors.
	hdBoardV2 = exLocal("test.board", contracts.Params{}, contracts.Mem("colors", contracts.NewMethod(exStr(), exArr(exStr()))))
	// hdLedger has entries of sessions in a table only: no host-keyed document, no live query.
	hdLedger = exLocal("test.ledger", contracts.Params{},
		contracts.Mem("put", contracts.NewMethod(exStr())),
		contracts.Mem("twice", contracts.NewMethod(exStr(), exArr(exInt()))),
		contracts.Mem("count", contracts.NewMethod(exStr(), exInt())))
	hdS1 = Session.Address(jsonObject("id", "S1"))
)

func hdMetaOf(version int, migrate func(*delta.JsonObject, int) (*delta.JsonObject, error)) *documents.StateDefinition {
	definition, err := documents.DefineState("meta", documents.StateOptions{
		Schema: exObj(exProp("title", exStr())), Version: version, Migrate: migrate,
		Initial: func() *delta.JsonObject { return jsonObject("title", "") },
		Keyed:   Session, Persist: true,
		CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
	})
	if err != nil {
		panic(err)
	}
	return definition
}

func hdTable(name string, columns ...store.ColumnProp) *store.TableDefinition {
	definition, err := store.DefineTable(name, store.TableOptions{Version: 1, Columns: columns, Key: []string{columns[0].Name, columns[1].Name}})
	if err != nil {
		panic(err)
	}
	return definition
}

func hdEvolved(previous *store.TableDefinition) *store.TableDefinition {
	definition, err := store.EvolveTable(previous, func(e *store.TableEditor) store.TableTailEditor {
		return e.Add("color", store.Str(contracts.Default("none"))).Rows(func(row store.Row) (any, error) {
			if row["label"] == "a" {
				return store.Row{"color": "blue"}, nil
			}
			return store.Row{"color": "red"}, nil
		})
	})
	if err != nil {
		panic(err)
	}
	return definition
}

// hdAddressJSON is an address as a live query's row holds it: `{ kind, params, key }`.
func hdAddressJSON(address contracts.Address) *delta.JsonObject {
	return jsonObject("kind", address.Kind, "params", address.Params, "key", address.Key)
}

// hdWorld is one test's `between`: where `Ledger.twice` waits between its two reads (its transaction holds the ledger's line
// meanwhile); entered tells that a call of it reached its wait.
type hdWorld struct {
	mu      sync.Mutex
	between chan struct{}
	entered chan struct{}
}

func newHdWorld() *hdWorld {
	return &hdWorld{between: hdClosed(), entered: make(chan struct{}, 8)}
}

func hdClosed() chan struct{} {
	free := make(chan struct{})
	close(free)
	return free
}

func (w *hdWorld) setBetween(gate chan struct{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.between = gate
}

func (w *hdWorld) waitBetween(ctx context.Context) error {
	w.mu.Lock()
	gate := w.between
	w.mu.Unlock()
	_, err := chordctx.Receive(ctx, gate)
	return err
}

// hdRowsOf is a page of the rows of session id in table.
func hdRowsOf(ctx context.Context, table store.Table, definition *store.TableDefinition, id string) (store.Page, error) {
	return table.Page(ctx, store.NewQuery(definition).Where(func(t store.ColumnRefs) store.Condition {
		return store.Eq(t["session"], Session.Address(jsonObject("id", id)))
	}), 10, store.PageOptions{})
}

// setup is the facet setup serving `Board` (with `meta` at metaDefinition, its labels at labelsDefinition) and `Ledger`: a
// reload takes it again with another table version.
func (w *hdWorld) setup(metaDefinition *documents.StateDefinition, labelsDefinition *store.TableDefinition) func(FacetHost, FacetSelf) error {
	return func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(hdBoard, func(_ *delta.JsonObject, plain Self) (contracts.Implementation, error) {
			self := plain.(StoreSelf)
			meta := self.State(metaDefinition).(*documents.Keyed)
			// Version 2 adds a defaulted column only: version 1's puts and queries fit both.
			labels := self.Table(labelsDefinition)
			pinned, err := meta.At(hdS1)
			if err != nil {
				return contracts.Implementation{}, err
			}
			rows := self.Query(labels, store.NewQuery(labelsDefinition).Where(func(t store.ColumnRefs) store.Condition { return store.Eq(t["session"], hdS1) }), 10)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"meta":   Expose(pinned),
					"labels": rows,
					"summary": self.Derive([]any{Expose(pinned), rows}, func(values []any) (delta.JsonValue, error) {
						title := values[0].(*delta.JsonObject).Value("title")
						return fmt.Sprintf("%v:%d", title, len(values[1].([]any))), nil
					}),
				},
				Keyed: map[string]contracts.KeyedWatchable[delta.JsonValue]{"metas": ExposeKeyed(meta)},
				Methods: map[string]contracts.MethodFunction{
					"write": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
						_, err := self.Tx(func(tx documents.Tx) (any, error) {
							session := Session.Address(jsonObject("id", exParam(args, "id")))
							draft, err := tx.Draft(meta, session)
							if err != nil {
								return nil, err
							}
							if err := draft.Set("title", args.Value("title")); err != nil {
								return nil, err
							}
							for _, label := range args.Value("labels").([]any) {
								if err := tx.Put(labels, store.Row{"session": session, "label": label}); err != nil {
									return nil, err
								}
							}
							return nil, nil
						}, ctx)
						return nil, err
					}),
					"title": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						found, err := meta.Get(ctx, Session.Address(jsonObject("id", id.(string))))
						if err != nil || found == nil {
							return "", err
						}
						return found.Value("title"), nil
					},
				},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		if err := h.Serve(hdView, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			at, err := self.Get(hdBoard, jsonObject()).Keyed("metas").At(Session.Address(jsonObject("id", exParam(params, "id"))))
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"meta": Expose(at)},
			}}, err
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(hdLedger, func(_ *delta.JsonObject, plain Self) (contracts.Implementation, error) {
			self := plain.(StoreSelf)
			entries := self.Table(hdEntries)
			return exImpl(nil, map[string]contracts.MethodFunction{
				"put": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					_, err := self.Tx(func(tx documents.Tx) (any, error) {
						return nil, tx.Put(entries, store.Row{"session": Session.Address(jsonObject("id", id.(string))), "n": int64(1)})
					}, ctx)
					return nil, err
				},
				"twice": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					value, err := self.Tx(func(tx documents.Tx) (any, error) {
						key := store.Row{"session": Session.Address(jsonObject("id", id.(string))), "n": int64(1)}
						count := func() (any, error) {
							row, err := tx.Get(entries, key)
							if err != nil || row == nil {
								return float64(0), err
							}
							return float64(row["n"].(int64)), nil
						}
						first, err := count()
						if err != nil {
							return nil, err
						}
						select {
						case w.entered <- struct{}{}:
						default:
						}
						if err := w.waitBetween(ctx); err != nil {
							return nil, err
						}
						second, err := count()
						if err != nil {
							return nil, err
						}
						return []any{first, second}, nil
					}, ctx)
					if err != nil {
						return nil, err
					}
					return value.([]any), nil
				},
				"count": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					page, err := hdRowsOf(ctx, entries, hdEntries, id.(string))
					return float64(len(page.Items)), err
				},
			}), nil
		}, ServeOptions{})
	}
}

// hdBoardHost is a host serving `setup(metaDefinition)` on a store, references to the board and the ledger.
type hdBoardHost struct {
	host   Host
	board  *Reference
	ledger *Reference
	errors *hostErrors
}

// served is a host serving `setup(metaDefinition)` on chordStore, its errors; nothing activated yet.
func (w *hdWorld) served(t *testing.T, chordStore *store.ChordStore, metaDefinition *documents.StateDefinition) (Host, *hostErrors) {
	t.Helper()
	return loaded(t, w.setup(metaDefinition, hdLabels), loadedOptions{Store: chordStore})
}

// board is a host serving `setup(metaDefinition)` on chordStore, references to the board and the ledger.
func (w *hdWorld) board(t *testing.T, chordStore *store.ChordStore, metaDefinition *documents.StateDefinition) hdBoardHost {
	t.Helper()
	h, errs := w.served(t, chordStore, metaDefinition)
	return hdBoardHost{host: h, board: mustGet(t, h, hdBoard, jsonObject()), ledger: mustGet(t, h, hdLedger, jsonObject()), errors: errs}
}

// hdResult is what a goroutine of hdGo ended with.
type hdResult[T any] struct {
	value T
	err   error
}

// hdGo runs work unawaited, as a Promise a test leaves pending.
func hdGo[T any](work func() (T, error)) <-chan hdResult[T] {
	done := make(chan hdResult[T], 1)
	go func() {
		value, err := work()
		done <- hdResult[T]{value, err}
	}()
	return done
}

// hdAwait is `await promise`.
func hdAwait[T any](t testing.TB, what string, done <-chan hdResult[T]) (T, error) {
	t.Helper()
	result := exReceive(t, what, done)
	return result.value, result.err
}

// hdSettled reports whether done has its result already, without taking it.
func hdSettled[T any](done <-chan hdResult[T]) bool { return len(done) > 0 }

// hdPending fails the test when done has ended: the operation it runs was to be waiting still.
func hdPending[T any](t testing.TB, what string, done <-chan hdResult[T]) {
	t.Helper()
	if hdSettled(done) {
		t.Fatalf("%s ended, want it waiting", what)
	}
}

// holdLedger holds the ledger's line in a transaction until the returned release (an operation waits for it, exclusive).
func (w *hdWorld) holdLedger(t *testing.T, ledger *Reference) (release func()) {
	t.Helper()
	gate := make(chan struct{})
	w.setBetween(gate)
	reads := hdGo(func() (delta.JsonValue, error) { return ledger.Call("twice", "S1", background) })
	exReceive(t, "the ledger's transaction waiting", w.entered)
	return func() {
		close(gate)
		_, err := hdAwait(t, "the ledger's reads", reads)
		must(t, err)
		w.setBetween(hdClosed())
	}
}

// hdWaitingOnLine is the number of holders waiting for the line of the live object of owner.
func hdWaitingOnLine(h Host, owner string) int {
	host := h.(*chordHost)
	waiting := 0
	host.enter(func() {
		for _, object := range host.objectList() {
			if object.owner() == owner {
				waiting += object.documents.Line().WaitingHolders()
			}
		}
	})
	return waiting
}

// hdGateState is whether an operation is exclusive and how many activations wait at the gate.
func hdGateState(h Host) (exclusive bool, waiting int) {
	host := h.(*chordHost)
	host.enter(func() { exclusive, waiting = host.hostData.isExclusive, len(host.hostData.opened) })
	return exclusive, waiting
}

// hdGateTail is the gate's last turn: it changes when an operation queues.
func hdGateTail(gate *hostDataGate) <-chan struct{} {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.tail
}

// hdTail is the host gate's last turn.
func hdTail(h Host) <-chan struct{} { return hdGateTail(h.(*chordHost).hostData) }

// hdEventually polls cond: what the host reaches in goroutines of its own.
func hdEventually(t testing.TB, what string, cond func() bool) {
	t.Helper()
	eventually(t, what, cond)
}

// hdParts is a fresh source of parts: one archive is read once.
func hdParts(parts []store.ArchivePart) store.PartSource { return store.SlicePartSource(parts) }

// hdExported is the parts of h's export of S1.
func hdExported(t *testing.T, h Host) []store.ArchivePart {
	t.Helper()
	export, err := h.ExportHostData(hdS1, background)
	must(t, err)
	return exCollectParts(t, export)
}

// hdArchiveOfS1 is S1's data from another store: title "one", labels a and b, one ledger entry.
func hdArchiveOfS1(t *testing.T, metaDefinition *documents.StateDefinition) []store.ArchivePart {
	t.Helper()
	w := newHdWorld()
	source := w.board(t, memoryStore(t), metaDefinition)
	exCall(t, source.board, "write", jsonObject("id", "S1", "title", "one", "labels", []any{"a", "b"}))
	exCall(t, source.ledger, "put", "S1")
	parts := hdExported(t, source.host)
	closeHosts(t, source.host)
	return parts
}

// hdMember is `ref.<member>.value`.
func hdMember(t *testing.T, ref *Reference, member string) delta.JsonValue {
	t.Helper()
	return exValue(t, ref, member)
}

// hdLabelsOf is the `label` of each row of a live query's value.
func hdLabelsOf(value delta.JsonValue) []any {
	labels := []any{}
	for _, row := range value.([]any) {
		labels = append(labels, row.(*delta.JsonObject).Value("label"))
	}
	return labels
}

func hdCount(t *testing.T, ledger *Reference) float64 {
	t.Helper()
	return exCall(t, ledger, "count", "S1").(float64)
}

// --- live owners ---------------------------------------------------------------------------------------------------

// host-data.test.ts › live owners › follow an import: a pinned key and a live query show the imported data, a resident key
// reads it.
func TestHostDataLiveOwnersFollowAnImportAPinnedKeyAndALiveQueryShowTheImportedDataAResidentKeyReadsIt(t *testing.T) {
	parts := hdArchiveOfS1(t, hdMeta)
	w := newHdWorld()
	live := w.board(t, memoryStore(t), hdMeta)
	h := live.host
	exCall(t, live.board, "write", jsonObject("id", "S1", "title", "old", "labels", []any{"z"}))
	exCall(t, live.board, "write", jsonObject("id", "S2", "title", "old two", "labels", []any{}))
	exEqual(t, "title(S2)", exCall(t, live.board, "title", "S2"), "old two")
	exEqual(t, "meta", hdMember(t, live.board, "meta"), jsonObject("title", "old"))
	_, err := h.ImportHostData(hdParts(parts), background, store.HostImportOptions{Existing: store.ExistingReplace})
	must(t, err)
	exEqual(t, "meta", hdMember(t, live.board, "meta"), jsonObject("title", "one"))
	rows := hdMember(t, live.board, "labels")
	exEqual(t, "labels", hdLabelsOf(rows), []any{"a", "b"})
	exEqual(t, "session", rows.([]any)[0].(*delta.JsonObject).Value("session"), hdAddressJSON(hdS1))
	// A copy under S2 replaces the resident S2 document: the next read is the imported one.
	s2 := Session.Address(jsonObject("id", "S2"))
	_, err = h.ImportHostData(hdParts(parts), background, store.HostImportOptions{Address: &s2, Existing: store.ExistingReplace})
	must(t, err)
	exEqual(t, "title(S2)", exCall(t, live.board, "title", "S2"), "one")
	closeHosts(t, h)
}

// host-data.test.ts › live owners › adopt every source in one batch: a derive over a document and a query sees one frame,
// never a torn state.
func TestHostDataLiveOwnersAdoptEverySourceInOneBatchADeriveOverADocumentAndAQuerySeesOneFrameNeverATornState(t *testing.T) {
	parts := hdArchiveOfS1(t, hdMeta)
	w := newHdWorld()
	live := w.board(t, memoryStore(t), hdMeta)
	h := live.host
	exCall(t, live.board, "write", jsonObject("id", "S1", "title", "old", "labels", []any{"z"}))
	watch, err := live.board.Member("summary").Watch(background)
	must(t, err)
	frames := nexttest.FramesOf(watch)
	_, err = h.ImportHostData(hdParts(parts), background, store.HostImportOptions{Existing: store.ExistingReplace})
	must(t, err)
	deliveredOn(h)
	// One frame from "old:1" to "one:2": no "one:1" or "old:2" in between.
	exEqual(t, "frames", valuesOfFrames(frames.Items()), []any{"one:2"})
	closeHosts(t, h)
}

// host-data.test.ts › live owners › follow a delete: the pinned key reads initial(), the live query is empty.
func TestHostDataLiveOwnersFollowADeleteThePinnedKeyReadsInitialTheLiveQueryIsEmpty(t *testing.T) {
	w := newHdWorld()
	live := w.board(t, memoryStore(t), hdMeta)
	h := live.host
	exCall(t, live.board, "write", jsonObject("id", "S1", "title", "one", "labels", []any{"a"}))
	counts, err := h.DeleteHostData(hdS1, background)
	must(t, err)
	if counts != (store.HostDataCounts{Documents: 1, Rows: 1}) {
		t.Fatalf("deleted = %+v, want {Documents:1 Rows:1}", counts)
	}
	exEqual(t, "meta", hdMember(t, live.board, "meta"), jsonObject("title", ""))
	exEqual(t, "labels", hdMember(t, live.board, "labels"), []any{})
	closeHosts(t, h)
}

// host-data.test.ts › live owners › wait for a transaction of a table's owner without slots or queries: its two reads see the
// same row.
func TestHostDataLiveOwnersWaitForATransactionOfATablesOwnerWithoutSlotsOrQueriesItsTwoReadsSeeTheSameRow(t *testing.T) {
	w := newHdWorld()
	live := w.board(t, memoryStore(t), hdMeta)
	h := live.host
	exCall(t, live.ledger, "put", "S1")
	gate := make(chan struct{})
	w.setBetween(gate)
	reads := hdGo(func() (delta.JsonValue, error) { return live.ledger.Call("twice", "S1", background) })
	exReceive(t, "the ledger's transaction waiting", w.entered)
	deleting := hdGo(func() (store.HostDataCounts, error) { return h.DeleteHostData(hdS1, background) })
	hdEventually(t, "the delete waiting for the ledger's line", func() bool { return hdWaitingOnLine(h, "test.ledger") > 0 })
	hdPending(t, "the delete", deleting)
	close(gate)
	got, err := hdAwait(t, "the ledger's reads", reads)
	must(t, err)
	exEqual(t, "twice", got, []any{float64(1), float64(1)})
	_, err = hdAwait(t, "the delete", deleting)
	must(t, err)
	if count := hdCount(t, live.ledger); count != 0 {
		t.Fatalf("count = %v, want 0", count)
	}
	w.setBetween(hdClosed())
	closeHosts(t, h)
}

// --- lifecycle -----------------------------------------------------------------------------------------------------

// host-data.test.ts › lifecycle › an abort before the commit was admitted writes nothing.
func TestHostDataLifecycleAnAbortBeforeTheCommitWasAdmittedWritesNothing(t *testing.T) {
	st := memoryStore(t)
	w := newHdWorld()
	live := w.board(t, st, hdMeta)
	h := live.host
	exCall(t, live.ledger, "put", "S1")
	gate := make(chan struct{})
	w.setBetween(gate)
	reads := hdGo(func() (delta.JsonValue, error) { return live.ledger.Call("twice", "S1", background) })
	exReceive(t, "the ledger's transaction waiting", w.entered)
	cancellable, cancel := chordctx.WithCancel(background)
	deleting := hdGo(func() (store.HostDataCounts, error) { return h.DeleteHostData(hdS1, cancellable) })
	hdEventually(t, "the delete waiting for the ledger's line", func() bool { return hdWaitingOnLine(h, "test.ledger") > 0 })
	seq := st.LastSeq()
	cancel(errors.New("given up"))
	_, err := hdAwait(t, "the delete", deleting)
	if err == nil || err.Error() != "given up" {
		t.Fatalf("delete = %v, want given up", err)
	}
	close(gate)
	_, err = hdAwait(t, "the ledger's reads", reads)
	must(t, err)
	w.setBetween(hdClosed())
	if st.LastSeq() != seq {
		t.Fatalf("lastSeq = %d, want %d: nothing written", st.LastSeq(), seq)
	}
	if count := hdCount(t, live.ledger); count != 1 {
		t.Fatalf("count = %v, want 1", count)
	}
	closeHosts(t, h)
}

// hdCancelAfterEnters is site whose `enter` cancels the operation's context right after its n-th section ran: the abort
// reaches the operation at a step of its own, deterministically, instead of while it waits for a line.
func hdCancelAfterEnters(site hostDataSite, n int, cancel func(error)) (wrapped hostDataSite, entered func() int) {
	var mu sync.Mutex
	count := 0
	enter := site.enter
	site.enter = func(work func()) {
		enter(work)
		mu.Lock()
		count++
		cancelNow := count == n
		mu.Unlock()
		if cancelNow {
			cancel(errors.New("given up"))
		}
	}
	return site, func() int {
		mu.Lock()
		defer mu.Unlock()
		return count
	}
}

// Go-only completion of host-data.test.ts › lifecycle › an abort before the commit was admitted writes nothing: Pi's test
// aborts while the delete waits for the ledger's line, which settles before the commit and leaves the commit fence's
// admission (`admit: () => context.abortSignal?.aborted !== true`) untested. Here the abort comes after every line was
// taken and the objects' keys and queries were gathered, right before the commit: the store refuses it `released` and
// writes nothing, and the same delete without the abort commits.
func TestHostDataLifecycleADeleteAbortedAfterItsLinesWereTakenIsRefusedAtAdmissionAndWritesNothing(t *testing.T) {
	st := memoryStore(t)
	w := newHdWorld()
	live := w.board(t, st, hdMeta)
	h := live.host
	exCall(t, live.ledger, "put", "S1")
	site, err := h.(*chordHost).dataSite("deleteHostData")
	must(t, err)
	seq := st.LastSeq()
	cancellable, cancel := chordctx.WithCancel(background)
	// coordinated enters: the objects owning the key's data, then (their lines held) its slots and live queries.
	aborting, entered := hdCancelAfterEnters(site, 2, cancel)
	counts, err := deleteFrom(aborting, hdS1, cancellable)
	wantChordError(t, err, data.CodeReleased, data.ReasonDiscarded, "store: the commit's attempt ended")
	if counts != (store.HostDataCounts{}) {
		t.Fatalf("aborted delete counts = %+v, want none", counts)
	}
	if got := entered(); got != 2 {
		t.Fatalf("sections entered = %d, want 2: the abort came after the lines were taken and nothing was adopted", got)
	}
	if st.LastSeq() != seq {
		t.Fatalf("lastSeq = %d, want %d: nothing written", st.LastSeq(), seq)
	}
	if count := hdCount(t, live.ledger); count != 1 {
		t.Fatalf("count = %v, want 1: the refused delete left the row", count)
	}
	// The lines were released: the same delete, not aborted, commits.
	counts, err = h.DeleteHostData(hdS1, background)
	must(t, err)
	if counts.Rows != 1 {
		t.Fatalf("delete = %+v, want its one row", counts)
	}
	if count := hdCount(t, live.ledger); count != 0 {
		t.Fatalf("count = %v, want 0", count)
	}
	closeHosts(t, h)
}

// Go-only completion of the same lifecycle test for an import: aborted after the owners' lines were taken, right before its
// commit, it writes nothing (the address stays free: the same import, not aborted, is then not refused as an existing one).
func TestHostDataLifecycleAnImportAbortedAfterItsLinesWereTakenIsRefusedAtAdmissionAndWritesNothing(t *testing.T) {
	parts := hdArchiveOfS1(t, hdMeta)
	st := memoryStore(t)
	w := newHdWorld()
	live := w.board(t, st, hdMeta)
	h := live.host
	site, err := h.(*chordHost).dataSite("importHostData")
	must(t, err)
	seq := st.LastSeq()
	cancellable, cancel := chordctx.WithCancel(background)
	// importInto enters: the tables' histories; coordinated's: the objects, then (their lines held) slots and live queries.
	aborting, entered := hdCancelAfterEnters(site, 3, cancel)
	counts, err := importInto(aborting, hdParts(parts), cancellable, store.HostImportOptions{})
	wantChordError(t, err, data.CodeReleased, data.ReasonDiscarded, "store: the commit's attempt ended")
	if counts != (store.HostDataCounts{}) {
		t.Fatalf("aborted import counts = %+v, want none", counts)
	}
	if got := entered(); got != 3 {
		t.Fatalf("sections entered = %d, want 3: the abort came after the lines were taken and nothing was adopted", got)
	}
	if st.LastSeq() != seq {
		t.Fatalf("lastSeq = %d, want %d: nothing written", st.LastSeq(), seq)
	}
	exEqual(t, "meta", hdMember(t, live.board, "meta"), jsonObject("title", ""))
	counts, err = h.ImportHostData(hdParts(parts), background, store.HostImportOptions{})
	must(t, err)
	if counts.Documents != 1 {
		t.Fatalf("import = %+v, want its one document: the address was free", counts)
	}
	exEqual(t, "meta", hdMember(t, live.board, "meta"), jsonObject("title", "one"))
	closeHosts(t, h)
}

// host-data.test.ts › lifecycle › an owner that cannot read an imported document fails alone; the other owners adopt.
func TestHostDataLifecycleAnOwnerThatCannotReadAnImportedDocumentFailsAloneTheOtherOwnersAdopt(t *testing.T) {
	parts := hdArchiveOfS1(t, hdMetaV2)
	w := newHdWorld()
	live := w.board(t, memoryStore(t), hdMeta)
	_, err := live.host.ImportHostData(hdParts(parts), background, store.HostImportOptions{})
	must(t, err)
	hdEventually(t, "the board failing", func() bool {
		for _, each := range live.errors.All() {
			if each.Kind == ErrObjectFailed && each.Object == "test.board" {
				return true
			}
		}
		return false
	})
	if count := hdCount(t, live.ledger); count != 1 {
		t.Fatalf("count = %v, want 1", count)
	}
	closeHosts(t, live.host)
}

// host-data.test.ts › lifecycle › a host without a store refuses every host data call.
func TestHostDataLifecycleAHostWithoutAStoreRefusesEveryHostDataCall(t *testing.T) {
	h := newHost(t, HostOptions{OnError: func(HostError) {}})
	mustLoad(t)(h.Load(nil, background))
	_, err := h.DeleteHostData(hdS1, background)
	wantChordError(t, err, data.CodeInvalid, data.ReasonArgs, "deleteHostData: the host has no store")
	if _, err = h.ExportHostData(hdS1, background); err == nil || !hasText(err.Error(), "exportHostData: the host has no store") {
		t.Fatalf("exportHostData = %v, want it to refuse: the host has no store", err)
	}
	_, err = h.ImportHostData(store.SlicePartSource([]any{}), background, store.HostImportOptions{})
	wantChordError(t, err, data.CodeInvalid, "", "importHostData: the host has no store")
	closeHosts(t, h)
}

// --- older and newer tables ----------------------------------------------------------------------------------------

// hdBoardV2Host is a host serving `BoardV2` (the board's owner with `LabelsV2`) on chordStore.
func hdBoardV2Host(t *testing.T, chordStore *store.ChordStore) Host {
	t.Helper()
	h, _ := loaded(t, func(facet FacetHost, _ FacetSelf) error {
		return facet.Serve(hdBoardV2, func(_ *delta.JsonObject, plain Self) (contracts.Implementation, error) {
			labels := plain.(StoreSelf).Table(hdLabelsV2)
			return exImpl(nil, map[string]contracts.MethodFunction{
				"colors": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					page, err := hdRowsOf(ctx, labels, hdLabelsV2, id.(string))
					if err != nil {
						return nil, err
					}
					colors := []any{}
					for _, row := range page.Items {
						colors = append(colors, fmt.Sprintf("%v=%v", row["label"], row["color"]))
					}
					return colors, nil
				},
			}), nil
		}, ServeOptions{})
	}, loadedOptions{Store: chordStore})
	return h
}

// host-data.test.ts › older and newer tables › an older table's rows migrate through the owner's history (its owner activated
// for it).
func TestHostDataOlderAndNewerTablesAnOlderTablesRowsMigrateThroughTheOwnersHistoryItsOwnerActivatedForIt(t *testing.T) {
	parts := hdArchiveOfS1(t, hdMeta)
	st := memoryStore(t)
	closeHosts(t, hdBoardV2Host(t, st))
	// The owner is not live: the import activates it to read its history.
	h := hdBoardV2Host(t, st)
	_, err := h.ImportHostData(hdParts(parts), background, store.HostImportOptions{})
	must(t, err)
	colors := mustGet(t, h, hdBoardV2, jsonObject())
	exEqual(t, "colors", exCall(t, colors, "colors", "S1"), []any{"a=blue", "b=red"})
	closeHosts(t, h)
}

// host-data.test.ts › older and newer tables › a newer table in the archive is refused.
func TestHostDataOlderAndNewerTablesANewerTableInTheArchiveIsRefused(t *testing.T) {
	var parts []store.ArchivePart
	for _, part := range hdArchiveOfS1(t, hdMeta) {
		if write, isTable := part.(*store.TableDefinitionWrite); isTable && write.Table == "test.board:labels" {
			newer := *write
			newer.Version = 2
			part = &newer
		}
		parts = append(parts, part)
	}
	w := newHdWorld()
	live := w.board(t, memoryStore(t), hdMeta)
	_, err := live.host.ImportHostData(hdParts(parts), background, store.HostImportOptions{})
	if err == nil || !hasText(err.Error(), "at version 2 in the archive, newer than 1 here") {
		t.Fatalf("import = %v, want it to refuse: at version 2 in the archive, newer than 1 here", err)
	}
	closeHosts(t, live.host)
}

// --- the gate ------------------------------------------------------------------------------------------------------

// hdOrder is the order things happened in, appended from several goroutines.
type hdOrder struct {
	mu   sync.Mutex
	list []string
}

func (o *hdOrder) add(entry string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.list = append(o.list, entry)
}

func (o *hdOrder) all() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.list...)
}

func (o *hdOrder) has(entry string) bool {
	return slices.Contains(o.all(), entry)
}

// host-data.test.ts › the gate › keeps load phases out while an operation is exclusive, and an operation waits for load
// phases under way.
func TestHostDataTheGateKeepsLoadPhasesOutWhileAnOperationIsExclusiveAndAnOperationWaitsForLoadPhasesUnderWay(t *testing.T) {
	loop := NewLoop()
	gate := newHostDataGate(loop)
	order := &hdOrder{}
	loading := make(chan struct{})
	holding := make(chan struct{})
	load := func(body func()) <-chan hdResult[struct{}] {
		return hdGo(func() (struct{}, error) {
			var entering *keyedPromise[struct{}]
			loop.Run(func() { entering = gate.enter() })
			if entering != nil {
				if _, err := entering.await(background); err != nil {
					return struct{}{}, err
				}
			}
			defer loop.Run(gate.leave)
			body()
			return struct{}{}, nil
		})
	}
	first := load(func() {
		order.add("load 1")
		<-loading
		order.add("load 1 done")
	})
	hdEventually(t, "load 1", func() bool { return order.has("load 1") })
	operating := hdGo(func() (struct{}, error) {
		return struct{}{}, gate.turn(func(exclusive func(section func() error) error) error {
			order.add("turn")
			return exclusive(func() error {
				order.add("exclusive")
				<-holding
				order.add("exclusive done")
				return nil
			})
		}, background)
	})
	hdEventually(t, "the operation exclusive", func() bool {
		var exclusive bool
		loop.Run(func() { exclusive = gate.isExclusive })
		return exclusive
	})
	second := load(func() { order.add("load 2") })
	hdEventually(t, "load 2 waiting", func() bool {
		var waiting int
		loop.Run(func() { waiting = len(gate.opened) })
		return waiting == 1
	})
	close(loading)
	_, err := hdAwait(t, "load 1", first)
	must(t, err)
	hdEventually(t, "the exclusive section", func() bool { return order.has("exclusive") })
	exEqual(t, "order", order.all(), []string{"load 1", "turn", "load 1 done", "exclusive"})
	close(holding)
	_, err = hdAwait(t, "the operation", operating)
	must(t, err)
	_, err = hdAwait(t, "load 2", second)
	must(t, err)
	exEqual(t, "order", order.all(), []string{"load 1", "turn", "load 1 done", "exclusive", "exclusive done", "load 2"})
}

// host-data.test.ts › the gate › an operation given up while waiting for its turn passes the turn on.
func TestHostDataTheGateAnOperationGivenUpWhileWaitingForItsTurnPassesTheTurnOn(t *testing.T) {
	gate := newHostDataGate(NewLoop())
	// queued runs turn and returns once it took its place: the order of the turns is the order of the calls.
	queued := func(turn func() error) <-chan hdResult[struct{}] {
		before := hdGateTail(gate)
		done := hdGo(func() (struct{}, error) { return struct{}{}, turn() })
		hdEventually(t, "a turn queued", func() bool { return hdGateTail(gate) != before })
		return done
	}
	running := make(chan struct{})
	first := queued(func() error {
		return gate.turn(func(exclusive func(section func() error) error) error {
			return exclusive(func() error { <-running; return nil })
		}, background)
	})
	cancellable, cancel := chordctx.WithCancel(background)
	second := queued(func() error {
		return gate.turn(func(func(func() error) error) error { return errors.New("second ran") }, cancellable)
	})
	ran := ""
	third := queued(func() error {
		return gate.turn(func(func(func() error) error) error { ran = "third"; return nil }, background)
	})
	cancel(errors.New("given up"))
	if _, err := hdAwait(t, "the second turn", second); err == nil || err.Error() != "given up" {
		t.Fatalf("second = %v, want given up", err)
	}
	hdPending(t, "the third turn", third)
	close(running)
	_, err := hdAwait(t, "the first turn", first)
	must(t, err)
	_, err = hdAwait(t, "the third turn", third)
	must(t, err)
	if ran != "third" {
		t.Fatalf("third ran = %q, want third", ran)
	}
}

// --- review round 3 ------------------------------------------------------------------------------------------------

// host-data.test.ts › review round 3 › an activation during an import (a view pinning the board's key) waits and reads what
// the import committed.
func TestHostDataReviewRound3AnActivationDuringAnImportAViewPinningTheBoardsKeyWaitsAndReadsWhatTheImportCommitted(t *testing.T) {
	parts := hdArchiveOfS1(t, hdMeta)
	w := newHdWorld()
	h, _ := w.served(t, memoryStore(t), hdMeta)
	ledger := mustGet(t, h, hdLedger, jsonObject())
	release := w.holdLedger(t, ledger)
	importing := hdGo(func() (store.HostDataCounts, error) {
		return h.ImportHostData(hdParts(parts), background, store.HostImportOptions{})
	})
	hdEventually(t, "the import waiting for the ledger's line", func() bool { return hdWaitingOnLine(h, "test.ledger") > 0 })
	getting := hdGo(func() (*Reference, error) { return h.Get(background, hdView, jsonObject("id", "S1"), GetOptions{}) })
	hdEventually(t, "the activation waiting at the gate", func() bool {
		exclusive, waiting := hdGateState(h)
		return exclusive && waiting > 0
	})
	hdPending(t, "the view's activation", getting)
	release()
	_, err := hdAwait(t, "the import", importing)
	must(t, err)
	view, err := hdAwait(t, "the view", getting)
	must(t, err)
	exEqual(t, "meta", hdMember(t, view, "meta"), jsonObject("title", "one"))
	closeHosts(t, h)
}

// host-data.test.ts › review round 3 › a reload during an import: the successor loads after it; a queued import sees the
// table its successor migrated.
func TestHostDataReviewRound3AReloadDuringAnImportTheSuccessorLoadsAfterItAQueuedImportSeesTheTableItsSuccessorMigrated(t *testing.T) {
	parts := hdArchiveOfS1(t, hdMeta)
	w := newHdWorld()
	live := w.board(t, memoryStore(t), hdMeta)
	h := live.host
	release := w.holdLedger(t, live.ledger)
	// An earlier operation holds the gate; the import queues behind it, the reload's successors wait at the gate.
	deleting := hdGo(func() (store.HostDataCounts, error) {
		return h.DeleteHostData(Session.Address(jsonObject("id", "S9")), background)
	})
	hdEventually(t, "the delete exclusive", func() bool {
		exclusive, _ := hdGateState(h)
		return exclusive && hdWaitingOnLine(h, "test.ledger") > 0
	})
	tail := hdTail(h)
	importing := hdGo(func() (store.HostDataCounts, error) {
		return h.ImportHostData(hdParts(parts), background, store.HostImportOptions{})
	})
	hdEventually(t, "the import queued", func() bool { return hdTail(h) != tail })
	reloading := hdGo(func() (ReloadReport, error) {
		return h.Reload([]HostFacet{{Name: "@test/app", Facet: DefineFacet(w.setup(hdMeta, hdLabelsV2))}}, background, ReloadOptions{})
	})
	hdEventually(t, "the reload under way", func() bool {
		var started bool
		h.(*chordHost).enter(func() { started = h.(*chordHost).reloading != nil })
		return started
	})
	release()
	_, err := hdAwait(t, "the delete", deleting)
	must(t, err)
	_, err = hdAwait(t, "the reload", reloading)
	must(t, err)
	_, err = hdAwait(t, "the import", importing)
	must(t, err)
	hdEventually(t, "the board showing the import", func() bool {
		value, err := live.board.Member("meta").Value()
		return err == nil && nexttest.JSONEqual(value, jsonObject("title", "one"))
	})
	var colors []any
	for _, row := range hdMember(t, live.board, "labels").([]any) {
		object := row.(*delta.JsonObject)
		colors = append(colors, fmt.Sprintf("%v=%v", object.Value("label"), object.Value("color")))
	}
	exEqual(t, "colors", colors, []any{"a=blue", "b=red"})
	closeHosts(t, h)
}

// host-data.test.ts › review round 3 › a cancellation after the commit was admitted changes nothing: it commits and is
// adopted.
func TestHostDataReviewRound3ACancellationAfterTheCommitWasAdmittedChangesNothingItCommitsAndIsAdopted(t *testing.T) {
	st, _, holdNextCommit := faultyStore(t)
	w := newHdWorld()
	live := w.board(t, st, hdMeta)
	h := live.host
	exCall(t, live.board, "write", jsonObject("id", "S1", "title", "one", "labels", []any{"a"}))
	held := holdNextCommit()
	cancellable, cancel := chordctx.WithCancel(background)
	deleting := hdGo(func() (store.HostDataCounts, error) { return h.DeleteHostData(hdS1, cancellable) })
	exReceive(t, "the commit entering", held.Entered())
	cancel(errors.New("too late"))
	held.Release()
	counts, err := hdAwait(t, "the delete", deleting)
	must(t, err)
	if counts != (store.HostDataCounts{Documents: 1, Rows: 1}) {
		t.Fatalf("deleted = %+v, want {Documents:1 Rows:1}", counts)
	}
	exEqual(t, "meta", hdMember(t, live.board, "meta"), jsonObject("title", ""))
	exEqual(t, "labels", hdMember(t, live.board, "labels"), []any{})
	closeHosts(t, h)
}

// host-data.test.ts › review round 3 › releases the owners it activated however the import ends (here refused: the owner
// declares no such table).
func TestHostDataReviewRound3ReleasesTheOwnersItActivatedHoweverTheImportEndsHereRefusedTheOwnerDeclaresNoSuchTable(t *testing.T) {
	parts := hdArchiveOfS1(t, hdMeta)
	st := memoryStore(t)
	// The store records the board's labels at version 2; the board served now declares no table.
	w := newHdWorld()
	first, _ := loaded(t, w.setup(hdMeta, hdLabelsV2), loadedOptions{Store: st})
	mustGet(t, first, hdBoard, jsonObject())
	closeHosts(t, first)
	h, _ := loaded(t, func(facet FacetHost, _ FacetSelf) error {
		return facet.Serve(hdBoardV2, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return exImpl(nil, map[string]contracts.MethodFunction{
				"colors": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return []any{}, nil },
			}), nil
		}, ServeOptions{})
	}, loadedOptions{Store: st})
	references := h.References(hdBoardV2)
	_, err := h.ImportHostData(hdParts(parts), background, store.HostImportOptions{})
	wantChordError(t, err, data.CodeInvalid, data.ReasonArgs, "importHostData: test.board is not served here to migrate its tables")
	var value delta.JsonValue
	h.(*chordHost).enter(func() { value, err = references.Value() })
	must(t, err)
	exEqual(t, "references", value, []any{})
	closeHosts(t, h)
}

func TestHostDataObjectsAreOrderedByIdentityInUTF16CodeUnitOrderAsJavaScriptSortsThem(t *testing.T) {
	// U+FF5E is one code unit 0xFF5E, U+1F600 the pair 0xD83D 0xDE00: by code point (and by byte) the first is smaller,
	// by code unit the second.
	low, high := "a\uff5e", "a\U0001f600"
	if got := compareUTF16(high, low); got >= 0 {
		t.Fatalf("compareUTF16(%q, %q) = %d, want < 0 (UTF-16 code unit order)", high, low, got)
	}
	if got := compareUTF16(low, high); got <= 0 {
		t.Fatalf("compareUTF16(%q, %q) = %d, want > 0", low, high, got)
	}
	if got := compareUTF16("abc", "abc"); got != 0 {
		t.Fatalf("compareUTF16 of equal strings = %d, want 0", got)
	}
}

// hdScratchDoc is the scratch object's in-memory document: declared by every activation of it, owned by no table.
var hdScratchDoc = func() *documents.StateDefinition {
	definition, err := documents.DefineState("scratch", documents.StateOptions{
		Schema: exObj(exProp("n", exInt())), Version: 1, Initial: func() *delta.JsonObject { return jsonObject("n", 0.0) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

// hdScratch has one in-memory document and no data of any host: no host data operation takes its line. It has params, so
// each get is a fresh activation (an object without params activates when the host loads).
var hdScratch = exLocal("test.scratch", contracts.Params{exProp("id", exStr())}, contracts.Mem("n", exObj(exProp("n", exInt()))))

// Go-only (Pi's TypeScript `documents.hydrate` throws a TypeError there, which its `#activate` catches as a failed
// activation; Go's nil slot is a crash of the host's loop): an activation waits at the gate while an operation is exclusive;
// its object ends meanwhile (a reload no longer serves it) and its documents with it; once the operation ends the activation
// resumes and must fail `released`, not hydrate the ended documents.
func TestHostDataAnActivationWaitingAtTheGateWhoseObjectEndedMeanwhileFailsReleased(t *testing.T) {
	w := newHdWorld()
	setup := func(h FacetHost, self FacetSelf) error {
		if err := w.setup(hdMeta, hdLabelsV2)(h, self); err != nil {
			return err
		}
		return h.Serve(hdScratch, func(_ *delta.JsonObject, plain Self) (contracts.Implementation, error) {
			counter := plain.State(hdScratchDoc).(*documents.Singleton)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": Expose(counter)},
			}}, nil
		}, ServeOptions{})
	}
	h, _ := loaded(t, setup, loadedOptions{Store: memoryStore(t)})
	ledger := mustGet(t, h, hdLedger, jsonObject())
	release := w.holdLedger(t, ledger)
	// The delete is exclusive, waiting for the ledger's line (the scratch object has no data of S9: its line is not taken).
	deleting := hdGo(func() (store.HostDataCounts, error) {
		return h.DeleteHostData(Session.Address(jsonObject("id", "S9")), background)
	})
	hdEventually(t, "the delete exclusive", func() bool {
		exclusive, _ := hdGateState(h)
		return exclusive && hdWaitingOnLine(h, "test.ledger") > 0
	})
	reading := hdGo(func() (delta.JsonValue, error) {
		scratchRef, err := h.Get(background, hdScratch, jsonObject("id", "a"), GetOptions{})
		if err != nil {
			return nil, err
		}
		return scratchRef.Member("n").Value()
	})
	hdEventually(t, "the scratch activation waiting at the gate", func() bool {
		_, waiting := hdGateState(h)
		return waiting > 0
	})
	var scratch *documents.Documents
	h.(*chordHost).enter(func() {
		for _, object := range h.(*chordHost).objectList() {
			if object.owner() == "test.scratch" {
				scratch = object.documents
			}
		}
	})
	if scratch == nil {
		t.Fatal("the scratch object is not live")
	}
	// A reload that no longer serves the scratch type ends its object, documents and all, while the delete holds the gate.
	reloading := hdGo(func() (ReloadReport, error) {
		return h.Reload([]HostFacet{{Name: "@test/app", Facet: DefineFacet(w.setup(hdMeta, hdLabelsV2))}}, background, ReloadOptions{})
	})
	hdEventually(t, "the scratch object's documents ended", func() bool {
		var ended bool
		h.(*chordHost).enter(func() { ended = scratch.AssertLive() != nil })
		return ended
	})
	release()
	_, err := hdAwait(t, "the delete", deleting)
	must(t, err)
	_, err = hdAwait(t, "the reload", reloading)
	must(t, err)
	// The type is no longer served: the read waits for it (an unbound name), the ended object's failed activation did not crash the host.
	hdPending(t, "the scratch read", reading)
	_, err = h.Reload([]HostFacet{{Name: "@test/app", Facet: DefineFacet(setup)}}, background, ReloadOptions{})
	must(t, err)
	value, err := hdAwait(t, "the scratch read", reading)
	must(t, err)
	exEqual(t, "scratch", value, jsonObject("n", 0.0))
	closeHosts(t, h)
}
