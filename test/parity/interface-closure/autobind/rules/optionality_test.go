package rules

import (
	"go/types"
	"strings"
	"testing"
)

func TestParseOptionality(t *testing.T) {
	cases := []struct {
		declared bool
		typ      string
		want     OptionState
	}{
		{false, "string", OptionState{Base: "string"}},
		{true, "string", OptionState{Optional: true, Base: "string"}},
		{false, "string | undefined", OptionState{Optional: true, Base: "string"}},
		{false, "string | null", OptionState{Nullable: true, Base: "string"}},
		{true, "string | null", OptionState{Optional: true, Nullable: true, Base: "string"}},
		{false, "undefined | null | number", OptionState{Optional: true, Nullable: true, Base: "number"}},
		{false, "((x: string) => void) | undefined", OptionState{Optional: true, Base: "((x: string) => void)"}},
		{false, "(string | number) | undefined", OptionState{Optional: true, Base: "string | number"}},
		{false, "Record<string, A | B> | undefined", OptionState{Optional: true, Base: "Record<string, A | B>"}},
		{false, `"a" | "b" | undefined`, OptionState{Optional: true, Base: `"a" | "b"`}},
	}
	for _, c := range cases {
		if got := ParseOptionality(c.declared, c.typ); got != c.want {
			t.Errorf("ParseOptionality(%v, %q) = %+v, want %+v", c.declared, c.typ, got, c.want)
		}
	}
}

const optionalSrc = `
import "encoding/json"
import "context"
type Opt[T any] struct{ Value T; Set bool }
type Option[T any] struct{ Value T; Set bool }
func (o Option[T]) MarshalJSON() ([]byte, error) { return nil, nil }
func (o *Option[T]) UnmarshalJSON([]byte) error { return nil }
type Nested struct{ A int }
type S struct {
	Plain        string
	PlainTagged  string           ` + "`json:\"plainTagged\"`" + `
	Omit         string           ` + "`json:\"omit,omitempty\"`" + `
	OmitZero     Nested           ` + "`json:\"omitZero,omitzero\"`" + `
	Ptr          *string          ` + "`json:\"ptr\"`" + `
	PtrOmit      *string          ` + "`json:\"ptrOmit,omitempty\"`" + `
	PtrPtr       **string         ` + "`json:\"ptrPtr\"`" + `
	Wrapped      Opt[string]      ` + "`json:\"wrapped\"`" + `
	WrappedOmit  Opt[string]      ` + "`json:\"wrappedOmit,omitempty\"`" + `
	WrappedSkip  Opt[string]      ` + "`json:\"-\"`" + `
	Encoded      Option[string]   ` + "`json:\"encoded,omitempty\"`" + `
	PtrPtrSkip   **string         ` + "`json:\"-\"`" + `
	PtrPtrNoTag  **string
	Slice        []string         ` + "`json:\"slice\"`" + `
	SliceOmit    []string         ` + "`json:\"sliceOmit,omitempty\"`" + `
	SliceNoTag   []string
	Map          map[string]any   ` + "`json:\"map\"`" + `
	Any          any              ` + "`json:\"any\"`" + `
	AnyOmit      any              ` + "`json:\"anyOmit,omitempty\"`" + `
	Raw          json.RawMessage  ` + "`json:\"raw\"`" + `
	RawOmit      json.RawMessage  ` + "`json:\"rawOmit,omitempty\"`" + `
	Hook         func()           ` + "`json:\"-\"`" + `
	HookNoTag    func()
	Ctx          context.Context  ` + "`json:\"-\"`" + `
	Count        int
}
type Opt2 = Opt[int]
func Maybe() *string { return nil }
func Pair() (string, bool) { return "", false }
func PairErr() (string, bool, error) { return "", false, nil }
func Plain() string { return "" }
func PlainErr() (string, error) { return "", nil }
func Triple() (string, int, bool) { return "", 0, false }
func Slice() []string { return nil }
func None() {}
func Params(a string, b *string, c []string, d ...string) {}
`

