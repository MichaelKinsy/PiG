package host

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// pi: packages/chord/src/next/state/batch.ts

// Ports packages/chord/test/next/state/batch.test.ts: the cases that run on a host with served objects, documents and
// references: "reentrancy" (every Chord call a derive compute may not make is refused with invalid/reentrant, in a
// commit's compute and in a lazy one, and the flag is cleared after a throwing compute) and "delivery" (a delivery
// that throws is reported and the later frames of the commit are still delivered).

var (
	batchNum = func() *documents.StateDefinition {
		definition, err := documents.DefineState("num", documents.StateOptions{
			Schema:  contracts.Obj(contracts.Prop("n", contracts.Int())),
			Version: 1,
			Initial: func() *delta.JsonObject { return jsonObject("n", 0.0) },
		})
		if err != nil {
			panic(err)
		}
		return definition
	}()
	batchProbeType = exLocal("app.probe", contracts.Params{},
		contracts.Mem("probe", contracts.Int()), contracts.Mem("fixed", contracts.Int()), contracts.Mem("set", contracts.NewMethod(contracts.Int())))
)

// batchAttempts is every Chord call a derive compute may not make: what each operation returned or panicked with.
type batchAttempts map[string]error

// batchOutcome is what a call returned, or the error it panicked with (a throw).
func batchOutcome(run func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err, _ = recovered.(error)
		}
	}()
	return run()
}

// batchProbe is a host whose `probe` derive runs attempt on every compute with n > 0. attempt gets the host and a
// reference to the probe itself; what each operation threw is recorded.
func batchProbe(t *testing.T, attempt func(host Host, self *Reference, change func() error) batchAttempts) (Host, *Reference, func() []batchAttempts) {
	t.Helper()
	var mu sync.Mutex
	var seen []batchAttempts
	var holder struct {
		host Host
		ref  *Reference
	}
	var ready atomic.Bool
	var holderMu sync.Mutex
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(batchProbeType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			num := stateOf(self, batchNum)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"probe": self.Derive([]any{num.Member()}, func(values []any) (delta.JsonValue, error) {
					n := values[0].(*delta.JsonObject).Value("n").(float64)
					if n > 0 && ready.Load() {
						holderMu.Lock()
						host, ref := holder.host, holder.ref
						holderMu.Unlock()
						attempts := attempt(host, ref, func() error { return num.Change(func(draft *delta.Object) { _ = draft.Set("n", 5.0) }, background) })
						mu.Lock()
						seen = append(seen, attempts)
						mu.Unlock()
					}
					if n > 1 {
						return nil, errors.New("compute threw")
					}
					return n, nil
				}),
				"fixed": exConstant(1.0),
			}, map[string]contracts.MethodFunction{
				"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return nil, num.Change(func(draft *delta.Object) { _ = draft.Set("n", args) }, ctx)
				},
			}), nil
		}, ServeOptions{})
	}, loadedOptions{})
	ref := mustGet(t, host, batchProbeType, jsonObject())
	holderMu.Lock()
	holder.host, holder.ref = host, ref
	holderMu.Unlock()
	ready.Store(true)
	return host, ref, func() []batchAttempts {
		mu.Lock()
		defer mu.Unlock()
		return append([]batchAttempts(nil), seen...)
	}
}

func batchAttemptsOf(ctx context.Context) func(host Host, self *Reference, change func() error) batchAttempts {
	return func(host Host, self *Reference, change func() error) batchAttempts {
		return batchAttempts{
			"watch": batchOutcome(func() error {
				_, err := self.Member("probe").Watch(ctx)
				return err
			}),
			"watch a constant member": batchOutcome(func() error {
				_, err := self.Member("fixed").Watch(ctx)
				return err
			}),
			"watch ref.state": batchOutcome(func() error {
				_, err := self.State().Watch(ctx)
				return err
			}),
			"release": batchOutcome(func() error { self.Release(); return nil }),
			"get": batchOutcome(func() error {
				_, err := host.Get(ctx, batchProbeType, jsonObject(), GetOptions{})
				return err
			}),
			"load":  batchOutcome(func() error { _, err := host.Load(nil, ctx); return err }),
			"close": batchOutcome(func() error { return host.Close(ctx) }),
			// `self.set(5)` runs a transaction, and Call waits for the method's result, which a compute (a section of the
			// host's loop) cannot do: the transaction it would run, a document change, is what is attempted.
			"transaction": batchOutcome(change),
		}
	}
}

func requireAllReentrant(t *testing.T, attempts batchAttempts) {
	t.Helper()
	for operation, err := range attempts {
		if chordErr, ok := data.IsChordErrorCode(err, data.CodeInvalid); !ok || chordErr.Reason != data.ReasonReentrant {
			t.Errorf("%s: err = %v, want invalid/reentrant", operation, err)
		}
	}
}

