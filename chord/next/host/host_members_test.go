package host

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Tests of packages/chord/test/next/host/members.test.ts: its two cases (TestHostGet...) serve a contract through a whole
// host and assert the message the host builds from the object's label and the cause. The other tests are own tests of the
// member check and of the resolution behind derives and contributions, driven directly; each names the upstream case
// whose behavior it mirrors.

// membersGone is a type no host serves: a reference held to it has a member whose getter throws (the check that left it
// unbound failed it `waiting`), and a keyed member a re-exposing object could follow.
var membersGone = mustContract(contracts.Local("app.gone", contracts.LocalDeclaration{
	Params:  contracts.Params{},
	Version: 1,
	Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int()), contracts.Mem("notes", contracts.Keyed(contracts.Int()))},
}))

// membersHeld is a reference on a loaded host held to membersGone: its state member `n` is a ReferenceMember whose
// getter throws not_served (Pi's reference member getter), its keyed member `notes` a ReferenceKeyed.
func membersHeld(t *testing.T) *Reference {
	t.Helper()
	host, _ := loaded(t, func(FacetHost, FacetSelf) error { return nil }, loadedOptions{})
	held, err := host.Hold(membersGone, jsonObject(), background, GetOptions{})
	must(t, err)
	t.Cleanup(held.Release)
	return held
}

// wantMembersNotServed fails the test unless err is the failure the getter of membersHeld's member throws.
func wantMembersNotServed(t *testing.T, err error) {
	t.Helper()
	chordErr, ok := data.IsChordErrorCode(err, data.CodeUnavailable)
	if !ok || chordErr == nil {
		t.Fatalf("error = %v, want the not-served failure of the held reference", err)
	}
}

func membersLocal(t *testing.T, name string, params contracts.Params, members ...contracts.LocalEntry) *contracts.Contract {
	t.Helper()
	contract, err := contracts.Local(name, contracts.LocalDeclaration{Version: 1, Params: params, Members: members})
	if err != nil {
		t.Fatalf("declare %s: %v", name, err)
	}
	return contract
}

func membersRemote(t *testing.T, name string, variants ...contracts.VariantEntry) *contracts.Contract {
	t.Helper()
	contract, err := contracts.Remote(name, contracts.RemoteDeclaration{
		Version:     1,
		Params:      contracts.Params{},
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return contracts.Address{}, nil },
		Variants:    variants,
	})
	if err != nil {
		t.Fatalf("declare %s: %v", name, err)
	}
	return contract
}

func membersCheck(contract *contracts.Contract, result factoryResult) (*string, *members, error) {
	return checkImplementation(contract, result)
}

func wantMembersError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func membersPlainMethod(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }

// membersOwner is the owner of a writable that stays usable.
type membersOwner struct{}

func (membersOwner) Context() context.Context { return memberBackground }
func (membersOwner) AssertUsable() error      { return nil }

// Own test, mirrors members.test.ts › the member check (Q14) › refuses an inherited property for a member: a member of the
// contract that the implementation does not own is not implemented, whatever its name. (An inherited `toString` has no
// Go form: the implementation lists only the members it owns.) The host adds the object's label to the error.
func TestMemberCheckRefusesAMethodTheImplementationDoesNotOwn(t *testing.T) {
	named := membersLocal(t, "app.named", contracts.Params{}, contracts.Mem("toString", contracts.NewMethod()))
	_, _, err := membersCheck(named, factoryResult{})
	wantMembersError(t, err, "toString: a method is a function")
	// A nil function is no function either.
	var none contracts.MethodFunction
	_, _, err = membersCheck(named, factoryResult{members: implementation{{name: "toString", value: none}}})
	wantMembersError(t, err, "toString: a method is a function")
}

