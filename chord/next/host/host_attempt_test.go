package host

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Ports packages/chord/test/next/host/attempt.test.ts: attempts (§6, §13, §14, §15): the failure kinds of an activation, the
// ending of an attempt (the seal fence, the drain), declarations, calls, remote variants, tables and eager migrations,
// and attempts parked on their dependencies. Every case runs a real host; the cases Pi runs under `fakeTimers()` run on
// the scopeLoop of host_scope_support_test.go (the clock moves only through advanceHost). A handler or an opener that Pi
// awaits is a blocking Go function on the task that calls it; what it waits for is a channel the test closes (signal).

var attemptListDoc = func() *documents.StateDefinition {
	definition, err := documents.DefineState("list", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("items", contracts.Arr(contracts.Str()))),
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("items", []any{}) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

var attemptPage = mustContract(contracts.Local("app.page", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
	Members: []contracts.LocalEntry{contracts.Mem("label", contracts.Str()), contracts.Mem("slow", contracts.NewMethod())},
}))

var attemptBroken = mustContract(contracts.Local("app.broken", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
	Members: []contracts.LocalEntry{contracts.Mem("label", contracts.Str())},
}))

// advanceHost is `await vi.advanceTimersByTimeAsync(ms)` on a host loaded on loop: a timer at a time, the host's turns
// made between them.
func advanceHost(loop *scopeLoop, host Host, ms int64) {
	turn(host)
	end := loop.Now() + ms
	for {
		next, found := int64(0), false
		loop.mu.Lock()
		for _, timer := range loop.timers {
			if !found || timer.at < next {
				next, found = timer.at, true
			}
		}
		loop.mu.Unlock()
		if !found || next > end {
			break
		}
		loop.AdvanceAsync(next - loop.Now())
		turn(host)
	}
	loop.AdvanceAsync(end - loop.Now())
	turn(host)
}

// until waits for what a task of the host makes true (an opener's effect, a close): cond is the observable of the hand-off.
func until(t testing.TB, what string, cond func() bool) { t.Helper(); scopeEventually(t, what, cond) }

// strings is a list a handler or opener appends to from its task.
type strs struct {
	mu   sync.Mutex
	list []string
}

func (s *strs) add(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = append(s.list, text)
}

func (s *strs) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.list)
}

func (s *strs) len() int { return len(s.all()) }

// --- failure kinds ------------------------------------------------------------------------------------------------------

type labelOnly struct{}

func (labelOnly) Value() delta.JsonValue { return "x" }
func (labelOnly) Watch(context.Context) (chord.WatchHandle[delta.JsonValue], error) {
	return nil, errors.New("native")
}

func TestAttemptFailureKindsFailBrokenReportedTheGetRejectsAtOnceWithAChordErrorNothingIsLeft(t *testing.T) {
	bad, err := documents.DefineState("bad", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("n", contracts.Int())),
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("n", "x") },
	})
	must(t, err)
	cases := []struct {
		name    string
		factory func(self Self) (contracts.Implementation, error)
		message string
	}{
		{"a throwing factory", func(Self) (contracts.Implementation, error) {
			return contracts.Implementation{}, errors.New("factory threw")
		}, "app.broken(1): factory threw"},
		{"a missing member", func(Self) (contracts.Implementation, error) {
			return contracts.Implementation{}, nil
		}, "app.broken(1): label: a state member is a JSON watchable"},
		{"an unknown member", func(Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"label": constant(""), "extra": constant(1.0)},
			}}, nil
		}, "app.broken(1): extra: not a member"},
		{"a native watchable as state (Q14)", func(Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"label": labelOnly{}},
			}}, nil
		}, "app.broken(1): label: a state member is a JSON watchable"},
		{"a throwing first derive", func(self Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"label": self.Derive(nil, func([]any) (delta.JsonValue, error) {
					return nil, errors.New("derive threw")
				})},
			}}, nil
		}, "app.broken(1): derive threw"},
		{"an initial() that fails its schema", func(self Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"label": self.Derive([]any{self.State(bad)}, func([]any) (delta.JsonValue, error) {
					return "", nil
				})},
			}}, nil
		}, "app.broken(1): bad: initial() does not match the schema"},
		{"an open that throws broken(error)", func(self Self) (contracts.Implementation, error) {
			self.Open(func(context.Context) (any, error) {
				return nil, data.Broken(errors.New("no remote configured"))
			}, func(any, NativeCloseReason, context.Context) error { return nil })
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"label": constant("")},
			}}, nil
		}, "app.broken(1): no remote configured"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			var served atomic.Int32
			host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
				return h.Serve(attemptBroken, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
					served.Add(1)
					return each.factory(self)
				}, ServeOptions{})
			}, loadedOptions{})
			_, first := host.Get(attemptBroken, jsonObject("id", 1.0), background, GetOptions{})
			wantChordError(t, first, data.CodeUnavailable, "", each.message)
			_, second := host.Get(attemptBroken, jsonObject("id", 1.0), background, GetOptions{})
			if second == nil || second.Error() != each.message {
				t.Fatalf("second get = %v, want %q", second, each.message)
			}
			if got := served.Load(); got != 2 {
				t.Fatalf("factory ran %d times, want 2", got)
			}
			if got := failures(errs.All()); !reflect.DeepEqual(got, []string{"app.broken(1): broken", "app.broken(1): broken"}) {
				t.Fatalf("failures = %v", got)
			}
			// One report per failure: a derive throwing while its attempt activates reports no derive_failed.
			var kinds []HostErrorKind
			for _, report := range errs.All() {
				kinds = append(kinds, report.Kind)
			}
			if !reflect.DeepEqual(kinds, []HostErrorKind{ErrActivationFailed, ErrActivationFailed}) {
				t.Fatalf("report kinds = %v", kinds)
			}
		})
	}
}

func TestAttemptAnOpenThatThrowsFailsTransientReportedTheGetWaitsThroughRetries(t *testing.T) {
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(attemptBroken, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(context.Context) (any, error) { return nil, errors.New("offline") },
				func(any, NativeCloseReason, context.Context) error { return nil })
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"label": constant("")},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	ten := 10 * time.Millisecond
	_, err := host.Get(attemptBroken, jsonObject("id", 1.0), background, GetOptions{ReadyTimeout: &ten})
	wantChordError(t, err, data.CodeUnavailable, "", "app.broken(1): offline")
	all := errs.All()
	if len(all) != 1 || all[0].Kind != ErrActivationFailed || all[0].Object != "app.broken(1)" ||
		all[0].Failure.Kind != data.FailureTransient || all[0].Failure.Error.Code != data.CodeUnavailable {
		t.Fatalf("errors = %+v, want one transient activation_failed of app.broken(1)", all)
	}
	closeHosts(t, host)
}

