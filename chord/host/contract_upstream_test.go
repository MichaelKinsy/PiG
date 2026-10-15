package host

// Ports packages/chord/test/contract.test.ts. "rejects ... opaque remote members" has a Go compile-time form: a
// VariantDef state is a Schema, so an opaque state member does not type-check; the remote case checks the same
// refusal through a local-only member kind instead.

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

var view = tObject("text", tString())

func chatV1Def() RemoteDef {
	return RemoteDef{
		Version: 1,
		Input:   tObject("session", tString(), "conversation", tNumber(Schema{"default": 0})),
		Host:    NewHostFamily("session:", "session"),
		Variants: Fields[VariantDef]{
			{"viewer", VariantDef{State: Fields[Schema]{{"view", view}}}},
			{"participant", VariantDef{
				State:   Fields[Schema]{{"view", view}},
				Methods: Fields[MethodSpec]{{"send", Method(tObject("text", tString()), tObject("id", tNumber()))}},
			}},
		},
	}
}

var chatV1 = Remote("t.chat", chatV1Def())

func mustPanicTypeError(t *testing.T, what string, run func()) {
	t.Helper()
	defer func() {
		t.Helper()
		if _, ok := recover().(*TypeError); !ok {
			t.Fatalf("%s did not throw a TypeError", what)
		}
	}()
	run()
}

func TestContractsNormalizeInputAndOmitDefaultsFromTheIdentity(t *testing.T) {
	a, err := Normalize(chatV1.Input, map[string]any{"session": "s", "extra": 1}, "input")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"session": "s", "conversation": float64(0)}; !reflect.DeepEqual(a, want) {
		t.Fatalf("normalized %#v, want %#v", a, want)
	}
	b, err := Normalize(chatV1.Input, map[string]any{"conversation": 0, "session": "s"}, "input")
	if err != nil {
		t.Fatal(err)
	}
	if CanonicalIdentity(chatV1.Input, a) != CanonicalIdentity(chatV1.Input, b) {
		t.Fatal("identities differ")
	}
	if got := CanonicalIdentity(chatV1.Input, a); got != `{"session":"s"}` {
		t.Fatalf("identity %s", got)
	}
	_, err = Normalize(chatV1.Input, map[string]any{"session": 1}, "input")
	if !IsChordError(err, CodeInvalid) || err.(*ChordError).Reason != "input" {
		t.Fatalf("normalize of a bad input: %v", err)
	}
}

func TestContractsFingerprintsAreStableAndDescriptionBased(t *testing.T) {
	if copy := Remote("t.chat", *chatV1.Remote); copy.Fingerprint != chatV1.Fingerprint {
		t.Fatal("a copy has another fingerprint")
	}
	def := *chatV1.Remote
	def.Version = 2
	if Remote("t.chat", def).Fingerprint == chatV1.Fingerprint {
		t.Fatal("version 2 has the version 1 fingerprint")
	}
	// The values Pi aa9cf15b computes for the same definitions (chat has a host placement) (remote("t.chat", ...).fingerprint and the local t.idx of
	// the opaque test below): the canonical description and hash agree byte for byte.
	if chatV1.Fingerprint != "e8ee3f28594f8426" {
		t.Fatalf("fingerprint %s, Pi e8ee3f28594f8426", chatV1.Fingerprint)
	}
	if got := idxContract().Fingerprint; got != "8559ef74b122b5ed" {
		t.Fatalf("local fingerprint %s, Pi 8559ef74b122b5ed", got)
	}
}

func TestContractsRejectReservedMemberNamesAndOpaqueRemoteMembers(t *testing.T) {
	mustPanicTypeError(t, "a reserved member", func() {
		Local("t.x", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"status", Method(nil, nil)}}})
	})
	mustPanicTypeError(t, "an opaque remote member", func() {
		describeMember("h", Opaque("x@1"), ScopeRemote)
	})
	mustPanicTypeError(t, "a malformed opaque id", func() { Opaque("x") })
}

func participant(state Schema, send MethodSpec) RemoteDef {
	def := chatV1Def()
	def.Variants = withField(def.Variants, "participant", VariantDef{State: Fields[Schema]{{"view", state}}, Methods: Fields[MethodSpec]{{"send", send}}})
	return def
}

// withField is fields with property name set to value, as assigning obj[name] = value: an existing property keeps its
// position.
func withField[V any](fields Fields[V], name string, value V) Fields[V] {
	out := slices.Clone(fields)
	for index := range out {
		if out[index].Name == name {
			out[index].Value = value
			return out
		}
	}
	return append(out, Field[V]{name, value})
}

