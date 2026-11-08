package host

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// pi: packages/chord/test/host/old-work.test.ts, packages/chord/test/host/reload-cycle.test.ts, packages/chord/test/host/object.test.ts
// Ports old work's bookkeeping (the call chain walk, its guard outside reloads, another host's old work, the chain's attempts
// for reload cycles), the reload cycles of rule 6 (a wait of old work on its own drain fails at once with
// `unavailable/reload_cycle`), and the "old work finishes on old code" describe of object.test.ts: the shapes of old work (a
// provider's running call reaching its dependent through `ctx.get` while both restart, chains, re-entry, mutual drains, raised
// versions, a dropped version, a created object, a root with and without the method's ctx, a routed call joining the drain).
//
// Forced Go differences. Pi's chain nodes are plain objects; here a node is a callNode with `on` an oldWorkSource, which the
// tests make from a draining flag. Pi awaits a handler's gate promise; a Go handler waits on a reloadGate's signal, and a test
// learns the call is there from its `entered`.

// --- OldWork ------------------------------------------------------------------------------------------------------------------

// owSource is an OldWorkSource with a settable `draining`.
type owSource struct{ drains bool }

func (s *owSource) draining() bool { return s.drains }

// owNode is a link of a chain: its source and a count of the `up` steps taken through it.
type owNode struct {
	source *owSource
	next   callNode
	steps  *int
}

func (n *owNode) on() oldWorkSource { return n.source }
func (n *owNode) up() callNode {
	*n.steps++
	return n.next
}

// owChain is a context with a chain of nodes on sources, innermost first, and the count of `up` steps taken through it.
func owChain(sources ...*owSource) (context.Context, *int) {
	steps := new(int)
	var node callNode
	for _, source := range slices.Backward(sources) {
		node = &owNode{source: source, next: node, steps: steps}
	}
	if node == nil {
		return background, steps
	}
	return chordctx.WithValue(background, callChainKey, node), steps
}

func TestOldWorkOfFindsADrainingAttemptAnywhereUpTheCallChain(t *testing.T) {
	var work oldWork
	work.open()
	deep, _ := owChain(&owSource{}, &owSource{}, &owSource{drains: true})
	shallow, _ := owChain(&owSource{}, &owSource{})
	if !work.of(deep) || work.of(shallow) || work.of(background) {
		t.Fatalf("of(deep, shallow, none) = %v, %v, %v, want true, false, false", work.of(deep), work.of(shallow), work.of(background))
	}
}

func TestOldWorkOfWalksNoChainWhileTheHostHasNoOldWorkOpenNoCostOutsideReloads(t *testing.T) {
	var work oldWork
	calls, steps := owChain(&owSource{}, &owSource{drains: true})
	if work.of(calls) || *steps != 0 {
		t.Fatalf("of = %v after %d steps with no old work open, want false after none", work.of(calls), *steps)
	}
	work.open()
	if !work.of(calls) || *steps != 1 {
		t.Fatalf("of = %v after %d steps with old work open, want true after one", work.of(calls), *steps)
	}
	work.close()
	if work.of(calls) || *steps != 1 {
		t.Fatalf("of = %v after %d steps once it closed, want false after the same one", work.of(calls), *steps)
	}
}

func TestAsRemoteOldWorkIsOldWorkWhileCountedWhateverChainItHad(t *testing.T) {
	var work oldWork
	chained, _ := owChain(&owSource{})
	marked := asRemoteOldWork(chained)
	if work.of(marked) {
		t.Fatalf("of(marked) = true with no old work open")
	}
	work.open()
	if !work.of(marked) {
		t.Fatalf("of(marked) = false with old work open")
	}
}

func TestAsRemoteOldWorkHasNoAttemptOfThisHostInItsChainAReloadCycleCannotPassThroughAnotherHostsDrain(t *testing.T) {
	local := &owSource{drains: true}
	if got := chainAttempts(asRemoteOldWork(background)); len(got) != 0 {
		t.Fatalf("chainAttempts(remote) = %v, want none", got)
	}
	above, _ := owChain(local)
	if got := chainAttempts(above); !reflect.DeepEqual(got, []oldWorkSource{local}) {
		t.Fatalf("chainAttempts = %v, want [local]", got)
	}
}

