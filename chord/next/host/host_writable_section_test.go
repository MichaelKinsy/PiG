package host

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Own test (no Pi file): Pi's `Writable.set` runs on the single event loop, as every continuation of an `await` does; a Go
// native's opener runs off the loop, so `Set` from there enters a section of its owner's host (state.SectionOwner), and
// from a section it nests. The derive reading the writable computes in the commit `Set` makes: it must run on the loop.

var writableSectionContract = mustContract(contracts.Local("app.writable-section", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
}))

func TestWritableSetOffTheLoopRunsItsCommitOnTheLoop(t *testing.T) {
	var (
		mu       sync.Mutex
		onLoop   = map[float64]bool{}
		writable state.Writable[any]
		host     Host
	)
	h := newHost(t, HostOptions{OnError: func(err HostError) { t.Errorf("onError %s", err.Kind) }})
	host = h
	loop := h.(*chordHost).events.(*Loop)
	mustLoad(t)(h.Load([]HostFacet{{Name: "@test/writable-section", Facet: DefineFacet(func(fh FacetHost, _ FacetSelf) error {
		return fh.Serve(writableSectionContract, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			writable = self.Writable(0.0)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"n": self.Derive([]any{writable}, func(values []any) (delta.JsonValue, error) {
						n := values[0].(float64)
						mu.Lock()
						onLoop[n] = loop.onLoop()
						mu.Unlock()
						return n, nil
					}),
				},
			}}, nil
		}, ServeOptions{})
	})}}, background))
	object, err := host.Get(background, writableSectionContract, jsonObject(), GetOptions{})
	must(t, err)
	watch, err := object.Member("n").Watch(background)
	must(t, err)
	watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error { return nil })
	t.Cleanup(func() { _, _ = watch.Stop() })

	const sets = 50
	var wg sync.WaitGroup
	for i := 1; i <= sets; i++ {
		wg.Go(func() {
			// The test's goroutine is in no section, as a native's opener.
			must(t, writable.Set(float64(i)))
		})
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	for n, ran := range onLoop {
		if n != 0 && !ran {
			t.Errorf("the derive computed %v off the loop: Set did not enter a section", n)
		}
	}
	if len(onLoop) < 2 {
		t.Fatalf("the derive computed %d values, want more than the initial one", len(onLoop))
	}
}

// A Set from inside a section (a handler, a derive's owner code) does not enter another one: the Get below calls a
// method that sets, as the sessions server's handlers do, and the set value reaches the member.
func TestWritableSetInASectionNests(t *testing.T) {
	h := newHost(t, HostOptions{OnError: func(err HostError) { t.Errorf("onError %s", err.Kind) }})
	nested := mustContract(contracts.Local("app.writable-nested", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int()), contracts.Mem("set", contracts.NewMethod(contracts.Int()))},
	}))
	mustLoad(t)(h.Load([]HostFacet{{Name: "@test/writable-nested", Facet: DefineFacet(func(fh FacetHost, _ FacetSelf) error {
		return fh.Serve(nested, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			writable := self.Writable(0.0)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"n": self.Derive([]any{writable}, func(values []any) (delta.JsonValue, error) { return values[0].(float64), nil }),
				},
				Methods: map[string]contracts.MethodFunction{
					"set": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
						return nil, writable.Set(args.(float64))
					},
				},
			}}, nil
		}, ServeOptions{})
	})}}, background))
	object, err := h.Get(background, nested, jsonObject(), GetOptions{})
	must(t, err)
	_, err = object.Call("set", 7.0, background)
	must(t, err)
	got, err := object.Member("n").Value()
	must(t, err)
	if got != 7.0 {
		t.Fatalf("n = %v, want 7", got)
	}
}