func TestContractsCoverAdditiveChangesAndRefuseBreakingOnes(t *testing.T) {
	additive := Remote("t.chat", participant(
		tObject("text", tString(), "extra", tNumber()),
		Method(tObject("text", tString(), "urgent", tOptional(tBoolean())), tObject("id", tNumber(), "at", tNumber())),
	))
	if reason := Coverage(additive.Description, chatV1.Description); reason != "" {
		t.Fatalf("additive coverage: %s", reason)
	}
	if adapter, err := Bind(additive, nil, chatV1); adapter != nil || err != nil {
		t.Fatalf("bind additive: %v", err)
	}
	required := Remote("t.chat", participant(view, Method(tObject("text", tString(), "urgent", tBoolean()), tObject("id", tNumber()))))
	if reason := Coverage(required.Description, chatV1.Description); !strings.Contains(reason, "arguments") {
		t.Fatalf("required coverage %q", reason)
	}
	if _, err := Bind(required, nil, chatV1); !IsChordError(err, CodeContractMismatch) {
		t.Fatalf("bind required: %v", err)
	}
	retypedDef := chatV1Def()
	retypedDef.Variants = withField(retypedDef.Variants, "viewer", VariantDef{State: Fields[Schema]{{"view", tObject("text", tNumber())}}})
	if reason := Coverage(Remote("t.chat", retypedDef).Description, chatV1.Description); !strings.Contains(reason, "state") {
		t.Fatalf("retyped coverage %q", reason)
	}
}

func TestContractsMethodsWithoutArgumentsOrResultAreDescribedWithoutThemTheirPresenceMustMatchAVoidConsumerAcceptsAResult(t *testing.T) {
	of := func(member MethodSpec) Description {
		return Local("t.methods", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"m", member}}}).Description
	}
	none := of(Method(nil, nil))
	variant, _ := none.Variants.Get("")
	if member, _ := variant.Get("m"); !reflect.DeepEqual(member, MemberDescription{Kind: MemberMethod}) {
		t.Fatalf("method() described as %+v", member)
	}
	withArgs := of(Method(tString(), nil))
	withResult := of(Method(nil, tNumber()))
	if reason := Coverage(none, none); reason != "" {
		t.Fatalf("none covers none: %s", reason)
	}
	if reason := Coverage(withArgs, none); !strings.Contains(reason, "arguments") {
		t.Fatalf("the consumer sends none, the provider expects some: %q", reason)
	}
	if reason := Coverage(none, withArgs); !strings.Contains(reason, "arguments") {
		t.Fatalf("the consumer sends some, the provider takes none: %q", reason)
	}
	if reason := Coverage(withResult, none); reason != "" {
		t.Fatalf("a void consumer ignores a result: %q", reason)
	}
	if reason := Coverage(none, withResult); !strings.Contains(reason, "result") {
		t.Fatalf("a consumer expecting a result needs one: %q", reason)
	}
}

func TestContractsBindOtherVersionsOnlyThroughAcceptsAdapters(t *testing.T) {
	def := chatV1Def()
	def.Version = 2
	def.Variants = Fields[VariantDef]{{"viewer", VariantDef{State: Fields[Schema]{{"body", view}}}}}
	v2 := Remote("t.chat", def)
	if _, err := Bind(v2, nil, chatV1); !IsChordError(err, CodeContractMismatch) {
		t.Fatalf("bind without adapter: %v", err)
	}
	marker := chordjson.ObjectOf()
	adapter := Adapter(func(current Implementation) Implementation { marker.Set("view", current.Value("body")); return marker })
	got, err := Bind(v2, Accepts{1: adapter}, chatV1)
	if err != nil || got == nil {
		t.Fatalf("bind with adapter: %v", err)
	}
	if result := got(chordjson.ObjectOf("body", "b")); result.Value("view") != "b" {
		t.Fatal("bind returned another adapter")
	}
}

func idxContract() *Contract {
	return Local("t.idx", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{
		{"harness", Opaque("h@1")},
		{"onChange", Method(tFunction([]Schema{tString()}, tVoid()), nil)},
	}})
}

func TestContractsCompareOpaqueMembersByNameVersionAndFunctionMembersStructurally(t *testing.T) {
	a := idxContract()
	members := withField(a.Local.Members, "harness", any(Opaque("h@2")))
	b := Local("t.idx", LocalDef{Version: 1, Input: a.Input, Members: members})
	if reason := Coverage(b.Description, a.Description); !strings.Contains(reason, "opaque") {
		t.Fatalf("opaque coverage %q", reason)
	}
	if !Fits(tFunction([]Schema{tUnknown()}, tVoid()), tFunction([]Schema{tString()}, tVoid())) {
		t.Fatal("a wider parameter does not fit")
	}
	if Fits(tFunction([]Schema{tString()}, tVoid()), tFunction([]Schema{tUnknown()}, tVoid())) {
		t.Fatal("a narrower parameter fits")
	}
}
