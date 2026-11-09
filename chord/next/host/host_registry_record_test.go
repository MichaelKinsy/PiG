package host

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// pi: packages/chord/src/registries/record.ts
// pi: packages/chord/src/registries/contribution.ts

// Ports the host-run cases of packages/chord/test/registries/record.test.ts that registries/record_test.go cannot
// run on a record alone: a watched registry composed in the batch of a document commit, a ring through a derive and
// through one factory, a followed contribution whose runtime ended, and an object's contribution while it lives, fails
// and is retried.

// rvNames is a registry whose composition is its table as `plugin:type=value` strings.
var rvNames = rvRegistryOf("app.names", 1)

func rvListed(table registries.RegistryTable[any], _ registries.ComposeContext) (any, error) {
	listed := []string{}
	for _, entry := range table {
		listed = append(listed, fmt.Sprintf("%s:%s=%v", entry.Plugin, entry.Type, entry.Value))
	}
	return listed, nil
}

func rvServeNames(h FacetHost) error {
	return h.Serve(rvNames, registries.RegistryProvider[any, any]{Compose: rvListed}, ServeOptions{})
}

var rvTwoSum = rvRegistryOf("app.sum", 1)

func TestComposeAWatchedRegistryInTheBatchOfTheCommitThatChangedAContributionOncePerBatch(t *testing.T) {
	meter := nexttest.Metered(t)
	doc, err := documents.DefineState("app.names", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("a", contracts.Int()), contracts.Prop("b", contracts.Int())),
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("a", 0.0, "b", 0.0) },
	})
	must(t, err)
	two := exLocal("app.two", contracts.Params{},
		contracts.Mem("both", contracts.NewMethod(contracts.Int())), contracts.Mem("sum", contracts.Int()))
	host, _ := rvPlugins(t, HostOptions{Telemetry: meter.Telemetry}, rvPlugin("@app/sum", func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(rvTwoSum, registries.RegistryProvider[any, any]{
			Compose: func(table registries.RegistryTable[any], _ registries.ComposeContext) (any, error) {
				sum := 0.0
				for _, entry := range table {
					object := entry.Value.(*delta.JsonObject)
					sum += object.Value("a").(float64) + object.Value("b").(float64)
				}
				return sum, nil
			},
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(two, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			document := stateOf(self, doc)
			self.(StoreSelf).Contribute(rvTwoSum, document.Member())
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"sum": self.Derive([]any{self.Registry(rvTwoSum)}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
			}, map[string]contracts.MethodFunction{
				"both": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					_, err := self.Tx(func(tx documents.Tx) (any, error) {
						draft, err := tx.Draft(document.s)
						if err != nil {
							return nil, err
						}
						if err := draft.Set("a", args); err != nil {
							return nil, err
						}
						return nil, draft.Set("b", args)
					}, ctx)
					return nil, err
				},
			}), nil
		}, ServeOptions{})
	}))
	ref := mustGet(t, host, two, jsonObject())
	var mu sync.Mutex
	var sums []any
	watch, err := ref.Member("sum").Watch(background)
	must(t, err)
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind == chord.WatchCallValue {
			mu.Lock()
			sums = append(sums, call.Value)
			mu.Unlock()
		}
		return nil
	})
	seen := func() []any {
		mu.Lock()
		defer mu.Unlock()
		return append([]any(nil), sums...)
	}
	// The watch's start value is the sum before the commit.
	base := meter.Counts()["chord.registry.composes"]
	before := len(seen())
	exCall(t, ref, "both", 2.0)
	scopeEventually(t, "the derive's frame", func() bool { return len(seen()) > before })
	nexttest.Delivered()
	// One commit, both fields: one compose in its batch, one frame of the derive over it.
	if got, want := seen()[before:], []any{4.0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sums = %v, want %v", got, want)
	}
	if got := meter.Counts()["chord.registry.composes"]; got != base+1 {
		t.Fatalf("composes = %v, want %v", got, base+1)
	}
	must(t, host.Close(background))
}

