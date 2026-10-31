package documents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/test/next/documents/transaction.test.ts
//
// Pi runs these on a `createHost` host: objects served with `h.serve`, reached through `host.get` references, and
// `self.tx`. The Go form runs them on the harness object of support_test.go (a DocumentsOwner with its attempts): an
// attempt's `Tx` is `self.tx`, its handles are the ones `self.state` returns, `host.close` is object.End, a restart is
// fail + a new attempt. Pi's `reference.sum.watch` (a derive over the object's two documents, a served member) is a
// state.Derive attached on the object's own batch.
//
// Promises are blocking calls: a transaction a test starts and awaits later runs on its own goroutine (goTx), and what
// `await delivered()` makes sure of before the next step (a transaction queued on the line, a commit queued in the
// store) is a wait on that very condition (waitFor), never a sleep.
//
// Designed out: the generic `Draft<T>`/`SingletonHandle<T>` types (a document is *JsonObject, a draft *delta.Object).
// Pi's `expect(draft).toBeInstanceOf(Promise)` has no Go form (Draft returns the error itself).

var (
	txA     = txMustDefine("a", false, false)
	txB     = mustDefineStr("b")
	txN     = txMustDefine("n", false, false)
	txP     = txMustDefine("p", false, true)
	txK     = txMustDefine("k", true, true)
	txEdits = mustTable()
)

func zeroN() *JsonObject { return obj("n", float64(0)) }

func txMustDefine(name string, keyed, persist bool) *StateDefinition {
	options := StateOptions{
		Schema:  contracts.Obj(contracts.Prop("n", contracts.Int())),
		Version: 1, Initial: zeroN, Keyed: keyed, Persist: persist,
	}
	if persist {
		options.CheckpointWhen = func(*JsonObject, []delta.Op, CheckpointInfo) bool { return false }
	}
	definition, err := DefineState(name, options)
	if err != nil {
		panic(err)
	}
	return definition
}

func mustDefineStr(name string) *StateDefinition {
	definition, err := DefineState(name, StateOptions{
		Schema:  contracts.Obj(contracts.Prop("s", contracts.Str())),
		Version: 1, Initial: func() *JsonObject { return obj("s", "") },
	})
	if err != nil {
		panic(err)
	}
	return definition
}

func mustTable() *store.TableDefinition {
	definition, err := store.DefineTable("edits", store.TableOptions{
		Version: 1, Columns: []store.ColumnProp{store.Col("id", store.Str()), store.Col("at", store.Int())}, Key: []string{"id"},
	})
	if err != nil {
		panic(err)
	}
	return definition
}

// --- helpers ---

type txResult struct {
	value any
	err   error
}

// goTx starts a transaction of attempt a on its own goroutine, as Pi starts a promise it awaits later.
func goTx(a *attempt, fn func(tx *Transaction) (any, error), ctx context.Context) <-chan txResult {
	done := make(chan txResult, 1)
	go func() {
		value, err := a.Tx(fn, ctx)
		done <- txResult{value, err}
	}()
	return done
}

func awaitTx(t *testing.T, done <-chan txResult) txResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(30 * time.Second):
		t.Fatal("a transaction never settled")
		return txResult{}
	}
}

// waitFor waits until condition holds (the deterministic form of Pi's `await delivered()` before a state it relies on).
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("never reached: %s", what)
		}
		time.Sleep(50 * time.Microsecond)
	}
}

// waitingOnLine is the number of holders queued on o's line behind the current one.
func waitingOnLine(o *object) int { return o.documents.Line().waitingHolders() }

// commitQueued is whether o's transaction holding the line sent its commit to the store's step (it waits there).
func commitQueued(o *object) bool {
	var queued bool
	o.Do(func() { queued = o.documents.holder != nil && o.documents.holder.queued != nil })
	return queued
}

func pendingOf(o *object, tx *Transaction) int {
	var pending int
	o.Do(func() { pending = tx.pending })
	return pending
}

func jsonText(t testing.TB, value any) string {
	t.Helper()
	text, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(text)
}

func valueOf(t testing.TB, o *object, handle *Singleton) *JsonObject {
	t.Helper()
	var value *JsonObject
	var err error
	o.Do(func() { value, err = handle.Value() })
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	return value
}

func txRequireValue(t testing.TB, o *object, handle *Singleton, want *JsonObject) {
	t.Helper()
	if got := jsonText(t, valueOf(t, o, handle)); got != jsonText(t, want) {
		t.Fatalf("value = %s, want %s", got, jsonText(t, want))
	}
}

func requireJSON(t testing.TB, name string, got, want any) {
	t.Helper()
	if g, w := jsonText(t, got), jsonText(t, want); g != w {
		t.Fatalf("%s = %s, want %s", name, g, w)
	}
}

func txSetN(t testing.TB, draft *delta.Object, n float64) {
	t.Helper()
	if err := draft.Set("n", n); err != nil {
		t.Fatalf("set: %v", err)
	}
}

func nOf(draft *delta.Object) float64 { return draft.Get("n").(float64) }

