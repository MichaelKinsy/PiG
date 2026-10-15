package rules

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"
)

// dsEnv is the part of the detector the data-shape rules use: Agree for scalars, and a property table for upstream interfaces.
type dsEnv struct{ props map[string][]string }

func (dsEnv) Package() string { return "test" }
func (dsEnv) Agree(up string, t types.Type) Verdict {
	b, _ := t.Underlying().(*types.Basic)
	switch {
	case up == "string" && b != nil && b.Info()&types.IsString != 0, up == "number" && b != nil && b.Info()&types.IsNumeric != 0:
		return Accept("scalar")
	case up == "string" || up == "number":
		return Refute("scalar")
	}
	return Undecided("fake env")
}
func (dsEnv) ResolveType(string) *types.TypeName { return nil }
func (dsEnv) AliasBody(string) (string, bool)    { return "", false }
func (e dsEnv) PropertyNames(n string) []string  { return e.props[n] }
func (dsEnv) Representation(string) (string, bool) {
	return "", false
}
func (dsEnv) Generic(string) bool { return false }

// check type-checks one source file against the standard library and returns its package scope.
func check(t *testing.T, src string) *types.Scope {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	pkg, err := conf.Check("x", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pkg.Scope()
}

func typeOf(t *testing.T, s *types.Scope, name string) types.Type {
	t.Helper()
	o := s.Lookup(name)
	if o == nil {
		t.Fatalf("no %s", name)
	}
	return o.Type()
}

const dsSource = `package x

import (
	"math/big"
	"net/http"
	"sync/atomic"
)

type Counter struct{ Calls atomic.Int64; Flag atomic.Bool }
type Entry struct{ Key string; Value *string }
type Ordered []Entry
func (Ordered) MarshalJSON() ([]byte, error) { return nil, nil }
func (*Ordered) UnmarshalJSON([]byte) error { return nil }
type Unordered []Entry
type Holder struct{ Sections *Ordered }
type Triple struct{ A, B, C string }
type Wide []Triple
func (Wide) MarshalJSON() ([]byte, error) { return nil, nil }
func (*Wide) UnmarshalJSON([]byte) error { return nil }

type Doer interface{ Do(*http.Request) (*http.Response, error) }
type WrongDoer interface{ Do(string) error }
type Opts struct{ Fetch *http.Client; Sender Doer; Wrong WrongDoer; Hook func(*http.Request) (*http.Response, error); Other func(int) int; Rt http.RoundTripper }
type Big struct{ A *big.Int; B int64; C int32 }
type Pair struct{ P [2]string; Q [3]string; R [2]int; S []string }

type Base struct { APIKey string ` + "`json:\"apiKey\"`" + `; MaxTokens int }
type Config struct { Base Base; Name string }
type Other struct{ Name string }
type Embedded struct{ Base }

type Failure struct{ Msg string; Err error }
func (f *Failure) Error() string { return f.Msg }
func (f *Failure) Unwrap() error { return f.Err }
type SubFailure struct{ Failure }
func (f *SubFailure) Unwrap() error { return &f.Failure }
type Wrapped struct{ Inner error }
func (w Wrapped) Error() string { return "w" }
func (w Wrapped) Cause() error { return w.Inner }
type NotError struct{ Msg string }
`

func field(t *testing.T, s *types.Scope, owner, name string) types.Type {
	t.Helper()
	st := typeOf(t, s, owner).Underlying().(*types.Struct)
	for f := range st.Fields() {
		if f.Name() == name {
			return f.Type()
		}
	}
	t.Fatalf("no field %s.%s", owner, name)
	return nil
}

func TestDS1AtomicNumber(t *testing.T) {
	s := check(t, dsSource)
	r := atomicNumber{}
	if v, ok := r.Type(dsEnv{}, "number", field(t, s, "Counter", "Calls")); !ok || v.OK != Yes {
		t.Fatalf("atomic.Int64: %+v %v", v, ok)
	}
	if v, ok := r.Type(dsEnv{}, "number | undefined", field(t, s, "Counter", "Calls")); !ok || v.OK != Yes {
		t.Fatalf("optional number: %+v %v", v, ok)
	}
	for _, c := range []struct {
		up string
		t  types.Type
	}{{"number", field(t, s, "Counter", "Flag")}, {"string", field(t, s, "Counter", "Calls")}, {"number | string", field(t, s, "Counter", "Calls")}} {
		if _, ok := r.Type(dsEnv{}, c.up, c.t); ok {
			t.Errorf("%s against %s must stay silent", c.up, c.t)
		}
	}
}

func TestDS2Fetch(t *testing.T) {
	s := check(t, dsSource)
	r := fetchFunction{}
	call := "{ (input: RequestInfo | URL, init?: RequestInit): Promise<Response>; (input: string | URL | Request, init?: RequestInit): Promise<Response>; } | undefined"
	for _, name := range []string{"Fetch", "Hook", "Rt"} {
		if v, ok := r.Type(dsEnv{}, call, field(t, s, "Opts", name)); !ok || v.OK != Yes {
			t.Errorf("%s: %+v %v", name, v, ok)
		}
	}
	mcpFetch := "(input: string | URL, init?: RequestInit) => Promise<Response>"
	if v, ok := r.Type(dsEnv{}, mcpFetch, field(t, s, "Opts", "Sender")); !ok || v.OK != Yes {
		t.Errorf("a fetch function type against an interface with Do(*http.Request): %+v %v", v, ok)
	}
	if _, ok := r.Type(dsEnv{}, mcpFetch, field(t, s, "Opts", "Wrong")); ok {
		t.Error("an interface whose Do does not take a request must not match")
	}
	if _, ok := r.Type(dsEnv{}, "(input: number) => Promise<Response>", field(t, s, "Opts", "Sender")); ok {
		t.Error("a function type that is not fetch-shaped must stay silent")
	}
	if _, ok := r.Type(dsEnv{}, "typeof fetch", field(t, s, "Opts", "Fetch")); !ok {
		t.Error("typeof fetch")
	}
	if _, ok := r.Type(dsEnv{}, call, field(t, s, "Opts", "Other")); ok {
		t.Error("an unrelated function must not match")
	}
	if _, ok := r.Type(dsEnv{}, "{ (x: number): Promise<number>; }", field(t, s, "Opts", "Fetch")); ok {
		t.Error("a call signature that does not return Response must not match")
	}
}

func TestDS3OrderedRecord(t *testing.T) {
	s := check(t, dsSource)
	r := orderedRecord{}
	for _, up := range []string{"Record<string, string | null>", "Partial<Record<string, string>>", "{ [k: string]: string }", "Record<string, string> | undefined"} {
		if v, ok := r.Type(dsEnv{}, up, typeOf(t, s, "Ordered")); !ok || v.OK != Yes {
			t.Errorf("%s: %+v %v", up, v, ok)
		}
	}
	if v, ok := r.Type(dsEnv{}, "Record<string, string> | undefined", field(t, s, "Holder", "Sections")); !ok || v.OK != Yes {
		t.Errorf("an optional record held by pointer: %+v %v", v, ok)
	}
	for name, c := range map[string]struct {
		up string
		t  types.Type
	}{
		"no JSON methods":   {"Record<string, string>", typeOf(t, s, "Unordered")},
		"three-field entry": {"Record<string, string>", typeOf(t, s, "Wide")},
		"value disagrees":   {"Record<string, number>", typeOf(t, s, "Ordered")},
		"not a record":      {"string[]", typeOf(t, s, "Ordered")},
		"number key":        {"Record<number, string>", typeOf(t, s, "Ordered")},
	} {
		if _, ok := r.Type(dsEnv{}, c.up, c.t); ok {
			t.Errorf("%s must stay silent", name)
		}
	}
}

func TestDS6Tuple(t *testing.T) {
	s := check(t, dsSource)
	r := fixedTuple{}
	if v, ok := r.Type(dsEnv{}, "[string, string]", field(t, s, "Pair", "P")); !ok || v.OK != Yes {
		t.Fatalf("pair: %+v %v", v, ok)
	}
	if v, ok := r.Type(dsEnv{}, "readonly [first: string, second: string]", field(t, s, "Pair", "P")); !ok || v.OK != Yes {
		t.Fatalf("labelled: %+v %v", v, ok)
	}
	for name, c := range map[string]struct {
		up string
		t  types.Type
	}{
		"length differs":   {"[string, string]", field(t, s, "Pair", "Q")},
		"element differs":  {"[string, string]", field(t, s, "Pair", "R")},
		"slice not array":  {"[string, string]", field(t, s, "Pair", "S")},
		"optional element": {"[string, second?: string]", field(t, s, "Pair", "P")},
		"rest element":     {"[string, ...string[]]", field(t, s, "Pair", "P")},
		"array":            {"string[]", field(t, s, "Pair", "P")},
	} {
		if _, ok := r.Type(dsEnv{}, c.up, c.t); ok {
			t.Errorf("%s must stay silent", name)
		}
	}
}

func TestDS7Bigint(t *testing.T) {
	s := check(t, dsSource)
	r := bigintRule{}
	for _, n := range []string{"A", "B"} {
		if v, ok := r.Type(dsEnv{}, "bigint", field(t, s, "Big", n)); !ok || v.OK != Yes {
			t.Errorf("%s: %+v %v", n, v, ok)
		}
	}
	if _, ok := r.Type(dsEnv{}, "bigint", field(t, s, "Big", "C")); ok {
		t.Error("a 32-bit integer cannot hold a bigint")
	}
	if _, ok := r.Type(dsEnv{}, "number", field(t, s, "Big", "B")); ok {
		t.Error("number is not bigint")
	}
}

func TestDS4InheritedThroughField(t *testing.T) {
	s := check(t, dsSource)
	env := dsEnv{props: map[string][]string{
		"Config": {"apiKey", "maxTokens", "name"},
		"Base":   {"apiKey", "maxTokens"},
		"Other":  {"name"},
	}}
	r := inheritedThroughField{}
	owner := s.Lookup("Config").(*types.TypeName)
	for _, c := range []struct{ prop, want string }{{"apiKey", "APIKey"}, {"maxTokens", "MaxTokens"}} {
		m, ok := r.Member(env, Property{Name: c.prop}, owner)
		if !ok || m.Name() != c.want {
			t.Errorf("%s: %v %v", c.prop, m, ok)
		}
	}
	// name is Config's own property, and Base does not list it: nothing is inherited.
	if m, ok := r.Member(env, Property{Name: "name"}, owner); ok {
		t.Errorf("an own property must not resolve through a field: %v", m)
	}
	// Base lists the property but the upstream owner does not: the field is not a base.
	if m, ok := r.Member(dsEnv{props: map[string][]string{"Base": {"apiKey"}}}, Property{Name: "apiKey"}, owner); ok {
		t.Errorf("the upstream owner must list the property: %v", m)
	}
	if _, ok := r.Member(env, Property{Name: "missing"}, owner); ok {
		t.Error("an unlisted property must stay silent")
	}
}

func TestDS5ErrorBaseMembers(t *testing.T) {
	s := check(t, dsSource)
	r := errorBaseMember{}
	failure := s.Lookup("Failure").(*types.TypeName)
	wrapped := s.Lookup("Wrapped").(*types.TypeName)
	notError := s.Lookup("NotError").(*types.TypeName)
	subFailure := s.Lookup("SubFailure").(*types.TypeName)
	for _, c := range []struct {
		owner *types.TypeName
		prop  string
		want  string
	}{{failure, "message", "Error"}, {failure, "cause", "Unwrap"}, {wrapped, "cause", "Cause"}} {
		m, ok := r.Member(dsEnv{}, Property{Name: c.prop}, c.owner)
		if !ok || m.Name() != c.want {
			t.Errorf("%s.%s: %v %v", c.owner.Name(), c.prop, m, ok)
		}
	}
	for _, c := range []struct {
		owner *types.TypeName
		prop  string
	}{{failure, "stack"}, {failure, "name"}, {notError, "message"}, {notError, "cause"}, {failure, "other"},
		// Unwrap to an embedded base error is Go's spelling of upstream class inheritance (McpAuthRequiredError extends McpHttpError), not Error.cause, which that class never sets.
		{subFailure, "cause"}} {
		if m, ok := r.Member(dsEnv{}, Property{Name: c.prop}, c.owner); ok {
			t.Errorf("%s.%s must stay silent, got %v", c.owner.Name(), c.prop, m)
		}
	}
}

func TestDataShapeRulesAreRegistered(t *testing.T) {
	want := map[string]bool{}
	for _, n := range []string{"DS1", "DS2", "DS3", "DS6", "DS7", "DS4", "DS5"} {
		want["data-shapes/"+n] = true
	}
	for _, r := range Registered() {
		delete(want, r)
	}
	if len(want) != 0 {
		t.Fatalf("not registered: %v", want)
	}
}
