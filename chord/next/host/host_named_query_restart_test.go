package host

// pi: packages/chord/src/host/named-query.ts, attempt.ts
// Ports the group "a provider's restart under its readers (code review rounds 1 and 2)" of
// packages/chord/test/host/named-query.test.ts @0db9861ec018: another owner's commit between the migration's commits, a list's
// `more` during the migration, a write admitted before the provider's seal and paused across it, a removed provider's reads,
// and the old code's query that nothing keeps. The cases that act between the migration's commits (Pi wraps `store.commit`
// and acts after the commit that records the table `migrating`) hold the first commit the reload starts instead.
//
// Forced differences (Go): Pi's `store.commit` pause (a write of order `write` that waits) is the faulty store's held
// commit step (`holdNextCommit`: the step stops at the statement that closes its writes); `migrated()` is the table record's
// version (2 once the migration ran) where Pi saw the state `migrating` at a commit; `void self.tx(...)` is a goroutine.

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/internal/storeseam"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

var (
	prGateSet = nqMust(store.DefineColumns("gate.columns", store.ColumnSetOptions{Columns: []store.ColumnProp{store.Col("id", store.Int())}, Key: []string{"id"}}))
	prGate    = nqMust(store.DefineTable("gate", store.TableOptions{Version: 1, Columns: append([]store.ColumnProp{}, prGateSet.Schemas...), Key: []string{"id"}, Exposes: prGateSet}))
	prOld     = mustTableDef("texts", store.TableOptions{Version: 1, Columns: []store.ColumnProp{store.Col("id", store.Int()), store.Col("text", store.Str())}, Key: []string{"id"}})
	prNew     = nqMust(store.EvolveTable(prOld, func(e *store.TableEditor) store.TableTailEditor {
		return e.Rename("text", "renamed").Rows(func(store.Row) (any, error) { return store.Row{}, nil })
	}))
	prItem     = exObj(exProp("id", exInt()), exProp("text", exStr()))
	prProvider = exLocal("app.provider", contracts.Params{},
		contracts.Mem("rows", contracts.Queries(prItem, []contracts.Property{contracts.Prop("all", exObj())})),
		contracts.Mem("put", contracts.NewMethod(prItem)),
		// As put, without waiting for its commit: a write that is no running call's.
		contracts.Mem("later", contracts.NewMethod(prItem)))
	prGates  = exLocal("app.gates", contracts.Params{}, contracts.Mem("put", contracts.NewMethod(exInt())))
	prView   = exLocal("app.view", contracts.Params{}, contracts.Mem("items", exObj(exProp("rows", exArr(prItem)), exProp("more", exBool()))))
	prScreen = exLocal("app.screen", contracts.Params{},
		contracts.Mem("items", exObj(exProp("rows", exArr(prItem)), exProp("moreAfter", exBool()))),
		contracts.Mem("more", contracts.NewMethod()))
)

// prProviderPlugin is the provider at the old code or the migrated one; declared, when given, gets the old code's named
// queries, as each factory run declared them.
func prProviderPlugin(migrated bool, declared *instances[OwnQuery]) HostFacet {
	return HostFacet{Name: "@test/provider", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(prProvider, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
			self := plain.(StoreSelf)
			definition, column := prOld, "text"
			if migrated {
				definition, column = prNew, "renamed"
			}
			table := self.Table(definition)
			rows := self.Queries(table, QueriesOptions{
				Map: func(row store.Row, _ PartnerRow) (delta.JsonValue, error) {
					return jsonObject("id", float64(row["id"].(int64)), "text", row[column]), nil
				},
			}, map[string]QueryBuilder{"all": {Build: func(delta.JsonValue) *store.Query {
				return store.NewQuery(definition).Where(func(r, _ store.ColumnRefs) store.Condition {
					return store.Exists(prGateSet, store.Match{{Column: "id", Ref: r["id"]}}, nil)
				})
			}}})
			if declared != nil && !migrated {
				own, _ := ownQueries(rows)
				declared.add(own["all"])
			}
			tx := func(ctx context.Context, row store.Row) error {
				_, err := self.Tx(func(tx documents.Tx) (any, error) { return nil, tx.Put(table, row) }, ctx)
				return err
			}
			rowOf := func(args *delta.JsonObject) store.Row {
				return store.Row{"id": int64(args.Value("id").(float64)), column: args.Value("text")}
			}
			later := exMethod(func(*delta.JsonObject, context.Context) (delta.JsonValue, error) { return nil, nil })
			if !migrated {
				later = exMethod(func(args *delta.JsonObject, _ context.Context) (delta.JsonValue, error) {
					// The commit outlives the call: its context must not end with it.
					go func() { _ = tx(background, rowOf(args)) }()
					return nil, nil
				})
			}
			return contracts.MembersImplementation{
				Queries: map[string]nqNamed{"rows": rows},
				Methods: map[string]contracts.MethodFunction{
					"put": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
						return nil, tx(ctx, rowOf(args))
					}),
					"later": later,
				},
			}, nil
		}, ServeOptions{})
	})}
}

