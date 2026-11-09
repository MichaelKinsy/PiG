package host

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// pi: packages/chord/src/registries/value.ts
// pi: packages/chord/src/registries/contribution.ts
// pi: packages/chord/src/registries/record.ts
// pi: packages/chord/src/registries/internal.ts

// Ports the host-run cases of packages/chord/test/registries/value.test.ts: the readers of a registry (`self.registry`,
// `host.registry`) on a loaded host with plugins, which the record-level cases in registries/value_test.go cannot run:
// "older readers (S2)" (value mapping, a reader newer than the provider, a contributor that failed live, a native that
// closed), "readers (S3)", "readers' memory" and "values' memory".

// rvTotal is the composed value of the sum registry: `{ total }`.
type rvTotal struct{ total float64 }

func rvRegistry(t testing.TB, name string, version int) registries.Registry[any, any] {
	t.Helper()
	registry, err := registries.DefineRegistry[any, any](name, registries.DefineRegistryOptions{Version: version})
	must(t, err)
	return registry
}

var (
	// Sum is the provider's version 2: contributions are numbers, the value their sum; SumV1 is a reader built against
	// version 1 (a plain number) and SumV3 one built against a version the provider does not know yet.
	rvSum   = rvRegistryOf("app.sum", 2)
	rvSumV1 = rvRegistryOf("app.sum", 1)
	rvSumV3 = rvRegistryOf("app.sum", 3)

	rvSetterType = exLocal("app.setter", contracts.Params{}, contracts.Mem("set", contracts.NewMethod(contracts.Int())))
	rvReaderType = exLocal("app.reader", contracts.Params{}, contracts.Mem("total", contracts.Int()))
)

func rvRegistryOf(name string, version int) registries.Registry[any, any] {
	registry, err := registries.DefineRegistry[any, any](name, registries.DefineRegistryOptions{Version: version})
	if err != nil {
		panic(err)
	}
	return registry
}

// rvTotalOf is Pi's `total`: the sum of the table.
func rvTotalOf(table registries.RegistryTable[any]) rvTotal {
	sum := 0.0
	for _, entry := range table {
		sum += entry.Value.(float64)
	}
	return rvTotal{total: sum}
}

// rvSetter serves Setter, contributing a writable number to Sum.
func rvSetter(initial float64) func(h FacetHost, _ FacetSelf) error {
	return func(h FacetHost, _ FacetSelf) error {
		return h.Serve(rvSetterType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			value := self.Writable(initial)
			self.(StoreSelf).Contribute(rvSum, value)
			return rvSetterImpl(value), nil
		}, ServeOptions{})
	}
}

func rvSetterImpl(value state.Writable[any]) contracts.Implementation {
	return exImpl(nil, map[string]contracts.MethodFunction{
		"set": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
			return nil, value.Set(args.(float64))
		},
	})
}

// rvServeReader serves Reader: `total` follows a registry's total.
func rvServeReader(h FacetHost, registry registries.Registry[any, any]) error {
	return h.Serve(rvReaderType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
		return exImpl(map[string]chord.Watchable[delta.JsonValue]{
			"total": self.Derive([]any{self.Registry(registry)}, func(values []any) (delta.JsonValue, error) {
				return values[0].(rvTotal).total, nil
			}),
		}, nil), nil
	}, ServeOptions{})
}

// rvPlugins is a host with background errors collected and plugins loaded.
func rvPlugins(t testing.TB, options HostOptions, plugins ...HostFacet) (Host, *hostErrors) {
	t.Helper()
	errs := &hostErrors{}
	options.OnError = errs.push
	host := newHost(t, options)
	report := mustLoad(t)(host.Load(plugins, background))
	errs.Failed = report.Failed
	return host, errs
}

func rvPlugin(name string, setup func(h FacetHost, self FacetSelf) error) HostFacet {
	return HostFacet{Name: name, Facet: DefineFacet(setup)}
}

// rvServeSum serves Sum with compose (and the accepts of version 1 when oldValue is set).
func rvServeSum(h FacetHost, compose func(table registries.RegistryTable[any]) (any, error), oldValue func(current any) (any, error)) error {
	provider := registries.RegistryProvider[any, any]{
		Compose: func(table registries.RegistryTable[any], _ registries.ComposeContext) (any, error) {
			return compose(table)
		},
	}
	if oldValue != nil {
		provider.Accepts = registries.RegistryAccepts[any, any]{1: {Value: oldValue}}
	}
	return h.Serve(rvSum, provider, ServeOptions{})
}

