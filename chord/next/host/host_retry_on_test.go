package host

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/test/next/host/retry-on.test.ts. The cases that run a host (the retries of a failed object, the wake
// remembered while it activates, wakes while live or parked, the collection of the object's wakes, the contributions
// leaving) are the TestRetryOn... tests at the end, on a real host as upstream's; the own tests of retrySource and
// retryWakes on a registry record ("app.models", composing the sum of the contributions, failing for a negative one, as
// serveModels does) come first, each naming the case whose behavior it checks.

// modelsEnv is the `app.models` registry of retry-on.test.ts on a batch of its own, and its value as a reader of version 1
// sees it.
type modelsEnv struct {
	t             *testing.T
	batch         *state.Batch
	record        *registries.RegistryRecord
	source        *registries.RegistryValueSource
	contributions *registries.RegistryContribution
}

func newModelsEnv(t *testing.T) *modelsEnv {
	t.Helper()
	e := &modelsEnv{t: t}
	e.batch = state.NewBatch(func(err state.BatchError) { t.Errorf("batch reported %s: %v", err.Kind, err.Error) }, nil)
	e.record = registries.NewRegistryRecord("app.models", e.batch, func(registries.RegistryError) {})
	err := e.record.Serve(1, registries.RegistryProvider[any, any]{
		Compose: func(table registries.RegistryTable[any], _ registries.ComposeContext) (any, error) {
			sum := 0
			for _, entry := range table {
				sum += entry.Value.(int)
			}
			if sum < 0 {
				return nil, errors.New("negative")
			}
			return sum, nil
		},
	})
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	e.source = e.record.Value(1)
	return e
}

// set is the models contributed changing to n (the Runtime object's `count.set(n)`), inside a commit.
func (e *modelsEnv) set(n int) {
	e.t.Helper()
	err := e.batch.Commit(func() error {
		if e.contributions != nil {
			e.record.Remove(e.contributions)
		}
		e.contributions = registries.NewRegistryContribution(e.record, registries.Contributor{Plugin: "@test/core", Type: "app.runtime"}, 1,
			registries.ContributionContent{Kind: registries.ContributionPlain, Value: n})
		e.record.Add(e.contributions)
		return nil
	}, chordctx.Background())
	if err != nil {
		e.t.Fatalf("commit: %v", err)
	}
}

// contribute adds a contribution of n by plugin and leaves it there (another object's models), inside a commit.
func (e *modelsEnv) contribute(plugin string, n int) {
	e.t.Helper()
	err := e.batch.Commit(func() error {
		e.record.Add(registries.NewRegistryContribution(e.record, registries.Contributor{Plugin: plugin, Type: "app.runtime"}, 1,
			registries.ContributionContent{Kind: registries.ContributionPlain, Value: n}))
		return nil
	}, chordctx.Background())
	if err != nil {
		e.t.Fatalf("commit: %v", err)
	}
}

// retry is a retryOn source of the registry value.
func (e *modelsEnv) retry() retrySourceType {
	e.t.Helper()
	source, err := retrySource(e.source, "app.user(1)")
	if err != nil {
		e.t.Fatalf("retrySource: %v", err)
	}
	return source
}

func TestRetrySourceTakesRegistryValuesAndConstants(t *testing.T) {
	e := newModelsEnv(t)
	if source, err := retrySource(e.source, "app.user(1)"); err != nil || source.value != e.source || source.constant != nil {
		t.Fatalf("retrySource(registry value) = %+v, %v", source, err)
	}
	constant := state.NewConstant(1.0)
	if source, err := retrySource(constant, "app.user(1)"); err != nil || source.constant != constant || source.value != nil {
		t.Fatalf("retrySource(constant) = %+v, %v", source, err)
	}
}

// Mirrors "refuses sources that end with an attempt: a reference's member, its own writable, `ref.state`".
func TestRetrySourceRefusesSourcesThatEndWithAnAttempt(t *testing.T) {
	e := newModelsEnv(t)
	loop := newFakeLoop()
	member := newActivity(loop, e.batch)
	refState := newReferenceState(func() *RefState { return refLive }, e.batch, nil)
	derive := state.NewDerive(nil, nil, e.batch)
	var nilValue *registries.RegistryValueSource
	var nilConstant *state.Constant
	for name, source := range map[string]any{"a member": member, "ref.state": refState, "a derive": derive, "nil": nil, "a nil registry value": nilValue, "a nil constant": nilConstant, "a string": "x"} {
		_, err := retrySource(source, "app.picky(0)")
		want := "app.picky(0): self.retryOn takes registry values and constants (sources that outlive the attempt)"
		if err == nil || err.Error() != want {
			t.Errorf("retrySource(%s) error = %v, want %q", name, err, want)
		}
	}
}

// Mirrors "retries a failed object when a source gets a new value": each new value of a followed source wakes, inside the
// batch that made it.
func TestRetryWakesWakeOnEachNewValue(t *testing.T) {
	e := newModelsEnv(t)
	wakes := &retryWakes{}
	if wakes.during != nil || wakes.leaving {
		t.Fatalf("a fresh retryWakes has during %v leaving %v", wakes.during, wakes.leaving)
	}
	count := 0
	wakes.replace([]retrySourceType{e.retry()}, func() { count++ })
	e.set(1)
	if count != 1 {
		t.Fatalf("wakes after the first value = %d, want 1", count)
	}
	e.set(2)
	if count != 2 {
		t.Fatalf("wakes after the second value = %d, want 2", count)
	}
}

// Mirrors "wakes on value calls only: a throwing compose wakes nothing, its next good composition does".
func TestRetryWakesOnValueCallsOnly(t *testing.T) {
	e := newModelsEnv(t)
	wakes := &retryWakes{}
	count := 0
	wakes.replace([]retrySourceType{e.retry()}, func() { count++ })
	e.set(-1)
	if count != 0 {
		t.Fatalf("wakes after a failing compose = %d, want 0", count)
	}
	e.set(2)
	if count != 1 {
		t.Fatalf("wakes after the next good composition = %d, want 1", count)
	}
}

// Mirrors the declaration of each attempt replacing the last one ("replaced by each attempt's declaration"): the previous
// set stops, constants follow nothing, and `stop` ends every follow.
func TestRetryWakesReplaceAndStop(t *testing.T) {
	e := newModelsEnv(t)
	wakes := &retryWakes{}
	var first, second int
	wakes.replace([]retrySourceType{e.retry()}, func() { first++ })
	wakes.replace([]retrySourceType{e.retry(), {constant: state.NewConstant(1.0)}}, func() { second++ })
	if len(wakes.subscribed) != 1 {
		t.Fatalf("subscriptions = %d, want 1 (a constant is followed by nothing)", len(wakes.subscribed))
	}
	e.set(1)
	if first != 0 || second != 1 {
		t.Fatalf("wakes first %d second %d, want 0 1", first, second)
	}
	wakes.stop()
	if len(wakes.subscribed) != 0 {
		t.Fatalf("subscriptions after stop = %d, want 0", len(wakes.subscribed))
	}
	e.set(2)
	if second != 1 {
		t.Fatalf("wakes after stop = %d, want 1", second)
	}
	// Stopping again, or replacing with nothing, is harmless.
	wakes.stop()
	wakes.replace(nil, func() { second++ })
	e.set(3)
	if second != 1 {
		t.Fatalf("wakes after an empty declaration = %d, want 1", second)
	}
}

// Own test: one observer follows every source of a declaration, including the same source twice, and stop leaves nothing
// followed.
func TestRetryWakesTheSameSourceTwice(t *testing.T) {
	e := newModelsEnv(t)
	wakes := &retryWakes{}
	count := 0
	wakes.replace([]retrySourceType{e.retry(), e.retry()}, func() { count++ })
	e.set(1)
	if count != 1 {
		t.Fatalf("wakes = %d, want 1", count)
	}
	wakes.stop()
	if e.source.Watched() {
		t.Fatal("the source is still followed after stop")
	}
}

// retryModels is the `app.models` registry of retry-on.test.ts: the number of models contributed; it fails when told to.
var retryModels = func() registries.Registry[any, any] {
	token, err := registries.DefineRegistry[any, any]("app.models", registries.DefineRegistryOptions{Version: 1})
	if err != nil {
		panic(err)
	}
	return token
}()

var retryRuntime = mustContract(contracts.Local("app.runtime", contracts.LocalDeclaration{Params: contracts.Params{},
	Version: 1,
	Members: []contracts.LocalEntry{contracts.Mem("set", contracts.NewMethod(contracts.Int()))},
}))

var retryUser = mustContract(contracts.Local("app.user", contracts.LocalDeclaration{Members: []contracts.LocalEntry{},
	Version: 1,
	Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
}))

// serveModels serves `Models` (failing for a negative sum) and `Runtime`, contributing a writable count of models.
func serveModels(h FacetHost) error {
	err := h.Serve(retryModels, registries.RegistryProvider[any, any]{
		Compose: func(table registries.RegistryTable[any], _ registries.ComposeContext) (any, error) {
			sum := 0.0
			for _, entry := range table {
				sum += entry.Value.(float64)
			}
			if sum < 0 {
				return nil, errors.New("negative")
			}
			return sum, nil
		},
	}, ServeOptions{})
	if err != nil {
		return err
	}
	return h.Serve(retryRuntime, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
		count := self.Writable(0.0)
		self.(StoreSelf).Contribute(retryModels, count)
		return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"set": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				var err error
				enterOf(self)(func() { err = count.Set(args) })
				return nil, err
			},
		}}}, nil
	}, ServeOptions{})
}