func TestChainAttemptsCollectsEachAttemptOnceDrainingOrNot(t *testing.T) {
	a, b := &owSource{drains: true}, &owSource{}
	calls, _ := owChain(a, b, a)
	if got := chainAttempts(calls); !reflect.DeepEqual(got, []oldWorkSource{a, b}) {
		t.Fatalf("chainAttempts = %v, want [a b]", got)
	}
	if got := chainAttempts(background); len(got) != 0 {
		t.Fatalf("chainAttempts(none) = %v, want none", got)
	}
}

// --- the call chain of a method's ctx (ctx.test.ts) ---------------------------------------------------------------------------

// --- shapes of old work -------------------------------------------------------------------------------------------------------

var (
	owProvider = mustContract(declareLocal("app.provider", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{
			contracts.Mem("work", contracts.NewMethod(contracts.Schema{}, contracts.Str())),
			contracts.Mem("x", contracts.NewMethod(contracts.Schema{}, contracts.Str())),
		},
	}))
	// owProviderV2 is `Provider` raised by a reload: a new member, `accepts` keeping version 1.
	owProviderV2 = mustContract(contracts.Evolve(owProvider, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.AddState("extra", contracts.Str()) }))
	owDependent  = mustContract(declareLocal("app.dependent", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{
			contracts.Mem("ping", contracts.NewMethod(contracts.Schema{}, contracts.Str())),
			contracts.Mem("slow", contracts.NewMethod(contracts.Schema{}, contracts.Str())),
		},
	}))
	owDependentV2 = mustContract(contracts.Evolve(owDependent, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.AddState("extra", contracts.Str()) }))
	// owThird is a third object the shapes create or chain through: it holds the provider.
	owThird = mustContract(declareLocal("app.third", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
		Members: []contracts.LocalEntry{contracts.Mem("ping", contracts.NewMethod(contracts.Schema{}, contracts.Str()))},
	}))
	owKeepsVersionOne = contracts.AcceptsTable{{Name: "1", Value: contracts.VersionMappings{}}}
)

// owShape is how a shape's plugin behaves: what `work` does after its gate, what `ping` and `slow` do, which contracts the
// reload raises ("provider", or "both").
type owShape struct {
	work  func(ctx Ctx, code string) (string, error)
	ping  func(provider *Reference, ctx Ctx, code string) (string, error)
	slow  func(provider *Reference, ctx Ctx, code string) (string, error)
	raise string
}

// owGate waits at a reloadGate (a nil one is a resolved promise).
func owGate(t testing.TB, queue *reloadGateQueue, ctx context.Context) {
	if held := queue.shift(); held != nil {
		held.entered.fire()
		held.open.await(t, ctx)
	}
}

// owPlugin is the shape's plugin at code (`v1`, then `v2` for the reload); `work` and `slow` first take a gate of gates.
func owPlugin(t testing.TB, code string, shape owShape, gates *reloadGateQueue) Facet {
	raised := code != "v1"
	provider, dependent := owProvider, owDependent
	if raised && shape.raise != "" {
		provider = owProviderV2
	}
	if raised && shape.raise == "both" {
		dependent = owDependentV2
	}
	return DefineFacet(func(h FacetHost, _ FacetSelf) error {
		state := map[string]chord.Watchable[delta.JsonValue]{}
		providerOptions, dependentOptions := ServeOptions{}, ServeOptions{}
		if provider == owProviderV2 {
			state["extra"] = constant("x")
			providerOptions.Accepts = owKeepsVersionOne
		}
		if err := h.Serve(provider, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: state,
				Methods: map[string]contracts.MethodFunction{
					"work": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						owGate(t, gates, ctx)
						return shape.work(ctx.(Ctx), code)
					},
					"x": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return code + " x", nil },
				},
			}}, nil
		}, providerOptions); err != nil {
			return err
		}
		if dependent == owDependentV2 {
			dependentOptions.Accepts = owKeepsVersionOne
		}
		if err := h.Serve(dependent, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			held := self.Get(provider, jsonObject())
			dependentState := map[string]chord.Watchable[delta.JsonValue]{}
			if dependent == owDependentV2 {
				dependentState["extra"] = constant("x")
			}
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: dependentState,
				Methods: map[string]contracts.MethodFunction{
					"ping": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						if shape.ping == nil {
							return code + " ping", nil
						}
						return shape.ping(held, ctx.(Ctx), code)
					},
					"slow": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						owGate(t, gates, ctx)
						if shape.slow == nil {
							return code + " slow", nil
						}
						return shape.slow(held, ctx.(Ctx), code)
					},
				},
			}}, nil
		}, dependentOptions); err != nil {
			return err
		}
		return h.Serve(owThird, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Get(provider, jsonObject())
			return contracts.Implementation{Members: contracts.VariantImplementation{
				Methods: map[string]contracts.MethodFunction{"ping": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return code + " third", nil }},
			}}, nil
		}, ServeOptions{})
	})
}

