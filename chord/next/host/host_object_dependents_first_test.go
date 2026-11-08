package host

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// pi: packages/chord/test/host/object.test.ts
// Ports the describe 'reload: fates start dependents first (decision "Swaps: dependents first")' of object.test.ts (Pi
// 0c2b568fd): a reload starts the removals first, then the other fates dependents first; the report keeps the order the objects
// went live. Pi's `flushed()` (the microtasks of the loop) is `turn(host)` here, and a wait for a state is a poll of it.

// dependentsBase is `Base`: a provider with its name and `echo`, which answers it.
var dependentsBase = mustContract(declareLocal("app.base", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{},
	Members: []contracts.LocalEntry{contracts.Mem("name", contracts.Str()), contracts.Mem("echo", contracts.NewMethod(contracts.Schema{}, contracts.Str()))},
}))

// dependentsBaseV2 is `BaseV2`: a member added, so a consumer of v2 binds only the new code.
var dependentsBaseV2 = func() *contracts.Contract {
	evolved, err := contracts.Evolve(dependentsBase, func(e *contracts.LocalEditor) *contracts.LocalEditor {
		return e.AddState("extra", contracts.Str())
	})
	if err != nil {
		panic(err)
	}
	return evolved
}()

// dependentsUser is `User`: a dependent with empty params, its label over the provider's name, `ask` through `echo`.
var dependentsUser = mustContract(declareLocal("app.user", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{},
	Members: []contracts.LocalEntry{contracts.Mem("label", contracts.Str()), contracts.Mem("ask", contracts.NewMethod(contracts.Schema{}, contracts.Str()))},
}))

// dependentsViewer is `Viewer`: a remote dependent held over a link.
var dependentsViewer = mustContract(declareRemote("app.viewer", contracts.RemoteDeclaration{
	Version:     1,
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return serverAddress(), nil },
	Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{
		State:   []contracts.Property{contracts.Prop("label", contracts.Str())},
		Methods: []contracts.MethodEntry{contracts.Meth("ask", contracts.NewMethod(contracts.Schema{}, contracts.Str()))},
	})},
}))

// dependentsServeBase serves `Base` as code code; with opened, its new code opens a native that waits for it (a restart).
func dependentsServeBase(t testing.TB, h FacetHost, code string, opened signal) error {
	return h.Serve(dependentsBase, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
		if opened != nil {
			self.Open(func(ctx context.Context) (any, error) { opened.await(t, ctx); return nil, nil },
				func(any, NativeCloseReason, context.Context) error { return nil })
		}
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"name": constant(code)},
			Methods: map[string]contracts.MethodFunction{"echo": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
				return code, nil
			}},
		}}, nil
	}, ServeOptions{})
}

// dependentsOver is a dependent's members as code over base: its label over the provider's name, `ask` through `echo`.
func dependentsOver(self Self, code string, base *Reference) contracts.VariantImplementation {
	return contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{
			"label": self.Derive([]any{base.Member("name")}, func(values []any) (delta.JsonValue, error) {
				return code + " " + values[0].(string), nil
			}),
		},
		Methods: map[string]contracts.MethodFunction{"ask": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			echoed, err := base.Call("echo", nil, ctx)
			if err != nil {
				return nil, err
			}
			return code + ">" + echoed.(string), nil
		}},
	}
}

// dependentsServeUser serves `User` as code code over `Base`.
func dependentsServeUser(h FacetHost, code string) error {
	return h.Serve(dependentsUser, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
		return contracts.Implementation{Members: dependentsOver(self, code, self.Get(dependentsBase, jsonObject()))}, nil
	}, ServeOptions{})
}

func dependentsLabel(t testing.TB, ref *Reference, want string) {
	t.Helper()
	reloadUntilLabel(t, ref, want)
}