// Own test, mirrors members.test.ts › the member check (Q14) › names a derive in errors by its member when exposed, else
// by its position.
func TestMemberCheckNamesADeriveInErrorsByItsMemberWhenExposedElseByItsPosition(t *testing.T) {
	h := newMemberHarness(t)
	bad := struct{ value int }{1}
	report := func(string, bool) func(error) { return func(error) {} }
	compute := func([]any) (delta.JsonValue, error) { return float64(1), nil }

	// n == 1: `inner` has no sources; `label` is exposed and reads `[inner, bad]`.
	inner := state.NewDerive(nil, compute, h.batch)
	label := state.NewDerive([]any{inner, bad}, compute, h.batch)
	exposed := contracts.NewNamedMap[state.DeriveSource](1)
	exposed.Set("label", label)
	err := attachDerives([]*state.Derive{inner, label}, exposed, report)
	wantMembersError(t, err, "label: source 1 is not a Chord watchable")

	// n == 2: `inner` reads `[bad]` and is not exposed: it is named by its position; `label` reads `[inner]`.
	inner = state.NewDerive([]any{bad}, compute, h.batch)
	label = state.NewDerive([]any{inner}, compute, h.batch)
	exposed = contracts.NewNamedMap[state.DeriveSource](1)
	exposed.Set("label", label)
	err = attachDerives([]*state.Derive{inner, label}, exposed, report)
	wantMembersError(t, err, "derive #1: source 0 is not a Chord watchable")
}

// Own test: attachDerives reports each derive's failure under the member exposing it (`derive_failed`), accepts a native
// source behind a derive, and fails with what a source's getter throws (members.ts `attachDerives`).
func TestAttachDerivesReportsUnderTheExposingMemberAcceptsNativeSourcesAndPropagatesAThrowingGetter(t *testing.T) {
	h := newMemberHarness(t)
	failing := func([]any) (delta.JsonValue, error) { return nil, errors.New("compute failed") }
	exposedDerive := state.NewDerive([]any{state.NewConstant(float64(1))}, failing, h.batch)
	bare := state.NewDerive([]any{state.NewConstant(float64(1))}, failing, h.batch)
	exposed := contracts.NewNamedMap[state.DeriveSource](1)
	exposed.Set("label", exposedDerive)
	type reported struct {
		member  string
		exposed bool
	}
	var asked []reported
	var failures []string
	report := func(member string, isExposed bool) func(error) {
		asked = append(asked, reported{member, isExposed})
		return func(err error) { failures = append(failures, member+": "+err.Error()) }
	}
	if err := attachDerives([]*state.Derive{bare, exposedDerive}, exposed, report); err != nil {
		t.Fatalf("attach: %v", err)
	}
	expectEqual(t, "reports asked", asked, []reported{{"", false}, {"label", true}})
	h.loop.Run(func() {
		_, _ = exposedDerive.Value()
		_, _ = bare.Value()
	})
	expectEqual(t, "failures", failures, []string{"label: compute failed", ": compute failed"})

	// A native source is a derive source though it is no JSON watchable.
	native := state.NewWritableSource[any](1, membersOwner{}, h.batch)
	viaNative := state.NewDerive([]any{native}, func([]any) (delta.JsonValue, error) { return float64(1), nil }, h.batch)
	if err := attachDerives([]*state.Derive{viaNative}, contracts.NewNamedMap[state.DeriveSource](0), report); err != nil {
		t.Fatalf("attach over a native source: %v", err)
	}
	throwing := state.NewDerive([]any{membersHeld(t).Member("n")}, failing, h.batch)
	err := attachDerives([]*state.Derive{throwing}, contracts.NewNamedMap[state.DeriveSource](0), report)
	wantMembersNotServed(t, err)
}

