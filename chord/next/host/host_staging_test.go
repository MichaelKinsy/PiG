package host

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// pi: packages/chord/src/next/host/staging.ts
// Ports packages/chord/test/next/host/staging.test.ts: staging (slice 7b §2, per plugin since slice 7c): a reload's setups
// and recorded factories; each kind of throw keeps the failing plugin on its old code, reported; recorded declarations decide
// an object's fate.

var stagingStore = mustContract(declareLocal("app.store", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{},
	Members: []contracts.LocalEntry{contracts.Mem("label", contracts.Str()), contracts.Mem("bump", contracts.NewMethod(contracts.Schema{}, contracts.Int()))},
}))

// stagingStoreImpl is `{ label: constant(label), bump: async () => 0 }`.
func stagingStoreImpl(label string) contracts.Implementation {
	implementation := impl("label", constant(label))
	implementation.Members.Methods = map[string]contracts.MethodFunction{
		"bump": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return float64(0), nil },
	}
	return implementation
}

// stagingServeStore is `serveStore(label, declare)`: serves Store labelled label, its factory running declare first.
func stagingServeStore(label string, declare func(self StoreSelf)) Facet {
	return DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(stagingStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			if declare != nil {
				declare(self.(StoreSelf))
			}
			return stagingStoreImpl(label), nil
		}, ServeOptions{})
	})
}

// stagingFailedReload is `failedReload`: the error of the one plugin facets' reload reports failed (it kept its old code).
func stagingFailedReload(t *testing.T, host Host, facets []HostFacet) error {
	t.Helper()
	report, err := host.Reload(facets, background, ReloadOptions{})
	must(t, err)
	if len(report.Failed) != 1 {
		t.Fatalf("failed = %v, want one", failedMessages(report.Failed))
	}
	return report.Failed[0].Error
}

func stagingDoc(t testing.TB, name string, version int, migrate func(old *delta.JsonObject, from int) (*delta.JsonObject, error)) *documents.StateDefinition {
	t.Helper()
	definition, err := documents.DefineState(name, documents.StateOptions{
		Schema: contracts.Obj(contracts.Prop("n", contracts.Int())), Version: version,
		Initial: func() *delta.JsonObject { return jsonObject("n", float64(0)) }, Migrate: migrate,
	})
	must(t, err)
	return definition
}

// Mirrors staging.test.ts "staging: a throw keeps the plugin on its old code, reported › <what>: the reload reports it, the plugin unchanged".
func TestStagingAThrowKeepsThePluginOnItsOldCodeReported(t *testing.T) {
	mustPanicFacet := func(declare func(self StoreSelf)) Facet { return stagingServeStore("v2", declare) }
	cases := []struct {
		what    string
		facet   func(t *testing.T) Facet
		message string
	}{
		{"a setup", func(*testing.T) Facet {
			return DefineFacet(func(FacetHost, FacetSelf) error { return errors.New("boom") })
		}, `plugin "@test/app": boom`},
		{"a factory", func(*testing.T) Facet {
			return mustPanicFacet(func(StoreSelf) { panic(errors.New("boom")) })
		}, `plugin "@test/app", app.store: boom`},
		{"a declaration error", func(t *testing.T) Facet {
			return mustPanicFacet(func(self StoreSelf) {
				self.State(stagingDoc(t, "d", 1, nil))
				self.State(stagingDoc(t, "d", 1, nil))
			})
		}, `state "d" is declared twice`},
		{"a document at an older version than the object's", func(t *testing.T) Facet {
			return mustPanicFacet(func(self StoreSelf) { self.State(stagingDoc(t, "doc", 1, nil)) })
		}, "version 1 is older than its documents' (2)"},
		{"a broken accepts table", func(*testing.T) Facet {
			return DefineFacet(func(h FacetHost, _ FacetSelf) error {
				return h.Serve(stagingStore, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
					return stagingStoreImpl("x"), nil
				}, ServeOptions{Accepts: contracts.AcceptsTable{{Name: "7", Value: contracts.VersionMappings{}}}})
			})
		}, "accepts[7]: not an older version"},
	}
	for _, c := range cases {
		t.Run(c.what+": the reload reports it, the plugin unchanged", func(t *testing.T) {
			host := newHost(t, HostOptions{OnError: noopOnError})
			defer closeHosts(t, host)
			v2 := stagingDoc(t, "doc", 2, func(*delta.JsonObject, int) (*delta.JsonObject, error) { return jsonObject("n", float64(0)), nil })
			mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: stagingServeStore("v1", func(self StoreSelf) { self.State(v2) })}}, background))
			ref := mustGet(t, host, stagingStore, jsonObject())
			err := stagingFailedReload(t, host, []HostFacet{{Name: "@test/app", Facet: c.facet(t)}})
			if !strings.Contains(err.Error(), c.message) {
				t.Fatalf("error = %q, want it to contain %q", err, c.message)
			}
			if label := memberOf(t, ref, "label"); label != "v1" {
				t.Fatalf("label = %v, want v1", label)
			}
			if state := refStateOf(ref); state.Kind != RefStateLive {
				t.Fatalf("state = %+v, want live", state)
			}
		})
	}
}