func TestCheckOptionalProperty(t *testing.T) {
	l := ouLoad(t, optionalSrc)
	req := ParseOptionality(false, "string")
	opt := ParseOptionality(true, "string")
	nul := ParseOptionality(false, "string | null")
	both := ParseOptionality(true, "string | null")
	cases := []struct {
		name  string
		up    OptionState
		field string
		opts  OptionalOptions
		want  Tri
		rule  string
	}{
		{"required plain", req, "Plain", OptionalOptions{}, Yes, "O1"},
		{"required tagged", req, "PlainTagged", OptionalOptions{}, Yes, "O1"},
		{"required omitempty drops values", req, "Omit", OptionalOptions{}, No, "O1"},
		{"required omitzero drops values", req, "OmitZero", OptionalOptions{}, No, "O1"},
		{"required pointer adds nil", req, "Ptr", OptionalOptions{}, No, "O1"},
		{"required wrapper adds absence", req, "Wrapped", OptionalOptions{}, No, "O1"},
		{"required slice", req, "Slice", OptionalOptions{}, Yes, "O1"},

		{"optional pointer", opt, "Ptr", OptionalOptions{}, Yes, "O2"},
		{"optional pointer omitempty", opt, "PtrOmit", OptionalOptions{}, Yes, "O2"},
		{"optional omitempty scalar", opt, "Omit", OptionalOptions{}, Yes, "O2"},
		{"optional omitzero struct", opt, "OmitZero", OptionalOptions{}, Yes, "O2"},
		{"optional wrapper marshals its struct fields", opt, "Wrapped", OptionalOptions{}, Unknown, "O2"},
		{"optional wrapper omitempty still marshals an object", opt, "WrappedOmit", OptionalOptions{}, Unknown, "O2"},
		{"optional wrapper not serialized", opt, "WrappedSkip", OptionalOptions{}, Yes, "O2"},
		{"optional wrapper encodes itself", opt, "Encoded", OptionalOptions{}, Yes, "O2"},
		{"optional plain scalar has no unset state", opt, "Plain", OptionalOptions{}, No, "O2"},
		{"optional tagged scalar has no unset state", opt, "PlainTagged", OptionalOptions{}, No, "O2"},
		{"optional plain scalar by documented zero", opt, "Plain", OptionalOptions{ZeroMeansUnset: true}, Yes, "O2"},
		{"optional struct is never zero-unset", ParseOptionality(true, "Nested"), "OmitZero", OptionalOptions{ZeroMeansUnset: true}, Yes, "O2"},
		{"optional plain int", ParseOptionality(true, "number"), "Count", OptionalOptions{}, No, "O2"},
		{"optional pointer to pointer", opt, "PtrPtr", OptionalOptions{}, No, "O2"},
		{"optional slice marshals null", opt, "Slice", OptionalOptions{}, No, "O2"},
		{"optional slice omitempty", opt, "SliceOmit", OptionalOptions{}, Yes, "O2"},
		{"optional slice without tag", opt, "SliceNoTag", OptionalOptions{}, Unknown, "O2"},
		{"optional hook not serialized", opt, "Hook", OptionalOptions{}, Yes, "O2"},
		{"optional hook without tag is a func", opt, "HookNoTag", OptionalOptions{}, Yes, "O2"},
		{"optional context skipped", opt, "Ctx", OptionalOptions{}, Yes, "O2"},
		{"optional any marshals null", opt, "Any", OptionalOptions{}, No, "O2"},
		{"optional any omitempty", opt, "AnyOmit", OptionalOptions{}, Yes, "O2"},

		{"nullable pointer", nul, "Ptr", OptionalOptions{}, Yes, "O3"},
		{"nullable pointer omitempty drops null", nul, "PtrOmit", OptionalOptions{}, No, "O3"},
		{"nullable any", nul, "Any", OptionalOptions{}, Yes, "O3"},
		{"nullable raw", nul, "Raw", OptionalOptions{}, Yes, "O3"},
		{"nullable slice", nul, "Slice", OptionalOptions{}, Yes, "O3"},
		{"nullable plain has no null", nul, "Plain", OptionalOptions{}, No, "O3"},
		{"nullable wrapper marshals its struct fields", nul, "Wrapped", OptionalOptions{}, Unknown, "O3"},
		{"nullable wrapper encodes itself", nul, "Encoded", OptionalOptions{}, Yes, "O3"},
		{"nullable pointer to pointer adds absence", nul, "PtrPtr", OptionalOptions{}, No, "O3"},

		{"optional nullable wrapper has one unset state", both, "Wrapped", OptionalOptions{}, Unknown, "O4"},
		{"optional nullable encoding wrapper has one unset state", both, "Encoded", OptionalOptions{}, Unknown, "O4"},
		{"optional nullable pointer to pointer decodes null as absent", both, "PtrPtr", OptionalOptions{}, No, "O4"},
		{"optional nullable pointer to pointer not serialized", both, "PtrPtrSkip", OptionalOptions{}, Yes, "O4"},
		{"optional nullable pointer to pointer without tag", both, "PtrPtrNoTag", OptionalOptions{}, Unknown, "O4"},
		{"optional nullable raw omitempty", both, "RawOmit", OptionalOptions{}, Yes, "O4"},
		{"optional nullable raw without omit", both, "Raw", OptionalOptions{}, Unknown, "O4"},
		{"optional nullable pointer collapses", both, "PtrOmit", OptionalOptions{}, Unknown, "O4"},
		{"optional nullable plain", both, "Plain", OptionalOptions{}, No, "O4"},
	}
	for _, c := range cases {
		got := CheckOptionalProperty(c.up, l.field(t, "S", c.field), c.opts)
		if got.Result != c.want || got.Rule != c.rule {
			t.Errorf("%s: got %s %s (%s), want %s %s", c.name, got.Rule, triName(got.Result), got.Why, c.rule, triName(c.want))
		}
	}
}