func TestReloadFatesStartDependentsFirstAPureDependentAndItsProviderInOnePluginBothSwapNoParkingItsContributionReplacedInOneBatch(t *testing.T) {
	names, err := registries.DefineRegistry[any, any]("app.user-names", registries.DefineRegistryOptions{Version: 1})
	must(t, err)
	var mu sync.Mutex
	var composed []string
	registry := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(names, registries.RegistryProvider[any, any]{
			Compose: func(table registries.RegistryTable[any], _ registries.ComposeContext) (any, error) {
				values := make([]string, len(table))
				for index, entry := range table {
					values[index] = entry.Value.(string)
				}
				mu.Lock()
				defer mu.Unlock()
				composed = append(composed, strings.Join(values, ","))
				return strings.Join(values, ","), nil
			},
		}, ServeOptions{})
	})
	app := func(code string) HostFacet {
		return HostFacet{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			must(t, dependentsServeBase(t, h, code, nil))
			return h.Serve(dependentsUser, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				self.(StoreSelf).Contribute(names, code)
				return contracts.Implementation{Members: dependentsOver(self, code, self.Get(dependentsBase, jsonObject()))}, nil
			}, ServeOptions{})
		})}
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{app("v1"), {Name: "@test/names", Facet: registry}}, background))
	user := mustGet(t, host, dependentsUser, jsonObject())
	value, err := host.Registry(background, names, GetOptions{})
	must(t, err)
	if got := value.Value(); got != "v1" {
		t.Fatalf("names = %v, want v1", got)
	}
	mu.Lock()
	composed = nil
	mu.Unlock()
	report := reloadMust(t)(host.Reload([]HostFacet{app("v2"), {Name: "@test/names", Facet: registry}}, background, ReloadOptions{}))
	// The report keeps the order of the objects: the provider first.
	reloadWantObjects(t, report, ReloadedObject{Object: "app.base", Kind: ReloadSwapped}, ReloadedObject{Object: "app.user", Kind: ReloadSwapped})
	turn(host)
	// The old contribution left in the batch the new one entered: one composition, never a table without it.
	if got := value.Value(); got != "v2" {
		t.Fatalf("names = %v, want v2", got)
	}
	mu.Lock()
	compositions := append([]string(nil), composed...)
	mu.Unlock()
	if len(compositions) != 1 || compositions[0] != "v2" {
		t.Fatalf("compositions = %v, want [v2]", compositions)
	}
	dependentsLabel(t, user, "v2 v2")
	asked, err := user.Call("ask", nil, background)
	must(t, err)
	if asked != "v2>v2" {
		t.Fatalf("ask = %v, want v2>v2", asked)
	}
}

func TestReloadFatesStartDependentsFirstARemoteDependentsHoldsNeverSeeLoadingItsCallsReachTheProvidersNewCode(t *testing.T) {
	serve := func(h FacetHost, code string) error {
		must(t, dependentsServeBase(t, h, code, nil))
		return h.Serve(dependentsViewer, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			return contracts.Implementation{Variant: "v", Members: dependentsOver(self, code, self.Get(dependentsBase, jsonObject()))}, nil
		}, ServeOptions{})
	}
	pair := linked(t, func(h FacetHost) error { return serve(h, "v1") }, linkedOptions{})
	defer closeHosts(t, pair.Client, pair.Server)
	viewer := mustGet(t, pair.Client, dependentsViewer, jsonObject())
	watch, err := refStateWatch(viewer, background)
	must(t, err)
	states := statesOf(t, watch)
	report := reloadMust(t)(reloadApp(pair.Server, func(h FacetHost) error { return serve(h, "v2") }))
	reloadWantObjects(t, report, ReloadedObject{Object: "app.base", Kind: ReloadSwapped}, ReloadedObject{Object: "app.viewer", Kind: ReloadSwapped})
	asked, err := viewer.Call("ask", nil, background)
	must(t, err)
	if asked != "v2>v2" {
		t.Fatalf("ask = %v, want v2>v2", asked)
	}
	settleLinked(pair)
	dependentsLabel(t, viewer, "v2 v2")
	if got := states.All(); len(got) != 0 {
		t.Fatalf("states = %v, want none", got)
	}
}

