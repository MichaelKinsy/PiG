package host

import (
	"context"
	"errors"
	"runtime"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/internal/parking"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// A method handler is an async function in Pi: one that throws rejects its call, and one that never resolves leaves the call
// pending until it aborts, with the host going on in both. A Go handler is a coroutine of the section that admits its call
// (Loop.Lend), so the two ways it can leave that Pi has no spelling of are tested here: ending its goroutine (runtime.Goexit,
// which testing.T.FailNow does) is a throw, and a Suspend from a goroutine it started, which could never give the section
// back, is refused rather than ignored.

var lendDesk = mustContract(declareLocal("app.lend-desk", contracts.LocalDeclaration{Params: contracts.Params{},
	Version: 1,
	Members: []contracts.LocalEntry{
		contracts.Mem("exit", contracts.NewMethod()),
		contracts.Mem("exitParked", contracts.NewMethod()),
		contracts.Mem("foreignSuspend", contracts.NewMethod(contracts.TSchema{}, contracts.Str())),
		contracts.Mem("who", contracts.NewMethod(contracts.TSchema{}, contracts.Str())),
	},
}))

// lendDeskRef is a reference to a desk whose handlers misbehave in the ways under test.
func lendDeskRef(t *testing.T) *Reference {
	t.Helper()
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(lendDesk, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				// A handler that ends its goroutine before its first wait: inside the section that admitted its call.
				"exit": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					runtime.Goexit()
					return nil, nil
				},
				// One that ends it after it suspended: what is left of it runs after the section.
				"exitParked": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					chordctx.Suspend(ctx)
					runtime.Goexit()
					return nil, nil
				},
				// One that suspends from a goroutine it started, and learns what that did.
				"foreignSuspend": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					outcome := make(chan any, 1)
					go func() {
						defer func() { outcome <- recover() }()
						chordctx.Suspend(ctx)
					}()
					got, err := chordctx.Receive(ctx, outcome)
					if err != nil {
						return nil, err
					}
					if failure, _ := got.(error); errors.Is(failure, parking.ErrForeignSuspend) {
						return "refused", nil
					}
					return "ignored", nil
				},
				"who": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) { return whoIs(ctx), nil },
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	ref, err := host.Get(background, lendDesk, jsonObject(), GetOptions{})
	must(t, err)
	return ref
}

// callAndSurvive calls method from a goroutine of its own and reports its error, failing the test when the goroutine ended
// inside the call (a Goexit that reached the caller).
func callAndSurvive(t *testing.T, ref *Reference, method string) error {
	t.Helper()
	var err error
	returned := false
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		_, err = ref.Call(method, nil, background)
		returned = true
	}()
	<-ended
	if !returned {
		t.Fatalf("%s: the caller's goroutine ended inside its call: the handler's runtime.Goexit reached it", method)
	}
	return err
}

// Pi's awaited handler that throws rejects its call and leaves the loop and the caller as they were: a handler that ends its
// goroutine is that throw, and the goroutine it ends is its own.
func TestMethodHandlerThatEndsItsGoroutineRejectsItsCallAndTheHostGoesOn(t *testing.T) {
	for _, method := range []string{"exit", "exitParked"} {
		t.Run(method, func(t *testing.T) {
			ref := lendDeskRef(t)
			err := callAndSurvive(t, ref, method)
			if !errors.Is(err, errHandlerGoexit) {
				t.Fatalf("%s = %v, want the call rejected with errHandlerGoexit", method, err)
			}
			// The loop is not left owned by the goroutine that ended: the next calls are admitted and settle, and the drain
			// of the host's close at cleanup is not held by the call that was abandoned.
			for range 3 {
				if err := callAndSurvive(t, ref, "who"); err != nil {
					t.Fatalf("a call after the handler ended its goroutine = %v", err)
				}
			}
			if err := callAndSurvive(t, ref, method); !errors.Is(err, errHandlerGoexit) {
				t.Fatalf("%s again = %v, want the call rejected with errHandlerGoexit", method, err)
			}
		})
	}
}