func TestContributionsACatchesARingThroughADeriveAndOneDeclaredWithinOneFactoryTheSameWay(t *testing.T) {
	a := rvRegistryOf("app.both-a", 1)
	b := rvRegistryOf("app.both-b", 1)
	both := mustContract(contracts.Local("app.both", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{}}))
	length := func(table registries.RegistryTable[any], _ registries.ComposeContext) (any, error) {
		return float64(len(table)), nil
	}
	host, errs := rvPlugins(t, HostOptions{}, rvPlugin("@app/both", func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(a, registries.RegistryProvider[any, any]{Compose: length}, ServeOptions{}); err != nil {
			return err
		}
		if err := h.Serve(b, registries.RegistryProvider[any, any]{Compose: length}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(both, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			store := self.(StoreSelf)
			store.Contribute(a, self.Registry(b))
			store.Contribute(b, self.Derive([]any{self.Registry(a)}, func(values []any) (delta.JsonValue, error) { return values[0], nil }))
			return contracts.Implementation{}, nil
		}, ServeOptions{})
	}))
	ref := mustGet(t, host, both, jsonObject())
	if state := refStateOf(ref); state.Kind != RefStateLive {
		t.Fatalf("state = %+v, want live", state)
	}
	_, err := host.Registry(background, a, GetOptions{})
	must(t, err)
	_, err = host.Registry(background, b, GetOptions{})
	must(t, err)
	cut := 0
	for _, reported := range errs.All() {
		if reported.Kind != HostErrorKind(registries.ErrorContributionFailed) {
			continue
		}
		cut++
		if chordErr, ok := data.IsChordErrorCode(reported.Registry.Error, data.CodeInvalid); !ok || chordErr.Reason != data.ReasonCycle {
			t.Fatalf("contribution_failed error = %v, want invalid/cycle", reported.Registry.Error)
		}
	}
	if cut == 0 {
		t.Fatalf("errors = %+v, want a contribution cut", errs.All())
	}
	if got := failures(errs.All()); len(got) != 0 {
		t.Fatalf("failures = %v, want none", got)
	}
	must(t, host.Close(background))
}

func TestContributionsAFollowedContributionWhoseRuntimeEndedItsObserverStaysWithItsLastValueR4TheSourceKeepsIt(t *testing.T) {
	follower := mustContract(contracts.Local("app.follower", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{}}))
	observed := make(chan FollowObserver[any], 1)
	host, errs := rvPlugins(t, HostOptions{}, rvPlugin("@app/a", func(h FacetHost, _ FacetSelf) error {
		if err := rvServeNames(h); err != nil {
			return err
		}
		return h.Serve(follower, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.(StoreSelf).Contribute(rvNames, self.Follow(func(observer FollowObserver[any], _ context.Context) (*FollowAttachment[delta.JsonValue], error) {
				observed <- observer
				return &FollowAttachment[delta.JsonValue]{Value: "followed", Detach: func() {}}, nil
			}))
			return contracts.Implementation{}, nil
		}, ServeOptions{})
	}))
	names, err := host.Registry(background, rvNames, GetOptions{})
	must(t, err)
	want := []string{"@app/a:app.follower=followed"}
	if got, _ := rvRead(names); !reflect.DeepEqual(got, want) {
		t.Fatalf("names.value = %v, want %v", got, want)
	}
	observer := <-observed
	host.(*chordHost).enter(func() { observer.End(chord.WatchEnd{Kind: chord.WatchEnded}) })
	turn(host)
	if got, _ := rvRead(names); !reflect.DeepEqual(got, want) {
		t.Fatalf("names.value after the end = %v, want %v", got, want)
	}
	for _, reported := range errs.All() {
		if reported.Kind == HostErrorKind(registries.ErrorContributionFailed) {
			t.Fatalf("errors = %+v, want no contribution_failed", errs.All())
		}
	}
	must(t, host.Close(background))
}