// prOthers is the other owner: the gates' commits, the view reading the provider's query and the screen listing it.
var prOthers = HostFacet{Name: "@test/others", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
	if err := h.Serve(prGates, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
		self := plain.(StoreSelf)
		table := self.Table(prGate)
		return exImpl(nil, map[string]contracts.MethodFunction{"put": func(arg delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			_, err := self.Tx(func(tx documents.Tx) (any, error) { return nil, tx.Put(table, store.Row{"id": int64(arg.(float64))}) }, ctx)
			return nil, err
		}}), nil
	}, ServeOptions{}); err != nil {
		return err
	}
	if err := h.Serve(prView, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
		self := plain.(StoreSelf)
		all := self.Get(prProvider, jsonObject()).Queries("rows")["all"]
		return exImpl(map[string]chord.Watchable[delta.JsonValue]{"items": self.LiveQuery(all, jsonObject(), 10)}, nil), nil
	}, ServeOptions{}); err != nil {
		return err
	}
	return h.Serve(prScreen, func(_ *delta.JsonObject, self Self) (contracts.MembersImplementation, error) {
		all := self.Get(prProvider, jsonObject()).Queries("rows")["all"]
		handle := self.List("rows", []ListSource{{Query: all, Args: jsonObject()}}, ListOptions{First: 1})
		return exImpl(map[string]chord.Watchable[delta.JsonValue]{"items": handle.Value()}, map[string]contracts.MethodFunction{
			"more": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) { return nil, handle.More(ctx) },
		}), nil
	}, ServeOptions{})
})}

// prHosted is Pi's `hosted_`: a host with the old provider and rows 1 and 2 behind their gates.
type prHosted struct {
	Host
	store    *store.ChordStore
	gates    *Reference
	provider *Reference
	meter    context.Context
	counts   func() map[string]float64
	// declared are the old code's named queries (Pi's `declared`).
	declared *instances[OwnQuery]
	// errors is what the host reported (`onError`): no case asserts it; one that fails prints it.
	errors *hostErrors
}

func prHost(t *testing.T, chordStore *store.ChordStore) *prHosted {
	t.Helper()
	_, ctx, counts := meteredHost(t)
	errs := &hostErrors{}
	host := newHost(t, HostOptions{Store: chordStore, OnError: errs.push})
	declared := &instances[OwnQuery]{}
	_, err := host.Load([]HostFacet{prProviderPlugin(false, declared), prOthers}, background)
	must(t, err)
	hosted := &prHosted{Host: host, store: chordStore, gates: mustGet(t, host, prGates, jsonObject()), provider: mustGet(t, host, prProvider, jsonObject()), meter: ctx, counts: counts, declared: declared, errors: errs}
	for id, text := range map[float64]string{1: "first", 2: "second"} {
		hosted.gate(t, background, id)
		if _, err := hosted.provider.Call("put", jsonObject("id", id, "text", text), background); err != nil {
			t.Fatalf("put: %v", err)
		}
	}
	return hosted
}

func (h *prHosted) gate(t *testing.T, ctx context.Context, id float64) {
	t.Helper()
	if _, err := h.gates.Call("put", delta.JsonValue(id), ctx); err != nil {
		t.Fatalf("gates.put: %v", err)
	}
}

// reload is `host.reload(plugins)`, not awaited: its error arrives on the channel.
func (h *prHosted) reload(plugins ...HostFacet) <-chan error {
	done := make(chan error, 1)
	go func() {
		report, err := h.Reload(plugins, background, ReloadOptions{})
		if err == nil && len(report.Failed) != 0 {
			err = fmt.Errorf("failed = %+v", report.Failed)
		}
		done <- err
	}()
	return done
}