// Own test of checkImplementation (members.ts): a local contract's members are checked as given: methods are functions,
// state members JSON watchables, keyed members keyed document handles; the order is the variant's.
func TestCheckImplementationChecksALocalContractsMembersAsGiven(t *testing.T) {
	h := newMemberHarness(t)
	store := membersLocal(t, "app.store", contracts.Params{},
		contracts.Mem("add", contracts.NewMethod()),
		contracts.Mem("count", contracts.Int()),
		contracts.Mem("list", contracts.Int()),
		contracts.Mem("notes", contracts.Keyed(contracts.Int())),
	)
	count := state.NewConstant(float64(1))
	list := state.NewDerive(nil, func([]any) (delta.JsonValue, error) { return float64(2), nil }, h.batch)
	notes := membersHeld(t).Keyed("notes")
	good := implementation{
		{"notes", notes}, {"list", list}, {"count", count}, {"add", membersPlainMethod},
	}
	variant, checked, err := membersCheck(store, factoryResult{members: good})
	if err != nil || variant != nil {
		t.Fatalf("check = %v, %v, want no variant and no error", variant, err)
	}
	expectEqual(t, "methods", checked.methods.Keys(), []string{"add"})
	expectEqual(t, "state", checked.state.Keys(), []string{"count", "list"})
	if checked.state.Value("count") != state.DeriveSource(count) || checked.state.Value("list") != state.DeriveSource(list) {
		t.Fatalf("the state members are not resolved to what Chord follows")
	}
	if got := checked.keyed.Value("notes"); got.kind != keyedReexposed || got.binding != notes.binding || got.member != "notes" {
		t.Fatalf("the keyed member = %+v, want the re-exposed member of the reference", got)
	}
	if out, err := checked.methods.Value("add")(nil, memberBackground); out != nil || err != nil {
		t.Fatalf("the method = %v, %v", out, err)
	}
	// The same function typed as a plain function value is a method too.
	plain := func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }
	if _, _, err := membersCheck(store, factoryResult{members: implementation{{"notes", notes}, {"list", list}, {"count", count}, {"add", plain}}}); err != nil {
		t.Fatalf("a plain function value: %v", err)
	}

	for _, tc := range []struct {
		name    string
		members implementation
		want    string
	}{
		{"a member the variant lacks", append(append(implementation{}, good...), implementationMember{"extra", 1}), "extra: not a member"},
		{"the first of several", implementation{{"zeta", 1}, {"alpha", 2}}, "zeta: not a member"},
		{"a method that is no function", implementation{{"add", "text"}}, "add: a method is a function"},
		{"a state member that is no watchable", implementation{{"add", membersPlainMethod}, {"count", 5}}, "count: a state member is a JSON watchable"},
		{"a missing state member", implementation{{"add", membersPlainMethod}}, "count: a state member is a JSON watchable"},
		{"a keyed member that is no keyed handle", implementation{{"add", membersPlainMethod}, {"count", count}, {"list", list}, {"notes", count}}, "notes: a keyed member is a keyed document handle"},
		{"a throwing state getter", implementation{{"add", membersPlainMethod}, {"count", membersHeld(t).Member("n")}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := membersCheck(store, factoryResult{members: tc.members})
			if tc.want == "" {
				wantMembersNotServed(t, err)
				return
			}
			wantMembersError(t, err, tc.want)
		})
	}
}

// Own test of checkImplementation (members.ts): a remote factory returns `{ variant, members }` naming one of the
// contract's current variants; the members are checked against that variant's.
func TestCheckImplementationChecksARemoteContractsVariant(t *testing.T) {
	two := membersRemote(t, "app.two",
		contracts.Var("full", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}}),
		contracts.Var("view", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("m", contracts.Int())}}),
	)
	full, view := "full", "view"
	variant, checked, err := membersCheck(two, factoryResult{variant: &view, members: implementation{{"m", state.NewConstant(float64(2))}}})
	if err != nil || *variant != "view" || checked.state.Keys()[0] != "m" {
		t.Fatalf("view = %v, %v, %v", variant, checked, err)
	}
	_, _, err = membersCheck(two, factoryResult{variant: &full, members: implementation{{"m", state.NewConstant(float64(2))}}})
	wantMembersError(t, err, "m: not a member")
	unknown := `we"ird`
	_, _, err = membersCheck(two, factoryResult{variant: &unknown})
	wantMembersError(t, err, `variant "we\"ird": not a variant of app.two`)
	_, _, err = membersCheck(two, factoryResult{})
	wantMembersError(t, err, "variant undefined: not a variant of app.two")
}

