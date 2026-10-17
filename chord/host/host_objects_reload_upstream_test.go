package host

// Ports packages/chord/test/host-objects-reload.test.ts: the host's reference to a host object across contract upgrades
// (reload.ts install, checkRefs; host.ts startHostObject; cluster.ts), locally and in a cluster.

import (
	"regexp"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// eagerInput and eagerFlags are host-objects-reload.test.ts:17-18.
var (
	eagerInput = tObject()
	eagerFlags = Schema{"type": "object", "patternProperties": Schema{"^.*$": tBoolean()}}
)

// eagerSwitchV1..V3 are host-objects-reload.test.ts:19-42.
var (
	eagerSwitchV1 = Local("t.eager-switch", LocalDef{Version: 1, Input: eagerInput, Members: Fields[any]{
		{"enabled", tBoolean()},
		{"set", Method(tBoolean(), nil)},
	}})
	eagerSwitchV2 = Local("t.eager-switch", LocalDef{Version: 2, Input: eagerInput, Members: Fields[any]{
		{"enabled", eagerFlags},
		{"set", Method(tObject("conversation", tNumber(), "enabled", tBoolean()), nil)},
	}})
	eagerSwitchV3 = Local("t.eager-switch", LocalDef{Version: 3, Input: eagerInput, Members: Fields[any]{
		{"enabled", eagerFlags},
		{"update", Method(tObject("conversation", tNumber(), "enabled", tBoolean()), nil)},
	}})
)

// eagerRead is host-objects-reload.test.ts:43 Read: values are functions returning the switch state.
var eagerRead = Registry[any](DefineRegistry[func() any]("t.eager-switch-read"))

type eagerSwitchFacetSet struct {
	v1 *Facet
	v2 func(valid bool) *Facet
	v3 *Facet
}

// eagerSwitchFacets is host-objects-reload.test.ts:45-135 switchFacets.
func eagerSwitchFacets(t *testing.T, log *reloadLog) eagerSwitchFacetSet {
	declareResource := func(self Self) error {
		_, err := self.Resource("watch", ResourceDeclaration[any]{
			Version: 1,
			Open:    func(ResourceOpenOptions) (any, error) { log.push("open"); return nil, nil },
			Close:   func(any) error { log.push("close"); return nil },
		})
		return err
	}
	// register is `self.register(Read, { current: () => state.enabled.value })`.
	register := func(self Self, enabled *Cell[any]) error {
		return self.Register(eagerRead, []Entry{{Name: "current", Value: func() any { return enabled.Value() }}})
	}
	setFlag := func(enabled *Cell[any]) methodFunc {
		return func(args any, ctx Ctx) (any, error) {
			conversation := strconv.FormatFloat(reloadField(args, "conversation").(float64), 'f', -1, 64)
			flag := reloadField(args, "enabled")
			if err := enabled.Change(ctx, func(draft any) { _ = draft.(*delta.Object).Set(conversation, flag) }); err != nil {
				return nil, err
			}
			return nil, nil
		}
	}
	v1 := &Facet{Name: "switch", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(eagerSwitchV1, func(_ any, _ Principal, self Self) (Implementation, error) {
			if err := declareResource(self); err != nil {
				return nil, err
			}
			enabled, err := self.State("enabled", tBoolean(), false, nil)
			if err != nil {
				return nil, err
			}
			if err := register(self, enabled); err != nil {
				return nil, err
			}
			return chordjson.ObjectOf(
				"enabled", enabled,
				"set", methodFunc(func(args any, ctx Ctx) (any, error) {
					if err := enabled.Replace(ctx, args, CellReplaceOptions{}); err != nil {
						return nil, err
					}
					return nil, nil
				}),
			), nil
		}, ServeOptions{Lifetime: LifetimeHost})
	}}}
	v2 := func(valid bool) *Facet {
		return &Facet{Name: "switch", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
			return api.Serve(eagerSwitchV2, func(_ any, _ Principal, self Self) (Implementation, error) {
				if err := declareResource(self); err != nil {
					return nil, err
				}
				enabled, err := self.State("enabled", eagerFlags, delta.JsonObjectOf(), &StateOptions{
					Version: 2,
					Migrate: func(old any, from int) (any, error) {
						if from != 1 {
							t.Errorf("migrate from %d, want 1", from)
						}
						if valid {
							return delta.JsonObjectOf("1", old), nil
						}
						return "invalid migration", nil
					},
				})
				if err != nil {
					return nil, err
				}
				if err := register(self, enabled); err != nil {
					return nil, err
				}
				return chordjson.ObjectOf("enabled", enabled, "set", setFlag(enabled)), nil
			}, ServeOptions{Lifetime: LifetimeHost})
		}}}
	}
	v3 := &Facet{Name: "switch", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(eagerSwitchV3, func(_ any, _ Principal, self Self) (Implementation, error) {
			if err := declareResource(self); err != nil {
				return nil, err
			}
			enabled, err := self.State("enabled", eagerFlags, delta.JsonObjectOf(), &StateOptions{Version: 2})
			if err != nil {
				return nil, err
			}
			if err := register(self, enabled); err != nil {
				return nil, err
			}
			return chordjson.ObjectOf("enabled", enabled, "update", setFlag(enabled)), nil
		}, ServeOptions{Lifetime: LifetimeHost})
	}}}
	return eagerSwitchFacetSet{v1: v1, v2: v2, v3: v3}
}

