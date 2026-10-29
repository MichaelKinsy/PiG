package registries_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports the registry-layer behavior of packages/chord/test/next/registries/value.test.ts that does not need a host,
// driven on RegistryRecord.Value(version) as `host.registry` and `self.registry` read it.
//
// Pending on the host (and contracts, documents, store) lanes, with their upstream case names: "older readers (S2)":
// "without a value mapping, and for a reader newer than the provider, park readers failed { waiting }" (`host.hold`,
// `host.registry` readyTimeout), "never hands out a mapped native whose contributor failed and closed it" (`self.open`,
// `self.follow`), "drops an older reader's last good mapping after a contributor it was built from failed live"
// (`self.follow`); "readers (S3)": all five ("activate once the registry has a value ... parks failed { waiting }",
// "a reader parked on a first `compose` that threw", "host.registry before the publication", "host.registry waits
// through no value", "once live, a later throwing `compose` changes nothing for the reader"; `host.hold`,
// `host.get`, `host.registry`); "readers' memory" ("an ended reader is collected", ChordObject attempts);
// "values' memory": "keeps no old composition once its readers stopped waiting and nobody follows it (Opus)"
// (attempts waiting on `self.registry`), and the host half of "a value an app keeps after `host.close` keeps no closed
// host" (the weak reference to the host); "observe": the abort of `withCancel` (`context` cancellation is the state
// lane's FollowedSource.Observe). The record-level halves are below.

type sumValue struct{ total int }

var (
	Sum   = registries.Registry[int, sumValue]{Kind: "registry", Name: "app.sum", Version: 2}
	SumV1 = registries.Registry[int, int]{Kind: "registry", Name: "app.sum", Version: 1}
)

func total(table registries.RegistryTable[int], _ registries.ComposeContext) (sumValue, error) {
	sum := 0
	for _, entry := range table {
		sum += entry.Value
	}
	return sumValue{total: sum}, nil
}

// observed collects what an observer was called with.
type observed struct {
	calls []chord.NativeWatchCall[any]
	ends  []chord.WatchEnd
}

func (o *observed) Advance(call chord.NativeWatchCall[any], _ context.Context) {
	o.calls = append(o.calls, call)
}
func (o *observed) End(end chord.WatchEnd) { o.ends = append(o.ends, end) }

// waiter is a parked attempt or a waiting `host.registry`.
type waiter struct {
	moved  int
	closed []*data.ChordError
}

func (w *waiter) DependencyMoved()            { w.moved++ }
func (w *waiter) Closed(err *data.ChordError) { w.closed = append(w.closed, err) }