func TestReentrancyRefusesWatchRefStateTooReleaseGetLoadCloseAndATransactionInACommitsDeriveComputes(t *testing.T) {
	host, reference, seen := batchProbe(t, batchAttemptsOf(background))
	watch, err := reference.Member("probe").Watch(background)
	must(t, err)
	watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error { return nil })
	exCall(t, reference, "set", 1.0)
	turn(host)
	attempts := seen()
	if len(attempts) == 0 {
		t.Fatal("the derive never computed with n > 0")
	}
	requireAllReentrant(t, attempts[0])
}

func TestReentrancyRefusesThemInALazyComputeOutsideACommit(t *testing.T) {
	_, reference, seen := batchProbe(t, batchAttemptsOf(background))
	exCall(t, reference, "set", 1.0)
	if got := seen(); len(got) != 0 {
		t.Fatalf("seen = %v, want none before the first read", got)
	}
	if got := rvMember(t, reference, "probe"); got != 1.0 {
		t.Fatalf("probe = %v, want 1", got)
	}
	attempts := seen()
	if len(attempts) == 0 {
		t.Fatal("the lazy compute never ran")
	}
	requireAllReentrant(t, attempts[0])
}

func TestReentrancyClearsTheFlagAfterAThrowingCompute(t *testing.T) {
	host, reference, _ := batchProbe(t, func(Host, *Reference, func() error) batchAttempts { return batchAttempts{} })
	exCall(t, reference, "set", 2.0)
	if _, err := reference.Member("probe").Value(); err == nil || err.Error() != "compute threw" {
		t.Fatalf("probe = %v, want compute threw", err)
	}
	again, err := host.Get(background, batchProbeType, jsonObject(), GetOptions{})
	must(t, err)
	if again.Member("probe") == nil {
		t.Fatal("probe is missing")
	}
	if result, err := reference.Call("set", 3.0, background); err != nil || result != nil {
		t.Fatalf("set after the throwing compute = %v, %v, want null", result, err)
	}
}

func TestDeliveryReportsADeliveryThatThrowsAndStillDeliversTheLaterFramesOfTheCommit(t *testing.T) {
	// Pi's `{ a: undefined }` is no JSON, so diffing it throws and the delivery is reported `delivery_failed`; the Go
	// derive returns a value no JSON has (a channel), which diffing refuses the same way.
	other, err := documents.DefineState("other", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("n", contracts.Int())),
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("n", 0.0) },
	})
	must(t, err)
	two := exLocal("app.two", contracts.Params{},
		contracts.Mem("bad", contracts.Obj(contracts.Prop("a", contracts.Int()))),
		contracts.Mem("other", contracts.Obj(contracts.Prop("n", contracts.Int()))),
		contracts.Mem("set", contracts.NewMethod(contracts.Int())))
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(two, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			num := stateOf(self, batchNum)
			otherDoc := stateOf(self, other)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				// `undefined` is no JSON: diffing it throws (here: a Go value no JSON has).
				"bad": self.Derive([]any{num.Member()}, func(values []any) (delta.JsonValue, error) {
					if values[0].(*delta.JsonObject).Value("n").(float64) == 0 {
						return jsonObject("a", 0.0), nil
					}
					return jsonObject("a", make(chan int)), nil
				}),
				"other": otherDoc.Member(),
			}, map[string]contracts.MethodFunction{
				"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					// `num` is adopted first, so the derive's delivery comes before `other`'s.
					_, err := self.Tx(func(tx documents.Tx) (any, error) {
						first, err := tx.Draft(num.s)
						if err != nil {
							return nil, err
						}
						_ = first.Set("n", args)
						second, err := tx.Draft(otherDoc.s)
						if err != nil {
							return nil, err
						}
						return nil, second.Set("n", args)
					}, ctx)
					return nil, err
				},
			}), nil
		}, ServeOptions{})
	}, loadedOptions{})
	ref := mustGet(t, host, two, jsonObject())
	bad, err := ref.Member("bad").Watch(background)
	must(t, err)
	bad.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error { return nil })
	watch, err := ref.Member("other").Watch(background)
	must(t, err)
	frames := nexttest.FramesOf(watch)
	exCall(t, ref, "set", 1.0)
	nexttest.Delivered()
	scopeEventually(t, "the later frame is delivered", func() bool { return frames.Len() >= 1 })
	var kinds []HostErrorKind
	for _, reported := range errs.All() {
		kinds = append(kinds, reported.Kind)
	}
	if want := []HostErrorKind{"delivery_failed"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("error kinds = %v, want %v", kinds, want)
	}
	items := frames.Items()
	if len(items) != 1 || !nexttest.JSONEqual(items[0].Value, jsonObject("n", 1.0)) {
		t.Fatalf("frames = %v, want [{ n: 1 }]", items)
	}
}