// eagerRef is host.hostObjects.get(name).
func eagerRef(host *HostImpl, name string) RefCore {
	var hold RefCore
	host.run(func() { hold, _ = host.hostObjects.Get(name) })
	return hold
}

// eagerCurrent is `host.registry(Read).get("current")!()`.
func eagerCurrent(t *testing.T, host *HostImpl) string {
	t.Helper()
	current, ok := host.Registry(eagerRead).Get("current", nil)
	if !ok {
		t.Fatal("no current entry")
	}
	return jsonText(t, current.(func() any)())
}

func eagerLoad(t *testing.T, host *HostImpl, facets ...*Facet) {
	t.Helper()
	if err := host.Load(reloadPlugins(facets...), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
}

// host-objects-reload.test.ts:137-176
func TestHostObjectsReloadTheHostsReferenceToAHostObjectFollowsContractBumpsAtCommitWithoutReopeningItsObjectOrResourcesFailuresRetainTheOldReference(t *testing.T) {
	var errorsMu sync.Mutex
	var errs []error
	host := CreateHost(HostOptions{Self: kindHost(), Dev: true, OnError: func(err error) {
		errorsMu.Lock()
		errs = append(errs, err)
		errorsMu.Unlock()
	}})
	t.Cleanup(func() { _ = host.Close() })
	var log reloadLog
	facets := eagerSwitchFacets(t, &log)
	eagerLoad(t, host, facets.v1)
	initial := eagerRef(host, eagerSwitchV1.Name)
	object := initial.refObject()
	original := reloadGet(t, host, eagerSwitchV1, map[string]any{})
	mustCall(t, original, "set", true, nil)
	original.Release()
	codeVersion := host.CodeVersion()
	if _, err := host.Reload(reloadPlugins(facets.v2(false)), ReloadOptions{}); !reloadIsCode(err, CodeInvalid) {
		t.Fatalf("invalid migration: %v", err)
	}
	if host.CodeVersion() != codeVersion {
		t.Fatalf("code version = %d, want %d", host.CodeVersion(), codeVersion)
	}
	if eagerRef(host, eagerSwitchV1.Name) != initial {
		t.Fatal("the failed reload replaced the host's reference")
	}
	if status := initial.Status(); status != RefLive {
		t.Fatalf("initial status = %s", status)
	}
	if got := eagerCurrent(t, host); got != "true" {
		t.Fatalf("current = %s", got)
	}
	report, err := host.Reload(reloadPlugins(facets.v2(true)), ReloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Gated) != 1 {
		t.Fatalf("gated = %v", report.Gated)
	}
	updated := eagerRef(host, eagerSwitchV2.Name)
	if updated.Contract() != eagerSwitchV2 {
		t.Fatal("the host's reference is not retokened to v2")
	}
	if updated.refObject() != object {
		t.Fatal("the host's reference changed objects")
	}
	if status := initial.Status(); status != RefReleased {
		t.Fatalf("initial status = %s", status)
	}
	var refs int
	host.run(func() { refs = object.refs.Len() })
	if refs != 1 {
		t.Fatalf("object holds = %d: attach the replacement before releasing the only old hold", refs)
	}
	if got := eagerCurrent(t, host); got != `{"1":true}` {
		t.Fatalf("current = %s", got)
	}
	modern := reloadGet(t, host, eagerSwitchV2, map[string]any{})
	mustCall(t, modern, "set", map[string]any{"conversation": float64(2), "enabled": true}, nil)
	modern.Release()
	report, err = host.Reload(reloadPlugins(facets.v3), ReloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Gated) != 0 {
		t.Fatalf("gated = %v", report.Gated)
	}
	if hold := eagerRef(host, eagerSwitchV3.Name); hold == nil || hold.Contract() != eagerSwitchV3 || hold.refObject() != object {
		t.Fatal("the host's reference is not retokened to v3 on the same object")
	}
	if status := updated.Status(); status != RefReleased {
		t.Fatalf("updated status = %s", status)
	}
	if got := eagerCurrent(t, host); got != `{"1":true,"2":true}` {
		t.Fatalf("current = %s", got)
	}
	if err := host.Ready(); err != nil {
		t.Fatal(err)
	}
	if got := log.items(); !slices.Equal(got, []string{"open"}) {
		t.Fatalf("log = %q", got)
	}
	errorsMu.Lock()
	if len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
	errorsMu.Unlock()
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	if got := log.items(); !slices.Equal(got, []string{"open", "close"}) {
		t.Fatalf("log = %q", got)
	}
}

// eagerReadV1 and eagerReadV2 are host-objects-reload.test.ts:178-187.
var (
	eagerReadV1 = Local("t.eager-read", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"read", Method(nil, tString())}}})
	eagerReadV2 = Local("t.eager-read", LocalDef{Version: 2, Input: tObject(), Members: Fields[any]{{"fetch", Method(nil, tString())}}})
)