func TestOlderReaders(t *testing.T) {
	t.Run("get the composition mapped back with accepts[old].value, once per composition", func(t *testing.T) {
		e := newEnv(t)
		var mapped []int
		record := serve(e, Sum, registries.RegistryProvider[int, sumValue]{
			Compose: total,
			Accepts: registries.RegistryAccepts[int, sumValue]{
				1: {Value: func(current sumValue) (any, error) {
					mapped = append(mapped, current.total)
					return current.total, nil
				}},
			},
		})
		n := newCell(1)
		contribute(e, Sum, "@app/sum", "app.setter", n.native())
		old, current := record.Value(1), record.Value(2)
		if old.Value() != 1 || old.Value() != 1 {
			t.Fatalf("old = %v", old.Value())
		}
		if got := current.Value(); got != (sumValue{total: 1}) {
			t.Fatalf("current = %v", got)
		}
		n.set(e, 4, nil)
		if got := old.Value(); got != 4 {
			t.Fatalf("old = %v, want 4", got)
		}
		if !reflect.DeepEqual(mapped, []int{1, 4}) {
			t.Fatalf("mapped = %v", mapped)
		}
		if got := e.meter.Counts()["chord.registry.adapts"]; got != 2 {
			t.Fatalf("adapts = %v, want 2", got)
		}
	})

	t.Run("without a value mapping, and for a reader newer than the provider, fail contract_mismatch", func(t *testing.T) {
		SumV3 := registries.Registry[int, sumValue]{Kind: "registry", Name: "app.sum", Version: 3}
		e := newEnv(t)
		record := serve(e, Sum, registries.RegistryProvider[int, sumValue]{Compose: total})
		for _, tc := range []struct {
			version int
			message string
		}{
			{SumV1.Version, "registry app.sum: version 1 is not served here (version 2 is)"},
			{SumV3.Version, "registry app.sum: version 3 is not served here (version 2 is)"},
		} {
			start := record.Value(tc.version).Start()
			chordErr, ok := data.IsChordError(start.Error)
			if start.Kind != state.StartFailed || start.HasLast || !ok || chordErr.Code != data.CodeContractMismatch || chordErr.Message != tc.message {
				t.Fatalf("version %d start = %#v", tc.version, start)
			}
		}
		// Not their fault: nothing is reported.
		if len(e.errors) != 0 {
			t.Fatalf("errors = %#v", e.errors)
		}
	})

	t.Run("drops an older reader's last good mapping after a contributor it was built from left (decision 5)", func(t *testing.T) {
		R := registries.Registry[int, int]{Kind: "registry", Name: "app.mapped", Version: 2}
		Old := registries.Registry[int, int]{Kind: "registry", Name: "app.mapped", Version: 1}
		e := newEnv(t)
		record := serve(e, R, registries.RegistryProvider[int, int]{
			Compose: func(table registries.RegistryTable[int], _ registries.ComposeContext) (int, error) {
				n := 0
				for _, entry := range table {
					n += entry.Value
				}
				return n, nil
			},
			Accepts: registries.RegistryAccepts[int, int]{
				1: {Value: func(n int) (any, error) {
					if n == 0 {
						return nil, errors.New("old version cannot map the empty table")
					}
					return n, nil
				}},
			},
		})
		contributor := contribute(e, R, "@app/mapping", "app.contributor", plain(42))
		old := record.Value(Old.Version)
		if got := old.Value(); got != 42 {
			t.Fatalf("old = %v", got)
		}
		e.change(func() { record.Remove(contributor) })
		start := old.Start()
		if start.Kind != state.StartFailed || start.HasLast || start.Error.Error() != "old version cannot map the empty table" {
			t.Fatalf("start = %#v", start)
		}
	})

	t.Run("keeps its last good mapping while every contribution it was built from is in the table", func(t *testing.T) {
		R := registries.Registry[int, int]{Kind: "registry", Name: "app.mapped", Version: 2}
		e := newEnv(t)
		record := serve(e, R, registries.RegistryProvider[int, int]{
			Compose: func(table registries.RegistryTable[int], _ registries.ComposeContext) (int, error) {
				return table[0].Value, nil
			},
			Accepts: registries.RegistryAccepts[int, int]{
				1: {Value: func(n int) (any, error) {
					if n < 0 {
						return nil, errors.New("negative")
					}
					return n, nil
				}},
			},
		})
		n := newCell(5)
		contribute(e, R, "@app/mapping", "", n.native())
		old := record.Value(1)
		if got := old.Value(); got != 5 {
			t.Fatalf("old = %v", got)
		}
		n.set(e, -1, nil)
		start := old.Start()
		if start.Kind != state.StartFailed || !start.HasLast || start.Last != 5 || start.Error.Error() != "negative" {
			t.Fatalf("start = %#v", start)
		}
		if got := old.Value(); got != 5 {
			t.Fatalf("old = %v, want the last good mapping", got)
		}
		// A mapping that failed reports once, whatever it is read.
		old.Start()
		if got := kinds(e.errors); !reflect.DeepEqual(got, []string{"registry_failed"}) {
			t.Fatalf("kinds = %v", got)
		}
	})

	t.Run("maps an `undefined` composition (Sol, Astra)", func(t *testing.T) {
		Nothing := mustDefine[int, any](t, "app.nothing", 2)
		e := newEnv(t)
		record := serve(e, Nothing, registries.RegistryProvider[int, any]{
			Compose: func(registries.RegistryTable[int], registries.ComposeContext) (any, error) { return nil, nil },
			Accepts: registries.RegistryAccepts[int, any]{1: {Value: func(any) (any, error) { return "mapped", nil }}},
		})
		if got := record.Value(1).Value(); got != "mapped" {
			t.Fatalf("value = %v", got)
		}
	})

	t.Run("a failed mapping heals when the composition is the one mapped before (Astra)", func(t *testing.T) {
		Count := mustDefine[int, int](t, "app.count", 2)
		e := newEnv(t)
		record := serve(e, Count, registries.RegistryProvider[int, int]{
			Compose: func(table registries.RegistryTable[int], _ registries.ComposeContext) (int, error) {
				n := 0
				for _, entry := range table {
					n += entry.Value
				}
				return n, nil
			},
			Accepts: registries.RegistryAccepts[int, int]{
				1: {Value: func(n int) (any, error) {
					if n < 0 {
						return nil, errors.New("negative")
					}
					return n, nil
				}},
			},
		})
		n := newCell(1)
		contribute(e, Count, "@app/count", "app.counter", n.native())
		observer := &observed{}
		if _, err := record.Value(1).Observe(observer, context.Background()); err != nil {
			t.Fatal(err)
		}
		n.set(e, -1, nil)
		// Back to the composition mapped before (the same number): the mapping heals.
		n.set(e, 1, nil)
		if len(observer.calls) != 2 {
			t.Fatalf("calls = %#v", observer.calls)
		}
		if first := observer.calls[0]; first.Kind != chord.WatchCallFailed || first.Error.Error() != "negative" {
			t.Fatalf("first call = %#v", first)
		}
		if second := observer.calls[1]; second.Kind != chord.WatchCallValue || second.Value != 1 {
			t.Fatalf("second call = %#v", second)
		}
	})
}