// serveUser serves `User`, whose open reads the models first and fails broken when there were none (after wait, if given);
// opens counts its attempts.
func serveUser(h FacetHost, opens *atomic.Int32, wait func()) error {
	return h.Serve(retryUser, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
		models := self.Registry(retryModels)
		self.RetryOn(models)
		self.Open(func(context.Context) (any, error) {
			var seen any
			enterOf(self)(func() { seen = models.Value() })
			opens.Add(1)
			if wait != nil {
				wait()
			}
			if seen == 0.0 {
				return nil, data.Broken(data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "no model"}, nil))
			}
			return nil, nil
		}, func(any, NativeCloseReason, context.Context) error { return nil })
		return contracts.Implementation{}, nil
	}, ServeOptions{})
}

func setModels(t *testing.T, runtime *Reference, n float64) {
	t.Helper()
	_, err := refCall(runtime, "set", n)
	must(t, err)
}

// "retries a failed object (a broken open) when a source gets a new value: the watches were installed before the open".
func TestRetryOnRetriesAFailedObjectABrokenOpenWhenASourceGetsANewValueTheWatchesWereInstalledBeforeTheOpen(t *testing.T) {
	var opens atomic.Int32
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := serveModels(h); err != nil {
			return err
		}
		return serveUser(h, &opens, nil)
	}, loadedOptions{})
	user := mustHold(t, host, retryUser, jsonObject("id", 1.0))
	failed := untilKind(t, user, RefStateFailed)
	if failed.Cause.Kind != data.FailureBroken {
		t.Fatalf("cause = %s, want broken", failed.Cause.Kind)
	}
	setModels(t, mustGet(t, host, retryRuntime, jsonObject()), 1)
	untilKind(t, user, RefStateLive)
	if got := opens.Load(); got != 2 {
		t.Fatalf("opens = %d, want 2", got)
	}
	closeHosts(t, host)
}