// Mirrors "staging: a throw keeps the plugin on its old code, reported › a remote type on a host without an address is refused, as at load".
func TestStagingARemoteTypeOnAHostWithoutAnAddressIsRefusedAsAtLoad(t *testing.T) {
	remoteType := mustContract(declareRemote("app.remote", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{},
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
		Variants:    []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}})},
	}))
	host := newHost(t, HostOptions{Principal: serverPrincipal(), OnError: noopOnError})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load(nil, background))
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(remoteType, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
			implementation := impl("n", constant(float64(1)))
			implementation.Variant = "v"
			return implementation, nil
		}, ServeOptions{})
	})
	err := stagingFailedReload(t, host, []HostFacet{{Name: "@test/app", Facet: facet}})
	if !strings.Contains(err.Error(), "a host without an address serves only local types") {
		t.Fatalf("error = %q", err)
	}
}

// Mirrors "staging: recorded declarations decide the fate › records without activating: no native opens, no reference binds, no document is made".
func TestStagingRecordsWithoutActivatingNoNativeOpensNoReferenceBindsNoDocumentIsMade(t *testing.T) {
	dep := mustContract(declareLocal("app.dep", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
	var opened atomic.Int32
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: stagingServeStore("v1", nil)}}, background))
	ref := mustGet(t, host, stagingStore, jsonObject())
	facet := stagingServeStore("v2", func(self StoreSelf) {
		selfOpen(self, func(context.Context) error { opened.Add(1); return nil }, func(NativeCloseReason) {})
		self.Get(dep, jsonObject())
		self.State(stagingDoc(t, "fresh", 1, nil))
	})
	report, err := host.Reload([]HostFacet{{Name: "@test/app", Facet: facet}}, background, ReloadOptions{})
	must(t, err)
	// Its readiness binds the recorded request: `Dep` is missing, so the restart is reported parked.
	if len(report.Objects) != 1 || report.Objects[0].Object != "app.store" || report.Objects[0].Kind != ReloadParked || report.Objects[0].Cause.Kind != data.FailureWaiting {
		t.Fatalf("objects = %+v, want app.store parked waiting", report.Objects)
	}
	// Neither staging nor the restart opened it: the new attempt parks on the missing `Dep` first.
	state := untilState(t, ref, func(current *RefState) bool { return current.Kind == RefStateFailed })
	if state.Cause.Kind != data.FailureWaiting || opened.Load() != 0 {
		t.Fatalf("state = %+v, opened %d natives; want failed waiting, none", state, opened.Load())
	}
}

