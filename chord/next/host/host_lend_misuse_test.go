package host

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/internal/parking"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// A method handler is an async function in Pi: one that throws rejects its call, and one that never resolves leaves the call
// pending until it aborts, with the host going on in both. A Go handler is a coroutine of the section that admits its call
// (Loop.Lend), so the way it can leave that Pi has no spelling of is tested here: ending its goroutine (runtime.Goexit, which
// testing.T.FailNow does) is a throw. What a goroutine it started waits for is not part of its synchronous part, as an
// `await` in Pi's unawaited async work is not.

var lendDesk = mustContract(declareLocal("app.lend-desk", contracts.LocalDeclaration{Params: contracts.Params{},
	Version: 1,
	Members: []contracts.LocalEntry{
		contracts.Mem("exit", contracts.NewMethod()),
		contracts.Mem("exitParked", contracts.NewMethod()),
		contracts.Mem("startedSuspend", contracts.NewMethod(contracts.TSchema{}, contracts.Str())),
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
				// One whose goroutine suspends: the handler reads, before its own first wait, whether that ended its synchronous
				// part, and what the goroutine's Suspend did.
				"startedSuspend": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					outcome := make(chan any, 1)
					go func() {
						defer func() { outcome <- recover() }()
						chordctx.Suspend(ctx)
					}()
					// A raw receive: the goroutine needs nothing of the host, and the section is still the handler's.
					panicked := <-outcome
					raised := ctx.Value(parking.Key).(*parking.Signal).Raised()
					return fmt.Sprintf("raised %v, panic %v", raised, panicked), nil
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

// An `await` inside async work that Pi's handler started and did not await does not end the handler's synchronous part: the
// handler goes on to its own next wait. A Suspend, as any wait, on a goroutine the handler started is that await: it neither
// ends the handler's synchronous part nor fails.
func TestChordctxSuspendOnAGoroutineTheHandlerStartedLeavesTheHandlersSynchronousPartAlone(t *testing.T) {
	ref := lendDeskRef(t)
	got, err := ref.Call("startedSuspend", nil, background)
	if err != nil {
		t.Fatal(err)
	}
	if got != "raised false, panic <nil>" {
		t.Fatalf("a Suspend from a goroutine the handler started: %v, want its synchronous part not ended and no panic", got)
	}
}

// Lend: what ends its goroutine inside a section, parked or not, leaves the section's goroutine and the loop as they were,
// and fn is called once more with abandoned true, on the handler's goroutine.
func TestLoopLendRunsAbandonedForAHandlerThatEndsItsGoroutine(t *testing.T) {
	for _, parked := range []bool{false, true} {
		for _, inSection := range []bool{true, false} {
			loop := NewLoop()
			abandonedRuns := 0
			survived := false
			run := func() {
				signal := parking.New()
				loop.Lend(signal, func(abandoned bool) {
					if abandoned {
						abandonedRuns++
						return
					}
					if parked {
						signal.Raise()
					}
					runtime.Goexit()
				})
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
				loop.Lend(signal, func(bool) { ran = true; signal.Return() })
				if !ran {
					t.Error("a handler lent after the Goexit did not run")
				}
			})
			loop.Wait()
		}
	}
}

// The goroutine of a coroutine whose handler ended it is still ending when the loop drops the coroutine: stopping it lets it
// finish, and iter.Pull reports that to whoever stops it as a Goexit. That must be a task of the loop's own, and never the
// runtime's cleanup goroutine (which would end, and stop every later cleanup of the process), so the cleanup of a spent
// coroutine is cancelled and nothing stops it twice. A loop's coroutines are dropped and collected between the handlers
// that end their goroutines; afterwards the process's cleanups still run and no goroutine is left.
func TestLoopLendStopsTheSpentCoroutineOfAHandlerThatEndedItsGoroutineOnATaskOfItsOwn(t *testing.T) {
	for range 32 {
		var done sync.WaitGroup
		done.Go(func() { goroutineid.Current() })
		done.Wait()
	}
	before := runtime.NumGoroutine()
	survived := 0
	for range 64 {
		loop := NewLoop()
		var done sync.WaitGroup
		done.Go(func() {
			loop.Run(func() {
				signal := parking.New()
				loop.Lend(signal, func(abandoned bool) {
					if !abandoned {
						runtime.Goexit()
					}
				})
			})
			survived++
		})
		done.Wait()
		loop.Wait()
		runtime.GC()
	}
	if survived != 64 {
		t.Fatalf("%d of 64 goroutines that lent their section survived the handler's Goexit", survived)
	}
	// The runtime's cleanup goroutine is alive: a cleanup of an object dropped now runs.
	fired := make(chan struct{})
	func() {
		sentinel := new([16]byte)
		runtime.AddCleanup(sentinel, func(struct{}) { close(fired) }, struct{}{})
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		runtime.GC()
		select {
		case <-fired:
		default:
			if time.Now().After(deadline) {
				t.Fatal("a cleanup of a dropped object never ran: the runtime's cleanup goroutine ended")
			}
			time.Sleep(time.Millisecond)
			continue
		}
		break
	}
	for range 200 {
		if runtime.NumGoroutine() <= before+2 {
			return
		}
		runtime.GC()
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%d goroutines after the spent coroutines were stopped, %d before", runtime.NumGoroutine(), before)
}