func TestContributionsAnObjectsContributionIsInTheTableFromLiveUntilItFailsAgainOnceRetriedUntilItEnds(t *testing.T) {
	run := storeRestartable(t, func(attempt int, self StoreSelf) {
		self.Contribute(rvNames, fmt.Sprintf("attempt %d", attempt))
	}, loadedOptions{Serve: func(h FacetHost) { must(t, rvServeNames(h)) }})
	names, err := run.Host.Registry(background, rvNames, GetOptions{})
	must(t, err)
	read := func() any {
		got, thrown := rvRead(names)
		if thrown != nil {
			t.Fatal(thrown)
		}
		return got
	}
	if got, want := read(), []string{"@test/app:app.store=attempt 1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("names.value = %v, want %v", got, want)
	}
	run.Fail()
	if got, want := read(), []string{}; !reflect.DeepEqual(got, want) {
		t.Fatalf("names.value after the failure = %v, want %v", got, want)
	}
	run.Retry()
	if got, want := read(), []string{"@test/app:app.store=attempt 2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("names.value after the retry = %v, want %v", got, want)
	}
	must(t, run.Host.Close(background))
	// Closed: nothing composes any more; a value a reader keeps reads the last composition.
	if got, want := read(), []string{"@test/app:app.store=attempt 2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("names.value after the close = %v, want %v", got, want)
	}
}

func TestComposeBeforeAnyGoodCompositionTheValueThrowsTheErrorAndHostRegistryRejectsWithItAtItsReadyTimeout(t *testing.T) {
	broken := rvRegistryOf("app.broken", 1)
	host, errs := rvPlugins(t, HostOptions{}, rvPlugin("@app/broken", func(h FacetHost, _ FacetSelf) error {
		return h.Serve(broken, registries.RegistryProvider[any, any]{
			Compose: func(registries.RegistryTable[any], registries.ComposeContext) (any, error) {
				return nil, errors.New("bug")
			},
		}, ServeOptions{})
	}))
	_, err := host.Registry(background, broken, rvWithin(5*time.Millisecond))
	if err == nil || err.Error() != "bug" {
		t.Fatalf("host.registry = %v, want bug", err)
	}
	var kinds []HostErrorKind
	for _, reported := range errs.All() {
		kinds = append(kinds, reported.Kind)
	}
	if want := []HostErrorKind{"registry_failed"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("error kinds = %v, want %v", kinds, want)
	}
	must(t, host.Close(background))
}

// rvServed is a registry its plugin serves: never listed as undeclared.
var rvServed = rvRegistryOf("app.served", 1)

// Ports record.test.ts `describe("registries without a provider (slice 12 §6, testPlugin)")`.
func TestRegistriesWithoutAProviderListsARegistryWithContributionsAndNoProviderWithTheNewestVersionContributed(t *testing.T) {
	older := rvRegistryOf("app.names", 1)
	newer := rvRegistryOf("app.names", 3)
	host, _ := rvPlugins(t, HostOptions{},
		rvPlugin("a", func(_ FacetHost, self FacetSelf) error { return self.Contribute(older, "a") }),
		rvPlugin("b", func(_ FacetHost, self FacetSelf) error { return self.Contribute(newer, "b") }),
		rvPlugin("c", func(h FacetHost, _ FacetSelf) error {
			return h.Serve(rvServed, registries.RegistryProvider[any, any]{
				Compose: func(registries.RegistryTable[any], registries.ComposeContext) (any, error) { return []string{}, nil },
			}, ServeOptions{})
		}),
	)
	got := host.(*chordHost).UndeclaredRegistries()
	if want := []UndeclaredRegistry{{Name: "app.names", Version: 3}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("undeclaredRegistries = %v, want %v", got, want)
	}
	must(t, host.Close(background))
}