func TestOptionalScalarNote(t *testing.T) {
	l := ouLoad(t, optionalSrc)
	v := CheckOptionalProperty(ParseOptionality(true, "string"), l.field(t, "S", "Omit"), OptionalOptions{})
	if v.Result != Yes || len(v.Notes) != 1 {
		t.Fatalf("an omitempty scalar must carry the zero-value note: %+v", v)
	}
	v = CheckOptionalProperty(ParseOptionality(true, "Nested"), l.field(t, "S", "OmitZero"), OptionalOptions{})
	if len(v.Notes) != 0 {
		t.Fatalf("a struct has no zero-value note: %+v", v)
	}
}

func TestCustomWrapper(t *testing.T) {
	l := ouLoad(t, `type Present[T any] struct{ V T }
type S struct{ F Present[int] `+"`json:\"-\"`"+` }`)
	up := ParseOptionality(true, "number")
	if v := CheckOptionalProperty(up, l.field(t, "S", "F"), OptionalOptions{}); v.Result != No {
		t.Fatalf("an unknown wrapper name is a plain struct: %+v", v)
	}
	if v := CheckOptionalProperty(up, l.field(t, "S", "F"), OptionalOptions{Wrappers: map[string]bool{"Present": true}}); v.Result != Yes {
		t.Fatalf("a configured wrapper is a presence wrapper: %+v", v)
	}
}