func rvTotalCompose(table registries.RegistryTable[any]) (any, error) { return rvTotalOf(table), nil }

// rvCallSet is `(await host.get(Setter)).set(n)`.
func rvCallSet(t testing.TB, host Host, n float64) {
	t.Helper()
	exCall(t, mustGet(t, host, rvSetterType, jsonObject()), "set", n)
}

// rvRead is `registry.value`: the last good composition, or the failure it throws.
func rvRead(value interface{ Value() any }) (got any, thrown error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			thrown, _ = recovered.(error)
		}
	}()
	return value.Value(), nil
}

func rvMember(t testing.TB, ref *Reference, member string) delta.JsonValue {
	t.Helper()
	value, err := ref.Member(member).Value()
	must(t, err)
	return value
}

func rvWithin(timeout time.Duration) GetOptions { return GetOptions{ReadyTimeout: &timeout} }

func TestOlderReadersGetTheCompositionMappedBackWithAcceptsOldValueOncePerComposition(t *testing.T) {
	meter := nexttest.Metered(t)
	var mapped []float64
	host, _ := rvPlugins(t, HostOptions{Telemetry: meter.Telemetry}, rvPlugin("@app/sum", func(h FacetHost, self FacetSelf) error {
		if err := rvServeSum(h, rvTotalCompose, func(current any) (any, error) {
			mapped = append(mapped, current.(rvTotal).total)
			return current.(rvTotal).total, nil
		}); err != nil {
			return err
		}
		return rvSetter(1)(h, self)
	}))
	old, err := host.Registry(background, rvSumV1, GetOptions{})
	must(t, err)
	current, err := host.Registry(background, rvSum, GetOptions{})
	must(t, err)
	for range 2 {
		if got, _ := rvRead(old); got != 1.0 {
			t.Fatalf("old.value = %v, want 1", got)
		}
	}
	if got, _ := rvRead(current); got != (rvTotal{total: 1}) {
		t.Fatalf("current.value = %v", got)
	}
	rvCallSet(t, host, 4)
	if got, _ := rvRead(old); got != 4.0 {
		t.Fatalf("old.value after set = %v, want 4", got)
	}
	if want := []float64{1, 4}; !reflect.DeepEqual(mapped, want) {
		t.Fatalf("mapped = %v, want %v", mapped, want)
	}
	if got := meter.Counts()["chord.registry.adapts"]; got != 2 {
		t.Fatalf("chord.registry.adapts = %v, want 2", got)
	}
	must(t, host.Close(background))
}

func TestOlderReadersWithoutAValueMappingAndForAReaderNewerThanTheProviderParkReadersFailedWaitingContractMismatch(t *testing.T) {
	newer := exLocal("app.newer", contracts.Params{}, contracts.Mem("total", contracts.Int()))
	older := exLocal("app.older", contracts.Params{}, contracts.Mem("total", contracts.Int()))
	host, errs := rvPlugins(t, HostOptions{}, rvPlugin("@app/sum", func(h FacetHost, _ FacetSelf) error {
		if err := rvServeSum(h, rvTotalCompose, nil); err != nil {
			return err
		}
		if err := h.Serve(newer, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"total": self.Derive([]any{self.Registry(rvSumV3)}, func(values []any) (delta.JsonValue, error) { return 0.0, nil }),
			}, nil), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(older, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"total": self.Derive([]any{self.Registry(rvSumV1)}, func(values []any) (delta.JsonValue, error) { return 0.0, nil }),
			}, nil), nil
		}, ServeOptions{})
	}))
	for _, registry := range []registries.Registry[any, any]{rvSumV1, rvSumV3} {
		_, err := host.Registry(background, registry, rvWithin(5*time.Millisecond))
		if chordErr, ok := data.IsChordErrorCode(err, data.CodeContractMismatch); !ok {
			t.Fatalf("host.registry(v%d) = %v, want contract_mismatch (%v)", registry.Version, err, chordErr)
		}
	}
	// Not their fault: waiting (a reload serving the version wakes them), no backoff, nothing failed.
	if got := failures(errs.All()); len(got) != 0 {
		t.Fatalf("failures = %v, want none", got)
	}
	held := mustHold(t, host, newer, jsonObject())
	state := awaitRefState(t, "newer waits", held, func(s *RefState) bool { return s.Kind == RefStateFailed })
	wantMessage := "waiting for registry app.sum: registry app.sum: version 3 is not served here (version 2 is)"
	if state.Cause.Kind != data.FailureWaiting || state.Cause.Error.Message != wantMessage || state.RetryAt != nil {
		t.Fatalf("state = %+v (%v), want failed waiting %q without retry", state, state.Cause.Error, wantMessage)
	}
	must(t, host.Close(background))
}