// owCall is `(await (await ctx.get(type, params)).method(ctx))`.
func owCall(ctx Ctx, typ *contracts.Contract, params *delta.JsonObject, method string) (string, error) {
	ref, err := ctx.Get(typ, params, GetOptions{})
	if err != nil {
		return "", err
	}
	value, err := ref.Call(method, nil, ctx)
	if err != nil {
		return "", err
	}
	text, _ := value.(string)
	return text, nil
}

// owHost is a host loaded with the shape at v1.
func owHost(t testing.TB, shape owShape, gates *reloadGateQueue, options HostOptions) Host {
	options.OnError = func(HostError) {}
	host := newHost(t, options)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: owPlugin(t, "v1", shape, gates)}}, background))
	return host
}

// owReload reloads the shape's plugin to v2.
func owReload(t testing.TB, host Host, shape owShape, gates *reloadGateQueue) ReloadReport {
	t.Helper()
	return reloadMust(t)(host.Reload([]HostFacet{{Name: "@test/app", Facet: owPlugin(t, "v2", shape, gates)}}, background, ReloadOptions{}))
}

// owRun loads the shape, starts the provider's `work` (held at a gate), reloads to `v2` and opens the gate: the answer of
// `work` (or "rejected: " and its error's message) and how long it took after the gate opened.
func owRun(t testing.TB, shape owShape) (string, time.Duration) {
	t.Helper()
	gate := newReloadGate()
	queue := reloadGates()
	host := owHost(t, shape, queue, HostOptions{})
	defer closeHosts(t, host)
	provider := mustGet(t, host, owProvider, jsonObject())
	mustGet(t, host, owDependent, jsonObject())
	queue.gates = append(queue.gates, gate)
	work := startReloadCall(provider, "work", nil)
	gate.entered.wait(t)
	owReload(t, host, shape, queue)
	opened := time.Now()
	gate.open.fire()
	return owAnswer(t, work), time.Since(opened)
}

// owAnswer is the call's answer, or "rejected: " and its error's message.
func owAnswer(t testing.TB, call *reloadCall) string {
	t.Helper()
	outcome := receive(t, call.done)
	if outcome.err != nil {
		return "rejected: " + outcome.err.Error()
	}
	text, _ := outcome.value.(string)
	return text
}

func TestOldWorkAProvidersRunningCallReachesItsDependentThroughCtxGetWhileBothRestartNoStall(t *testing.T) {
	answer, took := owRun(t, owShape{work: func(ctx Ctx, code string) (string, error) {
		got, err := owCall(ctx, owDependent, jsonObject(), "ping")
		return code + " work -> " + got, err
	}})
	// The dependent's new attempt binds the provider's open old attempt (rule 5) and goes live.
	if answer != "v1 work -> v2 ping" || took > time.Second {
		t.Fatalf("answer = %q after %v, want \"v1 work -> v2 ping\" within a second", answer, took)
	}
}

func TestOldWorkAChainOfThreeTheWorkReachesAnObjectWhoseNewAttemptNeedsTheDependentWhichNeedsTheProvider(t *testing.T) {
	answer, _ := owRun(t, owShape{work: func(ctx Ctx, code string) (string, error) {
		got, err := owCall(ctx, owThird, jsonObject("id", "a"), "ping")
		return code + " work -> " + got, err
	}})
	if answer != "v1 work -> v2 third" {
		t.Fatalf("answer = %q, want \"v1 work -> v2 third\"", answer)
	}
}

func TestOldWorkReEntryWorkDependentTheProviderAgainRunsOnTheProvidersOldAttempt(t *testing.T) {
	answer, _ := owRun(t, owShape{
		work: func(ctx Ctx, code string) (string, error) {
			got, err := owCall(ctx, owDependent, jsonObject(), "ping")
			return code + " work -> " + got, err
		},
		ping: func(provider *Reference, ctx Ctx, code string) (string, error) {
			value, err := provider.Call("x", nil, ctx)
			text, _ := value.(string)
			return code + " ping -> " + text, err
		},
	})
	if answer != "v1 work -> v2 ping -> v1 x" {
		t.Fatalf("answer = %q, want \"v1 work -> v2 ping -> v1 x\"", answer)
	}
}