func TestAttemptAnAttemptNotLiveWithinReadyTimeoutIsAbandonedAndFailsTransientTheOpenersContextAbortedWhatOpenedClosed(t *testing.T) {
	loop := newScopeLoop()
	var mu sync.Mutex
	var opener context.Context
	var closes strs
	forever := make(chan struct{})
	defer close(forever)
	ready := 100 * time.Millisecond
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(attemptBroken, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(context.Context) (any, error) { return "a", nil }, func(value any, reason NativeCloseReason, _ context.Context) error {
				closes.add(value.(string) + " " + string(reason.Kind))
				return nil
			})
			self.Open(func(ctx context.Context) (any, error) {
				mu.Lock()
				opener = ctx
				mu.Unlock()
				<-forever
				return nil, nil
			}, func(any, NativeCloseReason, context.Context) error { return nil })
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"label": constant("")},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{Loop: loop, ReadyTimeout: &ready})
	long := 150 * time.Millisecond
	got := make(chan error, 1)
	go func() {
		_, err := host.Get(attemptBroken, jsonObject("id", 1.0), background, GetOptions{ReadyTimeout: &long})
		got <- err
	}()
	until(t, "the second opener", func() bool { mu.Lock(); defer mu.Unlock(); return opener != nil })
	advanceHost(loop, host, 100)
	until(t, "the first native's close", func() bool { return closes.len() == 1 })
	mu.Lock()
	aborted := opener.Err() != nil
	mu.Unlock()
	if !aborted || !reflect.DeepEqual(closes.all(), []string{"a end"}) {
		t.Fatalf("opener aborted %v, closes %v, want true and [a end]", aborted, closes.all())
	}
	notLiveMessage := "app.broken(1): did not become live within 100 ms"
	all := errs.All()
	if len(all) != 1 || all[0].Kind != ErrActivationFailed || all[0].Failure.Kind != data.FailureTransient || all[0].Failure.Error.Code != data.CodeUnavailable ||
		all[0].Failure.Error.Message != notLiveMessage {
		t.Fatalf("errors = %+v, want one transient not-live report", all)
	}
	advanceHost(loop, host, 50)
	wantChordError(t, receive(t, got), data.CodeUnavailable, "", notLiveMessage)
}

func TestAttemptAnAttemptWaitingForItsPreviousInstancesTeardownIsBoundedByReadyTimeoutTooAbandonedTransientWithoutRunningItsFactoryRetriedAfterIt(t *testing.T) {
	loop := newScopeLoop()
	closing := newSignal()
	var factories atomic.Int32
	ready, closeTimeout := 100*time.Millisecond, 10*time.Second
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(attemptPage, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			factories.Add(1)
			self.Open(func(context.Context) (any, error) { return "native", nil }, func(any, NativeCloseReason, context.Context) error {
				closing.wait(t)
				return nil
			})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State:   map[string]chord.Watchable[delta.JsonValue]{"label": constant("p")},
				Methods: map[string]contracts.MethodFunction{"slow": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{Loop: loop, ReadyTimeout: &ready, CloseTimeout: &closeTimeout})
	mustGet(t, host, attemptPage, jsonObject("id", 1.0)).Release()
	next := mustHold(t, host, attemptPage, jsonObject("id", 1.0))
	advanceHost(loop, host, 100)
	until(t, "the abandonment's report", func() bool { return errs.Len() == 1 })
	if got := factories.Load(); got != 1 {
		t.Fatalf("factories = %d, want 1", got)
	}
	all := errs.All()
	if len(all) != 1 || all[0].Kind != ErrActivationFailed || all[0].Failure.Kind != data.FailureTransient ||
		all[0].Failure.Error.Message != "app.page(1): did not become live within 100 ms" {
		t.Fatalf("errors = %+v, want one transient not-live report", all)
	}
	closing.fire()
	advanceHost(loop, host, 1_000)
	untilKind(t, next, RefStateLive)
	if got := factories.Load(); got != 2 {
		t.Fatalf("factories = %d, want 2", got)
	}
}

func TestAttemptAKeyedDocumentsInitialFailingItsSchemaFailsThatTransactionNotTheObject(t *testing.T) {
	keys, err := documents.DefineState("keys", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("n", contracts.Int())),
		Version: 1,
		Keyed:   true,
		Initial: func() *delta.JsonObject { return jsonObject("n", "x") },
	})
	must(t, err)
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(attemptPage, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			handle := self.State(keys).(*documents.Keyed)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"label": constant("live")},
				Methods: map[string]contracts.MethodFunction{"slow": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					_, err := handle.Change("a", func(*delta.Object) (any, error) { return nil, nil }, ctx)
					return nil, err
				}},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	page := mustGet(t, host, attemptPage, jsonObject("id", 1.0))
	_, err = refCall(page, "slow", nil)
	if err == nil || err.Error() != "keys: initial() does not match the schema" {
		t.Fatalf("slow error = %v", err)
	}
	label, lerr := page.Member("label").Value()
	must(t, lerr)
	if label != "live" || errs.Len() != 0 {
		t.Fatalf("label %v, errors %v, want live and none", label, errs.All())
	}
	closeHosts(t, host)
}

func TestAttemptReleasesTheReferencesItAcquired(t *testing.T) {
	var pages atomic.Int32
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(attemptBroken, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			page := self.Get(attemptPage, jsonObject("id", 7.0))
			label := self.Derive([]any{page.Member("label")}, func([]any) (delta.JsonValue, error) { return nil, errors.New("late") })
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"label": label}}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(attemptPage, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			pages.Add(1)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State:   map[string]chord.Watchable[delta.JsonValue]{"label": constant("p")},
				Methods: map[string]contracts.MethodFunction{"slow": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	_, err := host.Get(attemptBroken, jsonObject("id", 1.0), background, GetOptions{})
	if err == nil || err.Error() != "app.broken(1): late" {
		t.Fatalf("get error = %v, want late", err)
	}
	if got := pages.Load(); got != 1 {
		t.Fatalf("pages = %d, want 1", got)
	}
	// The failed attempt's teardown releases its references once its natives closed (a task): wait for it.
	untilEnded(t, host)
	mustGet(t, host, attemptPage, jsonObject("id", 7.0))
	if got := pages.Load(); got != 2 {
		t.Fatalf("pages = %d, want 2", got)
	}
}

// --- ending ---------------------------------------------------------------------------------------------------------------

func TestAttemptEndingRejectsTheirCallersAtOnceTheHandlerRunsOnWithItsContextAbortedAndCannotCommit(t *testing.T) {
	started, proceed := newSignal(), newSignal()
	type outcome struct {
		aborted bool
		err     error
	}
	finished := make(chan outcome, 1)
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(attemptPage, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			list := self.State(attemptListDoc).(*documents.Singleton)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"label": constant("")},
				Methods: map[string]contracts.MethodFunction{"slow": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					started.fire()
					proceed.await(t, ctx)
					_, err := self.Tx(func(tx *documents.Transaction) (any, error) { return tx.Draft(list) }, background)
					finished <- outcome{aborted: ctx.Err() != nil, err: err}
					return nil, nil
				}},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	page := mustGet(t, host, attemptPage, jsonObject("id", 1.0))
	called := make(chan error, 1)
	go func() {
		_, err := refCall(page, "slow", nil)
		called <- err
	}()
	started.wait(t)
	page.Release()
	wantChordError(t, receive(t, called), data.CodeReleased, data.ReasonEnded, "app.page(1): ended")
	proceed.fire()
	got := receive(t, finished)
	if !got.aborted {
		t.Fatal("the handler's context was not aborted")
	}
	wantChordError(t, got.err, data.CodeReleased, data.ReasonEnded, "app.page(1): ended")
}