// overdueLoop is a loop whose timer of one delay is due the moment its section ends, before the goroutine that armed it
// runs on: the deadline of a `host.registry` call that the machine let the waiter miss, as a loaded CI runner does with a
// 5 ms one. Pi's single event loop cannot do that to its `await`: the microtasks after the arming section (the
// publication's, the first `usable()`) run before any timer, so the reason the registry cannot be read is what the
// deadline reports.
type overdueLoop struct {
	*Loop
	delay time.Duration
}

var _ sectionLoop = (*overdueLoop)(nil)

// AfterFunc is the real timer, except for the delay that is overdue: its job is queued as a microtask.
func (l *overdueLoop) AfterFunc(d time.Duration, job func()) Timer {
	if d != l.delay {
		return l.Loop.AfterFunc(d, job)
	}
	timer := &overdueTimer{job: job}
	l.Queue(func() {
		if timer.state.CompareAndSwap(timerPending, timerFired) {
			timer.job()
		}
	})
	return timer
}

type overdueTimer struct {
	state atomic.Int32
	job   func()
}

// Stop implements Timer.
func (t *overdueTimer) Stop() bool { return t.state.CompareAndSwap(timerPending, timerStopped) }

func TestReadersAHostRegistryWhoseDeadlineElapsedBeforeItsFirstLookReportsWhyTheRegistryIsNotUsable(t *testing.T) {
	const deadline = 5 * time.Millisecond
	host, _ := rvPlugins(t, HostOptions{loop: &overdueLoop{Loop: NewLoop(), delay: deadline}}, rvPlugin("@app/sum", func(h FacetHost, _ FacetSelf) error {
		return rvServeSum(h, rvTotalCompose, nil)
	}))
	for _, registry := range []registries.Registry[any, any]{rvSumV1, rvSumV3} {
		_, err := host.Registry(background, registry, rvWithin(deadline))
		if chordErr, ok := data.IsChordErrorCode(err, data.CodeContractMismatch); !ok {
			t.Fatalf("host.registry(v%d) = %v, want contract_mismatch (%v)", registry.Version, err, chordErr)
		}
	}
	_, err := host.Registry(background, rvRegistry(t, "app.nobody", 1), rvWithin(deadline))
	if chordErr, ok := data.IsChordErrorCode(err, data.CodeUnavailable); !ok || chordErr.Reason != data.ReasonUndeclared {
		t.Fatalf("host.registry(app.nobody) = %v, want unavailable/undeclared", err)
	}
	must(t, host.Close(background))
}

func TestOlderReadersDropAnOlderReadersLastGoodMappingAfterAContributorItWasBuiltFromFailedLive(t *testing.T) {
	current := rvRegistry(t, "app.mapped", 2)
	old := rvRegistry(t, "app.mapped", 1)
	contributor := exLocal("app.contributor", contracts.Params{}, contracts.Mem("n", contracts.Int()))
	observed := make(chan FollowObserver[any], 1)
	host, _ := rvPlugins(t, HostOptions{}, rvPlugin("@app/mapping", func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(current, registries.RegistryProvider[any, any]{
			Compose: func(table registries.RegistryTable[any], _ registries.ComposeContext) (any, error) {
				sum := 0.0
				for _, entry := range table {
					sum += entry.Value.(float64)
				}
				return sum, nil
			},
			Accepts: registries.RegistryAccepts[any, any]{1: {Value: func(n any) (any, error) {
				if n.(float64) == 0 {
					return nil, errors.New("old version cannot map the empty table")
				}
				return n, nil
			}}},
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(contributor, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.(StoreSelf).Contribute(current, 42.0)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"n": self.Follow(func(observer FollowObserver[any], _ context.Context) (*FollowAttachment[delta.JsonValue], error) {
					observed <- observer
					return &FollowAttachment[delta.JsonValue]{Value: 42.0, Detach: func() {}}, nil
				}),
			}, nil), nil
		}, ServeOptions{})
	}))
	reader, err := host.Registry(background, old, GetOptions{})
	must(t, err)
	if got, thrown := rvRead(reader); thrown != nil || got != 42.0 {
		t.Fatalf("old.value = %v, %v, want 42", got, thrown)
	}
	observer := <-observed
	host.(*chordHost).enter(func() { observer.End(chord.WatchEnd{Kind: chord.WatchFailed, Error: errors.New("runtime ended")}) })
	turn(host)
	scopeEventually(t, "the mapping is dropped", func() bool {
		_, thrown := rvRead(reader)
		return thrown != nil && strings.Contains(thrown.Error(), "old version cannot map the empty table")
	})
	must(t, host.Close(background))
}