func TestOldWorkSelfReEntryWorkCtxGetOfItsOwnObjectRunsOnItsOldAttempt(t *testing.T) {
	answer, _ := owRun(t, owShape{work: func(ctx Ctx, code string) (string, error) {
		got, err := owCall(ctx, owProvider, jsonObject(), "x")
		return code + " work -> " + got, err
	}})
	if answer != "v1 work -> v1 x" {
		t.Fatalf("answer = %q, want \"v1 work -> v1 x\"", answer)
	}
}

func TestOldWorkMutualDrainsTheProvidersCallReachesTheDependentWhileTheDependentsCallReachesTheProvider(t *testing.T) {
	shape := owShape{
		work: func(ctx Ctx, code string) (string, error) {
			got, err := owCall(ctx, owDependent, jsonObject(), "ping")
			return code + " work -> " + got, err
		},
		slow: func(provider *Reference, ctx Ctx, code string) (string, error) {
			value, err := provider.Call("x", nil, ctx)
			text, _ := value.(string)
			return code + " slow -> " + text, err
		},
	}
	first, second := newReloadGate(), newReloadGate()
	queue := reloadGates(first, second)
	host := owHost(t, shape, queue, HostOptions{})
	defer closeHosts(t, host)
	provider := mustGet(t, host, owProvider, jsonObject())
	dependent := mustGet(t, host, owDependent, jsonObject())
	work := startReloadCall(provider, "work", nil)
	first.entered.wait(t)
	slow := startReloadCall(dependent, "slow", nil)
	second.entered.wait(t)
	owReload(t, host, shape, queue)
	// The provider's call holds its old attempt open until the dependent's call has had its answer.
	second.open.fire()
	if got := owAnswer(t, slow); got != "v1 slow -> v1 x" {
		t.Fatalf("slow = %q, want \"v1 slow -> v1 x\"", got)
	}
	first.open.fire()
	if got := owAnswer(t, work); got != "v1 work -> v1 ping" && got != "v1 work -> v2 ping" {
		t.Fatalf("work = %q, want \"v1 work -> v1 ping\" or \"v1 work -> v2 ping\"", got)
	}
}

func TestOldWorkTheReloadRaisesTheProvidersVersionAndTheDependentsNewCodeNeedsItTheWorkRunsOnTheDependentsOldAttempt(t *testing.T) {
	answer, took := owRun(t, owShape{raise: "provider", work: func(ctx Ctx, code string) (string, error) {
		got, err := owCall(ctx, owDependent, jsonObject(), "ping")
		return code + " work -> " + got, err
	}})
	// The dependent's successor waits for the provider's: its old attempt stays open for old work (rule 3).
	if answer != "v1 work -> v1 ping" || took > time.Second {
		t.Fatalf("answer = %q after %v, want \"v1 work -> v1 ping\" within a second", answer, took)
	}
}

func TestOldWorkBothVersionsRaisedWithAcceptsOldWorksGetterAtTheOldVersionBindsTheDependentsOpenOldAttempt(t *testing.T) {
	answer, _ := owRun(t, owShape{raise: "both", work: func(ctx Ctx, code string) (string, error) {
		got, err := owCall(ctx, owDependent, jsonObject(), "ping")
		return code + " work -> " + got, err
	}})
	if answer != "v1 work -> v1 ping" {
		t.Fatalf("answer = %q, want \"v1 work -> v1 ping\"", answer)
	}
}