func TestCheckOptionalResult(t *testing.T) {
	l := ouLoad(t, optionalSrc)
	results := func(fn string) *types.Tuple { return l.typ(t, fn).(*types.Signature).Results() }
	cases := []struct {
		name string
		up   OptionState
		fn   string
		want Tri
	}{
		{"T | undefined as *T", ParseOptionality(false, "string | undefined"), "Maybe", Yes},
		{"T | undefined as (T, bool)", ParseOptionality(false, "string | undefined"), "Pair", Yes},
		{"T | undefined as (T, bool, error)", ParseResultOptionality("Promise<string | undefined>"), "PairErr", Yes},
		{"T | undefined as slice", ParseOptionality(false, "string[] | undefined"), "Slice", Yes},
		{"T | undefined as plain T", ParseOptionality(false, "string | undefined"), "Plain", No},
		{"T | undefined as (T, error)", ParseOptionality(false, "string | undefined"), "PlainErr", No},
		{"T | null as *T", ParseOptionality(false, "string | null"), "Maybe", Yes},
		{"T | null | undefined as *T", ParseOptionality(false, "string | null | undefined"), "Maybe", Unknown},
		{"required as plain", ParseOptionality(false, "string"), "Plain", Yes},
		{"required as (T, error)", ParseOptionality(false, "string"), "PlainErr", Yes},
		{"required as (T, bool)", ParseOptionality(false, "string"), "Pair", No},
		{"required as pointer", ParseOptionality(false, "string"), "Maybe", No},
		{"three results", ParseOptionality(false, "string"), "Triple", Unknown},
		{"no results", ParseOptionality(false, "string"), "None", Unknown},
	}
	for _, c := range cases {
		got := CheckOptionalResult(c.up, results(c.fn), OptionalOptions{})
		if got.Result != c.want || got.Rule != "O5" {
			t.Errorf("%s: got %s %s (%s), want %s", c.name, got.Rule, triName(got.Result), got.Why, triName(c.want))
		}
	}
}

func TestParseResultOptionality(t *testing.T) {
	for typ, want := range map[string]OptionState{
		"Promise<string | undefined>":   {Optional: true, Base: "string"},
		"Promise<string> | undefined":   {Optional: true, Base: "Promise<string>"},
		"string | undefined":            {Optional: true, Base: "string"},
		"Promise<void>":                 {Base: "void"},
		"Promise<A | null | undefined>": {Optional: true, Nullable: true, Base: "A"},
	} {
		if got := ParseResultOptionality(typ); got != want {
			t.Errorf("ParseResultOptionality(%q) = %+v, want %+v", typ, got, want)
		}
	}
}

func TestCheckOptionalParam(t *testing.T) {
	l := ouLoad(t, optionalSrc)
	sig := l.typ(t, "Params").(*types.Signature)
	p := sig.Params()
	last := p.Len() - 1
	elem := p.At(last).Type().(*types.Slice).Elem()
	cases := []struct {
		name     string
		up       OptionState
		t        types.Type
		variadic bool
		want     Tri
	}{
		{"required value", ParseOptionality(false, "string"), p.At(0).Type(), false, Yes},
		{"optional pointer", ParseOptionality(true, "string"), p.At(1).Type(), false, Yes},
		{"optional slice", ParseOptionality(true, "string[]"), p.At(2).Type(), false, Yes},
		{"optional value", ParseOptionality(true, "string"), p.At(0).Type(), false, No},
		{"nullable value", ParseOptionality(false, "string | null"), p.At(0).Type(), false, No},
		{"required pointer", ParseOptionality(false, "string"), p.At(1).Type(), false, No},
		{"optional variadic tail", ParseOptionality(true, "string"), elem, true, Yes},
		{"required variadic tail", ParseOptionality(false, "string"), elem, true, No},
	}
	for _, c := range cases {
		got := CheckOptionalParam(c.up, c.t, c.variadic, OptionalOptions{})
		if got.Result != c.want || got.Rule != "O6" {
			t.Errorf("%s: got %s %s (%s), want %s", c.name, got.Rule, triName(got.Result), got.Why, triName(c.want))
		}
	}
}

// fakeEnv lets a rule recurse through env.Agree with a function.
type fakeEnv struct {
	agree func(up string, t types.Type) Verdict
	alias map[string]string
}