// "does nothing while the object is parked failed { waiting }: its dependencies decide (dependency model §3.8)".
func TestRetryOnDoesNothingWhileTheObjectIsParkedFailedWaitingItsDependenciesDecide(t *testing.T) {
	missing := mustContract(contracts.Local("app.missing", contracts.LocalDeclaration{Members: []contracts.LocalEntry{}, Params: contracts.Params{}, Version: 1}))
	var factories atomic.Int32
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := serveModels(h); err != nil {
			return err
		}
		return h.Serve(retryUser, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			factories.Add(1)
			self.RetryOn(self.Registry(retryModels))
			self.Get(missing, jsonObject())
			return contracts.Implementation{}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	user := mustHold(t, host, retryUser, jsonObject("id", 1.0))
	waiting := untilKind(t, user, RefStateFailed)
	if waiting.Cause.Kind != data.FailureWaiting {
		t.Fatalf("cause = %s, want waiting", waiting.Cause.Kind)
	}
	setModels(t, mustGet(t, host, retryRuntime, jsonObject()), 1)
	turn(host)
	if got, factoriesRun := stateOfRef(user).Kind, factories.Load(); got != RefStateFailed || factoriesRun != 1 {
		t.Fatalf("state %s, factories %d, want failed and 1", got, factoriesRun)
	}
	closeHosts(t, host)
}

// "wakes on value calls only: a throwing compose wakes nothing, its next good composition does".
func TestRetryOnWakesOnValueCallsOnlyAThrowingComposeWakesNothingItsNextGoodCompositionDoes(t *testing.T) {
	var opens atomic.Int32
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := serveModels(h); err != nil {
			return err
		}
		return serveUser(h, &opens, nil)
	}, loadedOptions{})
	user := mustHold(t, host, retryUser, jsonObject("id", 1.0))
	untilKind(t, user, RefStateFailed)
	runtime := mustGet(t, host, retryRuntime, jsonObject())
	setModels(t, runtime, -1)
	turn(host)
	if got := opens.Load(); got != 1 {
		t.Fatalf("opens after a failing composition = %d, want 1", got)
	}
	setModels(t, runtime, 2)
	untilKind(t, user, RefStateLive)
	if got := opens.Load(); got != 2 {
		t.Fatalf("opens = %d, want 2", got)
	}
	closeHosts(t, host)
}