func TestOldWorkAskingForAVersionTheNewCodeDroppedWithNoOldAttemptServingItFailsAtOnce(t *testing.T) {
	dropped := mustContract(declareLocal("app.dropped", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("ping", contracts.NewMethod(contracts.Schema{}, contracts.Str()))}}))
	raised := mustContract(declareLocal("app.dropped", contracts.LocalDeclaration{Version: 2, Params: contracts.Params{}, Members: []contracts.LocalEntry{
		contracts.Mem("ping", contracts.NewMethod(contracts.Schema{}, contracts.Str())), contracts.Mem("n", contracts.Int()),
	}}))
	gate := newReloadGate()
	app := func(code string) Facet {
		return DefineFacet(func(h FacetHost, _ FacetSelf) error {
			if err := h.Serve(owProvider, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
				return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
					"work": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						gate.entered.fire()
						gate.open.await(t, ctx)
						got, err := owCall(ctx.(Ctx), dropped, jsonObject(), "ping")
						return got, err
					},
					"x": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return code, nil },
				}}}, nil
			}, ServeOptions{}); err != nil {
				return err
			}
			if code == "v1" {
				return h.Serve(dropped, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
					return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
						"ping": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return "v1 ping", nil },
					}}}, nil
				}, ServeOptions{})
			}
			return h.Serve(raised, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
				return contracts.Implementation{Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(0.0)},
					Methods: map[string]contracts.MethodFunction{
						"ping": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return "v2 ping", nil },
					},
				}}, nil
			}, ServeOptions{})
		})
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: app("v1")}}, background))
	provider := mustGet(t, host, owProvider, jsonObject())
	work := startReloadCall(provider, "work", nil)
	gate.entered.wait(t)
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@test/app", Facet: app("v2")}}, background, ReloadOptions{}))
	opened := time.Now()
	gate.open.fire()
	// No object of the dropped version is open: old work never waits for a code update (rule 2).
	wantChordError(t, work.rejected(t), data.CodeContractMismatch, "", "")
	if took := time.Since(opened); took > time.Second {
		t.Fatalf("it failed after %v, want at once", took)
	}
}

func TestOldWorkCreatingAnObjectThatDidNotExistItStartsOnTheNewCodeAndBindsTheProvidersOpenOldAttempt(t *testing.T) {
	answer, _ := owRun(t, owShape{work: func(ctx Ctx, code string) (string, error) {
		got, err := owCall(ctx, owThird, jsonObject("id", "fresh"), "ping")
		return code + " work -> " + got, err
	}})
	if answer != "v1 work -> v2 third" {
		t.Fatalf("answer = %q, want \"v1 work -> v2 third\"", answer)
	}
}

func TestOldWorkARootGivenTheMethodsCtxWithoutItsSignalIsOldWorkOneWithoutItIsNewWork(t *testing.T) {
	run := func(scope func(ctx Ctx) context.Context) string {
		var host Host
		shape := owShape{work: func(ctx Ctx, code string) (string, error) {
			value, err := host.Root(func(root Ctx) (any, error) {
				got, err := owCall(root, owProvider, jsonObject(), "x")
				return got, err
			}, scope(ctx), RootOptions{})
			text, _ := value.(string)
			return code + " work -> " + text, err
		}}
		gate := newReloadGate()
		queue := reloadGates()
		drain := 300 * time.Millisecond
		host = owHost(t, shape, queue, HostOptions{ReloadDrainTimeout: &drain})
		defer closeHosts(t, host)
		provider := mustGet(t, host, owProvider, jsonObject())
		queue.gates = append(queue.gates, gate)
		work := startReloadCall(provider, "work", nil)
		gate.entered.wait(t)
		owReload(t, host, shape, queue)
		gate.open.fire()
		return owAnswer(t, work)
	}
	if got := run(func(ctx Ctx) context.Context { return chordctx.WithoutAbortSignal(ctx) }); got != "v1 work -> v1 x" {
		t.Fatalf("a root given the method's ctx answered %q, want \"v1 work -> v1 x\"", got)
	}
	// Without the ctx the root's call is new work: it waits for the provider's new attempt, which waits for this call's drain,
	// until the reload drain bound cuts the call.
	if got := run(func(Ctx) context.Context { return background }); !strings.HasPrefix(got, "rejected: ") || !strings.Contains(got, "replaced by a reload") {
		t.Fatalf("a root without it answered %q, want a rejection \"...replaced by a reload\"", got)
	}
}