// Ports record.test.ts `describe("compose")` > `it("composes every served registry once at the end of load's setups, an
// empty table too, before any object activates")`.
func TestComposeEveryServedRegistryOnceAtTheEndOfLoadsSetupsAnEmptyTableTooBeforeAnyObjectActivates(t *testing.T) {
	meter := nexttest.Metered(t)
	other := rvRegistryOf("app.other", 1)
	reader := exLocal("app.reader", contracts.Params{}, contracts.Mem("seen", contracts.Int()))
	var mu sync.Mutex
	var seen any
	// activated is the objects activated when a compose ran: the title's "before any object activates".
	var activations, activatedAtCompose atomic.Int64
	host, _ := rvPlugins(t, HostOptions{Telemetry: meter.Telemetry}, rvPlugin("@app/provider", func(h FacetHost, self FacetSelf) error {
		if err := h.Serve(rvNames, registries.RegistryProvider[any, any]{
			Compose: func(table registries.RegistryTable[any], context registries.ComposeContext) (any, error) {
				activatedAtCompose.Add(activations.Load())
				return rvListed(table, context)
			},
		}, ServeOptions{}); err != nil {
			return err
		}
		if err := h.Serve(other, registries.RegistryProvider[any, any]{
			Compose: func(table registries.RegistryTable[any], _ registries.ComposeContext) (any, error) {
				activatedAtCompose.Add(activations.Load())
				return float64(len(table)), nil
			},
		}, ServeOptions{}); err != nil {
			return err
		}
		if err := self.Contribute(rvNames, "a"); err != nil {
			return err
		}
		return h.Serve(reader, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			activations.Add(1)
			names := self.Registry(rvNames)
			self.Open(func(context.Context) (any, error) {
				got, thrown := rvRead(names)
				if thrown != nil {
					return nil, thrown
				}
				mu.Lock()
				seen = got
				mu.Unlock()
				return nil, nil
			}, func(any, NativeCloseReason, context.Context) error { return nil })
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"seen": self.Derive([]any{self.Registry(other)}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
			}, nil), nil
		}, ServeOptions{})
	}))
	mu.Lock()
	got := seen
	mu.Unlock()
	if want := []string{"@app/provider:=a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("seen = %v, want %v", got, want)
	}
	if got := rvMember(t, mustGet(t, host, reader, jsonObject()), "seen"); got != 0.0 {
		t.Fatalf("Reader.seen = %v, want 0", got)
	}
	if got := meter.Counts()["chord.registry.composes"]; got != 2 {
		t.Fatalf("composes = %v, want 2", got)
	}
	if got := activatedAtCompose.Load(); got != 0 {
		t.Fatalf("composes ran after %d object activations, want both before any", got)
	}
	must(t, host.Close(background))
}

// Ports record.test.ts `describe("the last good composition (decision 5)")` > `it("is dropped when a contribution it was
// built from leaves: `.value` throws until `compose` succeeds")`.
func TestTheLastGoodCompositionIsDroppedWhenAContributionItWasBuiltFromLeavesValueThrowsUntilComposeSucceeds(t *testing.T) {
	count := rvRegistryOf("app.count", 1)
	run := storeRestartable(t, func(attempt int, self StoreSelf) {
		self.Contribute(count, float64(attempt))
	}, loadedOptions{Serve: func(h FacetHost) {
		must(t, h.Serve(count, registries.RegistryProvider[any, any]{
			Compose: func(table registries.RegistryTable[any], _ registries.ComposeContext) (any, error) {
				if len(table) == 0 {
					return nil, errors.New("no contributor")
				}
				return table[0].Value, nil
			},
		}, ServeOptions{}))
	}})
	value, err := run.Host.Registry(background, count, GetOptions{})
	must(t, err)
	if got, thrown := rvRead(value); thrown != nil || got != 1.0 {
		t.Fatalf("count.value = %v (%v), want 1", got, thrown)
	}
	// Its contributor failed: the last good value was built from its contribution (a native may have closed with it).
	run.Fail()
	if got, thrown := rvRead(value); thrown == nil || thrown.Error() != "no contributor" {
		t.Fatalf("count.value after the failure = %v (%v), want a throw of no contributor", got, thrown)
	}
	run.Retry()
	if got, thrown := rvRead(value); thrown != nil || got != 2.0 {
		t.Fatalf("count.value after the retry = %v (%v), want 2", got, thrown)
	}
	must(t, run.Host.Close(background))
}
