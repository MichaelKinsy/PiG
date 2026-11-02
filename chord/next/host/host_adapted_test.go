package host

import (
	"context"
	"reflect"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// pi: packages/chord/src/next/host/adapted.ts
// Ports packages/chord/test/next/host/adapted.test.ts (Q10, §22, slice 7b §7): consumers bound at an older version a provider
// accepts read its state mapped (diffs per commit), call it with mapped arguments and results or its replacement of a
// removed method, read keyed members mapped per key, and are refused a member the provider refuses. Every describe of the
// file is ported, in its order; the helpers are in host_adapted_support_test.go.

// adaptedNotesV1 is v1: a list of texts, `add` taking a text and returning the count, `clear`, keyed notes of a title.
var adaptedNotesV1 = mustContract(declareLocal("app.notes", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("items", contracts.Arr(contracts.Str())),
		contracts.Mem("add", contracts.NewMethod(contracts.Obj(contracts.Prop("text", contracts.Str())), contracts.Int())),
		contracts.Mem("clear", contracts.NewMethod()),
		contracts.Mem("notes", contracts.Keyed(contracts.Obj(contracts.Prop("title", contracts.Str())))),
	},
}))

// adaptedNotesV2 is v2: items carry a tag, `add` takes one and returns an object, `clear` is gone, notes have a body.
var adaptedNotesV2 = func() *contracts.Contract {
	tagged := contracts.Obj(contracts.Prop("text", contracts.Str()), contracts.Prop("tag", contracts.Str()))
	evolved, err := contracts.Evolve(adaptedNotesV1, func(e *contracts.LocalEditor) *contracts.LocalEditor {
		return e.State("items", contracts.Arr(tagged)).
			Method("add", tagged, contracts.Obj(contracts.Prop("count", contracts.Int()))).
			RemoveMethod("clear").
			Keyed("notes", contracts.Obj(contracts.Prop("title", contracts.Str()), contracts.Prop("body", contracts.Str())))
	})
	if err != nil {
		panic(err)
	}
	return evolved
}()

var adaptedItems = func() *documents.StateDefinition {
	definition, err := documents.DefineState("items", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("items", contracts.Arr(contracts.Obj(contracts.Prop("text", contracts.Str()), contracts.Prop("tag", contracts.Str()))))),
		Version: 1, Initial: func() *delta.JsonObject { return jsonObject("items", []any{}) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

var adaptedNotes = func() *documents.StateDefinition {
	definition, err := documents.DefineState("notes", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("title", contracts.Str()), contracts.Prop("body", contracts.Str())),
		Version: 1, Keyed: true, Initial: func() *delta.JsonObject { return jsonObject("title", "", "body", "") },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

// serveAdaptedV2 serves `NotesV2`, accepting v1 consumers (`clear` replaced, or refused with refuse).
func serveAdaptedV2(h FacetHost, refuse bool) error {
	return h.Serve(adaptedNotesV2, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
		items := stateOf(self, adaptedItems)
		notes := self.State(adaptedNotes).(*documents.Keyed)
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"items": self.Derive([]any{items.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("items"), nil
				}),
			},
			Keyed: kmKeyed(notes),
			Methods: map[string]contracts.MethodFunction{"add": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				given := args.(*delta.JsonObject)
				text, tag := given.Value("text"), given.Value("tag")
				if err := items.Change(func(draft *delta.Object) { _, _ = draft.Array("items").Push(jsonObject("text", text, "tag", tag)) }, ctx); err != nil {
					return nil, err
				}
				if _, err := notes.Change(ctx, text.(string), func(draft *delta.Object) (any, error) {
					_ = draft.Set("title", text)
					return nil, draft.Set("body", tag)
				}); err != nil {
					return nil, err
				}
				return jsonObject("count", float64(len(items.Value().Value("items").([]any)))), nil
			}},
		}}, nil
	}, ServeOptions{Accepts: contracts.AcceptsTable{{Name: "1", Value: contracts.VersionMappings{
		State: []contracts.Entry[contracts.StateMapping]{{Name: "items", Value: contracts.StateMapping{
			Func: func(current delta.JsonValue) (delta.JsonValue, error) {
				texts := []any{}
				for _, each := range current.([]any) {
					texts = append(texts, each.(*delta.JsonObject).Value("text"))
				}
				return texts, nil
			},
		}}},
		Methods: []contracts.Entry[contracts.MethodMapping]{
			{Name: "add", Value: contracts.MethodMapping{
				Args: func(old delta.JsonValue) (delta.JsonValue, error) {
					return jsonObject("text", old.(*delta.JsonObject).Value("text"), "tag", "v1"), nil
				},
				Result: func(current delta.JsonValue) (delta.JsonValue, error) {
					return current.(*delta.JsonObject).Value("count"), nil
				},
			}},
			{Name: "clear", Value: adaptedClear(refuse)},
		},
		Keyed: []contracts.Entry[contracts.KeyedMapping]{{Name: "notes", Value: adaptedKeyed(refuse)}},
	}}}})
}

func adaptedClear(refuse bool) contracts.MethodMapping {
	if refuse {
		return contracts.MethodMapping{Refuse: true}
	}
	return contracts.MethodMapping{Call: func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }}
}

func adaptedKeyed(refuse bool) contracts.KeyedMapping {
	if refuse {
		return contracts.KeyedMapping{Refuse: true}
	}
	return contracts.KeyedMapping{Func: func(current delta.JsonValue) (delta.JsonValue, error) {
		return jsonObject("title", current.(*delta.JsonObject).Value("title")), nil
	}}
}

// Mirrors "an older consumer of a provider's accepted version › reads state mapped, with diffs per commit; its calls are mapped forwards and their results back".
func TestAnOlderConsumerOfAProvidersAcceptedVersionReadsStateMappedWithDiffsPerCommitItsCallsAreMappedForwardsAndTheirResultsBack(t *testing.T) {
	meter := nexttest.Metered(t)
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return serveAdaptedV2(h, false) }, loadedOptions{Telemetry: meter.Telemetry, Context: meter.Context})
	defer closeHosts(t, host)
	old := mustGet(t, host, adaptedNotesV1, jsonObject())
	watch, err := old.Member("items").Watch(background)
	must(t, err)
	items := nexttest.ChangesOf(watch)
	for index, text := range []string{"a", "b"} {
		got, err := old.Call("add", jsonObject("text", text), background)
		must(t, err)
		if got != float64(index+1) {
			t.Fatalf("add %q = %v, want %d", text, got, index+1)
		}
	}
	deliveredOn(host)
	// Pi's `changesOf`: the exact list of calls, their kind and `retired` included (a failure or a retirement between the
	// two commits would show).
	calls := items.Items()
	want := [][]any{{[]any{"p", []any{}, 0.0, 0.0, []any{"a"}}}, {[]any{"p", []any{}, 1.0, 0.0, []any{"b"}}}}
	wantValues := []any{[]any{"a"}, []any{"a", "b"}}
	if len(calls) != 2 {
		t.Fatalf("items calls = %+v, want two value calls", calls)
	}
	frames := make([]nexttest.Frame[delta.JsonValue], len(calls))
	for i, call := range calls {
		if call.Kind != chord.WatchCallValue || call.Retired || call.Error != nil || !reflect.DeepEqual(call.Value, wantValues[i]) {
			t.Fatalf("items call %d = %+v, want a value call of %v, not retired", i, call, wantValues[i])
		}
		frames[i] = nexttest.Frame[delta.JsonValue]{Value: call.Value, Ops: call.Ops}
	}
	if !nexttest.JSONEqual(opsOf(frames), []any{want[0], want[1]}) {
		t.Fatalf("items ops = %+v, want the push ops %v", opsOf(frames), want)
	}
	// Once for the watch's first value, then once per commit: never per consumer or per read.
	if got := meter.Counts()["chord.contract.adapts"]; got != 3 {
		t.Fatalf("adapts = %v, want 3", got)
	}
	if value, _ := old.Member("items").Value(); !reflect.DeepEqual(value, []any{"a", "b"}) {
		t.Fatalf("items = %v, want [a b]", value)
	}
	if got := meter.Counts()["chord.contract.adapts"]; got != 3 {
		t.Fatalf("adapts after a read = %v, want 3", got)
	}
}

// Mirrors "› calls the provider's replacement of a removed method; a refused one fails contract_mismatch".
func TestAnOlderConsumerOfAProvidersAcceptedVersionCallsTheProvidersReplacementOfARemovedMethod(t *testing.T) {
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return serveAdaptedV2(h, false) }, loadedOptions{})
	defer closeHosts(t, host)
	old := mustGet(t, host, adaptedNotesV1, jsonObject())
	if got, err := old.Call("clear", nil, background); err != nil || got != nil {
		t.Fatalf("clear = %v, %v, want nil, nil", got, err)
	}
}

// Mirrors "› is refused a binding whose member the provider refuses: it waits contract_mismatch".
func TestAnOlderConsumerOfAProvidersAcceptedVersionIsRefusedABindingWhoseMemberTheProviderRefusesItWaitsContractMismatch(t *testing.T) {
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return serveAdaptedV2(h, true) }, loadedOptions{})
	defer closeHosts(t, host)
	old := mustHold(t, host, adaptedNotesV1, jsonObject())
	state := untilState(t, old, func(s *RefState) bool { return s.Kind == RefStateFailed })
	if state.Cause.Kind != data.FailureWaiting || state.Cause.Error.Code != data.CodeContractMismatch {
		t.Fatalf("state = %+v (cause %+v), want failed waiting contract_mismatch", state, state.Cause)
	}
	_, err := old.Call("clear", nil, background)
	wantChordError(t, err, data.CodeContractMismatch, "", "")
}