func TestOldWorkACallRoutedToAnOldAttemptAfterItsDrainBeganJoinsTheDrainItIsNotCutAtTheSeal(t *testing.T) {
	shape := owShape{raise: "provider", work: func(ctx Ctx, code string) (string, error) {
		got, err := owCall(ctx, owDependent, jsonObject(), "slow")
		return code + " work -> " + got, err
	}}
	gate, long := newReloadGate(), newReloadGate()
	queue := reloadGates()
	host := owHost(t, shape, queue, HostOptions{})
	defer closeHosts(t, host)
	provider := mustGet(t, host, owProvider, jsonObject())
	mustGet(t, host, owDependent, jsonObject())
	queue.gates = append(queue.gates, gate, long)
	work := startReloadCall(provider, "work", nil)
	gate.entered.wait(t)
	owReload(t, host, shape, queue)
	gate.open.fire()
	// The dependent's old attempt runs the routed `slow` while its successor waits for the provider's.
	long.entered.wait(t)
	turn(host)
	long.open.fire()
	if got := owAnswer(t, work); got != "v1 work -> v1 slow" {
		t.Fatalf("work = %q, want \"v1 work -> v1 slow\"", got)
	}
}

// --- reload cycles ------------------------------------------------------------------------------------------------------------

var rcFresh = mustContract(declareLocal("app.f", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
	Members: []contracts.LocalEntry{contracts.Mem("ping", contracts.NewMethod(contracts.Schema{}, contracts.Str()))},
}))

func rcDependent() *contracts.Contract {
	return mustContract(declareLocal("app.d", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem("ping", contracts.NewMethod(contracts.Schema{}, contracts.Str()))},
	}))
}

func TestReloadCycleAWaitOfOldWorkFailsAtOnceWhenOldWorkCreatesAnObjectWhoseNewCodeNeedsTheProvidersRaisedContract(t *testing.T) {
	raised := mustContract(declareLocal("app.p", contracts.LocalDeclaration{Version: 2, Params: contracts.Params{}, Members: []contracts.LocalEntry{
		contracts.Mem("work", contracts.NewMethod(contracts.Schema{}, contracts.Str())), contracts.Mem("x", contracts.NewMethod(contracts.Schema{}, contracts.Str())), contracts.Mem("n", contracts.Int()),
	}}))
	provider := mustContract(declareLocal("app.p", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{
		contracts.Mem("work", contracts.NewMethod(contracts.Schema{}, contracts.Str())), contracts.Mem("x", contracts.NewMethod(contracts.Schema{}, contracts.Str())),
	}}))
	gate := newReloadGate()
	facet := func(code string) Facet {
		return DefineFacet(func(h FacetHost, _ FacetSelf) error {
			if code == "v1" {
				if err := h.Serve(provider, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
					return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
						"work": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
							gate.entered.fire()
							gate.open.await(t, ctx)
							return owCall(ctx.(Ctx), rcFresh, jsonObject("id", "fresh"), "ping")
						},
						"x": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return "v1 x", nil },
					}}}, nil
				}, ServeOptions{}); err != nil {
					return err
				}
			} else if err := h.Serve(raised, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
				return contracts.Implementation{Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(0.0)},
					Methods: map[string]contracts.MethodFunction{
						"work": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return "v2", nil },
						"x":    func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return "v2 x", nil },
					},
				}}, nil
			}, ServeOptions{}); err != nil {
				return err
			}
			return h.Serve(rcFresh, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				// v2 needs the raised provider: its new attempt, which waits for this very work's drain.
				if code == "v1" {
					self.Get(provider, jsonObject())
				} else {
					self.Get(raised, jsonObject())
				}
				return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
					"ping": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return code + " f", nil },
				}}}, nil
			}, ServeOptions{})
		})
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: facet("v1")}}, background))
	ref := mustGet(t, host, provider, jsonObject())
	working := startReloadCall(ref, "work", nil)
	gate.entered.wait(t)
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@test/app", Facet: facet("v2")}}, background, ReloadOptions{}))
	opened := time.Now()
	gate.open.fire()
	err := working.rejected(t)
	wantChordError(t, err, data.CodeUnavailable, data.ReasonReloadCycle,
		"app.f(fresh): work its reload drains waits for it, and it waits for that drain (app.f(fresh) → app.p)")
	// The reload drain bound (5 s) never came into it.
	if took := time.Since(opened); took > time.Second {
		t.Fatalf("it failed after %v, want within a second", took)
	}
}