func TestAttemptEndingRejectsACallWhoseCallersContextIsAlreadyAbortedBeforeTheHandlerRuns(t *testing.T) {
	var slow atomic.Int32
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(attemptPage, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"label": constant("")},
				Methods: map[string]contracts.MethodFunction{"slow": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					slow.Add(1)
					return nil, nil
				}},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	page := mustGet(t, host, attemptPage, jsonObject("id", 1.0))
	cancelled, cancel := chordctx.WithCancel(background)
	cancel(errors.New("gone"))
	_, err := page.Call("slow", nil, cancelled)
	if err == nil || err.Error() != "gone" {
		t.Fatalf("call error = %v, want gone", err)
	}
	turn(host)
	if got := slow.Load(); got != 0 {
		t.Fatalf("handler ran %d times, want 0", got)
	}
	closeHosts(t, host)
}

func TestAttemptEndingWaitsForTheRunningHandlersOwnPromisesTheDrainBeforeTheNativesClose(t *testing.T) {
	var events strs
	started, proceed := newSignal(), newSignal()
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(attemptPage, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(context.Context) (any, error) { return "native", nil }, func(value any, reason NativeCloseReason, _ context.Context) error {
				events.add("close " + value.(string) + " " + string(reason.Kind))
				return nil
			})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"label": constant("")},
				Methods: map[string]contracts.MethodFunction{"slow": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					started.fire()
					proceed.await(t, ctx)
					events.add("handler settled")
					return nil, nil
				}},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	page := mustGet(t, host, attemptPage, jsonObject("id", 1.0))
	called := make(chan error, 1)
	go func() { _, err := refCall(page, "slow", nil); called <- err }()
	started.wait(t)
	page.Release()
	if err := receive(t, called); err == nil || err.Error() != "app.page(1): ended" {
		t.Fatalf("call error = %v, want ended", err)
	}
	turn(host)
	if got := events.all(); len(got) != 0 {
		t.Fatalf("events = %v, want none while the handler runs", got)
	}
	proceed.fire()
	until(t, "the native's close", func() bool { return events.len() == 2 })
	if got := events.all(); !reflect.DeepEqual(got, []string{"handler settled", "close native end"}) {
		t.Fatalf("events = %v", got)
	}
}

func TestAttemptEndingDrainsAHandlerThatReleasesItsObjectsLastReferenceBeforeItReturnsItsPromise(t *testing.T) {
	var events strs
	proceed := newSignal()
	var page *Reference
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(attemptPage, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			selfOpen(self, func(context.Context) error { return nil }, func(NativeCloseReason) { events.add("closed") })
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"label": constant("")},
				// The release runs before the handler has anything to return.
				Methods: map[string]contracts.MethodFunction{"slow": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					page.Release()
					proceed.await(t, ctx)
					events.add("handler settled")
					return nil, nil
				}},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	page = mustGet(t, host, attemptPage, jsonObject("id", 1.0))
	_, err := refCall(page, "slow", nil)
	if err == nil || err.Error() != "app.page(1): ended" {
		t.Fatalf("call error = %v, want ended", err)
	}
	turn(host)
	if got := events.all(); len(got) != 0 {
		t.Fatalf("events = %v, want none while the handler runs", got)
	}
	proceed.fire()
	until(t, "the close", func() bool { return events.len() == 2 })
	if got := events.all(); !reflect.DeepEqual(got, []string{"handler settled", "closed"}) {
		t.Fatalf("events = %v", got)
	}
}

func TestAttemptEndingRefusesTransactionsAtOnceDocumentsStayReadableUntilTheClosesAreDone(t *testing.T) {
	type seenAtClose struct {
		items any
		err   error
	}
	seen := make(chan seenAtClose, 1)
	var list *documents.Singleton
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(attemptPage, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			list = self.State(attemptListDoc).(*documents.Singleton)
			self.Open(func(context.Context) (any, error) { return nil, nil }, func(any, NativeCloseReason, context.Context) error {
				value, _ := list.Value()
				_, err := list.Change(func(*delta.Object) (any, error) { return nil, nil }, background)
				seen <- seenAtClose{items: value, err: err}
				return nil
			})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State:   map[string]chord.Watchable[delta.JsonValue]{"label": constant("")},
				Methods: map[string]contracts.MethodFunction{"slow": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	mustGet(t, host, attemptPage, jsonObject("id", 1.0)).Release()
	got := receive(t, seen)
	if !reflect.DeepEqual(got.items, any(jsonObject("items", []any{}))) {
		t.Fatalf("value at close = %v, want {items: []}", got.items)
	}
	wantChordError(t, got.err, data.CodeReleased, data.ReasonEnded, "app.page(1): ended")
	turn(host)
	untilEnded(t, host)
	var readErr error
	host.(*chordHost).enter(func() { _, readErr = list.Value() })
	if readErr == nil || readErr.Error() != "app.page(1): ended" {
		t.Fatalf("value after the closes = %v, want ended", readErr)
	}
}

func TestAttemptTheDrainIsBoundedByCloseTimeoutAHandlerStillRunningThenSeesItsNativesCloseWhichIsReported(t *testing.T) {
	loop := newScopeLoop()
	var closed atomic.Int32
	forever := make(chan struct{})
	defer close(forever)
	closeTimeout := time.Second
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(attemptPage, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			selfOpen(self, func(context.Context) error { return nil }, func(NativeCloseReason) { closed.Add(1) })
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"label": constant("")},
				Methods: map[string]contracts.MethodFunction{"slow": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					// Pi: `await new Promise(() => {})`.
					chordctx.Suspend(ctx)
					<-forever
					return nil, nil
				}},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{Loop: loop, CloseTimeout: &closeTimeout})
	page := mustGet(t, host, attemptPage, jsonObject("id", 1.0))
	go func() { _, _ = refCall(page, "slow", nil) }()
	// The call is admitted before the release ends the object.
	until(t, "the call to be admitted", func() bool { return activityOf(host).Calls == 1 })
	page.Release()
	// The drain's timer is made by the teardown task: the clock moves once it exists.
	until(t, "the drain's timer", func() bool { return loop.TimerCount() > 0 })
	advanceHost(loop, host, 999)
	if got := closed.Load(); got != 0 {
		t.Fatalf("closes at 999 ms = %d, want 0", got)
	}
	advanceHost(loop, host, 1)
	until(t, "the native's close", func() bool { return closed.Load() == 1 })
	all := errs.All()
	if len(all) != 1 || all[0].Kind != ErrDrainTimeout || all[0].Object != "app.page(1)" {
		t.Fatalf("errors = %+v, want one drain_timeout of app.page(1)", all)
	}
}