// Mirrors "› reads keyed members mapped per key: get, watch and a factory's at(key)".
func TestAnOlderConsumerOfAProvidersAcceptedVersionReadsKeyedMembersMappedPerKeyGetWatchAndAFactorysAtKey(t *testing.T) {
	view := mustContract(declareLocal("app.view", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("key", contracts.Str())},
		Members: []contracts.LocalEntry{contracts.Mem("title", contracts.Str())},
	}))
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{
		{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return serveAdaptedV2(h, false) })},
		{Name: "@test/view", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(view, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
				note, err := self.Get(adaptedNotesV1, jsonObject()).Keyed("notes").At(params.Value("key").(string))
				return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
					"title": self.Derive([]any{Expose(note)}, func(values []any) (delta.JsonValue, error) {
						return values[0].(*delta.JsonObject).Value("title"), nil
					}),
				}}}, err
			}, ServeOptions{})
		})},
	}, background))
	old := mustGet(t, host, adaptedNotesV1, jsonObject())
	if _, err := old.Call("add", jsonObject("text", "a"), background); err != nil {
		t.Fatalf("add: %v", err)
	}
	got, found, err := old.Keyed("notes").Get(background, "a")
	must(t, err)
	if !found || !reflect.DeepEqual(got, jsonObject("title", "a")) {
		t.Fatalf("notes.get(a) = %v (found %v), want {title: a}", got, found)
	}
	watch, err := old.Keyed("notes").Watch(background, "a")
	must(t, err)
	if !reflect.DeepEqual(watch.Value(), jsonObject("title", "a")) {
		t.Fatalf("notes.watch(a).value = %v, want {title: a}", watch.Value())
	}
	reader := mustGet(t, host, view, jsonObject("key", "a"))
	if title := memberOf(t, reader, "title"); title != "a" {
		t.Fatalf("view title = %v, want a", title)
	}
}

// Mirrors "reload: adapted members follow the provider's topology › rebuilds a changed-member adapter as a whole-state adapter when a later version removes it".
func TestReloadAdaptedMembersFollowTheProvidersTopologyRebuildsAChangedMemberAdapterAsAWholeStateAdapterWhenALaterVersionRemovesIt(t *testing.T) {
	v1 := mustContract(declareLocal("astra.adapt", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	v2, err := contracts.Evolve(v1, func(e *contracts.LocalEditor) *contracts.LocalEditor {
		return e.State("n", contracts.Obj(contracts.Prop("value", contracts.Int())))
	})
	must(t, err)
	v3, err := contracts.Evolve(v2, func(e *contracts.LocalEditor) *contracts.LocalEditor {
		return e.RemoveState("n").AddState("m", contracts.Int())
	})
	must(t, err)
	state := func(name string, f func(delta.JsonValue) (delta.JsonValue, error)) []contracts.Entry[contracts.StateMapping] {
		return []contracts.Entry[contracts.StateMapping]{{Name: name, Value: contracts.StateMapping{Func: f}}}
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	first := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(v2, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"n": constant(jsonObject("value", 5.0)),
			}}}, nil
		}, ServeOptions{Accepts: contracts.AcceptsTable{{Name: "1", Value: contracts.VersionMappings{
			State: state("n", func(n delta.JsonValue) (delta.JsonValue, error) { return n.(*delta.JsonObject).Value("value"), nil }),
		}}}})
	})
	second := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(v3, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"m": constant(9.0),
			}}}, nil
		}, ServeOptions{Accepts: contracts.AcceptsTable{{Name: "1", Value: contracts.VersionMappings{
			State: state("n", func(whole delta.JsonValue) (delta.JsonValue, error) { return whole.(*delta.JsonObject).Value("m"), nil }),
		}}}})
	})
	mustLoad(t)(host.Load([]HostFacet{{Name: "@astra/adapt", Facet: first}}, background))
	old := mustGet(t, host, v1, jsonObject())
	if got := memberOf(t, old, "n"); got != 5.0 {
		t.Fatalf("n = %v, want 5", got)
	}
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@astra/adapt", Facet: second}}, background, ReloadOptions{}))
	scopeEventually(t, "the old reference follows the new provider's mapping", func() bool {
		value, err := old.Member("n").Value()
		return err == nil && value == 9.0
	})
	old.Release()
}

// Mirrors "› maps existing local watches when the consumer's current version becomes an accepted older one".
func TestReloadAdaptedMembersFollowTheProvidersTopologyMapsExistingLocalWatchesWhenTheConsumersCurrentVersionBecomesAnAcceptedOlderOne(t *testing.T) {
	v1 := adaptedLocal("sol.state", contracts.Mem("x", contracts.Int()))
	v2 := adaptedEvolve(v1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("x", adaptedObjOf("n")) })
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@sol/state", Facet: adaptedConstants(v1, map[string]delta.JsonValue{"x": 1.0}, nil, nil)}}, background))
	old := mustGet(t, host, v1, jsonObject())
	_, calls := adaptedWatch(t, old, "x")
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@sol/state", Facet: adaptedConstants(v2,
		map[string]delta.JsonValue{"x": jsonObject("n", 7.0)},
		contracts.AcceptsTable{adaptedVersion("1", adaptedState("x", adaptedField("n")))}, nil)}}, background, ReloadOptions{}))
	mustGet(t, host, v2, jsonObject()).Release()
	adaptedExpectValues(t, host, calls, 7.0)
}

// Mirrors "› rebuilds an existing adapted member's dependencies when a later provider removes that member".
func TestReloadAdaptedMembersFollowTheProvidersTopologyRebuildsAnExistingAdaptedMembersDependenciesWhenALaterProviderRemovesThatMember(t *testing.T) {
	v1 := adaptedLocal("sol.removed-member", contracts.Mem("x", contracts.Int()))
	v2 := adaptedEvolve(v1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("x", adaptedObjOf("n")) })
	v3 := adaptedEvolve(v2, func(e *contracts.LocalEditor) *contracts.LocalEditor {
		return e.RemoveState("x").AddState("y", contracts.Int())
	})
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@sol/state", Facet: adaptedConstants(v2, map[string]delta.JsonValue{"x": jsonObject("n", 2.0)},
		contracts.AcceptsTable{adaptedVersion("1", adaptedState("x", adaptedField("n")))}, nil)}}, background))
	old := mustGet(t, host, v1, jsonObject())
	if got := memberOf(t, old, "x"); got != 2.0 {
		t.Fatalf("x = %v, want 2", got)
	}
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@sol/state", Facet: adaptedConstants(v3, map[string]delta.JsonValue{"y": 7.0},
		contracts.AcceptsTable{
			adaptedVersion("1", adaptedState("x", adaptedField("y"))),
			adaptedVersion("2", adaptedState("x", func(state delta.JsonValue) (delta.JsonValue, error) {
				return jsonObject("n", state.(*delta.JsonObject).Value("y")), nil
			})),
		}, nil)}}, background, ReloadOptions{}))
	mustGet(t, host, v3, jsonObject()).Release()
	if state := stateOfRef(old); state.Kind != RefStateLive {
		t.Fatalf("state = %+v, want live", state)
	}
	if got := memberOf(t, old, "x"); got != 7.0 {
		t.Fatalf("x = %v, want 7", got)
	}
}

// Mirrors "reload: adapted members publish one coherent frame (fix round 1) › does not publish the new mapping applied to the old attempt's state".
//
// Pi: packages/chord/test/next/host/adapted.test.ts:282 (does not publish the new mapping applied to the old attempt's state)
func TestReloadAdaptedMembersPublishOneCoherentFrameDoesNotPublishTheNewMappingAppliedToTheOldAttemptsState(t *testing.T) {
	v1 := adaptedLocal("astra.atomic", contracts.Mem("n", contracts.Int()))
	v2 := adaptedEvolve(v1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("n", contracts.Int()) })
	facet := func(n, factor float64) Facet {
		return adaptedConstants(v2, map[string]delta.JsonValue{"n": n}, contracts.AcceptsTable{adaptedVersion("1",
			adaptedState("n", func(v delta.JsonValue) (delta.JsonValue, error) { return v.(float64) * factor, nil }))}, nil)
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@astra/atomic", Facet: facet(2, 2)}}, background))
	old := mustGet(t, host, v1, jsonObject())
	watch, calls := adaptedWatch(t, old, "n")
	deliveredOn(host)
	if got := memberOf(t, old, "n"); got != 4.0 {
		t.Fatalf("n = %v, want 4", got)
	}
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@astra/atomic", Facet: facet(3, 3)}}, background, ReloadOptions{}))
	adaptedExpectValues(t, host, calls, 9.0)
	_, _ = watch.Stop()
	old.Release()
}

// Mirrors "› never computes a new accepted-version mapping over the old provider shape".
//
// Pi: packages/chord/test/next/host/adapted.test.ts:306 (never computes a new accepted-version mapping over the old provider shape)
func TestReloadAdaptedMembersPublishOneCoherentFrameNeverComputesANewAcceptedVersionMappingOverTheOldProviderShape(t *testing.T) {
	v1 := adaptedLocal("sol.mapping-publication", contracts.Mem("x", contracts.Int()))
	v2 := adaptedEvolve(v1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("x", adaptedObjOf("n")) })
	v3 := adaptedEvolve(v2, func(e *contracts.LocalEditor) *contracts.LocalEditor {
		return e.State("x", contracts.Obj(contracts.Prop("wrapped", adaptedObjOf("n"))))
	})
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@sol/state", Facet: adaptedConstants(v2, map[string]delta.JsonValue{"x": jsonObject("n", 2.0)},
		contracts.AcceptsTable{adaptedVersion("1", adaptedState("x", adaptedField("n")))}, nil)}}, background))
	old := mustGet(t, host, v1, jsonObject())
	_, calls := adaptedWatch(t, old, "x")
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@sol/state", Facet: adaptedConstants(v3,
		map[string]delta.JsonValue{"x": jsonObject("wrapped", jsonObject("n", 7.0))},
		contracts.AcceptsTable{
			adaptedVersion("1", adaptedState("x", adaptedField("wrapped", "n"))),
			adaptedVersion("2", adaptedState("x", adaptedField("wrapped"))),
		}, nil)}}, background, ReloadOptions{}))
	mustGet(t, host, v3, jsonObject()).Release()
	adaptedExpectValues(t, host, calls, 7.0)
}

