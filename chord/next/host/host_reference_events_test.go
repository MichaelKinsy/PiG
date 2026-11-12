package host

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"testing"
	"weak"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/test/host/reference-events.test.ts (Pi 8d30e6306): references' event members (subscriptions
// installed at once, rejected for a released reference or an aborted context, moving with their reference to a new
// instance; members re-exposed by another object, value and stamp unchanged, across their source's restart).

func tickerFacets(probe *tickerProbe) []HostFacet {
	return []HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return serveTickers(h, probe) })}}
}

func (p *tickerProbe) sourceCount() int { p.mu.Lock(); defer p.mu.Unlock(); return len(p.sources) }
func (p *tickerProbe) namedCount() int  { p.mu.Lock(); defer p.mu.Unlock(); return len(p.named) }

func TestReferenceEventSubscriptionsAreInstalledAtOnceWhileLoadingAndReceiveOnceTheirReferenceIsLive(t *testing.T) {
	k := ticking(t, loadedOptions{})
	var named *Reference
	var seen []any
	// One turn: the object's activation runs after it, so the reference is loading while it subscribes.
	hostTurn(k.Host, func() {
		var err error
		named, err = k.Host.Hold(background, NamedTicker, jsonObject("name", "a"), GetOptions{})
		must(t, err)
		on(t, named, "ticks", recorder(&seen, ""), background)
		if kind := named.State().Value().Kind; kind != RefStateLoading {
			t.Errorf("state = %v, want loading", kind)
		}
	})
	waitLive(t, named)
	hostTurn(k.Host, func() { _ = k.Probe.lastNamed().Emit(sample(1)) })
	flushed(k.Host)
	wantSeen(t, seen, 1.0)
}

func TestReferenceEventSubscriptionsRejectAnAbortedContextsReasonAndAReleasedReferencesReleasedInstallingNothing(t *testing.T) {
	k := ticking(t, loadedOptions{})
	ticker := mustGet(t, k.Host, Ticker, jsonObject())
	ctx, cancel := context.WithCancelCause(background)
	cancel(errors.New("gone"))
	_, err := ticker.Events("ticks").On(func(contracts.EventDelivery[delta.JsonValue]) {}, ctx)
	if err == nil || err.Error() != "gone" {
		t.Fatalf("err = %v, want gone", err)
	}
	ticker.Release()
	_, err = ticker.Events("ticks").On(func(contracts.EventDelivery[delta.JsonValue]) {}, background)
	var chordErr *data.ChordError
	if !errors.As(err, &chordErr) || chordErr.Code != data.CodeReleased {
		t.Fatalf("err = %#v, want released", err)
	}
}

func TestReferenceEventSubscriptionsEndWithTheirContextsAbort(t *testing.T) {
	k := ticking(t, loadedOptions{})
	ticker := mustGet(t, k.Host, Ticker, jsonObject())
	var seen []any
	ctx, cancel := context.WithCancel(background)
	defer cancel()
	on(t, ticker, "ticks", recorder(&seen, ""), ctx)
	hostTurn(k.Host, func() {
		_ = k.Source().Emit(sample(1))
		cancel()
	})
	flushed(k.Host)
	wantSeen(t, seen)
}

func TestReferenceEventSubscriptionsMoveWithTheirReferenceToARestartsNewInstanceAReloadNothingMissedIsReplayed(t *testing.T) {
	probe := &tickerProbe{}
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return serveTickers(h, probe) }, loadedOptions{})
	ticker := mustGet(t, host, Ticker, jsonObject())
	var seen []any
	on(t, ticker, "ticks", recorder(&seen, ""), background)
	hostTurn(host, func() { _ = probe.lastSource().Emit(sample(1)) })
	flushed(host)
	_, err := host.Reload(tickerFacets(probe), background, ReloadOptions{})
	must(t, err)
	// Staging ran the new factory against a recording self (an inert source), then the successor ran it.
	until(t, "the successor's source", func() bool { return probe.sourceCount() == 3 })
	waitLive(t, ticker)
	hostTurn(host, func() { _ = probe.lastSource().Emit(sample(2)) })
	flushed(host)
	wantSeen(t, seen, 1.0, 2.0)
}