func TestAttemptTheDrainAHandlerStuckPastItNoLongerCountsAsACallNorHoldsItsCtxGetReferences(t *testing.T) {
	loop := newScopeLoop()
	item := mustContract(contracts.Local("app.item", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("label", contracts.Str())},
	}))
	got := newSignal()
	forever := make(chan struct{})
	defer close(forever)
	closeTimeout := time.Second
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(item, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"label": constant("item")}}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(attemptPage, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"label": constant("")},
				Methods: map[string]contracts.MethodFunction{"slow": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					if _, err := ctx.(Ctx).Get(item, jsonObject("id", 7.0), GetOptions{}); err != nil {
						return nil, err
					}
					got.fire()
					chordctx.Suspend(ctx)
					<-forever
					return nil, nil
				}},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{Loop: loop, CloseTimeout: &closeTimeout})
	references := func() int {
		var count int
		host.(*chordHost).enter(func() {
			value, err := host.References(item).Value()
			must(t, err)
			count = reflect.ValueOf(value).Len()
		})
		return count
	}
	page := mustGet(t, host, attemptPage, jsonObject("id", 1.0))
	go func() { _, _ = refCall(page, "slow", nil) }()
	got.waitMain(t)
	if calls, refs := activityOf(host).Calls, references(); calls != 1 || refs != 1 {
		t.Fatalf("calls %d, references %d, want 1 and 1", calls, refs)
	}
	page.Release()
	until(t, "the drain's timer", func() bool { return loop.TimerCount() > 0 })
	advanceHost(loop, host, 1000)
	until(t, "the stuck handler to stop counting", func() bool { return activityOf(host).Calls == 0 && references() == 0 })
}

// --- declarations -------------------------------------------------------------------------------------------------------

// panicText is the error a declaration panicked with.
func panicText(work func()) (text string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if err, ok := recovered.(error); ok {
				text = err.Error()
				return
			}
			text = "non-error panic"
		}
	}()
	work()
	return ""
}