// Mirrors "reload: an older consumer across a removal (fix round 1) › keeps an older watch open and heals it after the type is served again".
func TestReloadAnOlderConsumerAcrossARemovalKeepsAnOlderWatchOpenAndHealsItAfterTheTypeIsServedAgain(t *testing.T) {
	v1 := adaptedLocal("sol.older-removal", contracts.Mem("x", contracts.Int()))
	v2 := adaptedEvolve(v1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("x", adaptedObjOf("n")) })
	facet := func(n float64) Facet {
		return adaptedConstants(v2, map[string]delta.JsonValue{"x": jsonObject("n", n)},
			contracts.AcceptsTable{adaptedVersion("1", adaptedState("x", adaptedField("n")))}, nil)
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@sol/older", Facet: facet(1)}}, background))
	old := mustGet(t, host, v1, jsonObject())
	_, calls := adaptedWatch(t, old, "x")
	reloadMust(t)(host.Reload(nil, background, ReloadOptions{}))
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@sol/older", Facet: facet(7)}}, background, ReloadOptions{}))
	mustGet(t, host, v2, jsonObject()).Release()
	// The removal's failure once, then the re-served value, whole.
	adaptedExpectValues(t, host, calls, nexttest.FailedValue{Failed: "sol.older-removal is not served here"}, 7.0)
}

// --- keyed members of an older version follow the provider (adapted.test.ts: reload describes, fix rounds 1 and 2) --------

// adaptedRowsDoc is `defineState("rows", { keyed: true, schema: obj({ n }), initial: n 0 })`.
var adaptedRowsDoc = func() *documents.StateDefinition {
	definition, err := documents.DefineState("rows", documents.StateOptions{
		Schema: contracts.Obj(contracts.Prop("n", contracts.Int())), Version: 1, Keyed: true,
		Initial: func() *delta.JsonObject { return jsonObject("n", 0.0) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

// adaptedKeyedType is a local type with the keyed member `member` of schema and `set` taking an int (plus the extras).
func adaptedKeyedType(name, member string, schema contracts.Schema) *contracts.Contract {
	return mustContract(declareLocal(name, contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem(member, contracts.Keyed(schema)), contracts.Mem("set", contracts.NewMethod(contracts.Int()))},
	}))
}

func adaptedEvolve(previous *contracts.Contract, edit func(e *contracts.LocalEditor) *contracts.LocalEditor) *contracts.Contract {
	evolved, err := contracts.Evolve(previous, edit)
	if err != nil {
		panic(err)
	}
	return evolved
}

// adaptedKeyedFacet serves contract with its keyed `member` over the rows document, `set` writing row key, `extra` the
// constant state member `extra` of a contract that has it, and the accepts table.
func adaptedKeyedFacet(contract *contracts.Contract, member, key string, extra bool, accepts contracts.AcceptsTable) Facet {
	return DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(contract, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			docs := self.State(adaptedRowsDoc).(*documents.Keyed)
			implementation := contracts.Implementation{Members: contracts.VariantImplementation{
				Keyed: map[string]contracts.KeyedWatchable[delta.JsonValue]{member: ExposeKeyed(docs)},
				Methods: map[string]contracts.MethodFunction{"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					_, err := docs.Change(ctx, key, func(draft *delta.Object) (any, error) { return nil, draft.Set("n", args) })
					return nil, err
				}},
			}}
			if extra {
				implementation.Members.State = map[string]chord.Watchable[delta.JsonValue]{"extra": constant(0.0)}
			}
			return implementation, nil
		}, ServeOptions{Accepts: accepts})
	})
}

// adaptedKeyedMapping is a keyed-only version mapping of member through f.
func adaptedKeyedMapping(version string, member string, f func(delta.JsonValue) (delta.JsonValue, error)) contracts.Entry[contracts.VersionMappings] {
	return contracts.Entry[contracts.VersionMappings]{Name: version, Value: contracts.VersionMappings{
		Keyed: []contracts.Entry[contracts.KeyedMapping]{{Name: member, Value: contracts.KeyedMapping{Func: f}}},
	}}
}

// adaptedValueOf is a reader's member value (nil when it fails).
func adaptedValueOf(ref *Reference, member string) delta.JsonValue {
	value, _ := ref.Member(member).Value()
	return value
}

// adaptedPinView serves a View whose member `name` reads the pinned row `key` of a keyed `member` of contract (`field` of the
// row when not empty).
func adaptedPinView(view, contract *contracts.Contract, member, key, field, name string) Facet {
	return DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(view, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			pin, err := self.Get(contract, jsonObject()).Keyed(member).At(key)
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				name: self.Derive([]any{Expose(pin)}, func(values []any) (delta.JsonValue, error) {
					if field == "" {
						return values[0], nil
					}
					return values[0].(*delta.JsonObject).Value(field), nil
				}),
			}}}, err
		}, ServeOptions{})
	})
}

// Mirrors "reload: keyed members of an older version follow the provider (fix round 1) › updates an open old-version watch when only its mapping changes".
func TestReloadKeyedMembersOfAnOlderVersionFollowTheProviderUpdatesAnOpenOldVersionWatchWhenOnlyItsMappingChanges(t *testing.T) {
	v1 := adaptedKeyedType("astra.keyed", "rows", contracts.Int())
	v2 := adaptedEvolve(v1, func(e *contracts.LocalEditor) *contracts.LocalEditor {
		return e.Keyed("rows", contracts.Obj(contracts.Prop("n", contracts.Int())))
	})
	facet := func(factor float64) Facet {
		return adaptedKeyedFacet(v2, "rows", "a", false, contracts.AcceptsTable{adaptedKeyedMapping("1", "rows", func(current delta.JsonValue) (delta.JsonValue, error) {
			return current.(*delta.JsonObject).Value("n").(float64) * factor, nil
		})})
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@astra/keyed", Facet: facet(2)}}, background))
	old := mustGet(t, host, v1, jsonObject())
	if _, err := old.Call("set", 3.0, background); err != nil {
		t.Fatalf("set: %v", err)
	}
	watch, err := old.Keyed("rows").Watch(background, "a")
	must(t, err)
	if watch.Value() != 6.0 {
		t.Fatalf("watch = %v, want 6", watch.Value())
	}
	watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error { return nil })
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@astra/keyed", Facet: facet(4)}}, background, ReloadOptions{}))
	turn(host)
	if _, err := old.Call("set", 5.0, background); err != nil {
		t.Fatalf("set 5: %v", err)
	}
	scopeEventually(t, "the watch maps through the new provider", func() bool { deliveredOn(host); return watch.Value() == 20.0 })
	fresh, _, err := old.Keyed("rows").Get(background, "a")
	must(t, err)
	if fresh != 20.0 {
		t.Fatalf("fresh get = %v, want 20", fresh)
	}
	_, _ = watch.Stop()
	old.Release()
}

// Mirrors "› updates mappings of an existing older watch and factory pin".
func TestReloadKeyedMembersOfAnOlderVersionFollowTheProviderUpdatesMappingsOfAnExistingOlderWatchAndFactoryPin(t *testing.T) {
	v1 := adaptedKeyedType("sol.keyed", "entries", contracts.Obj(contracts.Prop("old", contracts.Int())))
	v2 := adaptedEvolve(v1, func(e *contracts.LocalEditor) *contracts.LocalEditor {
		return e.Keyed("entries", contracts.Obj(contracts.Prop("n", contracts.Int())))
	})
	v3 := adaptedEvolve(v2, func(e *contracts.LocalEditor) *contracts.LocalEditor {
		return e.Keyed("entries", contracts.Obj(contracts.Prop("n", contracts.Int())))
	})
	view := mustContract(declareLocal("sol.keyed.view", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("old", contracts.Int())},
	}))
	viewFacet := adaptedPinView(view, v1, "entries", "k", "old", "old")
	oldOf := func(factor float64) func(delta.JsonValue) (delta.JsonValue, error) {
		return func(value delta.JsonValue) (delta.JsonValue, error) {
			return jsonObject("old", value.(*delta.JsonObject).Value("n").(float64)*factor), nil
		}
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{
		{Name: "@sol/keyed", Facet: adaptedKeyedFacet(v2, "entries", "k", false, contracts.AcceptsTable{adaptedKeyedMapping("1", "entries", oldOf(1))})},
		{Name: "@sol/view", Facet: viewFacet},
	}, background))
	old := mustGet(t, host, v1, jsonObject())
	reader := mustGet(t, host, view, jsonObject())
	if _, err := old.Call("set", 1.0, background); err != nil {
		t.Fatalf("set: %v", err)
	}
	watch, err := old.Keyed("entries").Watch(background, "k")
	must(t, err)
	calls := nexttest.ChangesOf(watch)
	reloadMust(t)(host.Reload([]HostFacet{
		{Name: "@sol/keyed", Facet: adaptedKeyedFacet(v3, "entries", "k", false, contracts.AcceptsTable{
			adaptedKeyedMapping("1", "entries", oldOf(10)),
			adaptedKeyedMapping("2", "entries", func(value delta.JsonValue) (delta.JsonValue, error) { return value, nil }),
		})},
		{Name: "@sol/view", Facet: viewFacet},
	}, background, ReloadOptions{}))
	mustGet(t, host, v3, jsonObject()).Release()
	if _, err := old.Call("set", 2.0, background); err != nil {
		t.Fatalf("set 2: %v", err)
	}
	scopeEventually(t, "the older watch and the pin map through the new provider", func() bool {
		deliveredOn(host)
		values := nexttest.ValuesOf(calls.Items())
		return len(values) > 0 && reflect.DeepEqual(values[len(values)-1], jsonObject("old", 20.0)) && adaptedValueOf(reader, "old") == 20.0
	})
	_, _ = watch.Stop()
}

// Mirrors "adapted members' lifetime (fix round 1) › drops an older version's adapted members when its last consumer leaves, while the object lives on".
func TestAdaptedMembersLifetimeDropsAnOlderVersionsAdaptedMembersWhenItsLastConsumerLeavesWhileTheObjectLivesOn(t *testing.T) {
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return serveAdaptedV2(h, false) }, loadedOptions{})
	defer closeHosts(t, host)
	current := mustGet(t, host, adaptedNotesV2, jsonObject())
	if before := adaptedMade(host, 1, false); len(before) != 0 {
		t.Fatalf("%d older mappings before the older reference", len(before))
	}
	old := mustGet(t, host, adaptedNotesV1, jsonObject())
	if got := memberOf(t, old, "items"); !reflect.DeepEqual(got, []any{}) {
		t.Fatalf("items = %v, want []", got)
	}
	// The older version's: those of the version shown stay while the object lives (the mappings read them).
	older := adaptedMade(host, 1, false)
	if len(older) == 0 {
		t.Fatal("reading the older version's items made no adapted members")
	}
	old.Release()
	turn(host)
	expectCollected(t, older...)
	if state := stateOfRef(current); state.Kind != RefStateLive {
		t.Fatalf("state = %+v, want live", state)
	}
}

