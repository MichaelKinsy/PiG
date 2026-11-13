package host

import (
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports the `events of an older consumer` describe of packages/chord/test/host/adapted.test.ts (Pi 8d30e6306).

var (
	adaptedSample = contracts.Obj(contracts.Prop("y", contracts.Int()))
	// adaptedTickerV1 has one event member; v2 adds a sample time to its values and a second member.
	adaptedTickerV1 = mustContract(contracts.Local("app.ticker", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("ticks", contracts.Event(adaptedSample))},
	}))
	adaptedTickerV2 = func() *contracts.Contract {
		evolved, err := contracts.Evolve(adaptedTickerV1, func(e *contracts.LocalEditor) *contracts.LocalEditor {
			return e.Event("ticks", contracts.Obj(contracts.Prop("y", contracts.Int()), contracts.Prop("t", contracts.Int()))).
				AddEvent("extra", adaptedSample)
		})
		if err != nil {
			panic(err)
		}
		return evolved
	}()
)

type adaptedTicker struct {
	Host    Host
	Errors  *hostErrors
	mu      sync.Mutex
	sources []contracts.EventSource[delta.JsonValue]
	selves  []Self
}

func (a *adaptedTicker) emit(y int) {
	a.mu.Lock()
	source := a.sources[len(a.sources)-1]
	a.mu.Unlock()
	hostTurn(a.Host, func() { _ = source.Emit(jsonObject("y", float64(y), "t", float64(y*10))) })
}

func (a *adaptedTicker) self() Self {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.selves[len(a.selves)-1]
}

// newAdaptedTicker serves v2 accepting v1 with `ticks` mapped by mapping, or refused when mapping is nil.
func newAdaptedTicker(t *testing.T, mapping contracts.Mapping) *adaptedTicker {
	t.Helper()
	made := &adaptedTicker{Errors: &hostErrors{}}
	made.Host = newHost(t, HostOptions{OnError: made.Errors.push})
	eventMapping := contracts.EventMapping{Func: mapping, Refuse: mapping == nil}
	_, err := made.Host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(adaptedTickerV2, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			ticks := self.Events()
			made.mu.Lock()
			made.sources = append(made.sources, ticks)
			made.selves = append(made.selves, self)
			made.mu.Unlock()
			return contracts.Implementation{Members: contracts.VariantImplementation{
				Events: map[string]contracts.EventMember[delta.JsonValue]{"ticks": ticks, "extra": self.Events()},
			}}, nil
		}, ServeOptions{Accepts: contracts.AcceptsTable{{Name: "1", Value: contracts.VersionMappings{
			Events: []contracts.Entry[contracts.EventMapping]{{Name: "ticks", Value: eventMapping}},
		}}}})
	})}}, background)
	must(t, err)
	return made
}

func yOnly(current delta.JsonValue) (delta.JsonValue, error) {
	return jsonObject("y", current.(*delta.JsonObject).Value("y")), nil
}

func pair(value delta.JsonValue) string {
	object := value.(*delta.JsonObject)
	text := strconv.FormatFloat(object.Value("y").(float64), 'f', -1, 64)
	if t, ok := object.Value("t").(float64); ok {
		text += "," + strconv.FormatFloat(t, 'f', -1, 64)
	}
	return text
}

func TestAdaptedEventsGetEachValueMappedByTheirVersionsMappingOncePerEmitForAllOfThem(t *testing.T) {
	var calls atomic.Int64
	k := newAdaptedTicker(t, func(current delta.JsonValue) (delta.JsonValue, error) {
		calls.Add(1)
		return yOnly(current)
	})
	var seen []any
	record := func(e contracts.EventDelivery[delta.JsonValue]) { seen = append(seen, pair(e.Value)) }
	for range 3 {
		old := mustGet(t, k.Host, adaptedTickerV1, jsonObject())
		on(t, old, "ticks", record, background)
	}
	current := mustGet(t, k.Host, adaptedTickerV2, jsonObject())
	on(t, current, "ticks", record, background)
	k.emit(1)
	flushed(k.Host)
	wantSeen(t, seen, "1", "1", "1", "1,10")
	if calls.Load() != 1 {
		t.Fatalf("mapping calls = %d, want 1", calls.Load())
	}
}