// Own test (Pi's `INERT`, `staging.ts`: every property and call of a handle of a recording `self` gives the proxy again, so
// `self.state(D).value` inside a factory is harmless while staging): a factory that reads and changes the handles it
// declares is not a staging failure; the plugin swaps.
func TestStagingAFactoryThatUsesTheHandlesItDeclaresIsInertNotAFailure(t *testing.T) {
	chordStore := memoryStore(t)
	items, err := store.DefineTable("items", store.TableOptions{Version: 1, Columns: []store.ColumnProp{store.Col("id", store.Str()), store.Col("name", store.Str())}, Key: []string{"id"}})
	must(t, err)
	single := stagingDoc(t, "single", 1, nil)
	keyed, err := documents.DefineState("keyed", documents.StateOptions{
		Schema: contracts.Obj(contracts.Prop("n", contracts.Int())), Version: 1, Keyed: true,
		Initial: func() *delta.JsonObject { return jsonObject("n", float64(0)) },
	})
	must(t, err)
	var runs atomic.Int32
	uses := func(self StoreSelf) {
		runs.Add(1)
		// A real attempt's calls would run its transactions and queries: only the recording's are made.
		_, recording := self.(*recordingSelf)
		singleHandle := self.State(single).(*documents.Singleton)
		_, _ = singleHandle.Value()
		keyedHandle := self.State(keyed).(*documents.Keyed)
		table := self.Table(items)
		if !recording {
			return
		}
		_, _ = singleHandle.Watch(background)
		_, _ = singleHandle.Change(background, func(draft *delta.Object) (any, error) { return nil, nil })
		_ = singleHandle.Retire(background)
		_, _ = keyedHandle.Get(background, "a")
		_, _ = keyedHandle.At("a")
		_, _, _ = keyedHandle.Keys(background).Next(background)
		_, _ = keyedHandle.Change(background, "a", func(draft *delta.Object) (any, error) { return nil, nil })
		_ = keyedHandle.Retire(background, "a")
		_, _ = table.Get(background, "a")
		_, _ = table.Count(background, store.NewQuery(items))
		_ = table.AssertVersion()
	}
	host := newHost(t, HostOptions{Store: chordStore, OnError: noopOnError})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: stagingServeStore("v1", uses)}}, background))
	ref := mustGet(t, host, stagingStore, jsonObject())
	report, err := host.Reload([]HostFacet{{Name: "@test/app", Facet: stagingServeStore("v2", uses)}}, background, ReloadOptions{})
	must(t, err)
	if len(report.Failed) != 0 {
		t.Fatalf("failed = %v, want none: a recorded factory's handles are inert", failedMessages(report.Failed))
	}
	if want := []ReloadedObject{{Object: "app.store", Kind: ReloadRestarted}}; !reflect.DeepEqual(report.Objects, want) {
		t.Fatalf("objects = %+v, want %+v", report.Objects, want)
	}
	scopeEventually(t, "the new attempt serves", func() bool { return memberOf(t, ref, "label") == "v2" })
	if runs.Load() != 3 {
		t.Fatalf("the factory ran %d times, want 3 (the first attempt, staging, the restart's new attempt)", runs.Load())
	}
}

// Ports packages/chord/test/host/staging.test.ts:145; the factories declare their table with `self.table(V1)` (staging.test.ts:150, StoreSelf.Table).
// Mirrors "staging: recorded declarations decide the fate › a table of the new factory at a newer version migrates at the restart's new attempt".
func TestStagingATableOfTheNewFactoryAtANewerVersionThanTheStoresRestartsTheObject(t *testing.T) {
	chordStore := memoryStore(t)
	v1, err := store.DefineTable("items", store.TableOptions{Version: 1, Columns: []store.ColumnProp{store.Col("id", store.Str()), store.Col("name", store.Str())}, Key: []string{"id"}})
	must(t, err)
	v2, err := store.EvolveTable(v1, func(e *store.TableEditor) *store.TableEditor { return e.Rename("name", "title") })
	must(t, err)
	host := newHost(t, HostOptions{Store: chordStore, OnError: noopOnError})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: stagingServeStore("v1", func(self StoreSelf) { self.Table(v1) })}}, background))
	ref := mustGet(t, host, stagingStore, jsonObject())
	same, err := host.Reload([]HostFacet{{Name: "@test/app", Facet: stagingServeStore("v2", func(self StoreSelf) { self.Table(v1) })}}, background, ReloadOptions{})
	must(t, err)
	if want := []ReloadedObject{{Object: "app.store", Kind: ReloadRestarted}}; !reflect.DeepEqual(same.Objects, want) {
		t.Fatalf("objects = %+v, want %+v", same.Objects, want)
	}
	scopeEventually(t, "the first restart's new attempt serves", func() bool { return memberOf(t, ref, "label") == "v2" })
	newer, err := host.Reload([]HostFacet{{Name: "@test/app", Facet: stagingServeStore("v3", func(self StoreSelf) { self.Table(v2) })}}, background, ReloadOptions{})
	must(t, err)
	if want := []ReloadedObject{{Object: "app.store", Kind: ReloadRestarted}}; !reflect.DeepEqual(newer.Objects, want) {
		t.Fatalf("objects = %+v, want %+v", newer.Objects, want)
	}
	// The table migrates at the restart's new attempt.
	scopeEventually(t, "the table migrates at the new attempt", func() bool {
		version, ok := chordStore.TableVersion(store.TableSqlName("app.store", "items"))
		return ok && version == 2
	})
}