// A call from inside another handler's section (Reference.Call's admission runs in the caller's section, which then drives the
// callee's handler) survives its callee's Goexit as well: the goroutine that would have ended is the caller handler's.
func TestMethodHandlerThatEndsItsGoroutineLeavesTheHandlerThatCalledItRunning(t *testing.T) {
	callee := mustContract(declareLocal("app.lend-callee", contracts.LocalDeclaration{Params: contracts.Params{},
		Version: 1,
		Members: []contracts.LocalEntry{contracts.Mem("exit", contracts.NewMethod())},
	}))
	desk := mustContract(declareLocal("app.lend-caller", contracts.LocalDeclaration{Params: contracts.Params{},
		Version: 1,
		Members: []contracts.LocalEntry{contracts.Mem("through", contracts.NewMethod(contracts.TSchema{}, contracts.Str()))},
	}))
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(callee, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"exit": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					runtime.Goexit()
					return nil, nil
				},
			}}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(desk, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"through": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					held, err := ctx.(Ctx).Get(callee, jsonObject(), GetOptions{})
					if err != nil {
						return nil, err
					}
					if _, err := held.Call("exit", nil, ctx); !errors.Is(err, errHandlerGoexit) {
						return nil, errors.New("the callee's call = " + errString(err))
					}
					return "survived", nil
				},
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	ref, err := host.Get(background, desk, jsonObject(), GetOptions{})
	must(t, err)
	got, err := ref.Call("through", nil, background)
	if err != nil || got != "survived" {
		t.Fatalf("through = %v, %v, want survived", got, err)
	}
}

func errString(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

// Pi has no `suspend`: an `await` ends an async function's synchronous part where it stands, and an `await` in work the
// handler started and did not await does not end the handler's. A Suspend a Go handler's goroutine makes is that second one,
// which would end nothing, so it is refused (whenever it runs: the handler's own next wait does not make it legitimate).
func TestChordctxSuspendOnAGoroutineTheHandlerStartedIsRefused(t *testing.T) {
	ref := lendDeskRef(t)
	got, err := ref.Call("foreignSuspend", nil, background)
	if err != nil {
		t.Fatal(err)
	}
	if got != "refused" {
		t.Fatalf("a Suspend from a goroutine the handler started was %v, want refused with parking.ErrForeignSuspend", got)
	}
}

// The handler's own goroutine is the only one that suspends it: another goroutine's Suspend of a context that carries no
// handler, or of a signal bound to no coroutine, is not refused.
func TestParkingSuspendOfAContextWithoutAHandlerOrOfAnUnboundSignalDoesNothing(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		chordctx.Suspend(background)
		signal := parking.New()
		chordctx.Suspend(parkingCarrier{Context: background, signal: signal})
		if !signal.Raised() {
			t.Error("a Suspend of an unbound signal from another goroutine did not raise it")
		}
	}()
	<-done
}

type parkingCarrier struct {
	context.Context
	signal *parking.Signal
}

func (c parkingCarrier) Value(key any) any {
	if key == parking.Key {
		return c.signal
	}
	return c.Context.Value(key)
}

// Lend: what ends its goroutine inside a section, parked or not, leaves the section's goroutine and the loop as they were,
// and abandoned runs once, on the handler's goroutine.
func TestLoopLendRunsAbandonedForAHandlerThatEndsItsGoroutine(t *testing.T) {
	for _, parked := range []bool{false, true} {
		for _, inSection := range []bool{true, false} {
			loop := NewLoop()
			abandonedRuns := 0
			survived := false
			run := func() {
				signal := parking.New()
				loop.Lend(signal, func() {
					if parked {
						signal.Raise()
					}
					runtime.Goexit()
				}, func() { abandonedRuns++ })
				survived = true
			}
			ended := make(chan struct{})
			go func() {
				defer close(ended)
				if inSection {
					loop.Run(run)
				} else {
					run()
				}
			}()
			<-ended
			loop.Wait()
			if !survived {
				t.Fatalf("parked %v, in a section %v: the goroutine that lent the section ended with the handler's", parked, inSection)
			}
			if abandonedRuns != 1 {
				t.Fatalf("parked %v, in a section %v: abandoned ran %d times, want once", parked, inSection, abandonedRuns)
			}
			// The loop is free: a section runs, and a handler lent after the Goexit runs as any.
			loop.Run(func() {
				signal := parking.New()
				ran := false
				loop.Lend(signal, func() { ran = true; signal.Return() }, nil)
				if !ran {
					t.Error("a handler lent after the Goexit did not run")
				}
			})
			loop.Wait()
		}
	}
}