func TestReaders(t *testing.T) {
	t.Run("a waiter is told at every change of its failure while the value is unusable, and once when it becomes usable", func(t *testing.T) {
		e := newEnv(t)
		record := serve(e, Sum, registries.RegistryProvider[int, sumValue]{
			Compose: func(table registries.RegistryTable[int], context registries.ComposeContext) (sumValue, error) {
				sum, _ := total(table, context)
				if sum.total == 0 {
					return sumValue{}, errors.New("empty")
				}
				return sum, nil
			},
		})
		value := record.Value(2)
		parked := &waiter{}
		release := value.Wait(parked)
		// The first start of a wait wakes nobody: the waiter reads it itself.
		if start := value.Start(); start.Kind != state.StartFailed || start.HasLast {
			t.Fatalf("start = %#v", start)
		}
		if parked.moved != 0 {
			t.Fatalf("moved = %d before any change", parked.moved)
		}
		n := newCell(0)
		contribute(e, Sum, "@app/setter", "app.setter", n.native())
		// Still no value (an empty sum), but another throw of `compose`: errors are compared by identity, so it is told.
		if parked.moved != 1 {
			t.Fatalf("moved = %d after another failure, want 1", parked.moved)
		}
		n.set(e, 3, nil)
		// Woken by the composition pulled in the batch of the set: it is usable now.
		if parked.moved != 2 {
			t.Fatalf("moved = %d, want 2", parked.moved)
		}
		if got := value.Value(); got != (sumValue{total: 3}) {
			t.Fatalf("value = %v", got)
		}
		n.set(e, 4, nil)
		if parked.moved != 2 {
			t.Fatalf("a usable value woke a waiter again: %d", parked.moved)
		}
		release()
		n.set(e, 0, nil)
		n.set(e, 5, nil)
		if parked.moved != 2 {
			t.Fatalf("a released waiter was woken: %d", parked.moved)
		}
	})

	t.Run("once live, a later failing compose changes nothing for the reader: its value keeps the last good one", func(t *testing.T) {
		e := newEnv(t)
		record := serve(e, Sum, registries.RegistryProvider[int, sumValue]{
			Compose: func(table registries.RegistryTable[int], context registries.ComposeContext) (sumValue, error) {
				sum, _ := total(table, context)
				if sum.total < 0 {
					return sumValue{}, errors.New("negative")
				}
				return sum, nil
			},
		})
		n := newCell(2)
		contribute(e, Sum, "@app/setter", "app.setter", n.native())
		value := record.Value(2)
		parked := &waiter{}
		defer value.Wait(parked)()
		if got := value.Value(); got != (sumValue{total: 2}) {
			t.Fatalf("value = %v", got)
		}
		n.set(e, -1, nil)
		if got := value.Value(); got != (sumValue{total: 2}) {
			t.Fatalf("value = %v, want the last good one", got)
		}
		// Usable all along (a failure with a last good value): the waiter is not woken.
		if parked.moved != 0 {
			t.Fatalf("moved = %d", parked.moved)
		}
	})

	t.Run("a close ends the waits: released/closed", func(t *testing.T) {
		e := newEnv(t)
		record := serve(e, Sum, registries.RegistryProvider[int, sumValue]{Compose: total})
		value := record.Value(2)
		parked := &waiter{}
		value.Wait(parked)
		record.Close()
		if len(parked.closed) != 1 {
			t.Fatalf("closed = %v", parked.closed)
		}
		chordErr := parked.closed[0]
		if chordErr.Code != data.CodeReleased || chordErr.Reason != data.ReasonClosed || chordErr.Message != "registry app.sum: closed" {
			t.Fatalf("error = %#v", chordErr)
		}
	})
}