// Mirrors "staging: recorded declarations decide the fate › a job declared at its version with another schema is refused at staging".
func TestStagingAJobDeclaredAtItsVersionWithAnotherSchemaIsRefusedAtStaging(t *testing.T) {
	job1, err := DefineJob("app.job", JobOptions{Version: 1, Args: contracts.Obj(), Result: contracts.Int()})
	must(t, err)
	job1b, err := DefineJob("app.job", JobOptions{Version: 1, Args: contracts.Obj(contracts.Prop("x", contracts.Int())), Result: contracts.Int()})
	must(t, err)
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	run := JobImplementation{Run: func(any, JobCtx) (any, error) { return float64(1), nil }}
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: stagingServeStore("v1", func(self StoreSelf) { self.Job(job1, run) })}}, background))
	mustGet(t, host, stagingStore, jsonObject())
	facet := stagingServeStore("v2", func(self StoreSelf) { self.Job(job1b, run) })
	err = stagingFailedReload(t, host, []HostFacet{{Name: "@test/app", Facet: facet}})
	if !strings.Contains(err.Error(), "schema changed without a version bump") {
		t.Fatalf("error = %q", err)
	}
}

// Mirrors "staging: recorded declarations decide the fate › a contribution outside a host-lifetime object is a declaration error at staging".
func TestStagingAContributionOutsideAHostLifetimeObjectIsADeclarationErrorAtStaging(t *testing.T) {
	names, err := registries.DefineRegistry[string, []string]("app.names", registries.DefineRegistryOptions{Version: 1})
	must(t, err)
	page := mustContract(declareLocal("app.page", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())}, Members: []contracts.LocalEntry{contracts.Mem("id", contracts.Int())}}))
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	pageFacet := func(contribute bool) Facet {
		return DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(page, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
				if contribute {
					self.(StoreSelf).Contribute(registries.Registry[any, any](names), "x")
				}
				return impl("id", constant(params.Value("id"))), nil
			}, ServeOptions{})
		})
	}
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/app", Facet: pageFacet(false)}}, background))
	mustGet(t, host, page, jsonObject("id", float64(1)))
	err = stagingFailedReload(t, host, []HostFacet{{Name: "@test/app", Facet: pageFacet(true)}})
	if !strings.Contains(err.Error(), "self.contribute is valid only in a local type with empty params") {
		t.Fatalf("error = %q", err)
	}
}

// Mirrors "staging: declaration parity with an attempt (fix round 1) › refuses retryOn of an attempt-owned writable, the plugin kept on its old code".
func TestStagingDeclarationParityWithAnAttemptRefusesRetryOnOfAnAttemptOwnedWritableThePluginKeptOnItsOldCode(t *testing.T) {
	typ := mustContract(declareLocal("astra.staging", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
	oldFacet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(typ, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return impl("n", constant(float64(1))), nil
		}, ServeOptions{})
	})
	newFacet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(typ, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.RetryOn(self.Writable(float64(0)))
			return impl("n", constant(float64(2))), nil
		}, ServeOptions{})
	})
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@astra/staging", Facet: oldFacet}}, background))
	old := mustGet(t, host, typ, jsonObject())
	report, err := host.Reload([]HostFacet{{Name: "@astra/staging", Facet: newFacet}}, background, ReloadOptions{})
	must(t, err)
	state := refStateOf(old)
	old.Release()
	got := []string{}
	for _, failed := range report.Failed {
		got = append(got, failed.Plugin)
	}
	if !reflect.DeepEqual(got, []string{"@astra/staging"}) || state.Kind != RefStateLive {
		t.Fatalf("failed %v, state %v; want [@astra/staging], live", got, state.Kind)
	}
}