func requireNoError(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func requireErrorText(t testing.TB, err error, want string) {
	t.Helper()
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func requireErrorContains(t testing.TB, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want it to contain %q", err, want)
	}
}

// requireReleased is `toMatchObject({ code: "released" })`, and the message when want is not empty.
func requireReleased(t testing.TB, err error, want string) {
	t.Helper()
	chordErr, ok := data.IsChordError(err)
	if !ok || chordErr.Code != data.CodeReleased {
		t.Fatalf("error = %v, want code released", err)
	}
	if want != "" && chordErr.Message != want {
		t.Fatalf("message = %q, want %q", chordErr.Message, want)
	}
}

func requirePanicRevoked(t testing.TB, use func()) {
	t.Helper()
	defer func() {
		recovered := recover()
		if err, _ := recovered.(error); !errors.Is(err, delta.ErrRevoked) {
			t.Fatalf("recovered %v, want ErrRevoked", recovered)
		}
	}()
	use()
}

// rootReplace is the frame of a root replacement: `[value, [["r", value]]]`.
func frameOf(value any, ops ...delta.Op) nexttest.Frame[delta.JsonValue] {
	return nexttest.Frame[delta.JsonValue]{Value: value, Ops: ops}
}

func rootOp(value any) delta.Op { return delta.Op{"r", value} }

// txObject is an object with documents; its batch counts into meter and its microtasks into micro when given.
func txObject(t *testing.T, label string, options objectOptions, meter *nexttest.Meter, micro *atomic.Int64) *object {
	t.Helper()
	options.label = label
	o := newObject(t, options)
	if meter == nil && micro == nil {
		return o
	}
	report := func(err state.BatchError) {
		o.mu.Lock()
		o.reports = append(o.reports, err)
		o.mu.Unlock()
	}
	enqueue := func(job func()) {
		if micro != nil {
			micro.Add(1)
		}
		go o.loop.do(job)
	}
	if meter != nil {
		o.batch = state.NewBatch(report, meter.Telemetry.Metrics())
	} else {
		o.batch = state.NewBatch(report, nil)
	}
	o.batch = o.batch.UseScheduler(o.scheduler).UseMicrotasks(enqueue)
	return o
}

// owned is one `app.owner(id)` object of Pi's `objects()`: two documents and `sum`, a derive over them.
type owned struct {
	o       *object
	attempt *attempt
	a       *Singleton
	b       *Singleton
	sum     *state.Derive
}

func ownedObject(t *testing.T, id int, meter *nexttest.Meter, micro *atomic.Int64) *owned {
	t.Helper()
	o := txObject(t, fmt.Sprintf("app.owner(%d)", id), objectOptions{}, meter, micro)
	at := o.newAttempt()
	a := at.Singleton(txA)
	b := at.Singleton(txB)
	requireNoError(t, at.Activate())
	var sum *state.Derive
	o.Do(func() {
		sum = state.NewDerive([]any{a.Slot, b.Slot}, func(values []any) (delta.JsonValue, error) {
			x, y := values[0].(*delta.JsonObject), values[1].(*delta.JsonObject)
			return fmt.Sprintf("%v%v", x.Value("n"), y.Value("s")), nil
		}, o.Batch())
		requireNoError(t, sum.Attach(func(source any, _ int) (state.DeriveInput, error) {
			return source.(state.DeriveInput), nil
		}, func(error) {}))
	})
	return &owned{o: o, attempt: at, a: a, b: b, sum: sum}
}

// objects is Pi's `objects()`: two objects with two documents and a derive over them.
func objects(t *testing.T, meter *nexttest.Meter) (one, two *owned) {
	t.Helper()
	return ownedObject(t, 1, meter, nil), ownedObject(t, 2, meter, nil)
}

func (w *owned) watchSum(t *testing.T) *nexttest.Collector[nexttest.Frame[delta.JsonValue]] {
	t.Helper()
	var watch chord.WatchHandle[delta.JsonValue]
	var err error
	w.o.Do(func() { watch, err = w.sum.Watch(background) })
	requireNoError(t, err)
	return nexttest.FramesOf(watch)
}

func watchOf(t *testing.T, o *object, handle *Singleton) chord.WatchHandle[delta.JsonValue] {
	t.Helper()
	watch, err := handle.Watch(background)
	requireNoError(t, err)
	return watch
}

// --- transactions ---

func TestTransactionsRunOneAtATimePerObjectAndReturnWhatTheirCallbackReturns(t *testing.T) {
	one, _ := objects(t, nil)
	var mu sync.Mutex
	var order []string
	push := func(text string) { mu.Lock(); order = append(order, text); mu.Unlock() }
	gate := make(chan struct{})
	entered := make(chan struct{})
	first := goTx(one.attempt, func(tx *Transaction) (any, error) {
		push("first")
		close(entered)
		<-gate
		draft, err := tx.Draft(one.a)
		if err != nil {
			return nil, err
		}
		txSetN(t, draft, 1)
		push("first done")
		return "one", nil
	}, background)
	<-entered
	second := goTx(one.attempt, func(tx *Transaction) (any, error) {
		push(fmt.Sprintf("second sees %v", valueOf(t, one.o, one.a).Value("n")))
		draft, err := tx.Draft(one.a)
		if err != nil {
			return nil, err
		}
		txSetN(t, draft, nOf(draft)+1)
		return "two", nil
	}, background)
	waitFor(t, "the second transaction queued on the line", func() bool { return waitingOnLine(one.o) == 1 })
	close(gate)
	if r := awaitTx(t, first); r.err != nil || r.value != "one" {
		t.Fatalf("first = %+v", r)
	}
	if r := awaitTx(t, second); r.err != nil || r.value != "two" {
		t.Fatalf("second = %+v", r)
	}
	if want := []string{"first", "first done", "second sees 1"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	txRequireValue(t, one.o, one.a, obj("n", float64(2)))
}

func TestTransactionsGiveTheSameDraftForTheSameDocument(t *testing.T) {
	one, _ := objects(t, nil)
	_, err := one.attempt.Tx(func(tx *Transaction) (any, error) {
		first, err := tx.Draft(one.a)
		if err != nil {
			return nil, err
		}
		second, err := tx.Draft(one.a)
		if err != nil {
			return nil, err
		}
		if first != second {
			t.Fatal("two drafts of one document in one transaction")
		}
		return nil, nil
	}, background)
	requireNoError(t, err)
}

// Pi's unawaited continuation of a warm `tx.draft` runs before the commit (rule 3). A Go Draft of a warm document
// returns at once on the callback's goroutine: it is no pending operation, and what the callback sets on it is
// committed.
func TestTransactionsSettleAWarmDraftAtOnce(t *testing.T) {
	one, _ := objects(t, nil)
	_, err := one.attempt.Tx(func(tx *Transaction) (any, error) {
		draft, err := tx.Draft(one.a)
		if err != nil {
			return nil, err
		}
		if pending := pendingOf(one.o, tx); pending != 0 {
			t.Fatalf("a warm draft counts %d pending operations", pending)
		}
		txSetN(t, draft, 7)
		return nil, nil
	}, background)
	requireNoError(t, err)
	txRequireValue(t, one.o, one.a, obj("n", float64(7)))
}

func TestTransactionsRejectADraftOfAHandleATransactionCannotUseInsteadOfThrowing(t *testing.T) {
	one, two := objects(t, nil)
	_, err := one.attempt.Tx(func(tx *Transaction) (any, error) {
		draft, err := tx.Draft(two.a)
		if draft != nil {
			t.Fatal("a draft of another object's document")
		}
		requireErrorContains(t, err, "drafts only its own object's documents")
		return nil, nil
	}, background)
	requireNoError(t, err)
}

func TestTransactionsCommitNothingWhenNothingChanged(t *testing.T) {
	meter := nexttest.Metered(t)
	one, _ := objects(t, meter)
	frames := one.watchSum(t)
	value := valueOf(t, one.o, one.a)
	computes := meter.Counts()["chord.derive.computes"]
	_, err := one.attempt.Tx(func(tx *Transaction) (any, error) {
		draft, err := tx.Draft(one.a)
		if err != nil {
			return nil, err
		}
		_ = nOf(draft)
		return nil, nil
	}, background)
	requireNoError(t, err)
	one.o.delivered()
	if valueOf(t, one.o, one.a) != value {
		t.Fatal("the document's value was replaced")
	}
	if got := frames.Items(); len(got) != 0 {
		t.Fatalf("frames = %v, want none", got)
	}
	if got := meter.Counts()["chord.derive.computes"]; got != computes {
		t.Fatalf("computes = %v, want %v", got, computes)
	}
}

func TestTransactionsDiscardEverythingOnAThrowAndOnAnAbortedContext(t *testing.T) {
	one, _ := objects(t, nil)
	change := func(tx *Transaction) {
		draft, err := tx.Draft(one.a)
		requireNoError(t, err)
		txSetN(t, draft, 5)
	}
	_, err := one.attempt.Tx(func(tx *Transaction) (any, error) {
		change(tx)
		return nil, errors.New("no")
	}, background)
	requireErrorText(t, err, "no")
	cancelled, cancel := chordctx.WithCancel(background)
	_, err = one.attempt.Tx(func(tx *Transaction) (any, error) {
		change(tx)
		cancel(errors.New("cancelled"))
		return nil, nil
	}, cancelled)
	requireErrorText(t, err, "cancelled")
	txRequireValue(t, one.o, one.a, obj("n", float64(0)))
}

func TestTransactionsRevokeTheirDraftsWhenTheySettle(t *testing.T) {
	one, _ := objects(t, nil)
	var kept *delta.Object
	_, err := one.attempt.Tx(func(tx *Transaction) (any, error) {
		draft, err := tx.Draft(one.a)
		if err != nil {
			return nil, err
		}
		kept = draft
		txSetN(t, kept, 1)
		return nil, nil
	}, background)
	requireNoError(t, err)
	requirePanicRevoked(t, func() { kept.Get("n") })
	settled, err := one.attempt.Tx(func(tx *Transaction) (any, error) { return tx, nil }, background)
	requireNoError(t, err)
	_, err = settled.(*Transaction).Draft(one.a)
	requireErrorText(t, err, "app.owner(1): the transaction has settled")
}

func TestTransactionsRefuseAnotherObjectsDocuments(t *testing.T) {
	one, two := objects(t, nil)
	_, err := one.attempt.Tx(func(tx *Transaction) (any, error) { return tx.Draft(two.a) }, background)
	requireErrorText(t, err, "app.owner(1): a transaction drafts only its own object's documents")
}

func TestTransactionsCommitTheObjectsDocumentsInOneBatchOneComputeOneFrame(t *testing.T) {
	meter := nexttest.Metered(t)
	one, _ := objects(t, meter)
	frames := one.watchSum(t)
	computes := meter.Counts()["chord.derive.computes"]
	_, err := one.attempt.Tx(func(tx *Transaction) (any, error) {
		a, err := tx.Draft(one.a)
		if err != nil {
			return nil, err
		}
		txSetN(t, a, 1)
		b, err := tx.Draft(one.b)
		if err != nil {
			return nil, err
		}
		if err := b.Set("s", "b"); err != nil {
			return nil, err
		}
		return nil, nil
	}, background)
	requireNoError(t, err)
	one.o.delivered()
	if got, want := frames.Items(), []nexttest.Frame[delta.JsonValue]{frameOf("1b", rootOp("1b"))}; !reflect.DeepEqual(got, want) {
		t.Fatalf("frames = %+v, want %+v", got, want)
	}
	if got := meter.Counts()["chord.derive.computes"]; got != computes+1 {
		t.Fatalf("computes = %v, want %v", got, computes+1)
	}
}

func TestTransactionsCommitTheirOtherChangesWhenTheyRetireAnAbsentDocument(t *testing.T) {
	one, _ := objects(t, nil)
	_, err := one.attempt.Tx(func(tx *Transaction) (any, error) {
		if err := tx.Retire(one.b); err != nil {
			return nil, err
		}
		draft, err := tx.Draft(one.a)
		if err != nil {
			return nil, err
		}
		txSetN(t, draft, 7)
		return nil, nil
	}, background)
	requireNoError(t, err)
	txRequireValue(t, one.o, one.a, obj("n", float64(7)))
	txRequireValue(t, one.o, one.b, obj("s", ""))
}

func TestTransactionsDraftARetiredDocumentAnewWatchesGetARootReplacement(t *testing.T) {
	one, _ := objects(t, nil)
	_, err := one.a.Change(func(draft *delta.Object) (any, error) { return nil, draft.Set("n", float64(3)) }, background)
	requireNoError(t, err)
	frames := nexttest.FramesOf(watchOf(t, one.o, one.a))
	_, err = one.attempt.Tx(func(tx *Transaction) (any, error) {
		if err := tx.Retire(one.a); err != nil {
			return nil, err
		}
		draft, err := tx.Draft(one.a)
		if err != nil {
			return nil, err
		}
		txSetN(t, draft, nOf(draft)+1)
		return nil, nil
	}, background)
	requireNoError(t, err)
	one.o.delivered()
	requireJSON(t, "frames", frames.Items(), []nexttest.Frame[delta.JsonValue]{frameOf(obj("n", float64(1)), rootOp(obj("n", float64(1))))})
}

// --- the in-memory change's cost ---

// Pi counts the microtask turns an in-memory change takes (at most five: an extra await per tx operation shows
// there). The Go form of a turn is a hop to another goroutine: the batch's microtask jobs a change queues.
func TestTheInMemoryChangesCostSettlesWithinFiveHops(t *testing.T) {
	var micro atomic.Int64
	one := ownedObject(t, 1, nil, &micro)
	for range 3 {
		before := micro.Load()
		_, err := one.a.Change(func(draft *delta.Object) (any, error) { return nil, draft.Set("n", nOf(draft)+1) }, background)
		requireNoError(t, err)
		if hops := micro.Load() - before; hops > 5 {
			t.Fatalf("an in-memory change took %d hops, want at most 5", hops)
		}
	}
	if n := valueOf(t, one.o, one.a).Value("n"); n != float64(3) {
		t.Fatalf("n = %v, want 3", n)
	}
}

// --- staged changes ---

// recorder is a staged change recording what happens to it, and the document value its adoption sees.
type recorder struct {
	mu     *sync.Mutex
	events *[]string
	o      *object
	a      *Singleton
}

func (r *recorder) log(text string) { r.mu.Lock(); *r.events = append(*r.events, text); r.mu.Unlock() }

func (r *recorder) Adopt() error {
	value, err := r.a.Value()
	if err != nil {
		return err
	}
	r.log(fmt.Sprintf("adopt sees %v", value.Value("n")))
	return nil
}
func (r *recorder) Committed() { r.log("committed") }
func (r *recorder) Discarded() { r.log("discarded") }

func stage(t *testing.T, o *object, tx *Transaction, owner any, make func() Staged) Staged {
	t.Helper()
	var staged Staged
	var err error
	o.Do(func() { staged, err = tx.Stage(owner, make) })
	requireNoError(t, err)
	return staged
}

func TestStagedChangesAdoptInTheCommitStepWithTheDocumentsThenHearOfTheCommitOnceTheLineIsFree(t *testing.T) {
	one, _ := objects(t, nil)
	var mu sync.Mutex
	var events []string
	owner := new(int)
	_, err := one.attempt.Tx(func(tx *Transaction) (any, error) {
		draft, err := tx.Draft(one.a)
		if err != nil {
			return nil, err
		}
		txSetN(t, draft, 7)
		staged := stage(t, one.o, tx, owner, func() Staged { return &recorder{&mu, &events, one.o, one.a} })
		// One per owner and transaction.
		if again := stage(t, one.o, tx, owner, func() Staged { return &recorder{&mu, new([]string), one.o, one.a} }); again != staged {
			t.Fatal("a second staged change of one owner in one transaction")
		}
		(&recorder{&mu, &events, one.o, one.a}).log("callback done")
		return nil, nil
	}, background)
	requireNoError(t, err)
	if want := []string{"callback done", "adopt sees 7", "committed"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestStagedChangesAdoptWithoutAnyDocumentChangeAndHearOfADiscardInsteadWhenTheTransactionThrows(t *testing.T) {
	one, _ := objects(t, nil)
	var mu sync.Mutex
	var events []string
	make := func() Staged { return &recorder{&mu, &events, one.o, one.a} }
	_, err := one.attempt.Tx(func(tx *Transaction) (any, error) {
		stage(t, one.o, tx, new(int), make)
		return nil, nil
	}, background)
	requireNoError(t, err)
	_, err = one.attempt.Tx(func(tx *Transaction) (any, error) {
		stage(t, one.o, tx, new(int), make)
		return nil, errors.New("no")
	}, background)
	requireErrorText(t, err, "no")
	if want := []string{"adopt sees 0", "committed", "discarded"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

// A transaction releases the line and tells its staged changes of the commit in one section (Pi's `finally { holder =
// undefined; release(); tx.settle(); }` is one synchronous section): a transaction queued on the line runs only after
// `committed`. A job's start queues its run's invocation in `committed`; an abort of that run, marked by a transaction
// that waited on the line behind the start's, must not commit ahead of it. The first transaction's goroutine is parked
// where it enters a section after its release, if it does: the second must still run after `committed`.
func TestStagedChangesHearOfTheCommitInTheSectionThatReleasesTheLineBeforeAQueuedTransactionRuns(t *testing.T) {
	one, _ := objects(t, nil)
	var mu sync.Mutex
	var events []string
	log := &recorder{&mu, &events, one.o, one.a}
	type holding struct {
		goroutine uint64
		released  <-chan struct{}
	}
	entered := make(chan holding, 1)
	proceed := make(chan struct{})
	first := ksGo(func() (*JsonObject, error) {
		_, err := one.attempt.Tx(func(tx *Transaction) (any, error) {
			line := one.o.documents.Line()
			line.mu.Lock()
			released := line.tail
			line.mu.Unlock()
			stage(t, one.o, tx, new(int), func() Staged { return log })
			entered <- holding{goroutineID(), released}
			<-proceed
			return nil, nil
		}, background)
		return nil, err
	})
	held := ksRecv(t, entered)
	// From the first's release on, its goroutine parks where it enters a section from outside one.
	stall := &loopStall{id: held.goroutine, release: make(chan struct{}), when: func() bool {
		select {
		case <-held.released:
			return true
		default:
			return false
		}
	}}
	one.o.loop.stall.Store(stall)
	second := ksGo(func() (*JsonObject, error) {
		_, err := one.attempt.Tx(func(*Transaction) (any, error) {
			log.log("second transaction")
			return nil, nil
		}, background)
		return nil, err
	})
	ksWaiting(t, one.o, 1)
	close(proceed)
	requireNoError(t, ksRecv(t, second).err)
	close(stall.release)
	requireNoError(t, ksRecv(t, first).err)
	if want := []string{"adopt sees 0", "committed", "second transaction"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

// --- the seal fence ---

const restartableLabel = "app.restartable(1)"

// sealable is a restartable object with `N`; its handles by attempt.
type sealable struct {
	o        *object
	attempts []*attempt
	handles  []*Singleton
}

func newSealable(t *testing.T) *sealable {
	t.Helper()
	s := &sealable{}
	factory := func(a *attempt) { s.handles = append(s.handles, a.Singleton(txN)) }
	var first *attempt
	s.o, first = activated(t, objectOptions{label: restartableLabel}, factory)
	s.attempts = []*attempt{first}
	return s
}

// fail is the live attempt failing, retry its successor.
func (s *sealable) fail() { s.attempts[len(s.attempts)-1].Fail() }

func (s *sealable) retry(t *testing.T) {
	t.Helper()
	next, err := s.o.restart(func(a *attempt) { s.handles = append(s.handles, a.Singleton(txN)) })
	requireNoError(t, err)
	s.attempts = append(s.attempts, next)
}

func (s *sealable) restart(t *testing.T) {
	t.Helper()
	s.fail()
	s.retry(t)
}

func TestTheSealFenceDiscardsTheTransactionHoldingTheLineAtTheSealTheLineIsFreeItsLaterUsesAreRefused(t *testing.T) {
	s := newSealable(t)
	gate := make(chan struct{})
	entered := make(chan struct{})
	var held *Transaction
	first := goTx(s.attempts[0], func(tx *Transaction) (any, error) {
		draft, err := tx.Draft(s.handles[0])
		if err != nil {
			return nil, err
		}
		txSetN(t, draft, 1)
		held = tx
		close(entered)
		<-gate
		return "late", nil
	}, background)
	<-entered
	s.restart(t)
	// The next attempt's transaction runs while the cut one's callback still waits.
	_, err := s.handles[1].Change(func(draft *delta.Object) (any, error) { return nil, draft.Set("n", float64(2)) }, background)
	requireNoError(t, err)
	close(gate)
	requireReleased(t, awaitTx(t, first).err, "")
	_, err = held.Draft(s.handles[1])
	requireReleased(t, err, restartableLabel+": the transaction was discarded at the seal")
	txRequireValue(t, s.o, s.handles[1], obj("n", float64(2)))
}

func TestTheSealFenceRefusesASealedAttemptsTransactionsWaitingForTheLineAndEveryNewOneItsHandlesStillRead(t *testing.T) {
	s := newSealable(t)
	gate := make(chan struct{})
	entered := make(chan struct{})
	first := goTx(s.attempts[0], func(tx *Transaction) (any, error) {
		close(entered)
		<-gate
		return nil, nil
	}, background)
	<-entered
	queued := make(chan txResult, 1)
	go func() {
		value, err := s.handles[0].Change(func(draft *delta.Object) (any, error) { return nil, draft.Set("n", float64(1)) }, background)
		queued <- txResult{value, err}
	}()
	waitFor(t, "the change queued on the line", func() bool { return waitingOnLine(s.o) == 1 })
	s.fail()
	requireReleased(t, awaitTx(t, queued).err, "")
	_, err := s.handles[0].Change(func(*delta.Object) (any, error) { return nil, nil }, background)
	requireReleased(t, err, "")
	txRequireValue(t, s.o, s.handles[0], obj("n", float64(0)))
	close(gate)
	requireReleased(t, awaitTx(t, first).err, "")
}

// --- tables in a transaction ---

// tableOwner is the owner of an object's table handles: the object's documents are their scope.
type tableOwner struct{ o *object }

func (owner *tableOwner) Label() string { return owner.o.label }
func (owner *tableOwner) Scope() any    { return owner.o.documents }
func (owner *tableOwner) AssertLive() error {
	var err error
	owner.o.Do(func() { err = owner.o.documents.AssertLive() })
	return err
}
func (owner *tableOwner) Fail(err *data.ChordError) { owner.o.Fail(err) }

var openFence = store.CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64) error { return nil }}

// storedObject is a restartable store object with `N` and `Edits` (and, persisted, `P` and the keyed `K`) on a store
// with an outbox; its handles by attempt.
type storedObject struct {
	t        *testing.T
	o        *object
	store    *store.ChordStore
	attempts []*attempt
	n        []*Singleton
	edits    []*store.TableHandle
	p        []*Singleton
	k        []*Keyed
	persists bool
}

// storedOn is a store object on st (a fresh store with an outbox when nil). With persists it declares P and K, else Edits.
func storedOn(t *testing.T, st *store.ChordStore, persists bool, meter *nexttest.Meter) *storedObject {
	t.Helper()
	if st == nil {
		st = memoryStore(t, true)
	}
	s := &storedObject{t: t, store: st, persists: persists}
	s.o = txObject(t, "app.restartable(1)", objectOptions{store: st}, meter, nil)
	if !persists {
		requireNoError(t, store.PrepareTables([]store.DeclaredTable{{Definition: txEdits, Table: "app.owner:edits"}}, st, openFence, "app.owner", background))
	}
	s.retryAttempt()
	return s
}

func (s *storedObject) retryAttempt() {
	s.t.Helper()
	declare := func(a *attempt) {
		s.n = append(s.n, a.Singleton(txN))
		if s.persists {
			s.p = append(s.p, a.Singleton(txP))
			s.k = append(s.k, a.Keyed(txK))
		} else {
			s.edits = append(s.edits, store.NewTableHandle(txEdits, "app.owner:edits", s.store, &tableOwner{s.o}))
		}
	}
	var next *attempt
	var err error
	if len(s.attempts) == 0 {
		next = s.o.newAttempt()
		declare(next)
		err = next.Activate()
	} else {
		next, err = s.o.restart(declare)
	}
	requireNoError(s.t, err)
	s.attempts = append(s.attempts, next)
}

func (s *storedObject) fail() { s.attempts[len(s.attempts)-1].Fail() }

func (s *storedObject) self() *attempt { return s.attempts[0] }

func rowOf(id string, at int64) store.Row { return store.Row{"id": id, "at": at} }

func (s *storedObject) tableRow(t *testing.T, handle *store.TableHandle, id string) store.Row {
	t.Helper()
	row, err := handle.Get(store.Row{"id": id}, background)
	requireNoError(t, err)
	return row
}

// holdingWrite is a staged write that holds its commit step inside its backend transaction: entered is closed when the
// step applies it, release lets the step finish. It records a `dropIndex` of a table the step does not have, which only
// the outbox sees.
type heldWrite struct {
	entered chan struct{}
	gate    chan struct{}
	once    sync.Once
	apply   func()
}

func newHeldWrite() *heldWrite {
	return &heldWrite{entered: make(chan struct{}), gate: make(chan struct{})}
}

func (w *heldWrite) StageOrder() store.WriteOrder { return store.OrderWrite }
func (w *heldWrite) Apply(*store.Step) ([]store.StoreWrite, error) {
	if w.apply != nil {
		w.apply()
		return nil, nil
	}
	close(w.entered)
	<-w.gate
	return []store.StoreWrite{&store.DropIndexWrite{Table: "x", Name: "y"}}, nil
}
func (w *heldWrite) release() { w.once.Do(func() { close(w.gate) }) }

// holdBackend holds the store's backend inside a maintenance transaction: reads wait until release.
func holdBackend(t *testing.T, st *store.ChordStore) (release func()) {
	t.Helper()
	entered := make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	done := make(chan error, 1)
	go func() {
		done <- st.Maintain(func(sqlite.SqliteExecutor) error {
			close(entered)
			<-gate
			return nil
		})
	}()
	<-entered
	return func() {
		once.Do(func() { close(gate) })
		requireNoError(t, <-done)
	}
}

func TestTablesInATransactionCommitRowsAndDocumentsInOneStepTheDocumentsAdoptInsideItOneSeq(t *testing.T) {
	s := storedOn(t, nil, false, nil)
	watch := watchOf(t, s.o, s.n[0])
	var seen nexttest.Collector[int64]
	watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error {
		seen.Append(s.store.LastSeq())
		return nil
	})
	s.o.delivered()
	before := s.store.LastSeq()
	_, err := s.self().Tx(func(tx *Transaction) (any, error) {
		if err := tx.Put(s.edits[0], rowOf("e1", 1)); err != nil {
			return nil, err
		}
		draft, err := tx.Draft(s.n[0])
		if err != nil {
			return nil, err
		}
		txSetN(t, draft, 1)
		return nil, nil
	}, background)
	requireNoError(t, err)
	s.o.delivered()
	if got := s.store.LastSeq(); got != before+1 {
		t.Fatalf("lastSeq = %d, want %d", got, before+1)
	}
	items := seen.Items()
	if len(items) == 0 || items[len(items)-1] != before+1 {
		t.Fatalf("the watch saw lastSeq %v, want %d last", items, before+1)
	}
	txRequireValue(t, s.o, s.n[0], obj("n", float64(1)))
	if row := s.tableRow(t, s.edits[0], "e1"); !reflect.DeepEqual(row, rowOf("e1", 1)) {
		t.Fatalf("row = %v", row)
	}
	_, _ = watch.Stop()
}

func TestTablesInATransactionInMemoryOnlyCommitsTakeNoStepAndNoSeq(t *testing.T) {
	meter := nexttest.Metered(t)
	s := storedOn(t, nil, false, nil)
	commits := meter.Counts()["chord.store.commits"]
	seq := s.store.LastSeq()
	_, err := s.self().Tx(func(tx *Transaction) (any, error) {
		draft, err := tx.Draft(s.n[0])
		if err != nil {
			return nil, err
		}
		txSetN(t, draft, 5)
		return nil, nil
	}, meter.Context)
	requireNoError(t, err)
	if got := s.store.LastSeq(); got != seq {
		t.Fatalf("lastSeq = %d, want %d", got, seq)
	}
	if got := meter.Counts()["chord.store.commits"]; got != commits {
		t.Fatalf("commits = %v, want %v", got, commits)
	}
}

func TestTablesInATransactionReadCommittedRowsWithPageAndRowsOverlayOwnWritesWithGet(t *testing.T) {
	s := storedOn(t, nil, false, nil)
	edits := s.edits[0]
	_, err := s.self().Tx(func(tx *Transaction) (any, error) { return nil, tx.Put(edits, rowOf("e1", 1)) }, background)
	requireNoError(t, err)
	_, err = s.self().Tx(func(tx *Transaction) (any, error) {
		requireNoError(t, tx.Put(edits, rowOf("e2", 2)))
		page, err := tx.Page(edits, store.NewQuery(txEdits), 10)
		requireNoError(t, err)
		var ids []string
		for _, row := range page.Items {
			ids = append(ids, row["id"].(string))
		}
		if want := []string{"e1"}; !reflect.DeepEqual(ids, want) {
			t.Fatalf("page ids = %v, want %v", ids, want)
		}
		all := []string{}
		rows := tx.Rows(edits, store.NewQuery(txEdits))
		for {
			row, ok, err := rows.Next(background)
			requireNoError(t, err)
			if !ok {
				break
			}
			all = append(all, row["id"].(string))
		}
		if want := []string{"e1"}; !reflect.DeepEqual(all, want) {
			t.Fatalf("rows = %v, want %v", all, want)
		}
		row, err := tx.Get(edits, store.Row{"id": "e2"})
		requireNoError(t, err)
		if !reflect.DeepEqual(row, rowOf("e2", 2)) {
			t.Fatalf("get = %v", row)
		}
		return nil, nil
	}, background)
	requireNoError(t, err)
	count, err := edits.Count(store.NewQuery(txEdits), background)
	requireNoError(t, err)
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}

func TestTablesInATransactionWriteNothingOnAThrowOrAnAbortAndRefuseACallbackThatSettlesWithAPendingRead(t *testing.T) {
	s := storedOn(t, nil, false, nil)
	edits := s.edits[0]
	seq := s.store.LastSeq()
	_, err := s.self().Tx(func(tx *Transaction) (any, error) {
		requireNoError(t, tx.Put(edits, rowOf("e1", 1)))
		return nil, errors.New("no")
	}, background)
	requireErrorText(t, err, "no")
	cancelled, cancel := chordctx.WithCancel(background)
	_, err = s.self().Tx(func(tx *Transaction) (any, error) {
		requireNoError(t, tx.Put(edits, rowOf("e1", 1)))
		cancel(nil)
		return nil, nil
	}, cancelled)
	if err == nil {
		t.Fatal("an aborted transaction committed")
	}
	// A read the callback did not await: the backend is held, so the read is underway when the callback returns.
	release := holdBackend(t, s.store)
	var read sync.WaitGroup
	_, err = s.self().Tx(func(tx *Transaction) (any, error) {
		read.Go(func() {
			_, _ = tx.Get(edits, store.Row{"id": "e1"})
		})
		waitFor(t, "the read underway", func() bool { return pendingOf(s.o, tx) == 1 })
		return nil, tx.Put(edits, rowOf("e1", 1))
	}, background)
	requireErrorContains(t, err, "settled before its pending tx operations")
	release()
	read.Wait()
	if got := s.store.LastSeq(); got != seq {
		t.Fatalf("lastSeq = %d, want %d", got, seq)
	}
	count, err := edits.Count(store.NewQuery(txEdits), background)
	requireNoError(t, err)
	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}
}

func TestTablesInATransactionRefuseAnotherObjectsTable(t *testing.T) {
	s := storedOn(t, nil, false, nil)
	other := storedOn(t, nil, false, nil)
	_, err := s.self().Tx(func(tx *Transaction) (any, error) { return nil, tx.Put(other.edits[0], rowOf("e1", 1)) }, background)
	requireErrorContains(t, err, "writes only its own object's tables")
}

func TestTablesInATransactionACommitQueuedBehindAnotherOwnersStepIsRefusedAtTheSealAnAdmittedOneIsAdopted(t *testing.T) {
	s := storedOn(t, nil, false, nil)
	// Hold the commit queue with a step that waits.
	held := newHeldWrite()
	holding := make(chan error, 1)
	go func() {
		_, err := s.store.Commit([]store.StagedWrite{held}, openFence, background)
		holding <- err
	}()
	<-held.entered
	queued := goTx(s.attempts[0], func(tx *Transaction) (any, error) {
		if err := tx.Put(s.edits[0], rowOf("e1", 1)); err != nil {
			return nil, err
		}
		draft, err := tx.Draft(s.n[0])
		if err != nil {
			return nil, err
		}
		txSetN(t, draft, 1)
		return nil, nil
	}, background)
	waitFor(t, "the commit queued in the store", func() bool { return commitQueued(s.o) })
	s.fail()
	held.release()
	<-holding
	requireReleased(t, awaitTx(t, queued).err, "")
	s.retryAttempt()
	txRequireValue(t, s.o, s.n[1], obj("n", float64(0)))
	if row := s.tableRow(t, s.edits[1], "e1"); row != nil {
		t.Fatalf("a refused commit wrote %v", row)
	}
	// Admitted, then sealed while in the step: durable, adopted into the object's slots, `self.tx` resolves. The step
	// seals the attempt from a write it applies (Pi wraps the fence's admit to fail right after the admission).
	admitted, err := s.attempts[1].Tx(func(tx *Transaction) (any, error) {
		if err := tx.Put(s.edits[1], rowOf("e2", 2)); err != nil {
			return nil, err
		}
		draft, err := tx.Draft(s.n[1])
		if err != nil {
			return nil, err
		}
		txSetN(t, draft, 2)
		sealing := newHeldWrite()
		sealing.apply = s.fail
		s.o.Do(func() {
			writes := tx.StageStore(s.store)
			*writes = append(*writes, sealing)
		})
		return "done", nil
	}, background)
	requireNoError(t, err)
	if admitted != "done" {
		t.Fatalf("admitted = %v", admitted)
	}
	s.retryAttempt()
	txRequireValue(t, s.o, s.n[2], obj("n", float64(2)))
	if row := s.tableRow(t, s.edits[2], "e2"); !reflect.DeepEqual(row, rowOf("e2", 2)) {
		t.Fatalf("row = %v", row)
	}
}

// --- persisted documents in a transaction ---

func TestPersistedDocumentsInATransactionCommitPersistedAndInMemoryDocumentsInOneStepADeltaCommitTakesThreeStatements(t *testing.T) {
	meter := nexttest.Metered(t)
	s := storedOn(t, nil, true, nil)
	n, p := s.n[0], s.p[0]
	_, err := s.self().Tx(func(tx *Transaction) (any, error) {
		a, err := tx.Draft(n)
		if err != nil {
			return nil, err
		}
		txSetN(t, a, 1)
		b, err := tx.Draft(p)
		if err != nil {
			return nil, err
		}
		txSetN(t, b, 1)
		return nil, nil
	}, meter.Context)
	requireNoError(t, err)
	if got := s.store.LastSeq(); got != 1 {
		t.Fatalf("lastSeq = %d, want 1", got)
	}
	before := meter.Counts()
	_, err = s.self().Tx(func(tx *Transaction) (any, error) {
		draft, err := tx.Draft(p)
		if err != nil {
			return nil, err
		}
		txSetN(t, draft, 2)
		return nil, nil
	}, meter.Context)
	requireNoError(t, err)
	after := meter.Counts()
	if got := after["chord.store.commits"] - before["chord.store.commits"]; got != 1 {
		t.Fatalf("commits = %v, want 1", got)
	}
	// The revision, the record's conditional UPDATE, lastSeq; plus the outbox commit.
	if got := after["chord.store.statements"] - before["chord.store.statements"]; got != 4 {
		t.Fatalf("statements = %v, want 4", got)
	}
	txRequireValue(t, s.o, n, obj("n", float64(1)))
	txRequireValue(t, s.o, p, obj("n", float64(2)))
}

func TestPersistedDocumentsInATransactionAKeyedRetirementTakes6asStatementsTheCountedDeleteTheRevisionsLastSeq(t *testing.T) {
	meter := nexttest.Metered(t)
	s := storedOn(t, nil, true, nil)
	k := s.k[0]
	_, err := s.self().Tx(func(tx *Transaction) (any, error) {
		draft, err := tx.Draft(k, "a")
		if err != nil {
			return nil, err
		}
		txSetN(t, draft, 1)
		return nil, nil
	}, meter.Context)
	requireNoError(t, err)
	before := meter.Counts()
	_, err = s.self().Tx(func(tx *Transaction) (any, error) { return nil, tx.Retire(k, "a") }, meter.Context)
	requireNoError(t, err)
	// No read of the record first, no second DELETE of its row; plus the outbox commit.
	if got := meter.Counts()["chord.store.statements"] - before["chord.store.statements"]; got != 4 {
		t.Fatalf("statements = %v, want 4", got)
	}
}

func TestPersistedDocumentsInATransactionAssignTheCreatedDocumentsIdsByTheirOrderInTheCommitRetireAndRecreateInOneCommit(t *testing.T) {
	s := storedOn(t, nil, true, nil)
	k := s.k[0]
	_, err := s.self().Tx(func(tx *Transaction) (any, error) {
		for _, step := range []struct {
			key string
			n   float64
		}{{"b", 1}, {"a", 2}} {
			draft, err := tx.Draft(k, step.key)
			if err != nil {
				return nil, err
			}
			txSetN(t, draft, step.n)
		}
		return nil, nil
	}, background)
	requireNoError(t, err)
	_, err = s.self().Tx(func(tx *Transaction) (any, error) {
		if err := tx.Retire(k, "b"); err != nil {
			return nil, err
		}
		draft, err := tx.Draft(k, "b")
		if err != nil {
			return nil, err
		}
		txSetN(t, draft, 3)
		return nil, nil
	}, background)
	requireNoError(t, err)
	commits, err := s.store.Outbox().Read(0, 10, background)
	requireNoError(t, err)
	type created struct {
		key string
		id  int64
	}
	var got []created
	for _, commit := range commits {
		for _, write := range commit.Writes {
			if create, ok := write.(*store.DocumentCreate); ok {
				key := "undefined"
				if create.Record.Key != nil {
					key = *create.Record.Key
				}
				got = append(got, created{key, create.Record.ID})
			}
		}
	}
	if want := []created{{"b", 2}, {"a", 3}, {"b", 4}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("created = %v, want %v", got, want)
	}
	var kinds []string
	for _, write := range commits[1].Writes {
		kinds = append(kinds, write.StoreWriteKind())
	}
	if want := []string{"document.retire", "document.create"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	// What is stored of `b` is the new record's: the next change is a delta on it.
	_, err = k.Change("b", func(draft *delta.Object) (any, error) { return nil, draft.Set("n", float64(4)) }, background)
	requireNoError(t, err)
	next, err := s.store.Outbox().Read(2, 10, background)
	requireNoError(t, err)
	if len(next[0].Writes) != 1 {
		t.Fatalf("writes = %v", next[0].Writes)
	}
	change, ok := next[0].Writes[0].(*store.DocumentChange)
	if !ok || change.ID != 4 {
		t.Fatalf("write = %+v, want a document.change of id 4", next[0].Writes[0])
	}
}

func TestPersistedDocumentsInATransactionDraftAColdKeyedDocumentByLoadingItInsideTheTransactionRefuseACallbackWithAPendingLoad(t *testing.T) {
	first := storedOn(t, nil, true, nil)
	_, err := first.k[0].Change("a", func(draft *delta.Object) (any, error) { return nil, draft.Set("n", float64(1)) }, background)
	requireNoError(t, err)
	first.o.End()
	s := storedOn(t, first.store, true, nil)
	k := s.k[0]
	_, err = s.self().Tx(func(tx *Transaction) (any, error) {
		draft, err := tx.Draft(k, "a")
		if err != nil {
			return nil, err
		}
		txSetN(t, draft, nOf(draft)+1)
		return nil, nil
	}, background)
	requireNoError(t, err)
	got, err := k.Get("a", background)
	requireNoError(t, err)
	requireJSON(t, "a", got, obj("n", float64(2)))
	// Not awaited: the draft is still loading when the callback returns (the backend is held, so it is underway).
	release := holdBackend(t, s.store)
	var loading sync.WaitGroup
	_, err = s.self().Tx(func(tx *Transaction) (any, error) {
		loading.Go(func() {
			_, _ = tx.Draft(k, "b")
		})
		waitFor(t, "the load underway", func() bool { return pendingOf(s.o, tx) == 1 })
		return nil, nil
	}, background)
	requireErrorContains(t, err, "the callback settled before its pending tx operations")
	release()
	loading.Wait()
}

// Pi's unawaited continuation of a warm keyed `tx.draft`: a Go Draft of a warm document returns at once, and what the
// callback sets on it is committed.
func TestPersistedDocumentsInATransactionSettleAWarmKeyedDraftAtOnce(t *testing.T) {
	s := storedOn(t, nil, true, nil)
	k := s.k[0]
	_, err := s.self().Tx(func(tx *Transaction) (any, error) {
		draft, err := tx.Draft(k, "a")
		if err != nil {
			return nil, err
		}
		txSetN(t, draft, 1)
		return nil, nil
	}, background)
	requireNoError(t, err)
	_, err = s.self().Tx(func(tx *Transaction) (any, error) {
		draft, err := tx.Draft(k, "a")
		if err != nil {
			return nil, err
		}
		if pending := pendingOf(s.o, tx); pending != 0 {
			t.Fatalf("a warm draft counts %d pending operations", pending)
		}
		txSetN(t, draft, 7)
		return nil, nil
	}, background)
	requireNoError(t, err)
	got, err := k.Get("a", background)
	requireNoError(t, err)
	requireJSON(t, "a", got, obj("n", float64(7)))
}

func TestPersistedDocumentsInATransactionACommitCutAtTheSealWhileItWaitsInTheStoresQueueRejectsReleasedAtOnceWritingNothing(t *testing.T) {
	s := storedOn(t, nil, true, nil)
	p := s.p[0]
	// A suspended export holds the commit queue.
	parts := store.NewStoreExport(s.store, background)
	if _, _, err := parts.Next(background); err != nil {
		t.Fatalf("export: %v", err)
	}
	change := make(chan txResult, 1)
	go func() {
		value, err := p.Change(func(draft *delta.Object) (any, error) { return nil, draft.Set("n", float64(5)) }, background)
		change <- txResult{value, err}
	}()
	waitFor(t, "the commit queued in the store", func() bool { return commitQueued(s.o) })
	s.o.End()
	requireReleased(t, awaitTx(t, change).err, "")
	requireNoError(t, parts.Close())
	if got := s.store.LastSeq(); got != 0 {
		t.Fatalf("lastSeq = %d, want 0", got)
	}
	next := storedOn(t, s.store, true, nil)
	txRequireValue(t, next.o, next.p[0], obj("n", float64(0)))
	_, err := next.p[0].Change(func(draft *delta.Object) (any, error) { return nil, draft.Set("n", float64(6)) }, background)
	requireNoError(t, err)
	if got := s.store.LastSeq(); got != 1 {
		t.Fatalf("lastSeq = %d, want 1", got)
	}
}

func TestPersistedDocumentsInATransactionAHostClosingWhileAnAdmittedCommitIsInTheSQLWaitsForItsAdoptionTheStoreStaysUsable(t *testing.T) {
	s := storedOn(t, nil, true, nil)
	p := s.p[0]
	// The next commit's step waits inside the backend transaction, after its admission.
	held := newHeldWrite()
	change := make(chan txResult, 1)
	go func() {
		value, err := p.Change(func(draft *delta.Object) (any, error) {
			s.o.Do(func() {
				writes := s.o.documents.holder.StageStore(s.store)
				*writes = append(*writes, held)
			})
			return nil, draft.Set("n", float64(5))
		}, background)
		change <- txResult{value, err}
	}()
	<-held.entered
	var closed atomic.Bool
	closing := make(chan struct{})
	go func() {
		s.o.End()
		closed.Store(true)
		close(closing)
	}()
	waitFor(t, "the close waiting for the line", func() bool { return waitingOnLine(s.o) == 1 })
	if closed.Load() {
		t.Fatal("the object ended while an admitted commit was in the SQL")
	}
	held.release()
	requireNoError(t, awaitTx(t, change).err)
	<-closing
	if got := s.store.LastSeq(); got != 1 {
		t.Fatalf("lastSeq = %d, want 1", got)
	}
	next := storedOn(t, s.store, true, nil)
	txRequireValue(t, next.o, next.p[0], obj("n", float64(5)))
	_, err := next.p[0].Change(func(draft *delta.Object) (any, error) { return nil, draft.Set("n", float64(6)) }, background)
	requireNoError(t, err)
	if got := s.store.LastSeq(); got != 2 {
		t.Fatalf("lastSeq = %d, want 2", got)
	}
}

// PENDING(host): "a handler's commit held in the SQL past closeTimeout still adopts before the object ends; the store
// stays usable" needs the host's `serve` of a method handler, `closeTimeout` and its `drain_timeout` report
// (`errors.map(kind)`), and the host's release of a handler's stuck call at the object's end. The documents'
// half of it (an admitted commit holds the line past the seal and adopts) is the previous test.