func TestReloadCycleFailsOnceALaterWaitClosesTheCycleTheNewAttemptsOpenerCallsTheProviderOldWorkWaitsOn(t *testing.T) {
	provider := owProvider
	dependent := rcDependent()
	gate := newReloadGate()
	facet := func(code string) Facet {
		return DefineFacet(func(h FacetHost, _ FacetSelf) error {
			if err := h.Serve(provider, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
				return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
					"work": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						gate.entered.fire()
						gate.open.await(t, ctx)
						return owCall(ctx.(Ctx), dependent, jsonObject(), "ping")
					},
					"x": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return code + " x", nil },
				}}}, nil
			}, ServeOptions{}); err != nil {
				return err
			}
			return h.Serve(dependent, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				held := self.Get(provider, jsonObject())
				// The new code's opener calls the provider: new work, which waits at its gate for its new attempt.
				if code == "v2" {
					self.Open(func(opening context.Context) (any, error) { _, err := held.Call("x", nil, opening); return nil, err },
						func(any, NativeCloseReason, context.Context) error { return nil })
				}
				return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
					"ping": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return code + " ping", nil },
				}}}, nil
			}, ServeOptions{})
		})
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: facet("v1")}}, background))
	ref := mustGet(t, host, provider, jsonObject())
	mustGet(t, host, dependent, jsonObject())
	working := startReloadCall(ref, "work", nil)
	gate.entered.wait(t)
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@test/app", Facet: facet("v2")}}, background, ReloadOptions{}))
	opened := time.Now()
	gate.open.fire()
	err := working.rejected(t)
	var chordErr *data.ChordError
	if !errors.As(err, &chordErr) || chordErr.Code != data.CodeUnavailable || chordErr.Reason != data.ReasonReloadCycle || !strings.Contains(chordErr.Message, "(app.d → app.provider)") {
		t.Fatalf("error = %v, want unavailable/reload_cycle naming (app.d → app.provider)", err)
	}
	if took := time.Since(opened); took > time.Second {
		t.Fatalf("it failed after %v, want within a second", took)
	}
}

func TestReloadCycleDoesNotFailAWaitWhoseTargetWaitsOnAnotherObjectsDrainOnlyNoCycleThroughThisWork(t *testing.T) {
	provider := owProvider
	dependent := rcDependent()
	gate := newReloadGate()
	facet := func(code string) Facet {
		return DefineFacet(func(h FacetHost, _ FacetSelf) error {
			if err := h.Serve(provider, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
				return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
					"work": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						gate.entered.fire()
						gate.open.await(t, ctx)
						return owCall(ctx.(Ctx), dependent, jsonObject(), "ping")
					},
					"x": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return code + " x", nil },
				}}}, nil
			}, ServeOptions{}); err != nil {
				return err
			}
			return h.Serve(dependent, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				self.Get(provider, jsonObject())
				return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
					"ping": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return code + " ping", nil },
				}}}, nil
			}, ServeOptions{})
		})
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: facet("v1")}}, background))
	ref := mustGet(t, host, provider, jsonObject())
	working := startReloadCall(ref, "work", nil)
	gate.entered.wait(t)
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@test/app", Facet: facet("v2")}}, background, ReloadOptions{}))
	gate.open.fire()
	// The dependent's new attempt binds the provider's open old attempt: the work's wait ends, never cut.
	if got := working.wait(t); got != "v2 ping" {
		t.Fatalf("work = %v, want \"v2 ping\"", got)
	}
}