// --- staging per plugin (slice 7c §4) ---------------------------------------------------------------------------

var (
	stagingNotesV1 = mustContract(declareLocal("app.notes", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("count", contracts.Int())}}))
	stagingNotesV2 = mustContract(declareLocal("app.notes", contracts.LocalDeclaration{Version: 2, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("count", contracts.Int()), contracts.Mem("title", contracts.Str())}}))
	stagingView    = mustContract(declareLocal("app.view", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("label", contracts.Str())}}))
	stagingExtra   = mustContract(declareLocal("app.extra", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
)

// stagingNotes is the notes provider at version, labelled through its count.
func stagingNotes(version int, count float64) Facet {
	return DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if version == 1 {
			return h.Serve(stagingNotesV1, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
				return impl("count", constant(count)), nil
			}, ServeOptions{})
		}
		return h.Serve(stagingNotesV2, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return impl("count", constant(count), "title", constant("t")), nil
		}, ServeOptions{})
	})
}

// stagingViewFacet is a consumer of the notes at version, labelled label and the count it reads.
func stagingViewFacet(version int, label string) Facet {
	return DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(stagingView, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			notes := stagingNotesV1
			if version == 2 {
				notes = stagingNotesV2
			}
			count := self.Get(notes, jsonObject()).Member("count")
			return impl("label", self.Derive([]any{count}, func(values []any) (delta.JsonValue, error) {
				return label + " " + stagingNumberText(values[0]), nil
			})), nil
		}, ServeOptions{})
	})
}

// stagingNumberText is a JSON number as a template literal writes it.
func stagingNumberText(value any) string { return fmt.Sprint(value) }

var stagingThrowing = DefineFacet(func(FacetHost, FacetSelf) error { return errors.New("setup threw") })

// Mirrors "staging per plugin (slice 7c §4) › a failing plugin keeps its old code; the others of the reload apply".
func TestStagingPerPluginAFailingPluginKeepsItsOldCodeTheOthersOfTheReloadApply(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/notes", Facet: stagingNotes(1, 1)}, {Name: "@test/store", Facet: stagingServeStore("v1", nil)}}, background))
	store := mustGet(t, host, stagingStore, jsonObject())
	counted := mustGet(t, host, stagingNotesV1, jsonObject())
	report, err := host.Reload([]HostFacet{{Name: "@test/notes", Facet: stagingThrowing}, {Name: "@test/store", Facet: stagingServeStore("v2", nil)}}, background, ReloadOptions{})
	must(t, err)
	wantReloadPlugins(t, report, nil, []string{"@test/store"}, nil)
	if got, want := failedMessages(report.Failed), [][2]string{{"@test/notes", `plugin "@test/notes": setup threw`}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("failed = %v, want %v", got, want)
	}
	if want := []ReloadedObject{{Object: "app.store", Kind: ReloadRestarted}}; !reflect.DeepEqual(report.Objects, want) {
		t.Fatalf("objects = %+v, want %+v", report.Objects, want)
	}
	// Restarted: the new attempt goes live once the old one ended.
	scopeEventually(t, "the store's new attempt serves", func() bool { return memberOf(t, store, "label") == "v2" })
	if got := memberOf(t, counted, "count"); got != float64(1) {
		t.Fatalf("count = %v, want 1", got)
	}
	if state := refStateOf(counted); state.Kind != RefStateLive {
		t.Fatalf("state = %+v, want live", state)
	}
	// Fixed later, it applies as any change does.
	fixed, err := host.Reload([]HostFacet{{Name: "@test/notes", Facet: stagingNotes(1, 2)}, {Name: "@test/store", Facet: stagingServeStore("v2", nil)}}, background, ReloadOptions{})
	must(t, err)
	if !reflect.DeepEqual(fixed.Plugins.Changed, []string{"@test/notes", "@test/store"}) || len(fixed.Failed) != 0 {
		t.Fatalf("changed %v, failed %v; want both, none", fixed.Plugins.Changed, failedMessages(fixed.Failed))
	}
}