// eagerReaderV1 is host-objects-reload.test.ts:188-191.
func eagerReaderV1() *Facet {
	return reloadServeFacet("read", eagerReadV1, func() Implementation {
		return chordjson.ObjectOf("read", methodFunc(func(any, Ctx) (any, error) { return "one", nil }))
	}, ServeOptions{Lifetime: LifetimeHost})
}

// eagerReaderV2 is host-objects-reload.test.ts:192-199.
func eagerReaderV2(adapt bool) *Facet {
	options := ServeOptions{Lifetime: LifetimeHost}
	if adapt {
		options.Accepts = Accepts{1: func(current Implementation) Implementation { return chordjson.ObjectOf("read", current.Value("fetch")) }}
	}
	return reloadServeFacet("read", eagerReadV2, func() Implementation {
		return chordjson.ObjectOf("fetch", methodFunc(func(any, Ctx) (any, error) { return "two", nil }))
	}, options)
}

// host-objects-reload.test.ts:201-220
func TestHostObjectsReloadExternalOldVersionConsumersStillRefuseAHostObjectsUpgradeWithoutAcceptsAndKeepTheirAdapterWhenItIsProvided(t *testing.T) {
	// Pi's onError rethrows: no background error is expected.
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(err error) { t.Errorf("onError: %v", err) }})
	t.Cleanup(func() { _ = host.Close() })
	eagerLoad(t, host, eagerReaderV1())
	own := eagerRef(host, eagerReadV1.Name)
	consumer := reloadGet(t, host, eagerReadV1, map[string]any{})
	if got := mustCall(t, consumer, "read", nil, nil); got != "one" {
		t.Fatalf("read = %v", got)
	}
	if _, err := host.Reload(reloadPlugins(eagerReaderV2(false)), ReloadOptions{}); !reloadIsCode(err, CodeContractMismatch) {
		t.Fatalf("reload without accepts: %v", err)
	}
	if eagerRef(host, eagerReadV1.Name) != own {
		t.Fatal("the refused reload replaced the host's reference")
	}
	if got := mustCall(t, consumer, "read", nil, nil); got != "one" {
		t.Fatalf("read = %v", got)
	}
	if _, err := host.Reload(reloadPlugins(eagerReaderV2(true)), ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	if hold := eagerRef(host, eagerReadV2.Name); hold == nil || hold.Contract() != eagerReadV2 {
		t.Fatal("the host's reference is not retokened to v2")
	}
	if got := mustCall(t, consumer, "read", nil, nil); got != "two" {
		t.Fatalf("read = %v", got)
	}
	if consumer.Contract() != eagerReadV1 {
		t.Fatal("only the bootstrap hold is retokened")
	}
	if got := mustCall(t, reloadGet(t, host, eagerReadV2, map[string]any{}), "fetch", nil, nil); got != "two" {
		t.Fatalf("fetch = %v", got)
	}
}