// Mirrors "reload: keyed members unchanged at a consumer's version follow a later mapping (fix round 1)".
func TestReloadKeyedMembersUnchangedAtAConsumersVersionFollowALaterMapping(t *testing.T) {
	rowsV1 := adaptedKeyedType("app.rows", "rows", contracts.Obj(contracts.Prop("n", contracts.Int())))
	// v2 adds a member: `rows` is unchanged for v1 consumers. v3 changes `rows` (same shape): v1 and v2 consumers read it mapped.
	rowsV2 := adaptedEvolve(rowsV1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.AddState("extra", contracts.Int()) })
	rowsV3 := adaptedEvolve(rowsV2, func(e *contracts.LocalEditor) *contracts.LocalEditor {
		return e.Keyed("rows", contracts.Obj(contracts.Prop("n", contracts.Int())))
	})
	view := mustContract(declareLocal("app.rows.view", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	viewFacet := adaptedPinView(view, rowsV1, "rows", "k", "n", "n")
	ten := func(value delta.JsonValue) (delta.JsonValue, error) {
		return jsonObject("n", value.(*delta.JsonObject).Value("n").(float64)*10), nil
	}
	rows := func(contract *contracts.Contract, accepts contracts.AcceptsTable) Facet {
		return adaptedKeyedFacet(contract, "rows", "k", contract != rowsV1, accepts)
	}
	later := contracts.AcceptsTable{adaptedKeyedMapping("1", "rows", ten), adaptedKeyedMapping("2", "rows", ten)}
	across := func(t *testing.T, first, second Facet) (watched, got, pinned delta.JsonValue) {
		host := newHost(t, HostOptions{OnError: func(HostError) {}})
		defer closeHosts(t, host)
		mustLoad(t)(host.Load([]HostFacet{{Name: "@app/rows", Facet: first}, {Name: "@app/view", Facet: viewFacet}}, background))
		old := mustGet(t, host, rowsV1, jsonObject())
		reader := mustGet(t, host, view, jsonObject())
		if _, err := old.Call("set", 1.0, background); err != nil {
			t.Fatalf("set: %v", err)
		}
		watch, err := old.Keyed("rows").Watch(background, "k")
		must(t, err)
		watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error { return nil })
		reloadMust(t)(host.Reload([]HostFacet{{Name: "@app/rows", Facet: second}, {Name: "@app/view", Facet: viewFacet}}, background, ReloadOptions{}))
		turn(host)
		if _, err := old.Call("set", 2.0, background); err != nil {
			t.Fatalf("set 2: %v", err)
		}
		// The watch is asserted below as the pin is: both land through loop tasks after the scheduler is idle.
		scopeEventually(t, "the later mapping reaches the watch and the pin", func() bool {
			deliveredOn(host)
			return adaptedValueOf(reader, "n") == 20.0 && reflect.DeepEqual(watch.Value(), jsonObject("n", 20.0))
		})
		got, _, err = old.Keyed("rows").Get(background, "k")
		must(t, err)
		watched, pinned = watch.Value(), adaptedValueOf(reader, "n")
		_, _ = watch.Stop()
		old.Release()
		reader.Release()
		return watched, got, pinned
	}
	for name, firstAccepts := range map[string]struct {
		first Facet
	}{
		"re-maps a key watched and pinned at the provider's current version once a later provider maps it": {rows(rowsV1, nil)},
		"re-maps a key of an older version its provider left unchanged once a later provider maps it":      {rows(rowsV2, contracts.AcceptsTable{{Name: "1"}})},
	} {
		t.Run(name, func(t *testing.T) {
			watched, got, pinned := across(t, firstAccepts.first, rows(rowsV3, later))
			if !reflect.DeepEqual(watched, jsonObject("n", 20.0)) || !reflect.DeepEqual(got, jsonObject("n", 20.0)) || pinned != 20.0 {
				t.Fatalf("watch %v, get %v, pin %v, want {n:20}, {n:20}, 20", watched, got, pinned)
			}
		})
	}
}

// Mirrors "reload: keyed sources of an older version through a removal (fix round 2) › re-maps an open older keyed watch and a factory pin through the re-served provider (Astra, Sol)".
func TestReloadKeyedSourcesOfAnOlderVersionThroughARemovalReMapsAnOpenOlderKeyedWatchAndAFactoryPinThroughTheReServedProvider(t *testing.T) {
	v1 := adaptedKeyedType("app.keyed.removal", "rows", contracts.Int())
	v2 := adaptedEvolve(v1, func(e *contracts.LocalEditor) *contracts.LocalEditor {
		return e.Keyed("rows", contracts.Obj(contracts.Prop("n", contracts.Int())))
	})
	view := mustContract(declareLocal("app.keyed.removal.view", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	viewFacet := adaptedPinView(view, v1, "rows", "a", "", "n")
	rows := func(factor float64) Facet {
		return adaptedKeyedFacet(v2, "rows", "a", false, contracts.AcceptsTable{adaptedKeyedMapping("1", "rows", func(current delta.JsonValue) (delta.JsonValue, error) {
			return current.(*delta.JsonObject).Value("n").(float64) * factor, nil
		})})
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@app/rows", Facet: rows(2)}, {Name: "@app/view", Facet: viewFacet}}, background))
	old := mustGet(t, host, v1, jsonObject())
	reader := mustGet(t, host, view, jsonObject())
	if _, err := old.Call("set", 3.0, background); err != nil {
		t.Fatalf("set: %v", err)
	}
	watch, err := old.Keyed("rows").Watch(background, "a")
	must(t, err)
	calls := nexttest.ChangesOf(watch)
	if watch.Value() != 6.0 || adaptedValueOf(reader, "n") != 6.0 {
		t.Fatalf("watch %v, pin %v, want 6, 6", watch.Value(), adaptedValueOf(reader, "n"))
	}
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@app/view", Facet: viewFacet}}, background, ReloadOptions{}))
	turn(host)
	// Frozen while the type is gone: the last mapped value.
	if watch.Value() != 6.0 {
		t.Fatalf("watch while removed = %v, want 6", watch.Value())
	}
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@app/rows", Facet: rows(4)}, {Name: "@app/view", Facet: viewFacet}}, background, ReloadOptions{}))
	turn(host)
	if _, err := old.Call("set", 5.0, background); err != nil {
		t.Fatalf("set 5: %v", err)
	}
	scopeEventually(t, "the watch and the pin map through the re-served provider", func() bool {
		deliveredOn(host)
		return watch.Value() == 20.0 && adaptedValueOf(reader, "n") == 20.0
	})
	if got, _, err := old.Keyed("rows").Get(background, "a"); err != nil || got != 20.0 {
		t.Fatalf("get = %v, %v, want 20", got, err)
	}
	if values := nexttest.ValuesOf(calls.Items()); values[len(values)-1] != 20.0 {
		t.Fatalf("last watch value = %v, want 20", values[len(values)-1])
	}
	_, _ = watch.Stop()
	old.Release()
	reader.Release()
}

// Mirrors "› keeps no removed instance's mappings once the reference moved, its watch open (decision: references only)".
func TestReloadKeyedSourcesOfAnOlderVersionThroughARemovalKeepsNoRemovedInstancesMappingsOnceTheReferenceMovedItsWatchOpenDecisionReferencesOnly(t *testing.T) {
	v1 := adaptedKeyedType("app.keyed.removal", "rows", contracts.Int())
	v2 := adaptedEvolve(v1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.Keyed("rows", adaptedObjOf("n")) })
	rows := func(factor float64) Facet {
		return adaptedKeyedFacet(v2, "rows", "a", false, contracts.AcceptsTable{adaptedKeyedMapping("1", "rows", adaptedRowsTimes(factor))})
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	adaptedLoadOne(t, host, "@app/rows", rows(2))
	old := mustGet(t, host, v1, jsonObject())
	if _, err := old.Call("set", 3.0, background); err != nil {
		t.Fatalf("set: %v", err)
	}
	watch, err := old.Keyed("rows").Watch(background, "a")
	must(t, err)
	watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error { return nil })
	// The removed instance's mapping of the key: nothing of it outlives it through the reference.
	removed := adaptedMade(host, 1, true)
	if len(removed) == 0 {
		t.Fatal("watching a key made no key mapping")
	}
	adaptedReloadNone(t, host)
	adaptedReloadOne(t, host, "@app/rows", rows(4))
	// The re-served object's documents start over (no store here).
	if _, err := old.Call("set", 3.0, background); err != nil {
		t.Fatalf("set again: %v", err)
	}
	scopeEventually(t, "the watch maps through the re-served provider", func() bool {
		turn(host)
		deliveredOn(host)
		return watch.Value() == 12.0
	})
	expectCollected(t, removed...)
	_, _ = watch.Stop()
	old.Release()
}

// Mirrors "reload: a version refusal keeps the waiting reference's last values (fix round 2) › keeps an older watch open while its version is refused and heals it when it is accepted again (Astra)".
func TestReloadAVersionRefusalKeepsTheWaitingReferencesLastValuesKeepsAnOlderWatchOpenWhileItsVersionIsRefusedAndHealsItWhenItIsAcceptedAgain(t *testing.T) {
	v1 := mustContract(declareLocal("app.version.return", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	v2 := adaptedEvolve(v1, func(e *contracts.LocalEditor) *contracts.LocalEditor {
		return e.State("n", contracts.Obj(contracts.Prop("value", contracts.Int())))
	})
	serve := func(value float64, accepts contracts.AcceptsTable) Facet {
		return DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(v2, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
				return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
					"n": constant(jsonObject("value", value)),
				}}}, nil
			}, ServeOptions{Accepts: accepts})
		})
	}
	accepted := func(value float64) Facet {
		return serve(value, contracts.AcceptsTable{{Name: "1", Value: contracts.VersionMappings{
			State: []contracts.Entry[contracts.StateMapping]{{Name: "n", Value: contracts.StateMapping{
				Func: func(v delta.JsonValue) (delta.JsonValue, error) { return v.(*delta.JsonObject).Value("value"), nil },
			}}},
		}}})
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@app/version", Facet: accepted(4)}}, background))
	old := mustGet(t, host, v1, jsonObject())
	watch, err := old.Member("n").Watch(background)
	must(t, err)
	calls := nexttest.ChangesOf(watch)
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@app/version", Facet: serve(2, nil)}}, background, ReloadOptions{}))
	state := untilState(t, old, func(s *RefState) bool { return s.Kind == RefStateFailed })
	if state.Cause.Kind != data.FailureWaiting {
		t.Fatalf("state = %+v, want failed waiting", state)
	}
	// Its last mapped value, read directly too.
	if read, _ := old.Member("n").Value(); watch.Value() != 4.0 || read != 4.0 {
		t.Fatalf("watch %v, read %v, want 4, 4", watch.Value(), read)
	}
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@app/version", Facet: accepted(9)}}, background, ReloadOptions{}))
	scopeEventually(t, "the watch heals", func() bool { deliveredOn(host); return watch.Value() == 9.0 })
	values := nexttest.ValuesOf(calls.Items())
	read, _ := old.Member("n").Value()
	if read != 9.0 || values[len(values)-1] != 9.0 {
		t.Fatalf("read %v, last %v, want 9, 9", read, values[len(values)-1])
	}
	// Pi's `closed: false`: the older watch stayed open throughout.
	if adaptedClosed(watch) {
		t.Fatal("the older watch closed: it must stay open while its version is refused and after it heals")
	}
	old.Release()
}