// Own test of contributionOf and its content (members.ts): a Chord watchable is followed, a native source is followed, any
// other value is plain whatever it is, a throwing getter fails the contribution, and a registry's value that closes a
// cycle of registries is refused `invalid/cycle`.
func TestContributionOfFollowsWatchablesAndNativesLeavesOtherValuesPlainAndRefusesACycle(t *testing.T) {
	h := newMemberHarness(t)
	record := registries.NewRegistryRecord("app.menu", h.batch, func(registries.RegistryError) {})
	contributor := registries.Contributor{Plugin: "@test/app"}
	constant := state.NewConstant(float64(1))
	native := state.NewWritableSource[any](1, membersOwner{}, h.batch)
	for _, tc := range []struct {
		name  string
		value any
		want  registries.ContributionContent
	}{
		{"a watchable", constant, registries.ContributionContent{Kind: registries.ContributionJSON, Source: constant}},
		{"a native source", native, registries.ContributionContent{Kind: registries.ContributionNative, Native: native}},
		{"a plain value", "text", registries.ContributionContent{Kind: registries.ContributionPlain, Value: "text"}},
		{"nothing", nil, registries.ContributionContent{Kind: registries.ContributionPlain}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := contentOf(tc.value)
			if err != nil {
				t.Fatalf("content: %v", err)
			}
			expectEqual(t, "content", got, tc.want)
		})
	}
	// A value with methods is a plain value (registries carry natives).
	if got, err := contentOf(membersPlainMethod); err != nil || got.Kind != registries.ContributionPlain || got.Value == nil {
		t.Fatalf("content of a function = %#v, %v, want it as a plain value", got, err)
	}
	throwing := membersHeld(t).Member("n")
	_, err := contentOf(throwing)
	wantMembersNotServed(t, err)
	contribution, err := contributionOf(record, contributor, 1, "text")
	if err != nil || contribution.Record != record || contribution.Contributor != contributor || contribution.Version != 1 {
		t.Fatalf("contribution = %#v, %v", contribution, err)
	}
	_, err = contributionOf(record, contributor, 1, throwing)
	wantMembersNotServed(t, err)
	// A registry's own value contributed to it closes a cycle of one registry.
	own := registries.NewRegistryValueSource(record, 1, h.batch)
	_, err = contributionOf(record, contributor, 1, own)
	if chordErr, ok := data.IsChordErrorCode(err, data.CodeInvalid); !ok || chordErr.Reason != data.ReasonCycle {
		t.Fatalf("a registry contributing its own value = %v, want invalid/cycle", err)
	}
}

// members.test.ts › the member check (Q14) › refuses an inherited property for a member.
func TestHostGetRefusesAnInheritedPropertyForAMember(t *testing.T) {
	named := mustContract(contracts.Local("app.named", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("toString", contracts.NewMethod())},
	}))
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		// Pi's `{}`: no own member, whatever the prototype has.
		return h.Serve(named, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	_, err := host.Get(named, jsonObject("id", float64(1)), background, GetOptions{})
	if err == nil || !strings.Contains(err.Error(), "app.named(1): toString: a method is a function") {
		t.Fatalf("get = %v, want it to fail with app.named(1): toString: a method is a function", err)
	}
}

// members.test.ts › the member check (Q14) › names a derive in errors by its member when exposed, else by its position.
func TestHostGetNamesADeriveInErrorsByItsMemberWhenExposedElseByItsPosition(t *testing.T) {
	named := mustContract(contracts.Local("app.named", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{contracts.Prop("n", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("label", contracts.Int())},
	}))
	bad := struct{ value int }{1}
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(named, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			compute := func(value float64) func([]any) (delta.JsonValue, error) {
				return func([]any) (delta.JsonValue, error) { return value, nil }
			}
			first := params.Value("n") == float64(1)
			var inner, label chord.Watchable[delta.JsonValue]
			if first {
				inner = self.Derive(nil, compute(1))
				label = self.Derive([]any{inner, bad}, compute(2))
			} else {
				inner = self.Derive([]any{bad}, compute(1))
				label = self.Derive([]any{inner}, compute(2))
			}
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"label": label},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	for _, tc := range []struct {
		n    float64
		want string
	}{
		{1, "app.named(1): label: source 1 is not a Chord watchable"},
		{2, "app.named(2): derive #1: source 0 is not a Chord watchable"},
	} {
		_, err := host.Get(named, jsonObject("n", tc.n), background, GetOptions{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("get n=%v = %v, want it to fail with %q", tc.n, err, tc.want)
		}
	}
}