func (f fakeEnv) Package() string                       { return "ai" }
func (f fakeEnv) Agree(up string, t types.Type) Verdict { return f.agree(up, t) }
func (f fakeEnv) ResolveType(string) *types.TypeName    { return nil }
func (f fakeEnv) PropertyNames(string) []string         { return nil }
func (f fakeEnv) Representation(string) (string, bool)  { return "", false }
func (f fakeEnv) Generic(string) bool                   { return false }
func (f fakeEnv) AliasBody(name string) (string, bool)  { b, ok := f.alias[name]; return b, ok }

// basicEnv agrees a string with Go strings and a number with Go numbers, which is what the built-in rules do for these types.
func basicEnv(alias map[string]string) fakeEnv {
	return fakeEnv{alias: alias, agree: func(up string, t types.Type) Verdict {
		b, _ := t.Underlying().(*types.Basic)
		switch {
		case b == nil:
			return Undecided("not basic")
		case up == "string" && b.Info()&types.IsString != 0, up == "number" && b.Info()&types.IsNumeric != 0:
			return Accept("basic")
		}
		return Refute("basic mismatch")
	}}
}

func TestFamilyOptionalityRulesAreRegisteredUnderTheFamily(t *testing.T) {
	got := strings.Join(Registered(), ",")
	if !strings.Contains(got, "optionality/O2w") {
		t.Errorf("O2w is not registered: %s", got)
	}
	// **T has no type rule: encoding/json decodes null into the outer pointer, so only the tag-aware O4 may accept it.
	if strings.Contains(got, "optionality/O4p") {
		t.Errorf("O4p must not be registered: %s", got)
	}
}

func TestPresenceWrapperRule(t *testing.T) {
	l := ouLoad(t, `type Opt[T any] struct{ V T }
type Option[T any] struct{ V T }
func (o Option[T]) MarshalJSON() ([]byte, error) { return nil, nil }
func (o *Option[T]) UnmarshalJSON([]byte) error { return nil }
type Box[T any] struct{ V T }
type S struct {
	Str Option[string]
	Raw Opt[string]
	Num Opt[int]
	Box Box[string]
	Pl  string
	PP  **string
	P   *string
	PPN **int
}`)
	env := basicEnv(nil)
	cases := []struct {
		name  string
		up    string
		field string
		want  Tri
		ok    bool
	}{
		{"optional string in Option[string]", "string | undefined", "Str", Yes, true},
		{"nullable string in Option[string]", "string | null", "Str", Yes, true},
		{"optional nullable has three states", "string | null | undefined", "Str", 0, false},
		{"wrapper without JSON methods marshals its fields", "string | undefined", "Raw", 0, false},
		{"payload disagrees", "string | undefined", "Num", No, true},
		{"required upstream is silent", "string", "Str", 0, false},
		{"not a wrapper name", "string | undefined", "Box", 0, false},
		{"not a named type", "string | undefined", "Pl", 0, false},
	}
	for _, c := range cases {
		v, ok := presenceWrapper{}.Type(env, c.up, l.field(t, "S", c.field).Type)
		if ok != c.ok || ok && v.OK != c.want {
			t.Errorf("%s: got %+v %v, want %s %v", c.name, v, ok, triName(c.want), c.ok)
		}
	}
	// No registered rule accepts **T for an optional nullable type: a type rule cannot see that the field is serialized, and
	// encoding/json decodes {"x":null} into a nil outer pointer.
	if v, ok := CheckType(env, "string | null | undefined", l.field(t, "S", "PP").Type); ok && v.OK == Yes {
		t.Errorf("a registered rule accepted **string for string | null | undefined: %+v", v)
	}
	if _, ok := CheckType(env, "string | undefined", l.field(t, "S", "Str").Type); !ok {
		t.Error("the registry must reach the wrapper rule")
	}
}
