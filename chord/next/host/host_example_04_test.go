package host

// pi: packages/chord/test/next/examples/04-watches.ts

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
)

// --- 04-watches.ts -------------------------------------------------------------------------------------------------

func TestExample04Watches(t *testing.T) {
	countDoc := exDefineState("count", documents.StateOptions{
		Schema:  exObj(exProp("n", exInt())),
		Initial: func() *delta.JsonObject { return jsonObject("n", float64(0)) },
	})
	counter := exLocal("example.counter", contracts.Params{},
		contracts.Mem("count", exObj(exProp("n", exInt()))), contracts.Mem("double", exInt()), contracts.Mem("half", exInt()),
		contracts.Mem("bump", contracts.NewMethod()), contracts.Mem("reset", contracts.NewMethod()))
	var reported exCollector
	errs := &hostErrors{}
	host := newHost(t, HostOptions{OnError: func(err HostError) {
		if err.Kind == ErrDeriveFailed {
			reported.add(fmt.Sprintf("reported: %s.%s: %v", err.Object, err.Member, err.Error))
			return
		}
		errs.push(err)
	}})
	mustLoad(t)(host.Load([]HostFacet{{Name: "@example/counter", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(counter, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			count := stateOf(self, countDoc)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"count": count.Member(),
				"double": self.Derive([]any{count.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("n").(float64) * 2, nil
				}),
				"half": self.Derive([]any{count.s}, func(values []any) (delta.JsonValue, error) {
					n := values[0].(*delta.JsonObject).Value("n").(float64)
					if int(n)%2 == 1 {
						return nil, fmt.Errorf("%d has no half", int(n))
					}
					return n / 2, nil
				}),
			}, map[string]contracts.MethodFunction{
				"bump": exMethod(func(_ *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return nil, count.Change(func(draft *delta.Object) { _ = draft.Set("n", draft.Get("n").(float64)+1) }, ctx)
				}),
				"reset": exMethod(func(_ *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return nil, count.Retire(ctx)
				}),
			}), nil
		}, ServeOptions{})
	})}}, background))
	counterRef := exGet(t, host, counter, jsonObject())
	// A watch holds the value at acquisition; a listener that keeps up gets one call per commit.
	watch, countFrames := exWatch(t, background, counterRef.Member("count"))
	exEqual(t, "acquired", watch.Value(), exJSON(t, `{"n":0}`))
	cancellable, cancel := chordctx.WithCancel(background)
	doubled, doubleFrames := exWatch(t, cancellable, counterRef.Member("double"))
	half, halfFrames := exWatch(t, background, counterRef.Member("half"))
	// A slow listener: commits made while it is busy fold into its next call.
	slow, err := counterRef.Member("count").Watch(background)
	must(t, err)
	slowStarted := make(chan struct{}, 1)
	slowRelease := make(chan struct{})
	var slowFrames exFrames
	slow.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind != chord.WatchCallValue {
			return nil
		}
		slowFrames.mu.Lock()
		slowFrames.frames = append(slowFrames.frames, exFrame{Kind: "value", Value: call.Value, Ops: call.Ops})
		first := len(slowFrames.frames) == 1
		slowFrames.mu.Unlock()
		if first {
			slowStarted <- struct{}{}
			<-slowRelease
		}
		return nil
	})
	// Pi's awaits and timers (`setTimeout` 0, then 50 ms) let a listener that keeps up take each commit before the next, and let
	// the slow listener's call finish before the reset. Here the watches deliver on goroutines of their own, so each commit
	// waits for the listeners that keep up to have taken it, and the reset for the slow listener's folded call.
	caughtUp := func(n int) {
		scopeEventually(t, fmt.Sprintf("the listeners that keep up took commit %d", n), func() bool {
			return len(countFrames.all()) == n && len(doubleFrames.all()) == n && len(halfFrames.all()) == n
		})
	}
	exCall(t, counterRef, "bump", nil)
	<-slowStarted
	caughtUp(1)
	exCall(t, counterRef, "bump", nil)
	caughtUp(2)
	exCall(t, counterRef, "bump", nil)
	caughtUp(3)
	close(slowRelease)
	scopeEventually(t, "the slow listener's folded call", func() bool { return len(slowFrames.all()) >= 2 })
	// Retiring the singleton resets it to initial(): watches get a root replacement.
	exCall(t, counterRef, "reset", nil)
	scopeEventually(t, "the frames of the three bumps and the reset", func() bool {
		return len(countFrames.all()) == 4 && len(slowFrames.all()) >= 2 && len(doubleFrames.all()) >= 4
	})
	want := []string{
		`{"n":1} [["s",["n"],1]]`, `{"n":2} [["s",["n"],2]]`, `{"n":3} [["s",["n"],3]]`, `{"n":0} [["r",{"n":0}]]`,
	}
	got := []string{}
	for _, frame := range countFrames.all() {
		got = append(got, exText(frame.Value)+" "+exText(frame.Ops))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("count frames = %v, want %v", got, want)
	}
	// The slow listener's second call folds the commits made while it was busy: the newest value, one op on one path.
	slowAll := slowFrames.all()
	exEqual(t, "slow first frame", slowAll[0].Value, exJSON(t, `{"n":1}`))
	exEqual(t, "slow folded frame", slowAll[1].Value, exJSON(t, `{"n":3}`))
	exEqual(t, "slow folded ops", slowAll[1].Ops, exJSON(t, `[["s",["n"],3]]`))
	// A failing derive: one `failed` call per failure, the watch stays open; the next value replaces the root.
	scopeEventually(t, "the half frames", func() bool { return len(halfFrames.all()) >= 4 })
	var halfKinds []string
	for _, frame := range halfFrames.all() {
		if frame.Kind == string(chord.WatchCallFailed) {
			halfKinds = append(halfKinds, "failed: "+frame.Error.Error())
		} else {
			halfKinds = append(halfKinds, "frame "+exText(frame.Value))
		}
	}
	wantHalf := []string{"failed: 1 has no half", "frame 1", "failed: 3 has no half", "frame 0"}
	if !reflect.DeepEqual(halfKinds, wantHalf) {
		t.Fatalf("half calls = %v, want %v", halfKinds, wantHalf)
	}
	if got := reported.all(); !reflect.DeepEqual(got, []string{
		"reported: example.counter.half: 1 has no half", "reported: example.counter.half: 3 has no half",
	}) {
		t.Fatalf("derive_failed reports = %v", got)
	}
	// Cancelling the watch's context ends it; releasing the reference ends the watches taken through it.
	cancel(nil)
	<-doubled.Closed()
	counterRef.Release()
	<-watch.Closed()
	_ = half
	_ = slow
	exCloseHost(t, host)
	if errs.Len() > 0 {
		t.Fatalf("background errors: %v", errs.All())
	}
}
