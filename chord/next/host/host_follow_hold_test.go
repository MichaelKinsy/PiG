package host

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Own test: a section of the host (chordHost.enter) holds the host's delivery
// scheduler, so what two commits of one section change reaches a watch of the followed member as one call, the frames of
// the section folded as the frames of one publication fold into the single microtask upstream queues for it. Without the
// hold the first commit's frame is delivered while the section still runs.
func TestFollowObserverSectionHoldsTheDeliverySchedulerSoTwoCommitsOfOneSectionAreOneCall(t *testing.T) {
	typ := exLocal("app.held", contracts.Params{}, contracts.Mem("n", contracts.Int()))
	observed := make(chan FollowObserver[any], 1)
	host, _ := rvPlugins(t, HostOptions{}, rvPlugin("@app/held", func(h FacetHost, _ FacetSelf) error {
		return h.Serve(typ, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"n": self.Follow(func(observer FollowObserver[any], _ context.Context) (*FollowAttachment[delta.JsonValue], error) {
					observed <- observer
					return &FollowAttachment[delta.JsonValue]{Value: memberCount(0), Detach: func() {}}, nil
				}),
			}, nil), nil
		}, ServeOptions{})
	}))
	reference, err := host.Hold(background, typ, delta.JsonObjectOf(), GetOptions{})
	must(t, err)
	observer := <-observed
	source := observer.(followObserverOf).source
	watch, err := reference.Member("n").Watch(background)
	must(t, err)
	var mu sync.Mutex
	var calls []float64
	first := make(chan struct{}, 8)
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		mu.Lock()
		calls = append(calls, memberCountOf(call.Value))
		mu.Unlock()
		first <- struct{}{}
		return nil
	})
	h := host.(*chordHost)
	h.scheduler.Wait()
	for len(first) > 0 {
		<-first
	}
	commit := func(n float64) {
		source.advance(chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallValue, Value: memberCount(n), Ops: []delta.Op{{"s", delta.Path{"n"}, n}}})
		must(t, h.batch.Commit(func() error { source.applyQueued(); return nil }, background))
	}
	h.enter(func() {
		commit(1)
		select {
		case <-first:
			t.Error("the first commit of the section was delivered while the section ran: the section does not hold the scheduler")
		case <-time.After(300 * time.Millisecond):
		}
		commit(2)
	})
	h.scheduler.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(calls) == 0 || calls[len(calls)-1] != 2 {
		t.Fatalf("calls = %v, want the last to be the section's 2", calls)
	}
	for _, n := range calls {
		if n == 1 {
			t.Fatalf("calls = %v: the first commit of the section reached the watch alone", calls)
		}
	}
	must(t, host.Close(background))
}

// sectionRecorder is a loop that records the sections it runs, in order, under its name.
type sectionRecorder struct {
	*Loop
	name     string
	sections *[]string
}

func (r sectionRecorder) Run(work func()) {
	*r.sections = append(*r.sections, r.name)
	r.Loop.Run(work)
}

// Own test: a publication step hands each host's followed calls to a section of that host, once: the calls of two sources of
// one loop enter one section (not one each), and the source of another loop gets its own. Nothing enters before the step ends.
func TestPublicationStepFlushesEachHostsFollowedCallsInOneSectionOfThatHost(t *testing.T) {
	var sections []string
	loopA := sectionRecorder{Loop: NewLoop(), name: "A", sections: &sections}
	loopB := sectionRecorder{Loop: NewLoop(), name: "B", sections: &sections}
	a1 := &followSource{loop: loopA, state: followState{kind: followDetached}}
	b := &followSource{loop: loopB, state: followState{kind: followDetached}}
	a2 := &followSource{loop: loopA, state: followState{kind: followDetached}}
	ctx, end := chordctx.WithStep(context.Background())
	call := chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallValue}
	for _, source := range []*followSource{a1, b, a2} {
		followObserverOf{source}.Advance(call, ctx)
	}
	if len(sections) != 0 {
		t.Fatalf("sections %v entered before the step ended", sections)
	}
	end()
	if want := []string{"A", "B"}; !reflect.DeepEqual(sections, want) {
		t.Fatalf("sections %v, want %v: one section per host", sections, want)
	}
}