func TestAttemptDeclarationsAreValidOnlyWhileTheFactoryRuns(t *testing.T) {
	var mu sync.Mutex
	var later Self
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(attemptPage, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			mu.Lock()
			later = self
			mu.Unlock()
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State:   map[string]chord.Watchable[delta.JsonValue]{"label": constant("")},
				Methods: map[string]contracts.MethodFunction{"slow": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	mustGet(t, host, attemptPage, jsonObject("id", 1.0))
	mu.Lock()
	self := later
	mu.Unlock()
	names, err := registries.DefineRegistry[any, any]("app.names", registries.DefineRegistryOptions{Version: 1})
	must(t, err)
	for _, each := range []struct {
		message string
		declare func()
	}{
		{"app.page(1): self.state is valid only in the factory", func() { self.State(attemptListDoc) }},
		{"app.page(1): self.derive is valid only in the factory", func() { self.Derive(nil, func([]any) (delta.JsonValue, error) { return 1.0, nil }) }},
		{"app.page(1): self.get is valid only in the factory", func() { self.Get(attemptPage, jsonObject("id", 2.0)) }},
		{"app.page(1): self.open is valid only in the factory", func() {
			self.Open(func(context.Context) (any, error) { return nil, nil }, func(any, NativeCloseReason, context.Context) error { return nil })
		}},
		{"app.page(1): self.registry is valid only in the factory", func() { self.Registry(names) }},
		{"app.page(1): self.writable is valid only in the factory", func() { self.Writable(1.0) }},
		{"app.page(1): self.retryOn is valid only in the factory", func() { self.RetryOn() }},
	} {
		if got := panicText(each.declare); got != each.message {
			t.Errorf("declaration panicked with %q, want %q", got, each.message)
		}
	}
	closeHosts(t, host)
}

func TestAttemptDeclarationsContributeOnlyInHostLifetimeObjectsR21SelfHasNoContributeADeclarationErrorAtRuntime(t *testing.T) {
	// Carries "expectTypeOf<Self>().not.toHaveProperty("contribute")": the Go mechanism is the method sets.
	if _, has := reflect.TypeFor[Self]().MethodByName("Contribute"); has {
		t.Error("Self has Contribute")
	}
	if _, has := reflect.TypeFor[StoreSelf]().MethodByName("Contribute"); !has {
		t.Error("StoreSelf has no Contribute")
	}
	names, err := registries.DefineRegistry[any, any]("app.names", registries.DefineRegistryOptions{Version: 1})
	must(t, err)
	storeType := mustContract(contracts.Local("app.store", contracts.LocalDeclaration{Members: []contracts.LocalEntry{}, Params: contracts.Params{}, Version: 1}))
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(names, registries.RegistryProvider[any, any]{
			Compose: func(table registries.RegistryTable[any], _ registries.ComposeContext) (any, error) {
				return float64(len(table)), nil
			},
		}, ServeOptions{}); err != nil {
			return err
		}
		if err := h.Serve(attemptPage, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.(StoreSelf).Contribute(names, "page")
			return contracts.Implementation{}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(storeType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.(StoreSelf).Contribute(names, "a")
			self.(StoreSelf).Contribute(names, "b")
			return contracts.Implementation{}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	_, err = host.Get(attemptPage, jsonObject("id", 1.0), background, GetOptions{})
	if err == nil || !hasText(err.Error(), "app.page(1): self.contribute is valid only in a local type with empty params") {
		t.Fatalf("page get = %v", err)
	}
	_, err = host.Get(storeType, jsonObject(), background, GetOptions{})
	if err == nil || !hasText(err.Error(), "app.store: registry app.names is contributed to twice") {
		t.Fatalf("store get = %v", err)
	}
	closeHosts(t, host)
}

func TestAttemptDeclarationsRefuseADocumentDefinitionDeclaredTwice(t *testing.T) {
	twice := mustContract(contracts.Local("app.twice", contracts.LocalDeclaration{Members: []contracts.LocalEntry{},
		Version: 1, Params: contracts.Params{contracts.Prop("n", contracts.Int())},
	}))
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(twice, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.State(attemptListDoc)
			self.State(attemptListDoc)
			return contracts.Implementation{}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	_, err := host.Get(twice, jsonObject("n", 2.0), background, GetOptions{})
	if err == nil || !hasText(err.Error(), `app.twice(2): state "list" is declared twice`) {
		t.Fatalf("get = %v", err)
	}
}

// --- calls ----------------------------------------------------------------------------------------------------------------

func TestAttemptCallsCheckArgumentsWithL1AndPassArgumentsAndResultsByReference(t *testing.T) {
	var mu sync.Mutex
	var seen []any
	result := jsonObject("items", []any{})
	echo := mustContract(contracts.Local("app.echo", contracts.LocalDeclaration{Params: contracts.Params{},
		Version: 1,
		Members: []contracts.LocalEntry{
			contracts.Mem("echo", contracts.NewMethod(
				contracts.Obj(contracts.Prop("text", contracts.Str())), contracts.Obj(contracts.Prop("items", contracts.Arr(contracts.Str()))))),
			contracts.Mem("none", contracts.NewMethod()),
		},
	}))
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(echo, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"echo": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					mu.Lock()
					seen = append(seen, args)
					mu.Unlock()
					return result, nil
				},
				// A method without arguments gets nothing but the context.
				"none": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					mu.Lock()
					seen = append(seen, args)
					mu.Unlock()
					return nil, nil
				},
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	ref := mustGet(t, host, echo, jsonObject())
	args := jsonObject("text", "t")
	got, err := ref.Call("echo", args, background)
	must(t, err)
	if got != delta.JsonValue(result) {
		t.Fatal("the result is not the handler's own object")
	}
	if got, err := ref.Call("none", nil, background); err != nil || got != nil {
		t.Fatalf("none = %v, %v, want nil", got, err)
	}
	mu.Lock()
	if len(seen) != 2 || seen[0] != any(args) || seen[1] != nil {
		t.Fatalf("seen = %v, want the args themselves and nil", seen)
	}
	mu.Unlock()
	_, err = ref.Call("echo", jsonObject("text", "t", "extra", 1.0), background)
	wantChordError(t, err, data.CodeInvalid, data.ReasonArgs, func() string {
		chordErr, _ := data.IsChordError(err)
		return chordErr.Message
	}())
}

func TestAttemptCallsTakeAPlainValueOrASynchronousThrowFromAHandlerThatIsNoAsyncFunction(t *testing.T) {
	plain := mustContract(contracts.Local("app.plain", contracts.LocalDeclaration{Params: contracts.Params{},
		Version: 1,
		Members: []contracts.LocalEntry{
			contracts.Mem("value", contracts.NewMethod(contracts.TSchema{}, contracts.Int())),
			contracts.Mem("fail", contracts.NewMethod()),
		},
	}))
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(plain, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"value": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return 7.0, nil },
				// A synchronous throw: a panic of plugin code is the rejection.
				"fail": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { panic(errors.New("sync throw")) },
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	ref := mustGet(t, host, plain, jsonObject())
	if got, err := ref.Call("value", nil, background); err != nil || got != 7.0 {
		t.Fatalf("value = %v, %v, want 7", got, err)
	}
	if _, err := ref.Call("fail", nil, background); err == nil || err.Error() != "sync throw" {
		t.Fatalf("fail = %v, want sync throw", err)
	}
}

func TestAttemptCallsCostOneValidationPerCallAndOneCompilationPerMethod(t *testing.T) {
	meter := nexttest.Metered(t)
	// A fresh declaration: compiled checks are cached per contract.
	fresh := mustContract(contracts.Local("app.store", contracts.LocalDeclaration{Params: contracts.Params{},
		Version: 1,
		Members: []contracts.LocalEntry{contracts.Mem("add", contracts.NewMethod(contracts.Obj(contracts.Prop("text", contracts.Str()))))},
	}))
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(fresh, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"add": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	ref := mustGet(t, host, fresh, jsonObject())
	for _, text := range []string{"a", "b"} {
		_, err := ref.Call("add", jsonObject("text", text), meter.Context)
		must(t, err)
	}
	if got := meter.Counts(); got["chord.data.compilations"] != 1 || got["chord.data.validations"] != 2 {
		t.Fatalf("counts = %v, want 1 compilation and 2 validations", got)
	}
}

// Carries "the call path's cost › makes at most 19 promises per local call (a guard: the drain and the cleanups share one
// reaction)": Pi counts promises with async_hooks; Go has none. The cost a call has here is the microtasks it queues on the
// host's loop (a settled call's cleanup and its result): the drain's admission and the cleanup share one, and the guard is
// that a call queues at most two.
func TestAttemptTheCallPathsCostAtMostTwoMicrotasksPerLocalCallTheDrainAndTheCleanupsShareOneReaction(t *testing.T) {
	loop := &countingSectionLoop{scopeLoop: newScopeLoop()}
	touch := mustContract(contracts.Local("app.touch", contracts.LocalDeclaration{Params: contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("touch", contracts.NewMethod())},
	}))
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(touch, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"touch": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
			}}}, nil
		}, ServeOptions{})
	}, loadedOptions{Loop: loop})
	ref := mustGet(t, host, touch, jsonObject())
	_, err := ref.Call("touch", nil, background)
	must(t, err)
	turn(host)
	before := loop.count()
	for range 100 {
		_, err := ref.Call("touch", nil, background)
		must(t, err)
	}
	turn(host)
	if perCall := float64(loop.count()-before) / 100; perCall > 2 {
		t.Fatalf("microtasks per call = %v, want at most 2", perCall)
	}
	closeHosts(t, host)
}

// --- remote variants -------------------------------------------------------------------------------------------------------

var attemptTwo = mustContract(contracts.Remote("app.two", contracts.RemoteDeclaration{Params: contracts.Params{},
	Version:     1,
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
	Variants: []contracts.VariantEntry{
		contracts.Var("full", contracts.VariantDeclaration{
			State:   []contracts.Property{contracts.Prop("n", contracts.Int())},
			Methods: []contracts.MethodEntry{contracts.Meth("bump", contracts.NewMethod())},
		}),
		contracts.Var("view", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}}),
	},
}))

// two is a host acting as `owner` whose factory for `Two` returns implementation.
func two(t *testing.T, implementation contracts.Implementation) (Host, *hostErrors) {
	t.Helper()
	errs := &hostErrors{}
	address := Server.Address(nil)
	host := newHost(t, HostOptions{Address: &address, Principal: ownerPrincipal(), OnError: errs.push})
	must(t, host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(attemptTwo, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
			return implementation, nil
		}, ServeOptions{})
	})}}, background))
	return host, errs
}