func TestReloadFatesStartDependentsFirstADependentWhoseNewCodeNeedsTheProvidersNewVersionParksThenGoesLiveNeverOnTheOldInstance(t *testing.T) {
	app := func(code string, next bool) func(h FacetHost) error {
		return func(h FacetHost) error {
			if next {
				must(t, h.Serve(dependentsBaseV2, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
					return contracts.Implementation{Members: contracts.VariantImplementation{
						State: map[string]chord.Watchable[delta.JsonValue]{"name": constant(code), "extra": constant("extra")},
						Methods: map[string]contracts.MethodFunction{"echo": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
							return code, nil
						}},
					}}, nil
				}, ServeOptions{}))
			} else {
				must(t, dependentsServeBase(t, h, code, nil))
			}
			return h.Serve(dependentsUser, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				if !next {
					return contracts.Implementation{Members: dependentsOver(self, code, self.Get(dependentsBase, jsonObject()))}, nil
				}
				base := self.Get(dependentsBaseV2, jsonObject())
				return contracts.Implementation{Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{
						"label": self.Derive([]any{base.Member("name"), base.Member("extra")}, func(values []any) (delta.JsonValue, error) {
							return code + " " + values[0].(string) + " " + values[1].(string), nil
						}),
					},
					Methods: map[string]contracts.MethodFunction{"ask": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						echoed, err := base.Call("echo", nil, ctx)
						if err != nil {
							return nil, err
						}
						return code + ">" + echoed.(string), nil
					}},
				}}, nil
			}, ServeOptions{})
		}
	}
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error { return app("v1", false)(h) }, loadedOptions{})
	defer closeHosts(t, host)
	user := mustGet(t, host, dependentsUser, jsonObject())
	report := reloadMust(t)(reloadApp(host, app("v2", true)))
	// Started first, the dependent's v2 request does not bind the provider still on v1 (`ChordObject.hold`): it waits unbound,
	// then binds the provider's swap and parks until its new instance is live.
	if len(report.Objects) != 2 || report.Objects[0] != (ReloadedObject{Object: "app.base", Kind: ReloadSwapped}) ||
		report.Objects[1].Object != "app.user" || report.Objects[1].Kind != ReloadParked || report.Objects[1].Cause.Kind != data.FailureWaiting ||
		!strings.Contains(report.Objects[1].Cause.Error.Message, "app.base v2 is not served here") {
		t.Fatalf("report objects = %+v", report.Objects)
	}
	untilKind(t, user, RefStateLive)
	dependentsLabel(t, user, "v2 v2 extra")
	asked, err := user.Call("ask", nil, background)
	must(t, err)
	if asked != "v2>v2" {
		t.Fatalf("ask = %v, want v2>v2", asked)
	}
	for _, each := range errs.All() {
		if each.Kind == ErrActivationFailed || each.Kind == ErrObjectFailed {
			t.Fatalf("host error %+v", each)
		}
	}
}

func TestReloadFatesStartDependentsFirstAPureDependentOfAProviderThatRestartsSwapsItsCallsParkUntilTheProvidersNewInstanceIsLive(t *testing.T) {
	opened := newSignal()
	app := func(code string, natives bool) func(h FacetHost) error {
		return func(h FacetHost) error {
			var gate signal
			if natives {
				gate = opened
			}
			must(t, dependentsServeBase(t, h, code, gate))
			return dependentsServeUser(h, code)
		}
	}
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return app("v1", false)(h) }, loadedOptions{})
	defer closeHosts(t, host)
	user := mustGet(t, host, dependentsUser, jsonObject())
	report := reloadMust(t)(reloadApp(host, app("v2", true)))
	reloadWantObjects(t, report, ReloadedObject{Object: "app.base", Kind: ReloadRestarted}, ReloadedObject{Object: "app.user", Kind: ReloadSwapped})
	// Live on its new code over the provider's last values while the provider restarts.
	untilKind(t, user, RefStateLive)
	dependentsLabel(t, user, "v2 v1")
	asked := startReloadCall(user, "ask", nil)
	turn(host)
	opened.fire()
	if got := asked.wait(t); got != "v2>v2" {
		t.Fatalf("ask = %v, want v2>v2", got)
	}
	dependentsLabel(t, user, "v2 v2")
}

func TestReloadFatesStartDependentsFirstAProviderThatWentLiveAfterItsDependentARetryAfterAFailureSwapsFirstTheDependentParksAndRecovers(t *testing.T) {
	var selves []Self
	app := func(code string) func(h FacetHost) error {
		return func(h FacetHost) error {
			must(t, h.Serve(dependentsBase, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				selves = append(selves, self)
				return contracts.Implementation{Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"name": constant(code)},
					Methods: map[string]contracts.MethodFunction{"echo": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
						return code, nil
					}},
				}}, nil
			}, ServeOptions{}))
			return dependentsServeUser(h, code)
		}
	}
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return app("v1")(h) }, loadedOptions{})
	defer closeHosts(t, host)
	user := mustGet(t, host, dependentsUser, jsonObject())
	base := mustGet(t, host, dependentsBase, jsonObject())
	failLiveThen(host, selves[len(selves)-1], data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil), func() {})
	untilKind(t, base, RefStateFailed)
	base.Retry()
	untilKind(t, base, RefStateLive)
	report := reloadMust(t)(reloadApp(host, app("v2")))
	// The objects in the order they went live: the dependent first, so the provider's swap starts before it.
	if len(report.Objects) != 2 || report.Objects[0].Object != "app.user" || report.Objects[0].Kind != ReloadParked ||
		report.Objects[0].Cause.Kind != data.FailureWaiting || report.Objects[1] != (ReloadedObject{Object: "app.base", Kind: ReloadSwapped}) {
		t.Fatalf("report objects = %+v", report.Objects)
	}
	untilKind(t, user, RefStateLive)
	dependentsLabel(t, user, "v2 v2")
	asked, err := user.Call("ask", nil, background)
	must(t, err)
	if asked != "v2>v2" {
		t.Fatalf("ask = %v, want v2>v2", asked)
	}
}