// Mirrors "› heals a watch of the previous current version once a later provider accepts it (Sol)".
func TestReloadAVersionRefusalKeepsTheWaitingReferencesLastValuesHealsAWatchOfThePreviousCurrentVersionOnceALaterProviderAcceptsIt(t *testing.T) {
	v1 := adaptedLocal("app.version.return", contracts.Mem("n", contracts.Int()))
	v2 := adaptedEvolve(v1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("n", adaptedObjOf("value")) })
	accepted := func(value float64) Facet {
		return adaptedConstants(v2, map[string]delta.JsonValue{"n": jsonObject("value", value)},
			contracts.AcceptsTable{adaptedVersion("1", adaptedState("n", adaptedField("value")))}, nil)
	}
	refused := adaptedConstants(v2, map[string]delta.JsonValue{"n": jsonObject("value", 2.0)}, nil, nil)
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	adaptedLoadOne(t, host, "@app/version", adaptedConstants(v1, map[string]delta.JsonValue{"n": 1.0}, nil, nil))
	old := mustGet(t, host, v1, jsonObject())
	_, calls := adaptedWatch(t, old, "n")
	adaptedReloadOne(t, host, "@app/version", refused)
	mustGet(t, host, v2, jsonObject()).Release()
	untilKind(t, old, RefStateFailed)
	adaptedReloadOne(t, host, "@app/version", accepted(3))
	mustGet(t, host, v2, jsonObject()).Release()
	untilKind(t, old, RefStateLive)
	adaptedUntilLast(t, host, calls, 3.0)
	old.Release()
}

// Mirrors "reload: watches of one version across a rollback (fix round 2) › keeps both generations of v1 watches after v1 to v2 to v1 to v2 (Sol)".
func TestReloadWatchesOfOneVersionAcrossARollbackKeepsBothGenerationsOfV1WatchesAfterV1ToV2ToV1ToV2(t *testing.T) {
	v1 := adaptedLocal("app.rollback", contracts.Mem("x", contracts.Int()))
	v2 := adaptedEvolve(v1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("x", adaptedObjOf("n")) })
	one := func(n float64) Facet { return adaptedConstants(v1, map[string]delta.JsonValue{"x": n}, nil, nil) }
	two := func(n float64) Facet {
		return adaptedConstants(v2, map[string]delta.JsonValue{"x": jsonObject("n", n)},
			contracts.AcceptsTable{adaptedVersion("1", adaptedState("x", adaptedField("n")))}, nil)
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	adaptedLoadOne(t, host, "@app/rollback", one(1))
	first := mustGet(t, host, v1, jsonObject())
	_, firstCalls := adaptedWatch(t, first, "x")
	adaptedReloadOne(t, host, "@app/rollback", two(2))
	mustGet(t, host, v2, jsonObject()).Release()
	adaptedReloadOne(t, host, "@app/rollback", one(3))
	second := mustGet(t, host, v1, jsonObject())
	_, secondCalls := adaptedWatch(t, second, "x")
	adaptedReloadOne(t, host, "@app/rollback", two(4))
	mustGet(t, host, v2, jsonObject()).Release()
	adaptedExpectValues(t, host, firstCalls, 2.0, 3.0, 4.0)
	adaptedExpectValues(t, host, secondCalls, 4.0)
	if got := memberOf(t, second, "x"); got != 4.0 {
		t.Fatalf("x = %v, want 4", got)
	}
	first.Release()
	second.Release()
}

// Mirrors "reload: an older consumer's reads while its type is removed (fix round 2) › reads its last mapped value, not the provider's raw shape, a member read before or not (Astra)".
func TestReloadAnOlderConsumersReadsWhileItsTypeIsRemovedReadsItsLastMappedValueNotTheProvidersRawShapeAMemberReadBeforeOrNot(t *testing.T) {
	v1 := adaptedLocal("app.removed.value", contracts.Mem("n", contracts.Int()), contracts.Mem("gone", contracts.Int()))
	v2 := adaptedEvolve(v1, func(e *contracts.LocalEditor) *contracts.LocalEditor {
		return e.State("n", adaptedObjOf("value")).RemoveState("gone")
	})
	facet := adaptedConstants(v2, map[string]delta.JsonValue{"n": jsonObject("value", 5.0)}, contracts.AcceptsTable{adaptedVersion("1",
		adaptedState("n", adaptedField("value")),
		adaptedState("gone", func(state delta.JsonValue) (delta.JsonValue, error) {
			return state.(*delta.JsonObject).Value("n").(*delta.JsonObject).Value("value").(float64) + 1, nil
		}))}, nil)
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	adaptedLoadOne(t, host, "@app/removed", facet)
	old := mustGet(t, host, v1, jsonObject())
	if got := memberOf(t, old, "n"); got != 5.0 {
		t.Fatalf("n = %v, want 5", got)
	}
	adaptedReloadNone(t, host)
	// `gone` was never read before the removal: the current version has no such member.
	got := map[string]any{"n": memberOf(t, old, "n"), "gone": memberOf(t, old, "gone")}
	adaptedWant(t, "members", got, map[string]any{"n": 5.0, "gone": 6.0})
	old.Release()
}

// Mirrors "reload: an older consumer's first read during a parked swap (fix round 2) › maps with the provider whose attempt is shown until the new one goes live (Sol)".
func TestReloadAnOlderConsumersFirstReadDuringAParkedSwapMapsWithTheProviderWhoseAttemptIsShownUntilTheNewOneGoesLive(t *testing.T) {
	v1 := adaptedLocal("app.lazy", contracts.Mem("x", contracts.Int()))
	v2 := adaptedEvolve(v1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("x", adaptedObjOf("n")) })
	missing := adaptedLocal("app.lazy.missing")
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	adaptedLoadOne(t, host, "@app/lazy", adaptedConstants(v1, map[string]delta.JsonValue{"x": 1.0}, nil, nil))
	old := mustGet(t, host, v1, jsonObject())
	// Nothing read before the swap: the first read comes while the new attempt parks on a missing dependency.
	parked := adaptedConstants(v2, map[string]delta.JsonValue{"x": jsonObject("n", 7.0)},
		contracts.AcceptsTable{adaptedVersion("1", adaptedState("x", adaptedField("n")))},
		func(self Self) { self.Get(missing, jsonObject()) })
	adaptedReloadOne(t, host, "@app/lazy", parked)
	deliveredOn(host)
	adaptedWantWaiting(t, old)
	if got := memberOf(t, old, "x"); got != 1.0 {
		t.Fatalf("x = %v, want 1", got)
	}
	_, calls := adaptedWatch(t, old, "x")
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@app/lazy", Facet: parked}, {Name: "@app/missing", Facet: adaptedEmptyFacet(missing)}}, background, ReloadOptions{}))
	deliveredOn(host)
	untilKind(t, old, RefStateLive)
	adaptedUntilLast(t, host, calls, 7.0)
	if got := memberOf(t, old, "x"); got != 7.0 {
		t.Fatalf("x = %v, want 7", got)
	}
	old.Release()
}

// --- reload: references keep their last values through a removal, a refusal and an end while they wait (fix round 3) ---------

// adaptedWaitingTypes are `app.waiting` (v1: keyed `rows` of int, `set`, params `id`) and v2 (rows of `{ n }`).
func adaptedWaitingTypes() (v1, v2 *contracts.Contract) {
	v1 = mustContract(declareLocal("app.waiting", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("rows", contracts.Keyed(contracts.Int())), contracts.Mem("set", contracts.NewMethod(contracts.Int()))},
	}))
	v2 = adaptedEvolve(v1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.Keyed("rows", adaptedObjOf("n")) })
	return v1, v2
}

// adaptedRowsTimes is `(current) => current.n * factor`.
func adaptedRowsTimes(factor float64) func(delta.JsonValue) (delta.JsonValue, error) {
	return func(current delta.JsonValue) (delta.JsonValue, error) {
		return current.(*delta.JsonObject).Value("n").(float64) * factor, nil
	}
}

// adaptedWaitingRows serves v2, `set` writing row "a"; mapping v1's rows times factor, or refusing v1 (accepted false).
func adaptedWaitingRows(v2 *contracts.Contract, factor float64, accepted bool) Facet {
	var accepts contracts.AcceptsTable
	if accepted {
		accepts = contracts.AcceptsTable{adaptedKeyedMapping("1", "rows", adaptedRowsTimes(factor))}
	}
	return adaptedKeyedFacet(v2, "rows", "a", false, accepts)
}