// host-objects-reload.test.ts:222-254
func TestHostObjectsReloadObjectOwnedOldVersionReferencesStillBlockAHostObjectsUpgradeUnlessTheirConsumerIsReplacedInTheSameBatch(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	consumerType := Local("t.eager-consumer", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"read", Method(nil, tString())}}})
	consumerFacet := func(reads *Contract, method string) *Facet {
		return &Facet{Name: "consumer", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
			return api.Serve(consumerType, func(_ any, _ Principal, self Self) (Implementation, error) {
				reader, err := self.Get(reads, map[string]any{}, nil)
				if err != nil {
					return nil, err
				}
				return chordjson.ObjectOf("read", methodFunc(func(_ any, ctx Ctx) (any, error) {
					return callMember(reader, method, nil, ctx)
				})), nil
			}, ServeOptions{})
		}}}
	}
	oldConsumer := consumerFacet(eagerReadV1, "read")
	newConsumer := consumerFacet(eagerReadV2, "fetch")
	eagerLoad(t, host, eagerReaderV1(), oldConsumer)
	consumer := reloadGet(t, host, consumerType, map[string]any{})
	if got := mustCall(t, consumer, "read", nil, nil); got != "one" {
		t.Fatalf("read = %v", got)
	}
	if _, err := host.Reload(reloadPlugins(eagerReaderV2(false)), ReloadOptions{}); !reloadIsCode(err, CodeContractMismatch) {
		t.Fatalf("reload without the consumer: %v", err)
	}
	if got := mustCall(t, consumer, "read", nil, nil); got != "one" {
		t.Fatalf("read = %v", got)
	}
	if _, err := host.Reload(reloadPlugins(eagerReaderV2(false), newConsumer), ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := mustCall(t, consumer, "read", nil, nil); got != "two" {
		t.Fatalf("read = %v", got)
	}
	if hold := eagerRef(host, eagerReadV2.Name); hold == nil || hold.Contract() != eagerReadV2 {
		t.Fatal("the host's reference is not retokened to v2")
	}
}

// host-objects-reload.test.ts:256-272
func TestHostObjectsReloadAForgottenContractBumpRemainsRefusedEvenWhenTheOnlyConsumerIsTheHostsOwnReference(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	eagerLoad(t, host, eagerReaderV1())
	incompatible := Local(eagerReadV1.Name, LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"fetch", Method(nil, tString())}}})
	_, err := host.Reload(reloadPlugins(reloadServeFacet("read", incompatible, func() Implementation {
		return chordjson.ObjectOf("fetch", methodFunc(func(any, Ctx) (any, error) { return "bad", nil }))
	}, ServeOptions{Lifetime: LifetimeHost})), ReloadOptions{})
	if !reloadIsCode(err, CodeContractMismatch) || !regexp.MustCompile(`without a version bump`).MatchString(err.Error()) {
		t.Fatalf("reload: %v", err)
	}
	if hold := eagerRef(host, eagerReadV1.Name); hold == nil || hold.Contract() != eagerReadV1 {
		t.Fatal("the host's reference changed")
	}
}