// Mirrors reload-cycle.test.ts "a cut wait keeps neither its target nor the old attempt it ran on".
func TestReloadCycleACutWaitKeepsNeitherItsTargetNorTheOldAttemptItRanOn(t *testing.T) {
	raised := mustContract(declareLocal("app.p", contracts.LocalDeclaration{Version: 2, Params: contracts.Params{}, Members: []contracts.LocalEntry{
		contracts.Mem("work", contracts.NewMethod(contracts.Schema{}, contracts.Str())), contracts.Mem("x", contracts.NewMethod(contracts.Schema{}, contracts.Str())), contracts.Mem("n", contracts.Int()),
	}}))
	provider := mustContract(declareLocal("app.p", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{
		contracts.Mem("work", contracts.NewMethod(contracts.Schema{}, contracts.Str())), contracts.Mem("x", contracts.NewMethod(contracts.Schema{}, contracts.Str())),
	}}))
	gate := newReloadGate()
	var mu sync.Mutex
	var selves []weak.Pointer[attempt]
	keep := func(self Self) {
		mu.Lock()
		selves = append(selves, weak.Make(self.(*attempt)))
		mu.Unlock()
	}
	facet := func(code string) Facet {
		return DefineFacet(func(h FacetHost, _ FacetSelf) error {
			if code == "v1" {
				if err := h.Serve(provider, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
					keep(self)
					return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
						"work": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
							gate.entered.fire()
							gate.open.await(t, ctx)
							return owCall(ctx.(Ctx), rcFresh, jsonObject("id", "fresh"), "ping")
						},
						"x": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return "v1 x", nil },
					}}}, nil
				}, ServeOptions{}); err != nil {
					return err
				}
			} else if err := h.Serve(raised, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
				return contracts.Implementation{Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(0.0)},
					Methods: map[string]contracts.MethodFunction{
						"work": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return "v2", nil },
						"x":    func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return "v2 x", nil },
					},
				}}, nil
			}, ServeOptions{}); err != nil {
				return err
			}
			return h.Serve(rcFresh, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				keep(self)
				if code == "v1" {
					self.Get(provider, jsonObject())
				} else {
					self.Get(raised, jsonObject())
				}
				return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
					"ping": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return code + " f", nil },
				}}}, nil
			}, ServeOptions{})
		})
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: facet("v1")}}, background))
	ref := mustGet(t, host, provider, jsonObject())
	working := startReloadCall(ref, "work", nil)
	gate.entered.wait(t)
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@test/app", Facet: facet("v2")}}, background, ReloadOptions{}))
	gate.open.fire()
	wantChordError(t, working.rejected(t), data.CodeUnavailable, data.ReasonReloadCycle, "")
	turn(host)
	// The old provider and the fresh object's attempts, the wait's chain and target.
	mu.Lock()
	all := append([]weak.Pointer[attempt](nil), selves...)
	mu.Unlock()
	nexttest.ExpectCollected(t, all...)
	ref.Release()
}

// Mirrors reload-cycle.test.ts "an opener's gate wait leaves no wait edge once it ended, nor keeps the old attempt".
func TestReloadCycleAnOpenersGateWaitLeavesNoWaitEdgeOnceItEndedNorKeepsTheOldAttempt(t *testing.T) {
	provider := owProvider
	dependent := rcDependent()
	gate := newReloadGate()
	var mu sync.Mutex
	var selves []weak.Pointer[attempt]
	facet := func(code string) Facet {
		return DefineFacet(func(h FacetHost, _ FacetSelf) error {
			if err := h.Serve(provider, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				if code == "v1" {
					mu.Lock()
					selves = append(selves, weak.Make(self.(*attempt)))
					mu.Unlock()
				}
				return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
					"work": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						if code == "v1" {
							gate.entered.fire()
							gate.open.await(t, ctx)
						}
						return code + " work", nil
					},
					"x": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return code + " x", nil },
				}}}, nil
			}, ServeOptions{}); err != nil {
				return err
			}
			return h.Serve(dependent, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				held := self.Get(provider, jsonObject())
				// The new code's opener calls the provider: it waits at its gate while the provider restarts.
				if code == "v2" {
					self.Open(func(opening context.Context) (any, error) { _, err := held.Call("x", nil, opening); return nil, err },
						func(any, NativeCloseReason, context.Context) error { return nil })
				}
				return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
					"ping": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return code + " ping", nil },
				}}}, nil
			}, ServeOptions{})
		})
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: facet("v1")}}, background))
	ref := mustGet(t, host, provider, jsonObject())
	dep := mustGet(t, host, dependent, jsonObject())
	working := startReloadCall(ref, "work", nil)
	gate.entered.wait(t)
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@test/app", Facet: facet("v2")}}, background, ReloadOptions{}))
	gate.open.fire()
	if got := working.wait(t); got != "v1 work" {
		t.Fatalf("work = %v, want \"v1 work\"", got)
	}
	scopeEventually(t, "the dependent's v2 ping", func() bool {
		got, err := dep.Call("ping", nil, background)
		return err == nil && got == "v2 ping"
	})
	object := reloadObjectOf(dep)
	var edges int
	host.(*chordHost).enter(func() { edges = len(object.waitEdges()) })
	if edges != 0 {
		t.Fatalf("the dependent has %d wait edges, want none", edges)
	}
	mu.Lock()
	all := append([]weak.Pointer[attempt](nil), selves...)
	mu.Unlock()
	nexttest.ExpectCollected(t, all...)
	ref.Release()
	dep.Release()
}