var pulse = mustContract(contracts.Local("app.pulse", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("beats", contracts.Event(Sample)),
		contracts.Mem("slow", contracts.NewMethod()),
	},
}))

func TestReferenceEventSubscriptionsKeepGettingAnOldAttemptsEmitsWhileAReloadDrainsItThenTheSuccessors(t *testing.T) {
	var mu sync.Mutex
	var sources []contracts.EventSource[delta.JsonValue]
	open := make(chan struct{})
	facets := func() []HostFacet {
		return []HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(pulse, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				beats := self.Events()
				mu.Lock()
				sources = append(sources, beats)
				mu.Unlock()
				return contracts.Implementation{Members: contracts.VariantImplementation{
					Events: map[string]contracts.EventMember[delta.JsonValue]{"beats": beats},
					Methods: map[string]contracts.MethodFunction{
						"slow": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
							if _, err := chordctx.Receive(ctx, open); err != nil {
								return nil, err
							}
							return nil, beats.Emit(sample(1))
						},
					},
				}}, nil
			}, ServeOptions{})
		})}}
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	_, err := host.Load(facets(), background)
	must(t, err)
	last := func() contracts.EventSource[delta.JsonValue] {
		mu.Lock()
		defer mu.Unlock()
		return sources[len(sources)-1]
	}
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(sources) }
	ref := mustGet(t, host, pulse, jsonObject())
	var seen []any
	on(t, ref, "beats", recorder(&seen, ""), background)
	// Old work: a call running on the old attempt when the reload commits; the drain waits for it.
	slow := make(chan error, 1)
	go func() { _, err := ref.Call("slow", nil, background); slow <- err }()
	flushed(host)
	reloaded := make(chan error, 1)
	go func() { _, err := host.Reload(facets(), background, ReloadOptions{}); reloaded <- err }()
	until(t, "the staged factory", func() bool { return count() >= 2 })
	flushed(host)
	if kind := ref.State().Value().Kind; kind != RefStateLive {
		t.Fatalf("state = %v, want live", kind)
	}
	close(open)
	must(t, <-slow)
	flushed(host)
	wantSeen(t, seen, 1.0)
	must(t, <-reloaded)
	waitLive(t, ref)
	hostTurn(host, func() { _ = last().Emit(sample(2)) })
	flushed(host)
	wantSeen(t, seen, 1.0, 2.0)
	must(t, host.Close(background))
}

// No Pi test pins `removed()` unbinding a binding's event taps (mutant N of the port's mutation run). An object that
// leaves its reference unbound in the turn of an emit (the flush runs after the turn): the reference is bound to nothing
// at the flush, so its subscription gets nothing of that emit. (A reload's removal seals the old attempt, which then
// refuses Emit, so only a same-turn emit can reach the flush.)
func TestReferenceEventSubscriptionsGetNothingOfAnEmitInTheTurnTheirReferenceWasUnbound(t *testing.T) {
	var mu sync.Mutex
	var sources []contracts.EventSource[delta.JsonValue]
	facets := []HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(pulse, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			beats := self.Events()
			mu.Lock()
			sources = append(sources, beats)
			mu.Unlock()
			return contracts.Implementation{Members: contracts.VariantImplementation{
				Events: map[string]contracts.EventMember[delta.JsonValue]{"beats": beats},
				Methods: map[string]contracts.MethodFunction{
					"slow": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
				},
			}}, nil
		}, ServeOptions{})
	})}}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	_, err := host.Load(facets, background)
	must(t, err)
	ref := mustGet(t, host, pulse, jsonObject())
	var seen []any
	on(t, ref, "beats", recorder(&seen, ""), background)
	flushed(host)
	hostTurn(host, func() {
		mu.Lock()
		beats := sources[0]
		mu.Unlock()
		must(t, beats.Emit(sample(1)))
		objects := host.(*chordHost).objectList()
		if len(objects) != 1 {
			t.Fatalf("objects = %d, want 1", len(objects))
		}
		objects[0].unbound(ref.binding, notServedHere(objects[0].served.contract, 1))
	})
	flushed(host)
	wantSeen(t, seen)
	must(t, host.Close(background))
}