func TestValuesMemory(t *testing.T) {
	t.Run("drops an older version's mapping once its last reader ended (Sol)", func(t *testing.T) {
		type mappedValue struct {
			n       int
			payload [1000]byte
		}
		New := mustDefine[int, int](t, "app.version-gc", 2)
		e := newEnv(t)
		var mapped weak.Pointer[mappedValue]
		maps := 0
		record := serve(e, New, registries.RegistryProvider[int, int]{
			Compose: func(registries.RegistryTable[int], registries.ComposeContext) (int, error) { return 1, nil },
			Accepts: registries.RegistryAccepts[int, int]{
				1: {Value: func(n int) (any, error) {
					value := &mappedValue{n: n}
					mapped = weak.Make(value)
					maps++
					return value, nil
				}},
			},
		})
		old := record.Value(1)
		func() {
			release := old.Read()
			if got := old.Value().(*mappedValue).n; got != 1 {
				t.Fatalf("n = %d", got)
			}
			release()
		}()
		nexttest.ExpectCollected(t, mapped)
		// Computed again on the next read.
		if got := old.Value().(*mappedValue).n; got != 1 || maps != 2 {
			t.Fatalf("n = %d after %d mappings", got, maps)
		}
	})

	t.Run("a value an app keeps after the host closed keeps no closed record, and reads its last composition (Sol, Astra)", func(t *testing.T) {
		Kept := mustDefine[int, int](t, "app.kept", 1)
		observer := &observed{}
		record, value := func() (weak.Pointer[registries.RegistryRecord], *registries.RegistryValueSource) {
			e := newEnv(t)
			record := serve(e, Kept, registries.RegistryProvider[int, int]{
				Compose: func(registries.RegistryTable[int], registries.ComposeContext) (int, error) { return 7, nil },
			})
			value := record.Value(1)
			if _, err := value.Observe(observer, context.Background()); err != nil {
				t.Fatal(err)
			}
			record.Close()
			return weak.Make(record), value
		}()
		// The observation ended with the record.
		if len(observer.ends) != 1 || observer.ends[0].Kind != chord.WatchEnded {
			t.Fatalf("ends = %#v", observer.ends)
		}
		nexttest.ExpectCollected(t, record)
		if got := value.Value(); got != 7 {
			t.Fatalf("value = %v, want 7", got)
		}
	})
}