type rvResource struct{ closed atomic.Bool }

func TestOlderReadersNeverHandOutAMappedNativeWhoseContributorFailedAndClosedItSol(t *testing.T) {
	current := rvRegistry(t, "app.resources", 2)
	old := rvRegistry(t, "app.resources", 1)
	owner := mustContract(contracts.Local("app.owner", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{}}))
	resource := &rvResource{}
	observed := make(chan FollowObserver[any], 1)
	host, _ := rvPlugins(t, HostOptions{}, rvPlugin("@app/resources", func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(current, registries.RegistryProvider[any, any]{
			Compose: func(table registries.RegistryTable[any], _ registries.ComposeContext) (any, error) {
				natives := make([]any, len(table))
				for i, entry := range table {
					natives[i] = entry.Value
				}
				return natives, nil
			},
			Accepts: registries.RegistryAccepts[any, any]{1: {Value: func(current any) (any, error) {
				natives := current.([]any)
				if len(natives) == 0 {
					return nil, errors.New("no current resource")
				}
				return natives[0], nil
			}}},
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(owner, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			native := self.Open(
				func(context.Context) (any, error) { return resource, nil },
				func(any, NativeCloseReason, context.Context) error { resource.closed.Store(true); return nil },
			)
			self.(StoreSelf).Contribute(current, native)
			self.Follow(func(observer FollowObserver[any], _ context.Context) (*FollowAttachment[delta.JsonValue], error) {
				select {
				case observed <- observer:
				default:
				}
				return &FollowAttachment[delta.JsonValue]{Value: 1.0, Detach: func() {}}, nil
			})
			return contracts.Implementation{}, nil
		}, ServeOptions{})
	}))
	reader, err := host.Registry(background, old, GetOptions{})
	must(t, err)
	got, thrown := rvRead(reader)
	if thrown != nil {
		t.Fatal(thrown)
	}
	opened, err := got.(Native[any]).Value()
	if err != nil || opened != resource {
		t.Fatalf("native value = %v, %v, want the resource", opened, err)
	}
	observer := <-observed
	host.(*chordHost).enter(func() { observer.End(chord.WatchEnd{Kind: chord.WatchFailed, Error: errors.New("runtime closed")}) })
	scopeEventually(t, "the failed contributor closed its native", resource.closed.Load)
	scopeEventually(t, "the mapping no longer hands the closed native out", func() bool {
		_, thrown := rvRead(reader)
		return thrown != nil && strings.Contains(thrown.Error(), "no current resource")
	})
	must(t, host.Close(background))
}

func TestReadersActivateOnceTheRegistryHasAValueAReaderOfARegistryNobodyServesParksFailedWaitingLoadWaitsForNothing(t *testing.T) {
	started := time.Now()
	host, errs := rvPlugins(t, HostOptions{}, rvPlugin("@app/reader", func(h FacetHost, _ FacetSelf) error { return rvServeReader(h, rvSum) }))
	if time.Since(started) >= time.Second {
		t.Fatalf("load took %v", time.Since(started))
	}
	if errs.Len() != 0 {
		t.Fatalf("errors = %v", errs.All())
	}
	held := mustHold(t, host, rvReaderType, jsonObject())
	state := awaitRefState(t, "the reader waits", held, func(s *RefState) bool { return s.Kind == RefStateFailed })
	wantMessage := "waiting for registry app.sum: registry app.sum: no plugin serves it here"
	var cause *data.ChordError
	if !errors.As(state.Cause.Error.Cause, &cause) {
		cause = nil
	}
	if state.Cause.Kind != data.FailureWaiting || state.Cause.Error.Code != data.CodeUnavailable || state.Cause.Error.Message != wantMessage ||
		cause == nil || cause.Code != data.CodeUnavailable || cause.Reason != data.ReasonUndeclared {
		t.Fatalf("state = %+v (%+v), want failed waiting: %s", state, state.Cause.Error, wantMessage)
	}
	_, err := host.Registry(background, rvSum, rvWithin(5*time.Millisecond))
	if chordErr, ok := data.IsChordErrorCode(err, data.CodeUnavailable); !ok || chordErr.Reason != data.ReasonUndeclared {
		t.Fatalf("host.registry = %v, want unavailable/undeclared", err)
	}
	must(t, host.Close(background))
}