// "remembers a wake while it activates: that attempt failing is retried at once".
func TestRetryOnRemembersAWakeWhileItActivatesThatAttemptFailingIsRetriedAtOnce(t *testing.T) {
	var opens atomic.Int32
	gate := newSignal()
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := serveModels(h); err != nil {
			return err
		}
		return serveUser(h, &opens, func() {
			if opens.Load() == 1 {
				gate.wait(t)
			}
		})
	}, loadedOptions{})
	runtime := mustGet(t, host, retryRuntime, jsonObject())
	user := mustHold(t, host, retryUser, jsonObject("id", 1.0))
	until(t, "the first attempt's open", func() bool { return opens.Load() == 1 })
	// The first attempt read no model and waits; a model arrives meanwhile; then that attempt fails broken.
	setModels(t, runtime, 1)
	if got := stateOfRef(user).Kind; got != RefStateLoading {
		t.Fatalf("state = %s, want loading", got)
	}
	gate.fire()
	untilKind(t, user, RefStateLive)
	if got := opens.Load(); got != 2 {
		t.Fatalf("opens = %d, want 2", got)
	}
	closeHosts(t, host)
}

// "ignores wakes while live".
func TestRetryOnIgnoresWakesWhileLive(t *testing.T) {
	var opens atomic.Int32
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := serveModels(h); err != nil {
			return err
		}
		return h.Serve(retryUser, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.RetryOn(self.Registry(retryModels))
			self.Open(func(context.Context) (any, error) {
				opens.Add(1)
				return nil, nil
			}, func(any, NativeCloseReason, context.Context) error { return nil })
			return contracts.Implementation{}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	user := mustHold(t, host, retryUser, jsonObject("id", 1.0))
	untilKind(t, user, RefStateLive)
	setModels(t, mustGet(t, host, retryRuntime, jsonObject()), 3)
	turn(host)
	if got, kind := opens.Load(), stateOfRef(user).Kind; got != 1 || kind != RefStateLive {
		t.Fatalf("opens %d, state %s, want 1 and live", got, kind)
	}
	closeHosts(t, host)
}

// "refuses sources that end with an attempt: a reference's member, its own writable, `ref.state`".
func TestRetryOnRefusesSourcesThatEndWithAnAttemptAReferencesMemberItsOwnWritableRefState(t *testing.T) {
	other := mustContract(contracts.Local("app.other", contracts.LocalDeclaration{Params: contracts.Params{},
		Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	picky := mustContract(contracts.Local("app.picky", contracts.LocalDeclaration{Members: []contracts.LocalEntry{},
		Version: 1, Params: contracts.Params{contracts.Prop("which", contracts.Int())},
	}))
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(other, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(1.0)},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(picky, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			ref := self.Get(other, jsonObject())
			sources := []any{ref.Member("n"), self.Writable(1.0), ref.State(), state.NewConstant(1.0)}
			self.RetryOn(sources[int(params.Value("which").(float64))])
			return contracts.Implementation{}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	for _, which := range []float64{0, 1, 2} {
		_, err := host.Get(picky, jsonObject("which", which), background, GetOptions{})
		want := "app.picky(" + strconv.Itoa(int(which)) + "): self.retryOn takes registry values and constants (sources that outlive the attempt)"
		if err == nil || !hasText(err.Error(), want) {
			t.Fatalf("get(which %v) error = %v, want %q", which, err, want)
		}
	}
	mustGet(t, host, picky, jsonObject("which", 3.0)).Release()
	closeHosts(t, host)
}

// "are the object's: a failed object that ends releases them, and is collected while the registry lives".
func TestRetryOnAreTheObjectsAFailedObjectThatEndsReleasesThemAndIsCollectedWhileTheRegistryLives(t *testing.T) {
	var opens atomic.Int32
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := serveModels(h); err != nil {
			return err
		}
		return serveUser(h, &opens, nil)
	}, loadedOptions{})
	user := mustHold(t, host, retryUser, jsonObject("id", 1.0))
	untilKind(t, user, RefStateFailed)
	var refs []weak.Pointer[chordObject]
	host.(*chordHost).enter(func() {
		for _, object := range host.(*chordHost).objectList() {
			if object.label() == "app.user(1)" {
				refs = append(refs, weak.Make(object))
			}
		}
	})
	user.Release()
	turn(host)
	if len(refs) == 0 {
		t.Fatal("no object of app.user(1) was found")
	}
	untilEnded(t, host)
	expectCollected(t, refs...)
	closeHosts(t, host)
}

// "does not wake an object by its own contributions leaving (it failed live: no retry without backoff)".
func TestRetryOnDoesNotWakeAnObjectByItsOwnContributionsLeavingItFailedLiveNoRetryWithoutBackoff(t *testing.T) {
	contributor := mustContract(contracts.Local("app.contributor", contracts.LocalDeclaration{Members: []contracts.LocalEntry{}, Params: contracts.Params{}, Version: 1}))
	var mu sync.Mutex
	var self Self
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := serveModels(h); err != nil {
			return err
		}
		return h.Serve(contributor, func(_ *delta.JsonObject, own Self) (contracts.Implementation, error) {
			mu.Lock()
			self = own
			mu.Unlock()
			own.(StoreSelf).Contribute(retryModels, 1.0)
			own.RetryOn(own.Registry(retryModels))
			return contracts.Implementation{}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	models, err := host.Registry(retryModels, background, GetOptions{})
	must(t, err)
	var watched nexttest.Collector[any]
	watch, err := models.Watch(background)
	must(t, err)
	watch.Start(func(_ context.Context, call chord.NativeWatchCall[any]) error {
		if call.Kind == chord.WatchCallValue {
			watched.Append(call.Value)
		}
		return nil
	})
	mu.Lock()
	failing := self
	mu.Unlock()
	failLive(failing, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
	turn(host)
	reference := mustHold(t, host, contributor, jsonObject())
	turn(host)
	failed := stateOfRef(reference)
	if failed.Kind != RefStateFailed || failed.Cause.Kind != data.FailureBroken {
		t.Fatalf("state = %+v, want failed broken", failed)
	}
	if got := watched.Items(); len(got) != 1 || got[0] != 0.0 {
		t.Fatalf("watched = %v, want [0]", got)
	}
	closeHosts(t, host)
}
