package documents

// Ports packages/chord/test/next/documents/address-source.test.ts

import (
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// testAddress is an address whose value the test sets (upstream's `Address extends AddressSource`).
type testAddress struct {
	AddressSource
	current *JsonObject
}

func (address *testAddress) Value() (delta.JsonValue, error) { return address.current, nil }

func newTestAddress(batch *state.Batch) *testAddress {
	address := &testAddress{current: obj("n", 0.0)}
	address.Init(batch, address)
	return address
}

// prepared is a write of `n` over base: the draft a tracker over base prepared (upstream builds the Prepared by hand;
// Go's is opaque, so a real draft stands for it, with the ops it emits).
func preparedWrite(t *testing.T, base *JsonObject, n float64) *delta.Prepared {
	t.Helper()
	tracker := delta.Track(base)
	change := tracker.BeginChange()
	if err := change.State().Set("n", n); err != nil {
		t.Fatal(err)
	}
	prepared, err := change.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

type watchedAddress struct {
	batch     *state.Batch
	scheduler *chord.DeliveryScheduler
	address   *testAddress
	changes   *nexttest.Collector[chord.WatchCall[delta.JsonValue]]
}

// watched is a watched address on its own batch, and the changes its watch got.
func watched(t *testing.T) *watchedAddress {
	t.Helper()
	scheduler := chord.NewDeliveryScheduler()
	batch := state.NewBatch(func(state.BatchError) {}, nil).UseScheduler(scheduler)
	address := newTestAddress(batch)
	handle, err := address.Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	return &watchedAddress{batch: batch, scheduler: scheduler, address: address, changes: nexttest.ChangesOf(handle)}
}

func (w *watchedAddress) commit(t *testing.T, adopt func()) {
	t.Helper()
	// A commit is one publication: its frames fold into one call, as the microtask upstream's advance queues runs
	// only after the commit's synchronous body (a host holds its scheduler for the commit).
	release := w.scheduler.Hold()
	err := w.batch.Commit(func() error { adopt(); return nil }, background)
	release()
	if err != nil {
		t.Fatal(err)
	}
	w.scheduler.Wait()
}

// requireChanges compares the watch's calls with the JSON text of upstream's expected array of
// `{ kind: "value", value, ops, retired }`.
func requireChanges(t *testing.T, calls []chord.WatchCall[delta.JsonValue], want string) {
	t.Helper()
	got := make([]any, len(calls))
	for i, call := range calls {
		ops := make([]any, len(call.Ops))
		for j, op := range call.Ops {
			ops[j] = []any(op)
		}
		kind := string(call.Kind)
		got[i] = obj("kind", kind, "value", call.Value, "ops", ops, "retired", call.Retired)
	}
	nexttest.RequireJSONEqual(t, nexttest.ParseJSON(t, nexttest.JSONText(t, got)), nexttest.ParseJSON(t, want))
}

func TestAddressFramesAWriteOverTheReadValueCarriesItsOpsOverAnotherBaseARootReplacement(t *testing.T) {
	w := watched(t)
	first := w.address.current
	w.commit(t, func() {
		w.address.current = obj("n", 1.0)
		w.address.Written(first, preparedWrite(t, first, 1))
	})
	second := w.address.current
	w.commit(t, func() {
		w.address.current = obj("n", 2.0)
		w.address.Written(second, preparedWrite(t, obj("n", 7.0), 2))
	})
	requireChanges(t, w.changes.Items(), `[
		{"kind":"value","value":{"n":1},"ops":[["s",["n"],1]],"retired":false},
		{"kind":"value","value":{"n":2},"ops":[["r",{"n":2}]],"retired":false}
	]`)
}

func TestAddressFramesARetirementReadsInitialWithTheFlagARecreationInTheSameBatchIsOneRootReplacementFlagged(t *testing.T) {
	w := watched(t)
	w.commit(t, func() {
		w.address.current = obj("n", 0.0)
		w.address.RetiredTo(w.address.current)
	})
	w.commit(t, func() {
		initial := w.address.current
		w.address.RetiredTo(initial)
		w.address.current = obj("n", 5.0)
		w.address.Written(initial, preparedWrite(t, initial, 5))
	})
	recreated := w.address.current
	w.commit(t, func() {
		w.address.current = obj("n", 6.0)
		w.address.Written(recreated, preparedWrite(t, recreated, 6))
	})
	requireChanges(t, w.changes.Items(), `[
		{"kind":"value","value":{"n":0},"ops":[["r",{"n":0}]],"retired":true},
		{"kind":"value","value":{"n":5},"ops":[["r",{"n":5}]],"retired":true},
		{"kind":"value","value":{"n":6},"ops":[["s",["n"],6]],"retired":false}
	]`)
}

func TestAddressFramesAReplacementOutsideACommitIsARootReplacement(t *testing.T) {
	w := watched(t)
	w.commit(t, func() {
		w.address.current = obj("n", 3.0)
		if err := w.address.Replaced(); err != nil {
			t.Error(err)
		}
	})
	requireChanges(t, w.changes.Items(), `[{"kind":"value","value":{"n":3},"ops":[["r",{"n":3}]],"retired":false}]`)
}

func TestUnwatchedAddressesKeepNoOpsOfAWriteNoFrameTakesThem(t *testing.T) {
	batch := state.NewBatch(func(state.BatchError) {}, nil)
	address := newTestAddress(batch)
	// The ops of the write, held by nothing but what the address keeps; the draft, tracker and prepared are dropped.
	collected := func() weak.Pointer[delta.Op] {
		base := address.current
		prepared := preparedWrite(t, base, 1)
		ops := prepared.Ops()
		if len(ops) == 0 {
			t.Fatal("the write emitted no ops")
		}
		ref := weak.Make(&ops[0])
		if err := batch.Commit(func() error {
			address.current = obj("n", 1.0)
			address.Written(base, prepared)
			return nil
		}, background); err != nil {
			t.Fatal(err)
		}
		return ref
	}()
	nexttest.ExpectCollected(t, collected)
	// The address itself is still here.
	value, err := address.Value()
	if err != nil {
		t.Fatal(err)
	}
	if text := nexttest.JSONText(t, value); text != `{"n":1}` {
		t.Errorf("value = %s", text)
	}
}