// rvFirstGoodCompose throws while the table sums to zero.
func rvFirstGoodCompose(table registries.RegistryTable[any]) (any, error) {
	sum := rvTotalOf(table)
	if sum.total == 0 {
		return nil, errors.New("empty")
	}
	return sum, nil
}

func TestReadersAReaderParkedOnAFirstComposeThatThrewGoesOnAtTheFirstGoodComposition(t *testing.T) {
	host, errs := rvPlugins(t, HostOptions{},
		rvPlugin("@app/sum", func(h FacetHost, _ FacetSelf) error {
			if err := rvServeSum(h, rvFirstGoodCompose, nil); err != nil {
				return err
			}
			return rvServeReader(h, rvSum)
		}),
		rvPlugin("@app/setter", rvSetter(0)),
	)
	if got := failures(errs.All()); len(got) != 0 {
		t.Fatalf("failures = %v", got)
	}
	held := mustHold(t, host, rvReaderType, jsonObject())
	if state := refStateOf(held); state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting {
		t.Fatalf("state = %+v, want failed waiting", state)
	}
	rvCallSet(t, host, 3)
	// Woken by the composition: no timer of its own.
	reader, err := host.Get(background, rvReaderType, jsonObject(), rvWithin(500*time.Millisecond))
	must(t, err)
	held.Release()
	if got := rvMember(t, reader, "total"); got != 3.0 {
		t.Fatalf("total = %v, want 3", got)
	}
	must(t, host.Close(background))
}

func TestReadersHostRegistryWaitsThroughNoValueUntilAFirstGoodCompositionWokenByItItsWaitThenGone(t *testing.T) {
	host, _ := rvPlugins(t, HostOptions{}, rvPlugin("@app/sum", func(h FacetHost, self FacetSelf) error {
		if err := rvServeSum(h, rvFirstGoodCompose, nil); err != nil {
			return err
		}
		return rvSetter(0)(h, self)
	}))
	type result struct {
		value *registries.RegistryValueSource
		err   error
	}
	waiting := make(chan result, 1)
	go func() {
		value, err := host.Registry(background, rvSum, rvWithin(500*time.Millisecond))
		waiting <- result{value, err}
	}()
	time.Sleep(10 * time.Millisecond)
	rvCallSet(t, host, 5)
	got := <-waiting
	must(t, got.err)
	if value, thrown := rvRead(got.value); thrown != nil || value != (rvTotal{total: 5}) {
		t.Fatalf("value = %v, %v, want { total: 5 }", value, thrown)
	}
	must(t, host.Close(background))
}

func TestReadersOnceLiveALaterThrowingComposeChangesNothingForTheReaderItsValueKeepsTheLastGoodOne(t *testing.T) {
	host, _ := rvPlugins(t, HostOptions{}, rvPlugin("@app/sum", func(h FacetHost, self FacetSelf) error {
		if err := rvServeSum(h, func(table registries.RegistryTable[any]) (any, error) {
			sum := rvTotalOf(table)
			if sum.total < 0 {
				return nil, errors.New("negative")
			}
			return sum, nil
		}, nil); err != nil {
			return err
		}
		if err := rvServeReader(h, rvSum); err != nil {
			return err
		}
		return rvSetter(2)(h, self)
	}))
	reader := mustGet(t, host, rvReaderType, jsonObject())
	if got := rvMember(t, reader, "total"); got != 2.0 {
		t.Fatalf("total = %v, want 2", got)
	}
	rvCallSet(t, host, -1)
	turn(host)
	if got := rvMember(t, reader, "total"); got != 2.0 {
		t.Fatalf("total after a throwing compose = %v, want 2", got)
	}
	if state := refStateOf(reader); state.Kind != RefStateLive {
		t.Fatalf("state = %+v, want live", state)
	}
	must(t, host.Close(background))
}