func TestAttemptRemoteVariantsTheFactoryChoosesOneTheReferenceNarrowsOnItsVariant(t *testing.T) {
	host, _ := two(t, contracts.Implementation{Variant: "view", Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(1.0)},
	}})
	ref := mustGet(t, host, attemptTwo, jsonObject())
	variant, bound, err := ref.Variant()
	must(t, err)
	if variant != "view" || !bound {
		t.Fatalf("variant = %q (bound %v), want view", variant, bound)
	}
	if n, err := ref.Member("n").Value(); err != nil || n != 1.0 {
		t.Fatalf("n = %v, %v, want 1", n, err)
	}
	closeHosts(t, host)
}

func TestAttemptRemoteVariantsFailTheActivationBroken(t *testing.T) {
	for _, each := range []struct {
		name           string
		implementation contracts.Implementation
		message        string
	}{
		{"an unknown variant", contracts.Implementation{Variant: "other"}, `variant "other": not a variant of app.two`},
		{"a missing member", contracts.Implementation{Variant: "full", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(1.0)},
		}}, "bump: a method is a function"},
		{"an extra member", contracts.Implementation{Variant: "view", Members: contracts.VariantImplementation{
			State:   map[string]chord.Watchable[delta.JsonValue]{"n": constant(1.0)},
			Methods: map[string]contracts.MethodFunction{"bump": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }},
		}}, "bump: not a member"},
	} {
		t.Run(each.name, func(t *testing.T) {
			host, errs := two(t, each.implementation)
			_, err := host.Get(attemptTwo, jsonObject(), background, GetOptions{})
			wantChordError(t, err, data.CodeUnavailable, "", "app.two: "+each.message)
			if got := failures(errs.All()); !reflect.DeepEqual(got, []string{"app.two: broken"}) {
				t.Fatalf("failures = %v", got)
			}
		})
	}
}

func TestAttemptRemoteVariantsAMemberOutsideTheBoundVariantFailsInvalidMember(t *testing.T) {
	host, _ := two(t, contracts.Implementation{Variant: "view", Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(1.0)},
	}})
	ref := mustGet(t, host, attemptTwo, jsonObject())
	_, err := ref.Call("bump", nil, background)
	wantChordError(t, err, data.CodeInvalid, data.ReasonMember, `app.two (variant view) has no method "bump"`)
}

// --- tables ---------------------------------------------------------------------------------------------------------------

var attemptStoreType = mustContract(contracts.Local("app.store", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
}))

func attemptEdits(t testing.TB) *store.TableDefinition {
	t.Helper()
	edits, err := store.DefineTable("edits", store.TableOptions{
		Version: 1,
		Columns: []store.ColumnProp{store.Col("id", store.Str()), store.Col("at", store.Int())},
		Key:     []string{"id"},
		Indexes: store.IndexRecords{{Name: "by_at", Columns: []string{"at"}}},
	})
	must(t, err)
	return edits
}

func storeImplementation() contracts.Implementation {
	return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(1.0)}}}
}

func TestAttemptTablesAFreshTableIsCreatedAtActivationAtItsVersionWithItsIndexesInOneCommit(t *testing.T) {
	chordStore := memoryStore(t, true)
	edits := attemptEdits(t)
	serve := func(h FacetHost, _ FacetSelf) error {
		return h.Serve(attemptStoreType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.(StoreSelf).Table(edits)
			return storeImplementation(), nil
		}, ServeOptions{})
	}
	host, _ := loaded(t, serve, loadedOptions{Store: chordStore})
	mustGet(t, host, attemptStoreType, jsonObject())
	table := chordStore.Table("app.store:edits")
	if table == nil || table.Version != 1 || table.State != "ready" {
		t.Fatalf("table = %+v, want version 1 ready", table)
	}
	commits, err := chordStore.Outbox().Read(0, 10, background)
	must(t, err)
	var kinds [][]string
	for _, commit := range commits {
		var writes []string
		for _, write := range commit.Writes {
			writes = append(writes, write.StoreWriteKind())
		}
		kinds = append(kinds, writes)
	}
	if !reflect.DeepEqual(kinds, [][]string{{"table", "index"}}) {
		t.Fatalf("outbox writes = %v, want [[table index]]", kinds)
	}
	closeHosts(t, host)
	again, _ := loaded(t, serve, loadedOptions{Store: chordStore})
	mustGet(t, again, attemptStoreType, jsonObject())
	if got := chordStore.LastSeq(); got != 1 {
		t.Fatalf("lastSeq = %d, want 1", got)
	}
	closeHosts(t, again)
	must(t, chordStore.Close(background))
}

func TestAttemptTablesFailTheActivationBroken(t *testing.T) {
	for _, each := range []struct {
		name    string
		declare func(t *testing.T, self StoreSelf, edits *store.TableDefinition)
		message string
	}{
		{"declared twice", func(_ *testing.T, self StoreSelf, edits *store.TableDefinition) {
			self.Table(edits)
			self.Table(edits)
		}, `table "edits" is declared twice`},
		{"another version recorded", func(t *testing.T, self StoreSelf, _ *store.TableDefinition) {
			other, err := store.DefineTable("edits", store.TableOptions{Version: 2, Columns: []store.ColumnProp{store.Col("id", store.Str())}, Key: []string{"id"}})
			must(t, err)
			self.Table(other)
		}, "older than the history's base 2"},
		{"another definition at the same version", func(t *testing.T, self StoreSelf, _ *store.TableDefinition) {
			other, err := store.DefineTable("edits", store.TableOptions{
				Version: 1, Columns: []store.ColumnProp{store.Col("id", store.Str()), store.Col("at", store.Int())}, Key: []string{"id"},
			})
			must(t, err)
			self.Table(other)
		}, "recorded with another definition"},
	} {
		t.Run(each.name, func(t *testing.T) {
			chordStore := memoryStore(t)
			edits := attemptEdits(t)
			first, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
				return h.Serve(attemptStoreType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
					self.(StoreSelf).Table(edits)
					return storeImplementation(), nil
				}, ServeOptions{})
			}, loadedOptions{Store: chordStore})
			mustGet(t, first, attemptStoreType, jsonObject())
			closeHosts(t, first)
			host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
				return h.Serve(attemptStoreType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
					each.declare(t, self.(StoreSelf), edits)
					return storeImplementation(), nil
				}, ServeOptions{})
			}, loadedOptions{Store: chordStore})
			_, err := host.Get(attemptStoreType, jsonObject(), background, GetOptions{})
			if err == nil || !hasText(err.Error(), each.message) {
				t.Fatalf("get = %v, want a message containing %q", err, each.message)
			}
			closeHosts(t, host)
			must(t, chordStore.Close(background))
		})
	}
}

func hasText(text, part string) bool { return strings.Contains(text, part) }