// wantReloadPlugins fails unless report's plugins are added, changed and removed (nil is none).
func wantReloadPlugins(t testing.TB, report ReloadReport, added, changed, removed []string) {
	t.Helper()
	want := PluginChanges{Added: added, Changed: changed, Removed: removed}
	if !reflect.DeepEqual(report.Plugins, want) {
		t.Fatalf("plugins = %+v, want %+v", report.Plugins, want)
	}
}

// Mirrors "staging per plugin › a factory throwing in staging fails its plugin alone; an added plugin that fails is left out".
func TestStagingPerPluginAFactoryThrowingInStagingFailsItsPluginAloneAnAddedPluginThatFailsIsLeftOut(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/notes", Facet: stagingNotes(1, 1)}, {Name: "@test/store", Facet: stagingServeStore("v1", nil)}}, background))
	mustGet(t, host, stagingStore, jsonObject())
	counted := mustGet(t, host, stagingNotesV1, jsonObject())
	report, err := host.Reload([]HostFacet{
		{Name: "@test/extra", Facet: stagingThrowing},
		{Name: "@test/notes", Facet: stagingNotes(1, 2)},
		{Name: "@test/store", Facet: stagingServeStore("v2", func(StoreSelf) { panic(errors.New("factory threw")) })},
	}, background, ReloadOptions{})
	must(t, err)
	wantReloadPlugins(t, report, nil, []string{"@test/notes"}, nil)
	want := [][2]string{{"@test/extra", `plugin "@test/extra": setup threw`}, {"@test/store", `plugin "@test/store", app.store: factory threw`}}
	if got := failedMessages(report.Failed); !reflect.DeepEqual(got, want) {
		t.Fatalf("failed = %v, want %v", got, want)
	}
	scopeEventually(t, "the notes' new attempt serves", func() bool { return memberOf(t, counted, "count") == float64(2) })
}

// Mirrors "staging per plugin › the closure rule: a consumer edited together with a failing provider keeps its old code".
func TestStagingPerPluginTheClosureRuleAConsumerEditedTogetherWithAFailingProviderKeepsItsOldCode(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/notes", Facet: stagingNotes(1, 1)}, {Name: "@test/view", Facet: stagingViewFacet(1, "old")}}, background))
	shown := mustGet(t, host, stagingView, jsonObject())
	report, err := host.Reload([]HostFacet{{Name: "@test/notes", Facet: stagingThrowing}, {Name: "@test/view", Facet: stagingViewFacet(2, "new")}}, background, ReloadOptions{})
	must(t, err)
	if len(report.Plugins.Changed) != 0 {
		t.Fatalf("changed = %v, want none", report.Plugins.Changed)
	}
	want := [][2]string{
		{"@test/notes", `plugin "@test/notes": setup threw`},
		{"@test/view", `plugin "@test/view": needs app.notes v2, which plugin "@test/notes" serves on its old code`},
	}
	if got := failedMessages(report.Failed); !reflect.DeepEqual(got, want) {
		t.Fatalf("failed = %v, want %v", got, want)
	}
	if len(report.Objects) != 0 {
		t.Fatalf("objects = %+v, want none", report.Objects)
	}
	turn(host)
	if got := memberOf(t, shown, "label"); got != "old 1" || refStateOf(shown).Kind != RefStateLive {
		t.Fatalf("label %v, state %v; want old 1, live", got, refStateOf(shown).Kind)
	}
	// Both fixed together: both apply.
	both, err := host.Reload([]HostFacet{{Name: "@test/notes", Facet: stagingNotes(2, 5)}, {Name: "@test/view", Facet: stagingViewFacet(2, "new")}}, background, ReloadOptions{})
	must(t, err)
	if !reflect.DeepEqual(both.Plugins.Changed, []string{"@test/notes", "@test/view"}) || len(both.Failed) != 0 {
		t.Fatalf("changed %v, failed %v", both.Plugins.Changed, failedMessages(both.Failed))
	}
	scopeEventually(t, "both restarted objects serve the new code", func() bool { return memberOf(t, shown, "label") == "new 5" })
}