// adaptedWatched is a v1 reference to object `id` with row "a" at 3 and an open, started watch of it.
type adaptedWatched struct {
	old   *Reference
	watch chord.WatchHandle[delta.JsonValue]
	calls adaptedCalls
}

func adaptedWatchRow(t testing.TB, host Host, v1 *contracts.Contract, id float64) adaptedWatched {
	t.Helper()
	old := mustGet(t, host, v1, jsonObject("id", id))
	if _, err := old.Call("set", 3.0, background); err != nil {
		t.Fatalf("set: %v", err)
	}
	watch, err := old.Keyed("rows").Watch(background, "a")
	must(t, err)
	return adaptedWatched{old: old, watch: watch, calls: nexttest.ChangesOf(watch)}
}

// untilValue waits until the watch shows want.
func (w adaptedWatched) untilValue(t testing.TB, host Host, want any) {
	t.Helper()
	scopeEventually(t, "the watch shows the awaited value", func() bool {
		turn(host)
		deliveredOn(host)
		return reflect.DeepEqual(w.watch.Value(), want)
	})
}

// Mirrors "reload: references keep their last values through a removal, a refusal and an end while they wait (fix round 3) › keeps an older keyed watch when the refusal idles its object, which ends (Astra)".
func TestReloadReferencesKeepTheirLastValuesThroughARemovalARefusalAndAnEndWhileTheyWaitKeepsAnOlderKeyedWatchWhenTheRefusalIdlesItsObjectWhichEnds(t *testing.T) {
	v1, v2 := adaptedWaitingTypes()
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	adaptedLoadOne(t, host, "@app/rows", adaptedWaitingRows(v2, 2, true))
	w := adaptedWatchRow(t, host, v1, 1)
	if w.watch.Value() != 6.0 {
		t.Fatalf("watch = %v, want 6", w.watch.Value())
	}
	// v1 refused: its only holder waits, so the object (with params) ends; its pinned keys wait too.
	adaptedReloadOne(t, host, "@app/rows", adaptedWaitingRows(v2, 0, false))
	turn(host)
	adaptedWantWaiting(t, w.old)
	adaptedReloadOne(t, host, "@app/rows", adaptedWaitingRows(v2, 4, true))
	if _, err := w.old.Call("set", 5.0, background); err != nil {
		t.Fatalf("set: %v", err)
	}
	w.untilValue(t, host, 20.0)
	got, _, err := w.old.Keyed("rows").Get(background, "a")
	must(t, err)
	adaptedWant(t, "closed, watch, get", []any{adaptedClosed(w.watch), w.watch.Value(), got}, []any{false, 20.0, 20.0})
	w.old.Release()
}

// Mirrors "› keeps an older keyed watch through a refusal and then a removal of its type (Sol)".
func TestReloadReferencesKeepTheirLastValuesThroughARemovalARefusalAndAnEndWhileTheyWaitKeepsAnOlderKeyedWatchThroughARefusalAndThenARemovalOfItsType(t *testing.T) {
	v1, v2 := adaptedWaitingTypes()
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	adaptedLoadOne(t, host, "@app/rows", adaptedWaitingRows(v2, 2, true))
	w := adaptedWatchRow(t, host, v1, 1)
	current := mustGet(t, host, v2, jsonObject("id", 1.0))
	adaptedReloadOne(t, host, "@app/rows", adaptedWaitingRows(v2, 0, false))
	deliveredOn(host)
	untilKind(t, w.old, RefStateFailed)
	current.Release()
	adaptedReloadNone(t, host)
	adaptedReloadOne(t, host, "@app/rows", adaptedWaitingRows(v2, 4, true))
	if _, err := w.old.Call("set", 3.0, background); err != nil {
		t.Fatalf("set: %v", err)
	}
	adaptedUntilLast(t, host, w.calls, 12.0)
	adaptedWant(t, "closed", adaptedClosed(w.watch), false)
	w.old.Release()
}

// Mirrors "› keeps no removed instance's mappings while its keyed watch stays open (Sol)".
func TestReloadReferencesKeepTheirLastValuesThroughARemovalARefusalAndAnEndWhileTheyWaitKeepsNoRemovedInstancesMappingsWhileItsKeyedWatchStaysOpen(t *testing.T) {
	v1, v2 := adaptedWaitingTypes()
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	adaptedLoadOne(t, host, "@app/rows", adaptedWaitingRows(v2, 2, true))
	w := adaptedWatchRow(t, host, v1, 1)
	removed := adaptedMade(host, 1, true)
	if len(removed) == 0 {
		t.Fatal("watching a key made no key mapping")
	}
	adaptedReloadNone(t, host)
	adaptedReloadOne(t, host, "@app/rows", adaptedWaitingRows(v2, 4, true))
	if _, err := w.old.Call("set", 3.0, background); err != nil {
		t.Fatalf("set: %v", err)
	}
	w.untilValue(t, host, 12.0)
	// The watch is still open: what it follows now is the re-served object's instance.
	expectCollected(t, removed...)
	w.old.Release()
}

// Mirrors "› reads an older member never read before at its last mapped value after a refusal (Astra)".
func TestReloadReferencesKeepTheirLastValuesThroughARemovalARefusalAndAnEndWhileTheyWaitReadsAnOlderMemberNeverReadBeforeAtItsLastMappedValueAfterARefusal(t *testing.T) {
	state1 := adaptedLocal("app.waiting.state", contracts.Mem("n", contracts.Int()))
	state2 := adaptedEvolve(state1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("n", adaptedObjOf("value")) })
	serve := func(value float64, accepts bool) Facet {
		var table contracts.AcceptsTable
		if accepts {
			table = contracts.AcceptsTable{adaptedVersion("1", adaptedState("n", adaptedField("value")))}
		}
		return adaptedConstants(state2, map[string]delta.JsonValue{"n": jsonObject("value", value)}, table, nil)
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	adaptedLoadOne(t, host, "@app/state", serve(4, true))
	old := mustGet(t, host, state1, jsonObject())
	adaptedReloadOne(t, host, "@app/state", serve(9, false))
	turn(host)
	if got := memberOf(t, old, "n"); got != 4.0 {
		t.Fatalf("n = %v, want 4", got)
	}
	old.Release()
}

// Mirrors "› follows a newer consumer through two rollbacks, its last value kept while refused (Sol)".
func TestReloadReferencesKeepTheirLastValuesThroughARemovalARefusalAndAnEndWhileTheyWaitFollowsANewerConsumerThroughTwoRollbacksItsLastValueKeptWhileRefused(t *testing.T) {
	x1 := adaptedLocal("app.waiting.rollback", contracts.Mem("x", contracts.Int()))
	x2 := adaptedEvolve(x1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("x", adaptedObjOf("n")) })
	one := func(n float64) Facet { return adaptedConstants(x1, map[string]delta.JsonValue{"x": n}, nil, nil) }
	two := func(n float64) Facet {
		return adaptedConstants(x2, map[string]delta.JsonValue{"x": jsonObject("n", n)},
			contracts.AcceptsTable{adaptedVersion("1", adaptedState("x", adaptedField("n")))}, nil)
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	adaptedLoadOne(t, host, "@app/rollback", two(1))
	held := mustGet(t, host, x2, jsonObject())
	_, calls := adaptedWatch(t, held, "x")
	for _, pair := range [][2]float64{{2, 3}, {4, 5}} {
		// A v1 provider cannot serve the v2 consumer: it waits with its source, then takes it back.
		adaptedReloadOne(t, host, "@app/rollback", one(pair[0]))
		deliveredOn(host)
		untilKind(t, held, RefStateFailed)
		adaptedReloadOne(t, host, "@app/rollback", two(pair[1]))
		mustGet(t, host, x2, jsonObject()).Release()
		deliveredOn(host)
	}
	// Each refusal's failure once, then each return's value, whole.
	refused := nexttest.FailedValue{Failed: "app.waiting.rollback v2 is not served here"}
	adaptedExpectValues(t, host, calls, refused, jsonObject("n", 3.0), refused, jsonObject("n", 5.0))
	adaptedWant(t, "x", memberOf(t, held, "x"), jsonObject("n", 5.0))
	held.Release()
}

// --- reload: a released reference's last values go with it while another version waits (fix round 4) -----------------------

// Mirrors "reload: a released reference's last values go with it while another version waits (fix round 4) › drops a removed version's mapped value when its last request leaves while another version waits (Astra)".
func TestReloadAReleasedReferencesLastValuesGoWithItWhileAnotherVersionWaitsDropsARemovedVersionsMappedValueWhenItsLastRequestLeavesWhileAnotherVersionWaits(t *testing.T) {
	h1 := mustContract(declareLocal("app.waiting.versions", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("n", adaptedObjOf("mapped"))},
	}))
	h2 := adaptedEvolve(h1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("n", adaptedObjOf("raw")) })
	facet := adaptedConstants(h2, map[string]delta.JsonValue{"n": jsonObject("raw", 7.0)}, contracts.AcceptsTable{adaptedVersion("1",
		adaptedState("n", func(v delta.JsonValue) (delta.JsonValue, error) {
			return jsonObject("mapped", v.(*delta.JsonObject).Value("raw")), nil
		}))}, nil)
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	adaptedLoadOne(t, host, "@app/versions", facet)
	older := mustGet(t, host, h1, jsonObject("id", 1.0))
	newer := mustGet(t, host, h2, jsonObject("id", 1.0))
	mapped := adaptedWeak(t, memberOf(t, older, "n"))
	// Both versions wait with their last values; v1's reference is released, v2's stays.
	adaptedReloadNone(t, host)
	older.Release()
	turn(host)
	expectCollected(t, mapped)
	newer.Release()
}