// host-objects-reload.test.ts:274-306
func TestHostObjectsReloadAClusterContractUpgradeRetokensTheServersAndWorkersReferencesToHostObjectsOnlyAfterUnanimousPreparation(t *testing.T) {
	var log reloadLog
	facets := eagerSwitchFacets(t, &log)
	var valid atomic.Bool
	server := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	worker := CreateHost(HostOptions{Self: kindHost(), Name: "worker", OnError: func(error) {}, Loader: func([]string) ([]Facet, error) {
		return []Facet{*facets.v2(valid.Load())}, nil
	}})
	t.Cleanup(func() {
		_ = worker.Close()
		_ = server.Close()
	})
	eagerLoad(t, server, facets.v1)
	eagerLoad(t, worker, facets.v1)
	down, up := MemoryLinkPair()
	workerName := "worker"
	if err := server.Accept(down, AcceptOptions{Kind: KindHost, Name: &workerName, Principal: map[string]any{"worker": true}}); err != nil {
		t.Fatal(err)
	}
	worker.Connect(up, ConnectOptions{Principal: kindServer()})
	// Pi waits one setImmediate for the hello exchange: wait until the server counts the worker as synced.
	waitFor(t, "the worker hello", func() bool {
		synced := false
		server.run(func() {
			for _, connection := range server.connections.Keys() {
				synced = synced || connection.synced
			}
		})
		return synced
	})
	serverOwn := eagerRef(server, eagerSwitchV1.Name)
	workerOwn := eagerRef(worker, eagerSwitchV1.Name)
	remote := map[string][]string{"worker": {"switch"}}
	if _, err := server.Reload(reloadPlugins(facets.v2(true)), ReloadOptions{Remote: remote}); !reloadIsCode(err, CodeInvalid) {
		t.Fatalf("reload with an invalid worker: %v", err)
	}
	if eagerRef(server, eagerSwitchV1.Name) != serverOwn || eagerRef(worker, eagerSwitchV1.Name) != workerOwn {
		t.Fatal("an aborted cluster reload replaced a host's reference")
	}
	valid.Store(true)
	if _, err := server.Reload(reloadPlugins(facets.v2(true)), ReloadOptions{Remote: remote}); err != nil {
		t.Fatal(err)
	}
	// Pi waits one setImmediate for the worker's commit.
	waitFor(t, "the worker commit", func() bool {
		hold := eagerRef(worker, eagerSwitchV2.Name)
		return hold != nil && hold.Contract() == eagerSwitchV2
	})
	if hold := eagerRef(server, eagerSwitchV2.Name); hold == nil || hold.Contract() != eagerSwitchV2 {
		t.Fatal("the server's eager hold is not retokened")
	}
	if got := eagerCurrent(t, server); got != `{"1":false}` {
		t.Fatalf("server current = %s", got)
	}
	if got := eagerCurrent(t, worker); got != `{"1":false}` {
		t.Fatalf("worker current = %s", got)
	}
	if server.CodeVersion() != worker.CodeVersion() {
		t.Fatalf("code versions %d and %d", server.CodeVersion(), worker.CodeVersion())
	}
	if got := log.items(); !slices.Equal(got, []string{"open", "open"}) {
		t.Fatalf("log = %q", got)
	}
}

// host-objects-reload.test.ts: a reload that changes a type's lifetime creates its host object, or lets it go.
func TestHostObjectsReloadAReloadThatChangesATypesLifetimeCreatesItsHostObjectOrLetsItGo(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	kept := Local("t.lifetime-change", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{}})
	var log reloadLog
	facet := func(lifetime Lifetime) *Facet {
		return &Facet{Name: "kept", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
			return api.Serve(kept, func(_ any, _ Principal, self Self) (Implementation, error) {
				_, err := self.Resource("life", ResourceDeclaration[any]{
					Version: 1,
					Open:    func(ResourceOpenOptions) (any, error) { log.push("open " + string(lifetime)); return nil, nil },
					Close:   func(any) error { log.push("close"); return nil },
				})
				return chordjson.ObjectOf(), err
			}, ServeOptions{Lifetime: lifetime})
		}}}
	}
	if err := host.Install(*facet(LifetimeReferenced), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := log.items(); len(got) != 0 {
		t.Fatalf("a referenced object waits for its first reference: log = %q", got)
	}
	if _, err := host.Reload(reloadPlugins(facet(LifetimeHost)), ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := host.Ready(); err != nil {
		t.Fatal(err)
	}
	if got := log.items(); !slices.Equal(got, []string{"open host"}) {
		t.Fatalf("created by the reload that made it a host object: log = %q", got)
	}
	if _, err := host.Reload(reloadPlugins(facet(LifetimeReferenced)), ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the host object to be disposed", func() bool { return len(log.items()) >= 2 })
	if got := log.items(); !slices.Equal(got, []string{"open host", "close"}) {
		t.Fatalf("nobody else references it: disposed: log = %q", got)
	}
}