// --- re-exposed members --------------------------------------------------------------------------------------------

func TestReferenceEventReexposedMembersForwardEachEventWithItsValueAndStampUnchanged(t *testing.T) {
	k := ticking(t, loadedOptions{})
	view := mustGet(t, k.Host, TickerView, jsonObject("name", "a"))
	var seen, own []contracts.EventDelivery[delta.JsonValue]
	on(t, view, "ticks", func(e contracts.EventDelivery[delta.JsonValue]) { seen = append(seen, e) }, background)
	source := k.Probe.lastNamed()
	var subscription contracts.EventSubscription
	hostTurn(k.Host, func() {
		var err error
		subscription, err = source.On(func(e contracts.EventDelivery[delta.JsonValue]) { own = append(own, e) }, background)
		must(t, err)
	})
	_ = subscription
	hostTurn(k.Host, func() { _ = source.Emit(sample(5)) })
	flushed(k.Host)
	if !reflect.DeepEqual(seen, own) || len(seen) != 1 {
		t.Fatalf("seen = %+v, own = %+v", seen, own)
	}
}

func TestReferenceEventReexposedMembersFollowTheirSourcesNewInstanceWithoutTheViewRestarting(t *testing.T) {
	k := ticking(t, loadedOptions{})
	view := mustGet(t, k.Host, TickerView, jsonObject("name", "a"))
	var seen []any
	on(t, view, "ticks", recorder(&seen, ""), background)
	named := mustGet(t, k.Host, NamedTicker, jsonObject("name", "a"))
	failLiveOf(k.Probe.lastNamedSelf())
	named.Retry()
	until(t, "the second named attempt", func() bool { return k.Probe.namedCount() == 2 })
	waitLive(t, named)
	hostTurn(k.Host, func() { _ = k.Probe.lastNamed().Emit(sample(3)) })
	flushed(k.Host)
	wantSeen(t, seen, 3.0)
}

// --- through failures and reloads ----------------------------------------------------------------------------------

func TestReferenceEventSubscriptionsOpenWhileTheObjectIsFailedAndReceiveOnceARetryIsLive(t *testing.T) {
	k := ticking(t, loadedOptions{})
	named := mustGet(t, k.Host, NamedTicker, jsonObject("name", "a"))
	failLiveOf(k.Probe.lastNamedSelf())
	untilKind(t, named, RefStateFailed)
	var seen []any
	on(t, named, "ticks", recorder(&seen, ""), background)
	named.Retry()
	until(t, "the second named attempt", func() bool { return k.Probe.namedCount() == 2 })
	waitLive(t, named)
	hostTurn(k.Host, func() { _ = k.Probe.lastNamed().Emit(sample(7)) })
	flushed(k.Host)
	wantSeen(t, seen, 7.0)
}

var (
	reloadSample = contracts.Obj(contracts.Prop("y", contracts.Num()))
	sourceV1     = mustContract(contracts.Local("app.source", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("ticks", contracts.Event(reloadSample))},
	}))
	timedSample = contracts.Obj(contracts.Prop("y", contracts.Num()), contracts.Prop("t", contracts.Num()))
	sourceV2    = func() *contracts.Contract {
		evolved, err := contracts.Evolve(sourceV1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.Event("ticks", timedSample) })
		if err != nil {
			panic(err)
		}
		return evolved
	}()
	viewer = mustContract(contracts.Local("app.viewer", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("ticks", contracts.Event(timedSample))},
	}))
)