func (h *prHosted) runs() float64 { return h.counts()["chord.query.runs"] }

// migrated is whether the provider's table is at the migrated code's version.
func (h *prHosted) migrated() bool {
	record := h.store.Table("app.provider:texts")
	return record != nil && record.Version == 2
}

func TestNamedQueryProvidersRestartAWriteAdmittedBeforeTheSealAndPausedAcrossItPublishesThenNoOldStatementRuns(t *testing.T) {
	// Test list 17: the old attempt's reads stay registered until its admitted work settled.
	chordStore, _, holdNextCommit := faultyStore(t)
	h := prHost(t, chordStore)
	view := mustGet(t, h.Host, prView, jsonObject())
	h.gate(t, background, 3)
	watch, err := view.Member("items").Watch(background)
	must(t, err)
	calls := nexttest.ChangesOf(watch)
	// The provider's next commit waits inside its step, after its admission and its reads' runs.
	held := holdNextCommit()
	if _, err := h.provider.Call("later", jsonObject("id", float64(3), "text", "third"), background); err != nil {
		t.Fatalf("later: %v", err)
	}
	<-held.Entered()
	// No call is running: the reload seals the provider at once, with the write still in its step.
	reloaded := h.reload(prProviderPlugin(true, nil), prOthers)
	deliveredOn(h.Host)
	if h.migrated() {
		t.Fatal("the provider migrated while its admitted write was in its step")
	}
	held.Release()
	if err := <-reloaded; err != nil {
		t.Fatalf("reload: %v", err)
	}
	// The reload ends with the old code sealed; the successor activates and migrates behind it.
	scopeEventually(t, "the provider migrated", func() bool { flushed(h.Host); deliveredOn(h.Host); return h.migrated() })
	flushed(h.Host)
	deliveredOn(h.Host)
	if !h.migrated() {
		t.Fatalf("the provider did not migrate: %+v", chordStore.Table("app.provider:texts"))
	}
	if got := calls.Items(); len(got) != 1 || got[0].Kind != chord.WatchCallValue {
		t.Fatalf("calls = %+v, want one value", got)
	}
	qrWant(t, "rows", qrValue(t, view, "items"), `{"rows":[{"id":1,"text":"first"},{"id":2,"text":"second"},{"id":3,"text":"third"}],"more":false}`)
	for _, read := range chordStore.Reads.On("app.provider:texts") {
		if read.Plan.Query.Table != prNew {
			t.Fatal("a read of the old code's statement stayed registered")
		}
	}
	_, _ = watch.Stop()
	closeHosts(t, h.Host)
}