func TestReadersMemoryAnEndedReaderIsCollectedWhileTheRegistryLivesItsDeriveAndItsReadLeaveTheValue(t *testing.T) {
	reader := exLocal("app.reader-id", contracts.Params{contracts.Prop("id", contracts.Int())}, contracts.Mem("total", contracts.Int()))
	var made weak.Pointer[attempt]
	host, _ := rvPlugins(t, HostOptions{}, rvPlugin("@app/sum", func(h FacetHost, self FacetSelf) error {
		if err := rvServeSum(h, rvTotalCompose, func(current any) (any, error) { return current.(rvTotal).total, nil }); err != nil {
			return err
		}
		if err := h.Serve(reader, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			made = weak.Make(self.(*attempt))
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"total": self.Derive([]any{self.Registry(rvSum), self.Registry(rvSumV1)}, func(values []any) (delta.JsonValue, error) {
					return values[0].(rvTotal).total + values[1].(float64), nil
				}),
			}, nil), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return rvSetter(1)(h, self)
	}))
	ref := mustGet(t, host, reader, jsonObject("id", 1.0))
	if got := rvMember(t, ref, "total"); got != 2.0 {
		t.Fatalf("total = %v, want 2", got)
	}
	ref.Release()
	turn(host)
	rvCallSet(t, host, 5)
	nexttest.ExpectCollected(t, made)
	must(t, host.Close(background))
}

type rvOldValue struct {
	n       float64
	payload []byte
}

func TestValuesMemoryDropsAnOlderVersionsMappingOnceItsLastReaderEndedSol(t *testing.T) {
	current := rvRegistry(t, "app.version-gc", 2)
	old := rvRegistry(t, "app.version-gc", 1)
	reader := exLocal("app.old-reader", contracts.Params{contracts.Prop("id", contracts.Int())}, contracts.Mem("n", contracts.Int()))
	var mapped weak.Pointer[rvOldValue]
	host, _ := rvPlugins(t, HostOptions{}, rvPlugin("@app/a", func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(current, registries.RegistryProvider[any, any]{
			Compose: func(registries.RegistryTable[any], registries.ComposeContext) (any, error) { return 1.0, nil },
			Accepts: registries.RegistryAccepts[any, any]{1: {Value: func(n any) (any, error) {
				value := &rvOldValue{n: n.(float64), payload: make([]byte, 1_000_000)}
				mapped = weak.Make(value)
				return value, nil
			}}},
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(reader, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"n": self.Derive([]any{self.Registry(old)}, func(values []any) (delta.JsonValue, error) { return values[0].(*rvOldValue).n, nil }),
			}, nil), nil
		}, ServeOptions{})
	}))
	ref := mustGet(t, host, reader, jsonObject("id", 1.0))
	if got := rvMember(t, ref, "n"); got != 1.0 {
		t.Fatalf("n = %v, want 1", got)
	}
	ref.Release()
	turn(host)
	nexttest.ExpectCollected(t, mapped)
	must(t, host.Close(background))
}

type rvBig struct{ payload []float64 }

func TestValuesMemoryKeepsNoOldCompositionOnceItsReadersStoppedWaitingAndNobodyFollowsItOpus(t *testing.T) {
	big := rvRegistry(t, "app.big", 1)
	counter := exLocal("app.counter", contracts.Params{}, contracts.Mem("set", contracts.NewMethod(contracts.Int())))
	waiting := mustContract(contracts.Local("app.waiting", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{}}))
	first := true
	var made []weak.Pointer[rvBig]
	host, _ := rvPlugins(t, HostOptions{}, rvPlugin("@app/a", func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(big, registries.RegistryProvider[any, any]{
			Compose: func(table registries.RegistryTable[any], _ registries.ComposeContext) (any, error) {
				if first {
					return nil, errors.New("first")
				}
				value := &rvBig{payload: make([]float64, 1000)}
				made = append(made, weak.Make(value))
				return value, nil
			},
		}, ServeOptions{}); err != nil {
			return err
		}
		if err := h.Serve(counter, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			value := self.Writable(0.0)
			self.(StoreSelf).Contribute(big, value)
			return rvSetterImpl(value), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(waiting, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Registry(big)
			return contracts.Implementation{}, nil
		}, ServeOptions{})
	}))
	ref := mustGet(t, host, counter, jsonObject())
	first = false
	// The waiting reader's composition (pulled in the batch of this set) wakes it.
	exCall(t, ref, "set", 1.0)
	turn(host)
	exCall(t, ref, "set", 2.0)
	value, err := host.Registry(background, big, GetOptions{})
	must(t, err)
	_, _ = rvRead(value)
	settledHeap()
	// Only the current composition is alive.
	alive := make([]bool, len(made))
	for i, composition := range made {
		alive[i] = composition.Value() != nil
	}
	if want := []bool{false, true}; !reflect.DeepEqual(alive, want) {
		t.Fatalf("compositions alive = %v, want %v", alive, want)
	}
	must(t, host.Close(background))
}