// Mirrors "› drops a refused version's snapshot when its last request leaves while an older one waits (Sol)".
func TestReloadAReleasedReferencesLastValuesGoWithItWhileAnotherVersionWaitsDropsARefusedVersionsSnapshotWhenItsLastRequestLeavesWhileAnOlderOneWaits(t *testing.T) {
	g1 := adaptedLocal("app.waiting.chain", contracts.Mem("x", adaptedObjOf("n")))
	g2 := adaptedEvolve(g1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("x", adaptedObjOf("n")) })
	g3 := adaptedEvolve(g2, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("x", adaptedObjOf("n")) })
	serve := func(contract *contracts.Contract, n float64) Facet {
		return adaptedConstants(contract, map[string]delta.JsonValue{"x": jsonObject("n", n)}, nil, nil)
	}
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	adaptedLoadOne(t, host, "@app/chain", serve(g1, 1))
	first := mustGet(t, host, g1, jsonObject())
	adaptedReloadOne(t, host, "@app/chain", serve(g2, 2))
	second := mustGet(t, host, g2, jsonObject())
	snapshot := adaptedWeak(t, memberOf(t, second, "x"))
	// v3 accepts neither: both wait; v2's last request leaves, v1's stays.
	adaptedReloadOne(t, host, "@app/chain", serve(g3, 3))
	mustGet(t, host, g3, jsonObject()).Release()
	second.Release()
	deliveredOn(host)
	untilKind(t, first, RefStateFailed)
	expectCollected(t, snapshot)
	first.Release()
}

// --- reload: stopping a keyed watch drops its pin and last value while its reference waits (fix round 4) ---------------------

// Mirrors "reload: stopping a keyed watch drops its pin and last value while its reference waits (fix round 4) › drops a stopped key's document while its reference still waits (Sol)".
func TestReloadStoppingAKeyedWatchDropsItsPinAndLastValueWhileItsReferenceWaitsDropsAStoppedKeysDocumentWhileItsReferenceStillWaits(t *testing.T) {
	k1 := mustContract(declareLocal("app.waiting.key", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{
			contracts.Mem("rows", contracts.Keyed(adaptedObjOf("n"))), contracts.Mem("extra", contracts.Int()),
			contracts.Mem("set", contracts.NewMethod(contracts.Int())),
		},
	}))
	k2 := adaptedEvolve(k1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("extra", contracts.Int()) })
	facet := adaptedKeyedFacet(k2, "rows", "k", true, contracts.AcceptsTable{adaptedVersion("1", adaptedState("extra", adaptedIdentity))})
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	adaptedLoadOne(t, host, "@app/key", facet)
	ref := mustGet(t, host, k1, jsonObject())
	if _, err := ref.Call("set", 3.0, background); err != nil {
		t.Fatalf("set: %v", err)
	}
	// A watch of the key while the type is removed (the reference waits, its key with its last value), then stopped; the watch
	// itself goes out of scope here.
	watched := func() weak.Pointer[delta.JsonObject] {
		watch, err := ref.Keyed("rows").Watch(background, "k")
		must(t, err)
		watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error { return nil })
		value := adaptedWeak(t, watch.Value())
		adaptedReloadNone(t, host)
		_, _ = watch.Stop()
		return value
	}
	document := watched()
	if state := stateOfRef(ref); state.Kind != RefStateFailed {
		t.Fatalf("state = %+v, want failed", state)
	}
	expectCollected(t, document)
	ref.Release()
}

// --- reload: a reference that never had values reads none during a parked swap (fix round 4) --------------------------------

// Mirrors "reload: a reference that never had values reads none during a parked swap (fix round 4) › has a newer reference read nothing during a parked swap, its watch waiting, then the new instance's values (Sol)".
func TestReloadAReferenceThatNeverHadValuesReadsNoneDuringAParkedSwapHasANewerReferenceReadNothingDuringAParkedSwapItsWatchWaitingThenTheNewInstancesValues(t *testing.T) {
	n1 := adaptedLocal("app.unshown", contracts.Mem("x", contracts.Int()))
	n2 := adaptedEvolve(n1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("x", adaptedObjOf("n")) })
	missing := adaptedLocal("app.unshown.missing")
	v2 := adaptedConstants(n2, map[string]delta.JsonValue{"x": jsonObject("n", 7.0)},
		contracts.AcceptsTable{adaptedVersion("1", adaptedState("x", adaptedField("n")))},
		func(self Self) { self.Get(missing, jsonObject()) })
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	adaptedLoadOne(t, host, "@app/unshown", adaptedConstants(n1, map[string]delta.JsonValue{"x": 1.0}, nil, nil))
	old := mustGet(t, host, n1, jsonObject())
	adaptedReloadOne(t, host, "@app/unshown", v2)
	newer := mustHold(t, host, n2, jsonObject())
	adaptedWantWaiting(t, newer)
	// The swap parks: the shown values are v1's; a v2 reference never had values, never v1's shape: its read throws the
	// parked swap's failure.
	if got := memberOf(t, old, "x"); got != 1.0 {
		t.Fatalf("old x = %v, want 1", got)
	}
	_, err := newer.Member("x").Value()
	wantChordError(t, err, data.CodeUnavailable, "", "")
	// A watch waits for its first values through the failure (Opus G3).
	waiting := adaptedWatchLater(newer, "x")
	reloadMust(t)(host.Reload([]HostFacet{{Name: "@app/unshown", Facet: v2}, {Name: "@app/missing", Facet: adaptedEmptyFacet(missing)}}, background, ReloadOptions{}))
	deliveredOn(host)
	untilKind(t, newer, RefStateLive)
	adaptedWant(t, "newer and old x", []any{memberOf(t, newer, "x"), memberOf(t, old, "x")}, []any{jsonObject("n", 7.0), 7.0})
	first := adaptedWaitWatch(t, waiting)
	second, err := newer.Member("x").Watch(background)
	must(t, err)
	adaptedWant(t, "watches", []any{first.Value(), second.Value()}, []any{jsonObject("n", 7.0), jsonObject("n", 7.0)})
	newer.Release()
	old.Release()
}

// --- reload: a reference reads its last value while its version's return parks (fix round 5) ------------------------------

// Mirrors "reload: a reference reads its last value while its version's return parks (fix round 5) › reads the last value its surviving watch shows after v2 to v1 to a parked v2 (Sol)".
func TestReloadAReferenceReadsItsLastValueWhileItsVersionsReturnParksReadsTheLastValueItsSurvivingWatchShowsAfterV2ToV1ToAParkedV2(t *testing.T) {
	r1 := adaptedLocal("app.return", contracts.Mem("x", contracts.Int()))
	r2 := adaptedEvolve(r1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("x", adaptedObjOf("n")) })
	missing := adaptedLocal("app.return.missing")
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	adaptedLoadOne(t, host, "@app/return", adaptedConstants(r2, map[string]delta.JsonValue{"x": jsonObject("n", 7.0)}, nil, nil))
	ref := mustGet(t, host, r2, jsonObject())
	watch, err := ref.Member("x").Watch(background)
	must(t, err)
	watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error { return nil })
	// A v1 provider cannot serve v2: the reference waits with its last value.
	adaptedReloadOne(t, host, "@app/return", adaptedConstants(r1, map[string]delta.JsonValue{"x": 1.0}, nil, nil))
	adaptedWant(t, "x", memberOf(t, ref, "x"), jsonObject("n", 7.0))
	// v2 is served again but its swap parks: the sources taken back are v2's own, shown before.
	parked := adaptedConstants(r2, map[string]delta.JsonValue{"x": jsonObject("n", 9.0)}, nil, func(self Self) { self.Get(missing, jsonObject()) })
	adaptedReloadOne(t, host, "@app/return", parked)
	deliveredOn(host)
	again, err := ref.Member("x").Watch(background)
	must(t, err)
	seven := jsonObject("n", 7.0)
	adaptedWant(t, "watch, x, new watch", []any{watch.Value(), memberOf(t, ref, "x"), again.Value()}, []any{seven, seven, seven})
	ref.Release()
}

// --- reload: one rule for reads and watches across a new object (fix round 6) --------------------------------------------------

// Mirrors "reload: one rule for reads and watches across a new object (fix round 6) › reads the last value its watch shows while the re-served object parks (Sol)".
func TestReloadOneRuleForReadsAndWatchesAcrossANewObjectReadsTheLastValueItsWatchShowsWhileTheReServedObjectParks(t *testing.T) {
	typ := adaptedLocal("app.successor", contracts.Mem("x", contracts.Int()))
	missing := adaptedLocal("app.successor.missing")
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	adaptedLoadOne(t, host, "@app/successor", adaptedConstants(typ, map[string]delta.JsonValue{"x": 7.0}, nil, nil))
	ref := mustGet(t, host, typ, jsonObject())
	watch, err := ref.Member("x").Watch(background)
	must(t, err)
	watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error { return nil })
	adaptedReloadNone(t, host)
	if got := memberOf(t, ref, "x"); got != 7.0 {
		t.Fatalf("x = %v, want 7", got)
	}
	// Re-served: a new object, which parks before any instance of it went live: the reference keeps its last value.
	parked := adaptedConstants(typ, map[string]delta.JsonValue{"x": 9.0}, nil, func(self Self) { self.Get(missing, jsonObject()) })
	adaptedReloadOne(t, host, "@app/successor", parked)
	deliveredOn(host)
	adaptedWantWaiting(t, ref)
	adaptedWant(t, "watch, x", []any{watch.Value(), memberOf(t, ref, "x")}, []any{7.0, 7.0})
	ref.Release()
}

// Mirrors "› opens a new watch at its last value while the re-served object's native opens (Sol)".
func TestReloadOneRuleForReadsAndWatchesAcrossANewObjectOpensANewWatchAtItsLastValueWhileTheReServedObjectsNativeOpens(t *testing.T) {
	typ := adaptedLocal("app.successor", contracts.Mem("x", contracts.Int()))
	gate := newAdaptedGate()
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	adaptedLoadOne(t, host, "@app/successor", adaptedConstants(typ, map[string]delta.JsonValue{"x": 7.0}, nil, nil))
	ref := mustGet(t, host, typ, jsonObject())
	adaptedReloadNone(t, host)
	opening := adaptedConstants(typ, map[string]delta.JsonValue{"x": 9.0}, nil, gate.native)
	reloading := adaptedReload(host, HostFacet{Name: "@app/successor", Facet: opening})
	gate.entered.wait(t)
	deliveredOn(host)
	untilKind(t, ref, RefStateLoading)
	if got := memberOf(t, ref, "x"); got != 7.0 {
		t.Fatalf("x = %v, want 7", got)
	}
	// At once, not after hydration: the source of its version exists.
	watch, err := ref.Member("x").Watch(background)
	must(t, err)
	if watch.Value() != 7.0 {
		t.Fatalf("watch = %v, want 7", watch.Value())
	}
	calls := nexttest.ChangesOf(watch)
	gate.resolve()
	must(t, receive(t, reloading))
	adaptedUntilLast(t, host, calls, 9.0)
	ref.Release()
}