func TestAdaptedEventsDropAnEmitOnlyForTheirVersionWhenTheMappingThrowsReportedOncePerFailureEpisodeTriedAgainNext(t *testing.T) {
	k := newAdaptedTicker(t, func(current delta.JsonValue) (delta.JsonValue, error) {
		y := current.(*delta.JsonObject).Value("y").(float64)
		if y < 0 {
			return nil, &mappingFailure{"no " + strconv.FormatFloat(y, 'f', -1, 64)}
		}
		return yOnly(current)
	})
	var old, current []any
	on(t, mustGet(t, k.Host, adaptedTickerV1, jsonObject()), "ticks", func(e contracts.EventDelivery[delta.JsonValue]) {
		old = append(old, e.Value.(*delta.JsonObject).Value("y"))
	}, background)
	on(t, mustGet(t, k.Host, adaptedTickerV2, jsonObject()), "ticks", func(e contracts.EventDelivery[delta.JsonValue]) {
		current = append(current, e.Value.(*delta.JsonObject).Value("y"))
	}, background)
	for _, y := range []int{-1, -2, 3, -4} {
		k.emit(y)
		flushed(k.Host)
	}
	wantSeen(t, old, 3.0)
	wantSeen(t, current, -1.0, -2.0, 3.0, -4.0)
	errs := k.Errors.All()
	if len(errs) != 2 {
		t.Fatalf("errors = %+v", errs)
	}
	for i, message := range []string{"no -1", "no -4"} {
		e := errs[i]
		if e.Kind != ErrEventMappingFailed || e.Member != "ticks" || e.Version != 1 || e.Error == nil || e.Error.Error() != message {
			t.Fatalf("error %d = %+v", i, e)
		}
	}
	if errs[0].Object != "app.ticker" {
		t.Fatalf("object = %q", errs[0].Object)
	}
}

type mappingFailure struct{ message string }

func (m *mappingFailure) Error() string { return m.message }

func TestAdaptedEventsAreRefusedTheirBindingWhenTheProviderRefusesAChangedEventMember(t *testing.T) {
	k := newAdaptedTicker(t, nil)
	timeout := 20 * time.Millisecond
	_, err := k.Host.Get(background, adaptedTickerV1, jsonObject(), GetOptions{ReadyTimeout: &timeout})
	var chordErr *data.ChordError
	if !errors.As(err, &chordErr) || chordErr.Code != data.CodeContractMismatch {
		t.Fatalf("err = %#v, want contract_mismatch", err)
	}
	if mustGet(t, k.Host, adaptedTickerV2, jsonObject()).Events("ticks") == nil {
		t.Fatal("the current version has no ticks")
	}
}

func TestAdaptedEventsBindWithARemovedEventMemberAndReceiveNoneOfItTheUnchangedOnesAsTheyAre(t *testing.T) {
	pulseV1 := mustContract(contracts.Local("app.pulse", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{
			contracts.Mem("beat", contracts.Event(adaptedSample)), contracts.Mem("old", contracts.Event(adaptedSample)),
		},
	}))
	pulseV2, err := contracts.Evolve(pulseV1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.RemoveEvent("old") })
	must(t, err)
	var mu sync.Mutex
	var beats []contracts.EventSource[delta.JsonValue]
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	_, err = host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(pulseV2, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			beat := self.Events()
			mu.Lock()
			beats = append(beats, beat)
			mu.Unlock()
			return contracts.Implementation{Members: contracts.VariantImplementation{
				Events: map[string]contracts.EventMember[delta.JsonValue]{"beat": beat},
			}}, nil
		}, ServeOptions{Accepts: contracts.AcceptsTable{{Name: "1", Value: contracts.VersionMappings{}}}})
	})}}, background)
	must(t, err)
	older := mustGet(t, host, pulseV1, jsonObject())
	var seen []any
	on(t, older, "beat", func(e contracts.EventDelivery[delta.JsonValue]) { seen = append(seen, "beat "+pair(e.Value)) }, background)
	on(t, older, "old", func(e contracts.EventDelivery[delta.JsonValue]) { seen = append(seen, "old "+pair(e.Value)) }, background)
	mu.Lock()
	beat := beats[len(beats)-1]
	mu.Unlock()
	hostTurn(host, func() { _ = beat.Emit(jsonObject("y", 1.0)) })
	flushed(host)
	wantSeen(t, seen, "beat 1")
	if state := older.State().Value(); state.Kind != RefStateLive {
		t.Fatalf("state = %+v", state)
	}
}

func TestAdaptedEventsKeepNoMappingOnceTheirVersionsLastReferenceLeftGC(t *testing.T) {
	k := newAdaptedTicker(t, yOnly)
	node := func() weak.Pointer[eventNode] {
		old := mustGet(t, k.Host, adaptedTickerV1, jsonObject())
		on(t, old, "ticks", func(contracts.EventDelivery[delta.JsonValue]) {}, background)
		k.emit(1)
		flushed(k.Host)
		mapped := k.self().(*attempt).adaptedAt(1).event("ticks")
		if mapped == nil {
			t.Fatal("no mapping was made")
		}
		made := weak.Make(mapped)
		old.Release()
		flushed(k.Host)
		return made
	}()
	expectCollected(t, node)
	k.emit(2)
	flushed(k.Host)
}