// Mirrors "staging per plugin › takes the plugins whose new code failed before staging (`failed`): the closure rule and the report see them".
func TestStagingPerPluginTakesThePluginsWhoseNewCodeFailedBeforeStagingTheClosureRuleAndTheReportSeeThem(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	provider := stagingNotes(1, 1)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/notes", Facet: provider}, {Name: "@test/view", Facet: stagingViewFacet(1, "old")}}, background))
	shown := mustGet(t, host, stagingView, jsonObject())
	// Its new build failed: the caller passes its loaded facet, and says so.
	before := []PluginFailure{{Plugin: "@test/notes", Error: errors.New("build failed")}}
	report, err := host.Reload([]HostFacet{{Name: "@test/notes", Facet: provider}, {Name: "@test/view", Facet: stagingViewFacet(2, "new")}}, background, ReloadOptions{Failed: before})
	must(t, err)
	want := [][2]string{
		{"@test/notes", "build failed"},
		{"@test/view", `plugin "@test/view": needs app.notes v2, which plugin "@test/notes" serves on its old code`},
	}
	if got := failedMessages(report.Failed); !reflect.DeepEqual(got, want) {
		t.Fatalf("failed = %v, want %v", got, want)
	}
	if len(report.Plugins.Changed) != 0 || memberOf(t, shown, "label") != "old 1" {
		t.Fatalf("changed %v, label %v; want none, old 1", report.Plugins.Changed, memberOf(t, shown, "label"))
	}
}

// Mirrors "staging per plugin › the closure rule checks a request as a real bind does, the kept provider's host address included (Astra)".
func TestStagingPerPluginTheClosureRuleChecksARequestAsARealBindDoesTheKeptProvidersHostAddressIncluded(t *testing.T) {
	// The provider's own code places every id but 1 on another host; the consumer's contract computes this host.
	provided := mustContract(declareRemote("app.provider", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
		HostAddress: func(params *delta.JsonObject) (contracts.Address, error) {
			if params.Value("id") != float64(1) {
				return elsewhereKind.Address(nil), nil
			}
			return hereKind.Address(nil), nil
		},
		Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}})},
	}))
	consumed := mustContract(declareRemote("app.provider", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return hereKind.Address(nil), nil },
		Variants:    []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}})},
	}))
	consumer := mustContract(declareLocal("app.consumer", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
	consumerFacet := func(id float64) Facet {
		return DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(consumer, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				n := self.Get(consumed, jsonObject("id", id)).Member("n")
				return impl("n", self.Derive([]any{n}, func(values []any) (delta.JsonValue, error) { return values[0], nil })), nil
			}, ServeOptions{})
		})
	}
	address := hereKind.Address(nil)
	host := newHost(t, HostOptions{Address: &address, Principal: ownerPrincipal(), OnError: noopOnError})
	defer closeHosts(t, host)
	providerFacet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(provided, func(params *delta.JsonObject, _ data.Principal, _ Self) (contracts.Implementation, error) {
			implementation := impl("n", constant(params.Value("id")))
			implementation.Variant = "main"
			return implementation, nil
		}, ServeOptions{})
	})
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/provider", Facet: providerFacet}, {Name: "@test/consumer", Facet: consumerFacet(1)}}, background))
	shown := mustGet(t, host, consumer, jsonObject())
	report, err := host.Reload([]HostFacet{{Name: "@test/provider", Facet: stagingThrowing}, {Name: "@test/consumer", Facet: consumerFacet(2)}}, background, ReloadOptions{})
	must(t, err)
	got := []string{}
	for _, failed := range report.Failed {
		got = append(got, failed.Plugin)
	}
	if !reflect.DeepEqual(got, []string{"@test/provider", "@test/consumer"}) {
		t.Fatalf("failed = %v, want provider and consumer", got)
	}
	if state := refStateOf(shown); state.Kind != RefStateLive || memberOf(t, shown, "n") != float64(1) {
		t.Fatalf("state %v, n %v; want live, 1", state.Kind, memberOf(t, shown, "n"))
	}
}