func TestReferenceEventSubscriptionsKeepNoNodeOfAnInstanceAReloadReplacedWhileReferencesSurvive(t *testing.T) {
	var mu sync.Mutex
	var sources []contracts.EventSource[delta.JsonValue]
	var sourceSelves, viewerSelves []Self
	facets := func() []HostFacet {
		return []HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			err := h.Serve(sourceV2, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				ticks := self.Events()
				mu.Lock()
				sources = append(sources, ticks)
				sourceSelves = append(sourceSelves, self)
				mu.Unlock()
				return contracts.Implementation{Members: contracts.VariantImplementation{
					Events: map[string]contracts.EventMember[delta.JsonValue]{"ticks": ticks},
				}}, nil
			}, ServeOptions{Accepts: contracts.AcceptsTable{{Name: "1", Value: contracts.VersionMappings{
				Events: []contracts.Entry[contracts.EventMapping]{{Name: "ticks", Value: contracts.EventMapping{
					Func: func(current delta.JsonValue) (delta.JsonValue, error) {
						return jsonObject("y", current.(*delta.JsonObject).Value("y")), nil
					},
				}}},
			}}}})
			if err != nil {
				return err
			}
			return h.Serve(viewer, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				mu.Lock()
				viewerSelves = append(viewerSelves, self)
				mu.Unlock()
				return contracts.Implementation{Members: contracts.VariantImplementation{
					Events: map[string]contracts.EventMember[delta.JsonValue]{"ticks": self.Get(sourceV2, jsonObject()).Events("ticks")},
				}}, nil
			}, ServeOptions{})
		})}}
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	_, err := host.Load(facets(), background)
	must(t, err)
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(sources) }
	older := mustGet(t, host, sourceV1, jsonObject())
	view := mustGet(t, host, viewer, jsonObject())
	var seen []string
	var seenMu sync.Mutex
	push := func(prefix string) func(contracts.EventDelivery[delta.JsonValue]) {
		return func(e contracts.EventDelivery[delta.JsonValue]) {
			seenMu.Lock()
			defer seenMu.Unlock()
			seen = append(seen, prefix+" "+strconv.FormatFloat(e.Value.(*delta.JsonObject).Value("y").(float64), 'f', -1, 64))
		}
	}
	on(t, older, "ticks", push("old"), background)
	on(t, view, "ticks", push("view"), background)
	mu.Lock()
	source, sourceSelf, viewerSelf := sources[len(sources)-1], sourceSelves[len(sourceSelves)-1], viewerSelves[len(viewerSelves)-1]
	mu.Unlock()
	hostTurn(host, func() { _ = source.Emit(jsonObject("y", 1.0, "t", 0.0)) })
	flushed(host)
	// The nodes delivering: the instance's own source, the viewer's re-exposure, the older version's mapping.
	own := weak.Make(source.(*ownEventSource))
	reexposed := weak.Make(viewerSelf.(*attempt).eventOf("ticks"))
	mapped := weak.Make(sourceSelf.(*attempt).adaptedAt(1).event("ticks"))
	if own.Value() == nil || reexposed.Value() == nil || mapped.Value() == nil {
		t.Fatal("a delivering node is missing")
	}
	source = nil
	mu.Lock()
	sources, sourceSelves, viewerSelves = nil, nil, nil
	mu.Unlock()
	_, err = host.Reload(facets(), background, ReloadOptions{})
	must(t, err)
	// Staging ran the new factory against a recording self (an inert source), then the successor ran it.
	until(t, "the successor's source", func() bool { return count() == 2 })
	waitLive(t, older)
	waitLive(t, view)
	expectCollected(t, own)
	expectCollected(t, reexposed)
	expectCollected(t, mapped)
	mu.Lock()
	next := sources[len(sources)-1]
	mu.Unlock()
	hostTurn(host, func() { _ = next.Emit(jsonObject("y", 2.0, "t", 0.0)) })
	flushed(host)
	seenMu.Lock()
	got := append([]string(nil), seen...)
	seenMu.Unlock()
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"old 1", "old 2", "view 1", "view 2"}) {
		t.Fatalf("seen = %v", got)
	}
	must(t, host.Close(background))
}