func TestAttemptTablesNeedALocalTypeWithEmptyParamsP3AndAHostWithAStore(t *testing.T) {
	edits := attemptEdits(t)
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(attemptPage, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.(StoreSelf).Table(edits)
			return contracts.Implementation{}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(attemptStoreType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.(StoreSelf).Table(edits)
			return storeImplementation(), nil
		}, ServeOptions{})
	}, loadedOptions{})
	_, err := host.Get(attemptPage, jsonObject("id", 1.0), background, GetOptions{})
	if err == nil || !hasText(err.Error(), "persist needs a stable identity") {
		t.Fatalf("page get = %v", err)
	}
	_, err = host.Get(attemptStoreType, jsonObject(), background, GetOptions{})
	if err == nil || !hasText(err.Error(), "persist needs a store") {
		t.Fatalf("store get = %v", err)
	}
	closeHosts(t, host)
}

// --- eager migrations ---------------------------------------------------------------------------------------------------------

const migrationTable = "app.store:edits"

func editsV1(t testing.TB) *store.TableDefinition {
	t.Helper()
	definition, err := store.DefineTable("edits", store.TableOptions{
		Version: 1, Columns: []store.ColumnProp{store.Col("id", store.Str()), store.Col("at", store.Int())}, Key: []string{"id"},
	})
	must(t, err)
	return definition
}

// seeded is a store whose edits table is at version 1 with count rows.
func seeded(t *testing.T, count int) *store.ChordStore {
	t.Helper()
	chordStore := memoryStore(t)
	definition := editsV1(t)
	must(t, store.PrepareTables([]store.DeclaredTable{{Definition: definition, Table: migrationTable}}, chordStore, openFence(), "app.store", background))
	var staged []store.StagedWrite
	changes := store.NewTableChanges(store.NewTableHandle(definition, migrationTable, chordStore, tableOwner()), &staged)
	for i := range count {
		must(t, changes.Put(store.Row{"id": "e" + leftPad(i), "at": int64(i)}))
	}
	_, err := chordStore.Commit(staged, openFence(), background)
	must(t, err)
	return chordStore
}

func leftPad(i int) string {
	text := strconv.Itoa(i)
	for len(text) < 3 {
		text = "0" + text
	}
	return text
}

// evolved is version 2 adding `note`, whose rows runs each per row first.
func evolved(t testing.TB, each func(id string) error) *store.TableDefinition {
	t.Helper()
	definition, err := store.EvolveTable(editsV1(t), func(e *store.TableEditor) *store.TableEditor {
		return e.Add("note", store.Opt(store.Str())).Rows(func(row store.Row) (any, error) {
			if err := each(row["id"].(string)); err != nil {
				return nil, err
			}
			return store.Row{"note": "#" + strconv.FormatInt(row["at"].(int64), 10)}, nil
		})
	})
	must(t, err)
	return definition
}

func TestAttemptEagerMigrationsPauseTheReadyTimeoutDeadlineAndResumeItWithTheBudgetLeft(t *testing.T) {
	loop := newScopeLoop()
	chordStore := seeded(t, 250)
	forever := make(chan struct{})
	defer close(forever)
	// Each row takes 10 ms of the fake clock: 2.5 s of migration against a 1 s deadline.
	edits := evolved(t, func(string) error { loop.Advance(10); return nil })
	ready := time.Second
	loading := make(chan struct {
		host Host
		errs *hostErrors
	}, 1)
	go func() {
		host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
			return h.Serve(attemptStoreType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				self.(StoreSelf).Table(edits)
				// The factory takes 600 ms of the budget; the native after the migration never opens.
				loop.Advance(600)
				self.Open(func(ctx context.Context) (any, error) {
					select {
					case <-ctx.Done():
						return nil, errors.New("aborted")
					case <-forever:
						return nil, nil
					}
				}, func(any, NativeCloseReason, context.Context) error { return nil })
				return storeImplementation(), nil
			}, ServeOptions{})
		}, loadedOptions{Store: chordStore, ReadyTimeout: &ready, Loop: loop})
		loading <- struct {
			host Host
			errs *hostErrors
		}{host, errs}
	}()
	until(t, "the table to be migrated", func() bool {
		table := chordStore.Table(migrationTable)
		return table != nil && table.Version == 2 && table.State == "ready"
	})
	if got := loop.Now(); got != 3_100 {
		t.Fatalf("now = %d, want 3100", got)
	}
	// 399 ms then 1 ms of the budget left.
	loop.AdvanceAsync(399)
	loop.AdvanceAsync(1)
	result := receive(t, loading)
	if got := loop.Now(); got != 3_500 {
		t.Fatalf("now = %d, want 3500", got)
	}
	all := result.errs.All()
	if len(all) != 1 || all[0].Kind != ErrActivationFailed || all[0].Failure.Kind != data.FailureTransient ||
		all[0].Failure.Error.Message != "app.store: did not become live within 1000 ms" {
		t.Fatalf("errors = %+v, want one transient not-live report", all)
	}
	closeHosts(t, result.host)
	must(t, chordStore.Close(background))
}

func TestAttemptEagerMigrationsATimerFiringDuringALongMigrationAnotherActivationsDeadlineKeepsItsBound(t *testing.T) {
	loop := newScopeLoop()
	chordStore := seeded(t, 250)
	forever := make(chan struct{})
	defer close(forever)
	edits := evolved(t, func(string) error { loop.Advance(10); return nil })
	slow := mustContract(contracts.Local("app.slow", contracts.LocalDeclaration{Params: contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	ready := time.Second
	// Both types have empty params: the host activates both at load, the start barrier waiting for both.
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(slow, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(ctx context.Context) (any, error) {
				select {
				case <-ctx.Done():
					return nil, errors.New("aborted")
				case <-forever:
					return nil, nil
				}
			}, func(any, NativeCloseReason, context.Context) error { return nil })
			return storeImplementation(), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(attemptStoreType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.(StoreSelf).Table(edits)
			return storeImplementation(), nil
		}, ServeOptions{})
	}, loadedOptions{Store: chordStore, ReadyTimeout: &ready, Loop: loop})
	if got := loop.Now(); got != 2_500 {
		t.Fatalf("now = %d, want 2500", got)
	}
	all := errs.All()
	if len(all) != 1 || all[0].Kind != ErrActivationFailed || all[0].Object != "app.slow" || all[0].Failure.Kind != data.FailureTransient {
		t.Fatalf("errors = %+v, want one transient activation_failed of app.slow", all)
	}
	ref := mustGet(t, host, attemptStoreType, jsonObject())
	if n, err := ref.Member("n").Value(); err != nil || n != 1.0 {
		t.Fatalf("n = %v, %v, want 1", n, err)
	}
	closeHosts(t, host)
	must(t, chordStore.Close(background))
}