// Mirrors "staging per plugin › the closure rule covers a name nobody serves while a plugin failed, and runs to a fixpoint".
func TestStagingPerPluginTheClosureRuleCoversANameNobodyServesWhileAPluginFailedAndRunsToAFixpoint(t *testing.T) {
	middleType := mustContract(declareLocal("app.middle", contracts.LocalDeclaration{Version: 2, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
	topType := mustContract(declareLocal("app.top", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	chain := func(typ, needed *contracts.Contract, needs bool) Facet {
		return DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(typ, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				if needs {
					return impl("n", Expose(self.Get(needed, jsonObject()).Member("n"))), nil
				}
				return impl("n", constant(float64(0))), nil
			}, ServeOptions{})
		})
	}
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/middle", Facet: chain(middleType, stagingExtra, false)}, {Name: "@test/top", Facet: chain(topType, middleType, false)}}, background))
	mustGet(t, host, topType, jsonObject())
	mustGet(t, host, middleType, jsonObject())
	// The new extra plugin fails: the middle's new code needs Extra, served by nobody now; the top's new code needs the
	// middle, which keeps its old code (another version is not involved: its request binds to the old one).
	report, err := host.Reload([]HostFacet{
		{Name: "@test/extra", Facet: stagingThrowing},
		{Name: "@test/middle", Facet: chain(middleType, stagingExtra, true)},
		{Name: "@test/top", Facet: chain(topType, middleType, true)},
	}, background, ReloadOptions{})
	must(t, err)
	want := [][2]string{
		{"@test/extra", `plugin "@test/extra": setup threw`},
		{"@test/middle", `plugin "@test/middle": needs app.extra v1, which no plugin serves now (a plugin failed)`},
	}
	if got := failedMessages(report.Failed); !reflect.DeepEqual(got, want) {
		t.Fatalf("failed = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(report.Plugins.Changed, []string{"@test/top"}) {
		t.Fatalf("changed = %v, want [@test/top]", report.Plugins.Changed)
	}
}

// Mirrors "staging per plugin › without a failing plugin, a request that does not bind is an ordinary wait: the consumer applies and parks".
func TestStagingPerPluginWithoutAFailingPluginARequestThatDoesNotBindIsAnOrdinaryWaitTheConsumerAppliesAndParks(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/notes", Facet: stagingNotes(1, 1)}, {Name: "@test/view", Facet: stagingViewFacet(1, "old")}}, background))
	shown := mustGet(t, host, stagingView, jsonObject())
	report, err := host.Reload([]HostFacet{{Name: "@test/notes", Facet: stagingNotes(1, 1)}, {Name: "@test/view", Facet: stagingViewFacet(2, "new")}}, background, ReloadOptions{})
	must(t, err)
	if !reflect.DeepEqual(report.Plugins.Changed, []string{"@test/notes", "@test/view"}) || len(report.Failed) != 0 {
		t.Fatalf("changed %v, failed %v", report.Plugins.Changed, failedMessages(report.Failed))
	}
	if len(report.Objects) != 2 || report.Objects[0] != (ReloadedObject{Object: "app.notes", Kind: ReloadRestarted}) ||
		report.Objects[1].Object != "app.view" || report.Objects[1].Kind != ReloadParked {
		t.Fatalf("objects = %+v, want notes restarted and view parked", report.Objects)
	}
	turn(host)
	if state := refStateOf(shown); state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting {
		t.Fatalf("state = %+v, want failed waiting", state)
	}
}

// Mirrors "staging per plugin › a type moving between plugins while its old owner fails: the new owner collides and keeps its old code too".
func TestStagingPerPluginATypeMovingBetweenPluginsWhileItsOldOwnerFailsTheNewOwnerCollidesAndKeepsItsOldCodeToo(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/a", Facet: stagingNotes(1, 1)}, {Name: "@test/b", Facet: DefineFacet(func(FacetHost, FacetSelf) error { return nil })}}, background))
	counted := mustGet(t, host, stagingNotesV1, jsonObject())
	report, err := host.Reload([]HostFacet{{Name: "@test/a", Facet: stagingThrowing}, {Name: "@test/b", Facet: stagingNotes(1, 2)}}, background, ReloadOptions{})
	must(t, err)
	want := [][2]string{
		{"@test/a", `plugin "@test/a": setup threw`},
		{"@test/b", `plugin "@test/b": app.notes is served twice (plugin "@test/a")`},
	}
	if got := failedMessages(report.Failed); !reflect.DeepEqual(got, want) {
		t.Fatalf("failed = %v, want %v", got, want)
	}
	if got := memberOf(t, counted, "count"); got != float64(1) {
		t.Fatalf("count = %v, want 1", got)
	}
}