func TestNamedQueryProvidersRestartARemovedProvidersReadsLeaveTheIndexItsReadersKeepTheirRowsAndAnotherOwnersCommitRunsNothing(t *testing.T) {
	// Round 2 (Opus S2): `Reads.Retire` at the old attempt's end, with no successor and no hold to mask it.
	chordStore := memoryStore(t)
	h := prHost(t, chordStore)
	view := mustGet(t, h.Host, prView, jsonObject())
	before, err := view.Member("items").Value()
	must(t, err)
	// Two reads over each table: the view's live query and the first page of the screen's list (an object of empty params
	// lives from the load, the screen of `others` included, as in Pi).
	for _, table := range []string{"app.provider:texts", "app.gates:gate"} {
		if reads := chordStore.Reads.On(table); len(reads) != 2 {
			t.Fatalf("the view's and the screen's reads are registered over %s %d times before the reload, want twice", table, len(reads))
		}
	}
	if _, err := h.Reload([]HostFacet{prOthers}, background, ReloadOptions{}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	flushed(h.Host)
	// Pi's check, as it has it: its table name `app.others:gate` is no table of the test (the gates' table is
	// `app.gates:gate`), so it holds before the reload as well.
	if reads := chordStore.Reads.On("app.others:gate"); len(reads) != 0 {
		t.Fatalf("%d reads of the removed provider's table stay in the index", len(reads))
	}
	// Mine (Go-only): the same check on the real tables the view's read uses (the provider's own and the gates', which its
	// `exists` joins), over which it was registered before the reload. Pi's probe (the case with the real table names, Pi
	// 7cc21c24f6b5): both reads are gone when `await host.reload()` resolves, after `await flushed()` and after
	// `await delivered()`: Pi's `remove` (object.ts:1093) starts `#tearDown` in the background and its `attempt.detach()`
	// (`reads.retire`, attempt.ts:1421) runs after the awaits of `#tearDown` (object.ts:1625-1628), a microtask chain with no
	// gate in it, which `setTimeout(0)` ends. The same chain here is the remove's teardown task (awaitTask): deliveredOn
	// joins it, so the check is strict.
	deliveredOn(h.Host)
	if reads := chordStore.Reads.On("app.provider:texts"); len(reads) != 0 {
		t.Fatalf("%d reads of the removed provider's table stay in the index", len(reads))
	}
	if reads := chordStore.Reads.On("app.gates:gate"); len(reads) != 0 {
		t.Fatalf("%d reads over the gates' table stay in the index", len(reads))
	}
	runsBefore := h.runs()
	h.gate(t, h.meter, 1)
	h.gate(t, h.meter, 3)
	if runs := h.runs() - runsBefore; runs != 0 {
		t.Fatalf("another owner's commits ran %v reads", runs)
	}
	if after, err := view.Member("items").Value(); err != nil || after != before {
		t.Fatalf("the view's rows changed: %v, %v", after, err)
	}
	closeHosts(t, h.Host)
}

func TestNamedQueryProvidersRestartRunsNoStatementCompiledForTheTableAsItWasNoFailedFrameWhileAnotherOwnerCommits(t *testing.T) {
	// Round 1 (Sol 3, Opus S1; test list 11a): the other owner commits between the migration's commits, and that commit
	// runs no read at all. Pi's `store.commit` wrapper acts after the commit that records the table `migrating`; here the
	// commit step the reload starts is held, the other owner's commit queues behind it, and both run before the
	// migration's next commit (which is enlisted after the held step returned).
	chordStore, _, holdNextCommit := faultyStore(t)
	h := prHost(t, chordStore)
	view := mustGet(t, h.Host, prView, jsonObject())
	watch, err := view.Member("items").Watch(background)
	must(t, err)
	calls := nexttest.ChangesOf(watch)
	// Pi's `meter.counts()` is undefined here: the meter counts the runs under its context only (the other owner's commit
	// below), and Pi's host has no telemetry, so the successor's own first read is not counted (prHost).
	held := holdNextCommit()
	reloaded := h.reload(prProviderPlugin(true, nil), prOthers)
	<-held.Entered()
	if !chordStore.Reads.IsHeld("app.provider:texts") {
		t.Fatal("the table's reads are not held at the migration's first commit")
	}
	other := startQueued(t, chordStore, func() { h.gate(t, h.meter, 1) })
	held.Release()
	<-other
	if err := <-reloaded; err != nil {
		t.Fatalf("reload: %v", err)
	}
	flushed(h.Host)
	deliveredOn(h.Host)
	if !h.migrated() {
		t.Fatal("the provider did not migrate")
	}
	if runs := h.runs(); runs != 0 {
		t.Fatalf("the other owner's commit ran %v reads", runs)
	}
	if got := calls.Items(); len(got) != 0 {
		t.Fatalf("calls = %+v, want none", got)
	}
	qrWant(t, "rows", qrValue(t, view, "items"), `{"rows":[{"id":1,"text":"first"},{"id":2,"text":"second"}],"more":false}`)
	_, _ = watch.Stop()
	closeHosts(t, h.Host)
}

// texts are the `text`s of the rows of a screen's list (`screen.items.value.rows.map((row) => row.text)`).
func prTexts(t *testing.T, screen *Reference) []string {
	t.Helper()
	value, err := screen.Member("items").Value()
	must(t, err)
	texts := []string{}
	for _, row := range value.(*delta.JsonObject).Value("rows").([]any) {
		texts = append(texts, row.(*delta.JsonObject).Value("text").(string))
	}
	return texts
}

func TestNamedQueryProvidersRestartAListsMoreDuringTheMigrationWaitsForTheSuccessorNoReadOfTheOldStatementIsRegistered(t *testing.T) {
	// Round 2 (Sol): `more` on the retired query registered a read of the old plan, which stayed in the index and failed
	// "no such column" on the next commit. Pi acts after the commit that records the table `migrating`: here the commit step
	// the reload starts is held; the other owner's commit and the `more` come behind it.
	chordStore, _, holdNextCommit := faultyStore(t)
	h := prHost(t, chordStore)
	// What the host reported, on failure only: the calls below fail with `ended` and say no more (the gate run on ac54a50746,
	// 2026-11-18, failed at the two calls once and was not reproduced).
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		reports := h.errors.All()
		if len(reports) == 0 {
			t.Log("host reports: none")
		}
		for _, report := range reports {
			t.Logf("host report: %s object=%q query=%q member=%q error=%v failure=%v/%v", report.Kind, report.Object, report.Query, report.Member, report.Error, report.Failure.Kind, report.Failure.Error)
		}
	})
	screen := mustGet(t, h.Host, prScreen, jsonObject())
	qrWant(t, "items", qrValue(t, screen, "items"), `{"rows":[{"id":1,"text":"first"}],"moreAfter":true}`)
	held := holdNextCommit()
	reloaded := h.reload(prProviderPlugin(true, nil), prOthers)
	<-held.Entered()
	if !chordStore.Reads.IsHeld("app.provider:texts") {
		t.Fatal("the table's reads are not held at the migration's first commit")
	}
	other := startQueued(t, chordStore, func() { h.gate(t, background, 1) })
	// Not awaited here: it ends when the successor is live, which waits for the migration's commits.
	more := make(chan error, 1)
	go func() { _, err := screen.Call("more", nil, background); more <- err }()
	held.Release()
	<-other
	if err := <-reloaded; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := <-more; err != nil {
		t.Fatalf("more: %v", err)
	}
	lsSettled(h.Host)
	if got := prTexts(t, screen); fmt.Sprint(got) != "[first second]" {
		t.Fatalf("texts = %v, want [first second]", got)
	}
	reads := chordStore.Reads.On("app.provider:texts")
	if len(reads) == 0 {
		t.Fatal("no read of the provider's table is registered")
	}
	for _, read := range reads {
		if read.Plan.Query.Table != prNew {
			t.Fatal("a read of the old code's statement stayed registered")
		}
	}
	// A later commit of the other owner runs the list's pages: none fails.
	h.gate(t, background, 2)
	lsSettled(h.Host)
	if got := prTexts(t, screen); fmt.Sprint(got) != "[first second]" {
		t.Fatalf("texts after the gate = %v, want [first second]", got)
	}
	// Nothing of the handover keeps the old code's query.
	expectCollected(t, h.declared.at(0))
	closeHosts(t, h.Host)
}