func TestObserve(t *testing.T) {
	t.Run("calls a synchronous observer inside the batch that composed; it counts as watching", func(t *testing.T) {
		Labels := mustDefine[string, string](t, "app.labels", 1)
		e := newEnv(t)
		record := serve(e, Labels, registries.RegistryProvider[string, string]{
			Compose: func(table registries.RegistryTable[string], _ registries.ComposeContext) (string, error) {
				out := ""
				for i, entry := range table {
					if i > 0 {
						out += ","
					}
					out += entry.Value
				}
				return out, nil
			},
		})
		label := newCell("a")
		contribute(e, Labels, "@app/labels", "app.label", label.native())
		value := record.Value(1)
		observer := &observed{}
		observation, err := value.Observe(observer, context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if observation.Value != "a" {
			t.Fatalf("observation = %v", observation.Value)
		}
		label.set(e, "b", nil)
		// Already called: synchronously, in the set's batch.
		if len(observer.calls) != 1 || observer.calls[0].Kind != chord.WatchCallValue || observer.calls[0].Value != "b" {
			t.Fatalf("calls = %#v", observer.calls)
		}
		again := &observed{}
		detached, err := value.Observe(again, context.Background())
		if err != nil {
			t.Fatal(err)
		}
		detached.Detach()
		label.set(e, "d", nil)
		if len(again.calls) != 0 || len(observer.calls) != 2 {
			t.Fatalf("a detached observer was called: %d/%d", len(again.calls), len(observer.calls))
		}
		record.Close()
		if len(observer.ends) != 1 || observer.ends[0].Kind != chord.WatchEnded {
			t.Fatalf("ends = %#v", observer.ends)
		}
	})
}

// typedObserved is a NativeObserver[string] collecting its calls.
type typedObserved struct {
	calls []chord.NativeWatchCall[string]
}

func (o *typedObserved) Advance(call chord.NativeWatchCall[string], _ context.Context) {
	o.calls = append(o.calls, call)
}
func (o *typedObserved) End(chord.WatchEnd) {}

func TestRegistryValue(t *testing.T) {
	t.Run("a typed view reads, observes and watches the composition as V", func(t *testing.T) {
		Labels := mustDefine[string, string](t, "app.labels", 1)
		e := newEnv(t)
		record := serve(e, Labels, registries.RegistryProvider[string, string]{
			Compose: func(table registries.RegistryTable[string], _ registries.ComposeContext) (string, error) {
				if len(table) == 0 {
					return "", nil
				}
				return table[0].Value, nil
			},
		})
		label := newCell("a")
		contribute(e, Labels, "@app/labels", "app.label", label.native())
		var typed registries.RegistryValue[string] = registries.NewRegistryValue[string](record.Value(1))
		if got := typed.Value(); got != "a" {
			t.Fatalf("value = %q", got)
		}
		observer := &typedObserved{}
		observation, err := typed.Observe(observer, context.Background())
		if err != nil || observation.Value != "a" {
			t.Fatalf("observation = %#v, %v", observation, err)
		}
		label.set(e, "b", nil)
		if len(observer.calls) != 1 || observer.calls[0].Kind != chord.WatchCallValue || observer.calls[0].Value != "b" {
			t.Fatalf("calls = %#v", observer.calls)
		}
		observation.Detach()
		handle, err := typed.Watch(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if got := handle.Value(); got != "b" {
			t.Fatalf("watch value = %q", got)
		}
		if _, err := handle.Stop(); err != nil {
			t.Fatal(err)
		}
	})
}