func TestAttemptEagerMigrationsAThrowingRowsFailsTheObjectBrokenTheTableMigratingARetryResumesAfterTheCommittedChunks(t *testing.T) {
	chordStore := seeded(t, 250)
	var crash atomic.Bool
	crash.Store(true)
	var patched strs
	edits := evolved(t, func(id string) error {
		if crash.Load() && id == "e150" {
			return errors.New("crash")
		}
		patched.add(id)
		return nil
	})
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(attemptStoreType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.(StoreSelf).Table(edits)
			return storeImplementation(), nil
		}, ServeOptions{})
	}, loadedOptions{Store: chordStore})
	held := mustHold(t, host, attemptStoreType, jsonObject())
	failed := untilKind(t, held, RefStateFailed)
	if failed.Cause.Kind != data.FailureBroken || !hasText(failed.Cause.Error.Message, "crash") {
		t.Fatalf("state = %+v, want failed broken crash", failed)
	}
	if all := errs.All(); len(all) != 1 || all[0].Kind != ErrActivationFailed || all[0].Failure.Kind != data.FailureBroken {
		t.Fatalf("errors = %+v, want one broken activation_failed", all)
	}
	if table := chordStore.Table(migrationTable); table == nil || table.Version != 2 || table.State != "migrating" {
		t.Fatalf("table = %+v, want version 2 migrating", table)
	}
	crash.Store(false)
	before := patched.len()
	held.Retry()
	untilKind(t, held, RefStateLive)
	resumed := patched.all()[before:]
	if len(resumed) != 150 || resumed[0] != "e100" {
		t.Fatalf("patched %d rows from %v, want 150 from e100", len(resumed), resumed[:min(1, len(resumed))])
	}
	if table := chordStore.Table(migrationTable); table == nil || table.Version != 2 || table.State != "ready" {
		t.Fatalf("table = %+v, want version 2 ready", table)
	}
	held.Release()
	closeHosts(t, host)
	must(t, chordStore.Close(background))
}

// --- parked attempts -------------------------------------------------------------------------------------------------------------

var (
	attemptView    = mustContract(contracts.Local("app.view", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
	attemptMissing = mustContract(contracts.Local("app.missing", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
	// attemptViewer is a view with params: no host lifetime, so `load` does not wait for it.
	attemptViewer = mustContract(contracts.Local("app.viewer", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
)

func TestAttemptParkedAttemptsRunNoTimerWhileParkedNoDependencyDeadlineNoBackoffTheFactoryRunOnce(t *testing.T) {
	loop := newScopeLoop()
	var factories atomic.Int32
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(attemptView, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			factories.Add(1)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": Expose(self.Get(attemptMissing, jsonObject()).Member("n"))},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{Loop: loop})
	view := mustHold(t, host, attemptView, jsonObject())
	turn(host)
	advanceHost(loop, host, 10*60_000)
	waiting := stateOfRef(view)
	if waiting.Kind != RefStateFailed || waiting.Cause.Kind != data.FailureWaiting || waiting.RetryAt != nil ||
		waiting.Cause.Error.Message != "waiting for app.missing: app.missing is not served here" {
		t.Fatalf("state = %+v (cause %+v), want failed waiting for app.missing", waiting, waiting.Cause)
	}
	if got := loop.TimerCount(); got != 0 {
		t.Fatalf("timers = %d, want 0", got)
	}
	if got := factories.Load(); got != 1 || errs.Len() != 0 {
		t.Fatalf("factories %d, errors %v, want 1 and none", got, errs.All())
	}
	view.Release()
}

func TestAttemptParkedAttemptsPauseTheReadyTimeoutDeadlineWhileParkedTheOwnStepsGetTheBudgetLeft(t *testing.T) {
	loop := newScopeLoop()
	slowType := mustContract(contracts.Local("app.slow", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	forever, opened := make(chan struct{}), newSignal()
	defer close(forever)
	ready := 10 * time.Second
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(slowType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(ctx context.Context) (any, error) {
				return nil, sleepOn(loop, 5*time.Second, ctx)
			}, func(any, NativeCloseReason, context.Context) error { return nil })
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(1.0)}}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(attemptViewer, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			slow := self.Get(slowType, jsonObject("id", 1.0))
			self.Open(func(ctx context.Context) (any, error) {
				opened.fire()
				select {
				case <-ctx.Done():
					return nil, errors.New("aborted")
				case <-forever:
					return nil, nil
				}
			}, func(any, NativeCloseReason, context.Context) error { return nil })
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"n": Expose(slow.Member("n"))}}}, nil
		}, ServeOptions{})
	}, loadedOptions{Loop: loop, ReadyTimeout: &ready})
	view := mustHold(t, host, attemptViewer, jsonObject("id", 1.0))
	// Parked 5 s on its dependency; then its own open hangs: it fails 10 s after the boundary, not 10 s after start.
	until(t, "the slow dependency's sleep timer", func() bool { return hasTimerAt(loop, 5_000) })
	advanceHost(loop, host, 5_000)
	// The view goes on to its own steps with the budget it had left: its deadline runs again from here.
	opened.waitMain(t)
	advanceHost(loop, host, 9_999)
	if got := failures(errs.All()); len(got) != 0 {
		t.Fatalf("failures = %v, want none", got)
	}
	advanceHost(loop, host, 1)
	until(t, "the view's failure", func() bool { return len(failures(errs.All())) == 1 })
	if got := failures(errs.All()); !reflect.DeepEqual(got, []string{"app.viewer(1): transient"}) {
		t.Fatalf("failures = %v", got)
	}
	view.Release()
}

func TestAttemptParkedAttemptsGoOnPastTheDependencyCheckADependencyLeavingLiveDuringTheOwnStepsChangesNothingR18(t *testing.T) {
	dependencyType := mustContract(contracts.Local("app.dependency", contracts.LocalDeclaration{Params: contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	gate, opening := newSignal(), newSignal()
	var mu sync.Mutex
	var dependencySelf Self
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(dependencyType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			mu.Lock()
			dependencySelf = self
			mu.Unlock()
			return storeImplementation(), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(attemptViewer, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			dependency := self.Get(dependencyType, jsonObject())
			self.Open(func(context.Context) (any, error) {
				opening.fire()
				gate.wait(t)
				return nil, nil
			}, func(any, NativeCloseReason, context.Context) error { return nil })
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"n": Expose(dependency.Member("n"))}}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	view := mustHold(t, host, attemptViewer, jsonObject("id", 1.0))
	opening.waitMain(t)
	// The view opens its own native; its dependency fails live meanwhile.
	mu.Lock()
	failing := dependencySelf
	mu.Unlock()
	failLive(failing, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "offline"}, nil))
	turn(host)
	gate.fire()
	untilKind(t, view, RefStateLive)
	if got := failures(errs.All()); len(got) != 0 {
		t.Fatalf("failures = %v, want none", got)
	}
	view.Release()
}