func TestNamedQueryProvidersRestartKeepsNothingOfTheOldCodesNamedQueryOnceItsReadersReadTheSuccessors(t *testing.T) {
	h := prHost(t, memoryStore(t))
	view := mustGet(t, h.Host, prView, jsonObject())
	screen := mustGet(t, h.Host, prScreen, jsonObject())
	if err := <-h.reload(prProviderPlugin(true, nil), prOthers); err != nil {
		t.Fatalf("reload: %v", err)
	}
	// The reload returns with the old code sealed; the successor migrates behind it, and its readers read it once it is live.
	until(t, "the view reads the successor's query", func() bool {
		lsSettled(h.Host)
		value, err := view.Member("items").Value()
		return err == nil && len(value.(*delta.JsonObject).Value("rows").([]any)) == 2
	})
	if got := prTexts(t, screen); len(got) != 1 {
		t.Fatalf("screen rows = %v, want 1", got)
	}
	if n := h.declared.count(); n != 1 {
		t.Fatalf("declared = %d, want 1", n)
	}
	expectCollected(t, h.declared.at(0))
	closeHosts(t, h.Host)
}

// startQueued runs call on a goroutine, as Pi starts a promise it awaits later, and returns its completion once call took its
// place in the store's commit queue (or finished without one). Calls started one after another through it queue in that
// order. The store's commit queue is observed through the test seam: Pi's tests wrap `store.commit`.
func startQueued(t testing.TB, chordStore *store.ChordStore, call func()) <-chan struct{} {
	t.Helper()
	before := storeseam.QueueTail(chordStore)
	done := make(chan struct{})
	go func() {
		defer close(done)
		call()
	}()
	deadline := time.Now().Add(10 * time.Second)
	for storeseam.QueueTail(chordStore) == before {
		select {
		case <-done:
			return done
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("a call neither took its place in the commit queue nor finished")
		}
		runtime.Gosched()
	}
	return done
}