// --- reload: an older version only a pending provider maps has no values yet (fix round 7) ---------------------------------

// adaptedPending is `pending()`: v2 shown (no accepts for v1), a reload to v3 (accepting v1 and v2) gated on a native; a new v1 hold, loading.
type adaptedPending struct {
	host      Host
	ref       *Reference
	gate      *adaptedGate
	reloading <-chan error
}

func adaptedPendingProvider(t testing.TB) adaptedPending {
	t.Helper()
	a1 := adaptedLocal("app.pending.accept", contracts.Mem("x", contracts.Int()))
	a2 := adaptedEvolve(a1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("x", adaptedObjOf("n")) })
	a3 := adaptedEvolve(a2, func(e *contracts.LocalEditor) *contracts.LocalEditor {
		return e.State("x", contracts.Obj(contracts.Prop("wrapped", adaptedObjOf("n"))))
	})
	gate := newAdaptedGate()
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	adaptedLoadOne(t, host, "@app/accept", adaptedConstants(a2, map[string]delta.JsonValue{"x": jsonObject("n", 2.0)}, nil, nil))
	v3 := adaptedConstants(a3, map[string]delta.JsonValue{"x": jsonObject("wrapped", jsonObject("n", 7.0))}, contracts.AcceptsTable{
		adaptedVersion("1", adaptedState("x", adaptedField("wrapped", "n"))),
		adaptedVersion("2", adaptedState("x", adaptedField("wrapped"))),
	}, gate.native)
	reloading := adaptedReload(host, HostFacet{Name: "@app/accept", Facet: v3})
	gate.entered.wait(t)
	deliveredOn(host)
	return adaptedPending{host: host, ref: mustHold(t, host, a1, jsonObject()), gate: gate, reloading: reloading}
}

// Mirrors "reload: an older version only a pending provider maps has no values yet (fix round 7) › reads not_ready rather than asking the shown provider for a mapping it lacks (Sol)".
func TestReloadAnOlderVersionOnlyAPendingProviderMapsHasNoValuesYetReadsNotReadyRatherThanAskingTheShownProviderForAMappingItLacks(t *testing.T) {
	p := adaptedPendingProvider(t)
	defer closeHosts(t, p.host)
	if state := stateOfRef(p.ref); state.Kind != RefStateLoading {
		t.Fatalf("state = %+v, want loading", state)
	}
	_, err := p.ref.Member("x").Value()
	wantChordError(t, err, data.CodeNotReady, "", "")
	p.gate.resolve()
	must(t, receive(t, p.reloading))
	deliveredOn(p.host)
	untilKind(t, p.ref, RefStateLive)
	if got := memberOf(t, p.ref, "x"); got != 7.0 {
		t.Fatalf("x = %v, want 7", got)
	}
	p.ref.Release()
}

// Mirrors "› opens a watch once the accepting provider is live, not before (Sol)".
func TestReloadAnOlderVersionOnlyAPendingProviderMapsHasNoValuesYetOpensAWatchOnceTheAcceptingProviderIsLiveNotBefore(t *testing.T) {
	p := adaptedPendingProvider(t)
	defer closeHosts(t, p.host)
	opening := adaptedWatchLater(p.ref, "x")
	deliveredOn(p.host)
	turn(p.host)
	select {
	case <-opening:
		t.Fatal("the watch opened before the accepting provider was live")
	default:
	}
	p.gate.resolve()
	must(t, receive(t, p.reloading))
	watch := adaptedWaitWatch(t, opening)
	if watch.Value() != 7.0 {
		t.Fatalf("watch = %v, want 7", watch.Value())
	}
	p.ref.Release()
}

// --- an older consumer keeps nothing of its instance's mapping (decision "References are the only indirection") -------------

// Mirrors "an older consumer keeps nothing of its instance's mapping › lets an ended instance's mapping derives go while the older reference waits with an open watch".
func TestAnOlderConsumerKeepsNothingOfItsInstancesMappingLetsAnEndedInstancesMappingDerivesGoWhileTheOlderReferenceWaitsWithAnOpenWatch(t *testing.T) {
	m1 := adaptedLocal("app.mapped.gc", contracts.Mem("n", contracts.Int()))
	m2 := adaptedEvolve(m1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.State("n", adaptedObjOf("value")) })
	facet := adaptedConstants(m2, map[string]delta.JsonValue{"n": jsonObject("value", 4.0)},
		contracts.AcceptsTable{adaptedVersion("1", adaptedState("n", adaptedField("value")))}, nil)
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	adaptedLoadOne(t, host, "@app/mapped", facet)
	old := mustGet(t, host, m1, jsonObject())
	_, calls := adaptedWatch(t, old, "n")
	used := adaptedMade(host, 1, false)
	if len(used) == 0 {
		t.Fatal("watching n made no adapted members")
	}
	adaptedReloadNone(t, host)
	deliveredOn(host)
	// Its last mapped value and the failure once; the instance's mapping went with it.
	adaptedExpectValues(t, host, calls, nexttest.FailedValue{Failed: "app.mapped.gc is not served here"})
	adaptedWant(t, "n", memberOf(t, old, "n"), 4.0)
	expectCollected(t, used...)
	old.Release()
}

// --- an instance's mapping detaches only what it made (final review) -----------------------------------------------------------

// adaptedCounter is `defineState("counter", { version: 1, schema: obj({ n }), initial: () => ({ n: 1 }) })`.
var adaptedCounter = func() *documents.StateDefinition {
	definition, err := documents.DefineState("counter", documents.StateOptions{
		Schema: adaptedObjOf("n"), Version: 1, Initial: func() *delta.JsonObject { return jsonObject("n", 1.0) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

// Mirrors "an instance's mapping detaches only what it made (final review) › keeps an unchanged member, the instance's own derive, when the last older reference leaves (Astra)".
func TestAnInstancesMappingDetachesOnlyWhatItMadeKeepsAnUnchangedMemberTheInstancesOwnDeriveWhenTheLastOlderReferenceLeaves(t *testing.T) {
	d1 := adaptedLocal("app.shared-derive", contracts.Mem("n", contracts.Int()), contracts.Mem("set", contracts.NewMethod(contracts.Int())))
	d2 := adaptedEvolve(d1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.AddState("extra", contracts.Int()) })
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(d2, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			counter := stateOf(self, adaptedCounter)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"n": self.Derive([]any{counter.s}, func(values []any) (delta.JsonValue, error) {
						return values[0].(*delta.JsonObject).Value("n").(float64) * 2, nil
					}),
					"extra": constant(0.0),
				},
				Methods: map[string]contracts.MethodFunction{"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return nil, counter.Change(func(draft *delta.Object) { _ = draft.Set("n", args) }, ctx)
				}},
			}}, nil
		}, ServeOptions{Accepts: contracts.AcceptsTable{{Name: "1"}}})
	})
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	adaptedLoadOne(t, host, "@app/derive", facet)
	newer := mustGet(t, host, d2, jsonObject())
	older := mustGet(t, host, d1, jsonObject())
	// v1 reads `n` unchanged: the instance's own derive, not a mapping.
	if got := memberOf(t, older, "n"); got != 2.0 {
		t.Fatalf("older n = %v, want 2", got)
	}
	_, calls := adaptedWatch(t, newer, "n")
	older.Release()
	if _, err := newer.Call("set", 3.0, background); err != nil {
		t.Fatalf("set: %v", err)
	}
	adaptedExpectValues(t, host, calls, 6.0)
	adaptedWant(t, "n", memberOf(t, newer, "n"), 6.0)
	newer.Release()
}

// --- keyed mappings per member (final review) ----------------------------------------------------------------------------------

// Mirrors "keyed mappings per member (final review) › maps one document exposed as two keyed members through each member's own mapping (Astra)".
func TestKeyedMappingsPerMemberMapsOneDocumentExposedAsTwoKeyedMembersThroughEachMembersOwnMapping(t *testing.T) {
	k1 := adaptedLocal("app.keyed-alias",
		contracts.Mem("first", contracts.Keyed(contracts.Int())), contracts.Mem("second", contracts.Keyed(contracts.Int())),
		contracts.Mem("set", contracts.NewMethod(contracts.Int())))
	k2 := adaptedEvolve(k1, func(e *contracts.LocalEditor) *contracts.LocalEditor {
		return e.Keyed("first", adaptedObjOf("n")).Keyed("second", adaptedObjOf("n"))
	})
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(k2, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			rows := self.State(adaptedRowsDoc).(*documents.Keyed)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				Keyed: map[string]contracts.KeyedWatchable[delta.JsonValue]{"first": ExposeKeyed(rows), "second": ExposeKeyed(rows)},
				Methods: map[string]contracts.MethodFunction{"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					_, err := rows.Change(ctx, "a", func(draft *delta.Object) (any, error) { return nil, draft.Set("n", args) })
					return nil, err
				}},
			}}, nil
		}, ServeOptions{Accepts: contracts.AcceptsTable{{Name: "1", Value: contracts.VersionMappings{Keyed: []contracts.Entry[contracts.KeyedMapping]{
			{Name: "first", Value: contracts.KeyedMapping{Func: adaptedField("n")}},
			{Name: "second", Value: contracts.KeyedMapping{Func: adaptedRowsTimes(10)}},
		}}}}})
	})
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	adaptedLoadOne(t, host, "@app/keyed-alias", facet)
	ref := mustGet(t, host, k1, jsonObject())
	if _, err := ref.Call("set", 5.0, background); err != nil {
		t.Fatalf("set: %v", err)
	}
	first, err := ref.Keyed("first").Watch(background, "a")
	must(t, err)
	second, err := ref.Keyed("second").Watch(background, "a")
	must(t, err)
	read, _, err := ref.Keyed("second").Get(background, "a")
	must(t, err)
	adaptedWant(t, "first, second, read", []any{first.Value(), second.Value(), read}, []any{5.0, 50.0, 50.0})
	_, _ = first.Stop()
	_, _ = second.Stop()
	ref.Release()
}
