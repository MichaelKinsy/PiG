package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// goTypes type-checks a snippet and returns the named declarations' types.
func goTypes(t *testing.T, src string) map[string]types.Type {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", "package x\n"+src, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := (&types.Config{Importer: importer.Default()}).Check("x", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]types.Type{}
	for _, n := range pkg.Scope().Names() {
		out[n] = pkg.Scope().Lookup(n).Type()
	}
	return out
}

func TestTypeRules(t *testing.T) {
	gt := goTypes(t, `
import (
	"context"
	"iter"
	"net/http"
)
type Str string
type Num int
type Named struct{ A string }
type Other struct{ B int }
type Pair struct {
	Key   string
	Value float64
}
type Triple struct {
	Key   string
	Value float64
	Other string
}
type Sum interface{ isSum() }
type JSONish interface{}
var (
	OP  []Pair
	OT  []Triple
	OPN []struct {
		Key   int
		Value float64
	}
	S   string
	N   float64
	B   bool
	SS  []string
	M   map[string]int
	Fn  func(a string) error
	Any any
	St  Str
	Nm  Named
	Ot  Other
	Sm  Sum
	Obj struct{ A string; Count int }
	Bs  []byte
	MS  map[string]string
	H   http.Header
	CS  <-chan string
	CR  <-chan struct{ Value string; Err error }
	CI  <-chan int
	CE  <-chan struct{}
	SC  chan<- string
	SQ  iter.Seq[string]
	SQ2 iter.Seq2[string, int]
	FB  func(func(string) bool) bool
	FI  func(func(string) int)
	Strs []Str
	Arr2 [2]string
	Nums []int
	SetS map[string]struct{}
	SetN map[int]struct{}
	ES  *EvStream
	EB  *EvBad
)

type EvStream struct{}

func (*EvStream) Events(ctx context.Context) iter.Seq[string] { return nil }

type EvBad struct{}

func (*EvBad) Events(ctx context.Context, n int) iter.Seq[string] { return nil }
func (*EvBad) Seq() iter.Seq2[string, int]                         { return nil }
func (*EvBad) more(context.Context) iter.Seq[string]                { return nil }
func (*EvBad) Many() (iter.Seq[string], error)                     { return nil, nil }
func (*EvBad) Str(s string) iter.Seq[string]                       { return nil }`)
	c := &checker{renames: map[string]string{"Alias": "Other"}, generics: map[string]bool{"T": true}, aliases: aliasTable{"p": {"Lits": new(`"a" | "b"`), "Open": new(`"a" | (string & {})`), "Rec": new(`Record<string, number>`)}}, pkg: "p"}
	cases := []struct {
		up   string
		goT  string
		want tri
	}{
		{"string", "S", yes}, {"string", "N", no}, {"number", "N", yes}, {"number", "S", no}, {"boolean", "B", yes}, {"boolean", "S", no},
		{`"a" | "b"`, "St", yes}, {`"a" | "b"`, "N", no}, {"string | undefined", "S", yes},
		{"string[]", "SS", yes}, {"readonly string[]", "SS", yes}, {"string[]", "SetS", yes}, {"number[]", "SetS", no}, {"string[]", "SetN", no}, {"string[]", "MS", no}, {"string[]", "S", no}, {"number[]", "SS", no},
		{"Record<string, number>", "M", yes}, {"Record<string, string>", "M", no}, {"Record<string, number>", "S", no},
		{"Record<string, number>", "OP", yes}, {"Record<string, string>", "OP", no}, {"Record<string, number>", "OT", no}, {"Record<string, number>", "OPN", no}, {"Record<Keys>", "OP", unknown},
		{"(a: string) => void", "Fn", yes}, {"(a: string, b: number) => void", "Fn", no}, {"() => Promise<void>", "Fn", no},
		{"unknown", "Any", yes}, {"unknown", "S", unknown},
		{"Named", "Nm", yes}, {"Named", "Ot", unknown}, {"Alias", "Ot", yes}, {"T", "Ot", yes},
		{"Lits", "St", yes}, {"Open", "St", yes}, {"Open", "N", no}, {"Rec", "M", yes},
		{"A | B", "Sm", yes}, {"A | B", "Any", unknown},
		{"{ A: string; Count: number }", "Obj", yes}, {"{ A: string; Missing: number }", "Obj", no}, {"{ A: string }", "S", unknown},
		{"Uint8Array", "Bs", yes}, {"Uint8Array", "S", no}, {"Uint8Array<ArrayBufferLike>", "Bs", yes}, {"Buffer<ArrayBuffer>", "Bs", yes},
		{"Uint8Array<ArrayBufferLike>", "S", no},
		{"ProcessEnv", "SS", yes}, {"NodeJS.ProcessEnv", "MS", yes}, {"ProcessEnv", "M", no}, {"ProcessEnv", "S", no},
		{"Headers", "H", yes}, {"Headers", "MS", unknown}, {"Headers", "Nm", unknown}, {"ProcessEnv", "Bs", no}, {"Uint8ArrayLike", "Bs", unknown},
		{`"type Role = \"user\" | \"assistant\";\ntype X = 1;"`, "S", yes}, {`"a\nb"`, "N", no}, {`"a\nb" | Foo`, "S", unknown}, {`"plain"`, "Str", yes},
		{`readonly ["a", "b"]`, "Strs", yes}, {`["a", "b"]`, "Arr2", yes}, {`["a", "b", "c"]`, "Arr2", no}, {`readonly ["a", "b"]`, "Nums", no}, {`readonly ["a", 1]`, "Strs", unknown}, {`[]`, "Strs", unknown},
		{"Promise<string>", "CS", yes}, {"Promise<string>", "CR", yes}, {"Promise<string>", "CI", no}, {"Promise<string>", "CE", no}, {"Promise<void>", "CE", yes}, {"Promise<string>", "SC", no},
		{"Iterable<string>", "SQ", yes}, {"Iterable<number>", "SQ", no}, {"Iterable<string>", "SQ2", unknown}, {"AsyncIterable<string>", "ES", yes}, {"AsyncIterable<number>", "ES", no}, {"Iterable<string>", "EB", unknown}, {"Iterable<string>", "FB", unknown}, {"Iterable<string>", "FI", unknown},
	}
	for _, tc := range cases {
		got := c.agree(tc.up, gt[tc.goT])
		if got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
}

//go:fix inline
func TestSignatureRules(t *testing.T) {
	gt := goTypes(t, `
import "context"
func C(ctx context.Context) {}
func CF(ctx context.Context, force bool) {}
func A(a string, b int) (string, error) { return "", nil }
func V(prefix string, rest ...int) {}
func O(opts ...string) {}
func R(a string) (int, bool) { return 0, false }
func N() {}
func K(data string, columns, rows int, moveCursor ...bool) string { return "" }
func L(cwd, agentDir string) []string { return nil }
func Cat(provider string) []int { return nil }
var _ = A
`)
	c := &checker{generics: map[string]bool{}}
	sig := func(name string) *types.Signature { return gt[name].(*types.Signature) }
	cases := []struct {
		name string
		call callShape
		want tri
		why  string
	}{
		{"A", callShape{Parameters: []param{{Name: "a", Type: "string"}, {Name: "b", Type: "number"}}, Returns: "string"}, yes, ""},
		{"A", callShape{Parameters: []param{{Name: "a", Type: "string"}}, Returns: "string"}, no, "S4"},
		{"A", callShape{Parameters: []param{{Name: "a", Type: "string"}, {Name: "b", Type: "string"}}, Returns: "string"}, no, "T1"},
		{"A", callShape{Parameters: []param{{Name: "a", Type: "string"}, {Name: "b", Type: "number"}}, Returns: "void"}, no, "S5"},
		{"A", callShape{Parameters: []param{{Name: "a", Type: "string"}, {Name: "b", Type: "number"}}, Returns: "Promise<string>"}, yes, ""},
		{"A", callShape{Parameters: []param{{Name: "a", Type: "string"}, {Name: "b", Type: "number"}, {Name: "signal", Type: "AbortSignal"}}, Returns: "string"}, no, "S1"},
		{"V", callShape{Parameters: []param{{Name: "prefix", Type: "string"}, {Name: "rest", Type: "number", Optional: true}}, Returns: "void"}, yes, ""},
		{"V", callShape{Parameters: []param{{Name: "prefix", Type: "string"}, {Name: "rest", Type: "number"}}, Returns: "void"}, no, "S3"},
		{"O", callShape{Parameters: []param{{Name: "opts", Type: "string[]"}}, Returns: "void"}, yes, ""},
		{"R", callShape{Parameters: []param{{Name: "a", Type: "string"}}, Returns: "number | undefined"}, yes, ""},
		{"N", callShape{Returns: "void"}, yes, ""},
		// S1 applies to a signal, not to an options object that also holds one: the object's other members need a Go parameter.
		{"C", callShape{Parameters: []param{{Name: "options", Type: "{ signal: AbortSignal; force?: boolean; }"}}, Returns: "void"}, no, "S4"},
		{"CF", callShape{Parameters: []param{{Name: "signal", Type: "AbortSignal | undefined", Optional: true}, {Name: "force", Type: "boolean"}}, Returns: "void"}, yes, ""},
		{"C", callShape{Parameters: []param{{Name: "signals", Type: "readonly (AbortSignal | undefined)[]"}}, Returns: "void"}, no, "S4"},
		// S8: an options bag spreads over Go positional parameters named after its members (a literal, a Pick, or optional members).
		{"K", callShape{Parameters: []param{{Name: "data", Type: "string"}, {Name: "options", Type: "{ columns?: number; rows?: number; moveCursor?: boolean; }", Optional: true}}, Returns: "string"}, yes, ""},
		{"L", callShape{Parameters: []param{{Name: "options", Type: "{ cwd: string; agentDir: string; }"}}, Returns: "string[]"}, yes, ""},
		{"L", callShape{Parameters: []param{{Name: "options", Type: "{ cwd: string; agentDir: string; extra: string; }"}}, Returns: "string[]"}, no, "S4"},
		{"L", callShape{Parameters: []param{{Name: "options", Type: "{ cwd: string; other: string; }"}}, Returns: "string[]"}, no, "S4"},
		{"L", callShape{Parameters: []param{{Name: "options", Type: "Pick<Policy, \"cwd\" | \"agentDir\">"}}, Returns: "string[]"}, yes, ""},
		// A result union that names the same type twice (Promise<T | undefined> | T | undefined) is T.
		{"R", callShape{Parameters: []param{{Name: "a", Type: "string"}}, Returns: "Promise<number | undefined> | number | undefined"}, yes, ""},
	}
	for _, tc := range cases {
		got := c.signature(tc.call, sig(tc.name))
		if got.ok != tc.want || tc.why != "" && !strings.Contains(got.why, tc.why) {
			t.Errorf("signature %s %+v = %v (%s), want %v %s", tc.name, tc.call, got.ok, got.why, tc.want, tc.why)
		}
	}
}

func TestSignalAndFunctionTypeParsing(t *testing.T) {
	for up, want := range map[string]bool{
		"AbortSignal": true, "AbortSignal | undefined": true, "(AbortSignal | undefined)": true, "AbortSignal | null": true,
		"((e: string, signal?: AbortSignal) => void) | undefined": false, "{ signal: AbortSignal; force?: boolean; }": false,
		"readonly (AbortSignal | undefined)[]": false, "AbortSignal | string": false, "undefined": false,
	} {
		if got := isSignalType(up); got != want {
			t.Errorf("isSignalType(%q) = %v, want %v", up, got, want)
		}
	}
	call, ok := parseFuncType("(currentValue: string, done: (selectedValue?: string, options?: { navigateTo?: string; }) => void) => Component")
	if !ok || len(call.Parameters) != 2 || call.Parameters[1].Name != "done" || call.Returns != "Component" {
		t.Errorf("a nested function type parameter: %+v %v", call, ok)
	}
}

func TestNameRules(t *testing.T) {
	for _, tc := range []struct{ up, goName, want string }{
		{"add", "Add", ruleExact}, {"Add", "Add", ruleExact}, {"uuidv7", "UUIDv7", ruleFolded}, {"resolveHttpProxyUrl", "ResolveHTTPProxyURL", ruleFolded},
		{"CODEMODE_OPTIONS_PREFIX", "CodemodeOptionsPrefix", ruleFolded}, {"KeybindingsManager", "TUIKeybindingsManager", ""}, {"a", "b", ""},
	} {
		if got := nameRule(tc.up, tc.goName); got != tc.want {
			t.Errorf("nameRule(%q, %q) = %q, want %q", tc.up, tc.goName, got, tc.want)
		}
	}
	// NM7 (naming family): the Mcp product prefix is the package, so McpClient is NewClient as well (hand-closed in the interface mapping).
	if ctorRule("McpClient", "NewMcpClient") != ruleCtor || ctorRule("McpClient", "NewClient") != ruleCtor || ctorRule("McpClient", "NewCli") != "" || ctorRule("X", "New") != "" {
		t.Error("constructor rule")
	}
}

func TestParseID(t *testing.T) {
	p := parseID("pkg:coding-agent/core/tools#Foo::property:bar::call:2")
	if p.Pkg != "coding-agent" || p.Entry != "core/tools" || p.Name != "Foo" || p.Member != "bar" || p.Call != "2" || p.IsCLI {
		t.Errorf("%+v", p)
	}
	if q := parseID("pkg:ai/.#Models::construct:0"); q.Construct != "0" || q.Name != "Models" {
		t.Errorf("%+v", q)
	}
	if !parseID("cli:pi-config/--approve").IsCLI || topID("pkg:a/.#X::property:y::call:0") != "pkg:a/.#X" || isUnit("pkg:a/.#X::call:0") {
		t.Error("cli, topID or isUnit")
	}
}

func TestSplitTop(t *testing.T) {
	got := splitTop(`A | (b: C | D) => E | "x | y" | Record<string, F | G>`, "|")
	want := []string{"A", "(b: C | D) => E", `"x | y"`, "Record<string, F | G>"}
	if strings.Join(got, "§") != strings.Join(want, "§") {
		t.Errorf("splitTop = %q", got)
	}
}

func TestAliasBody(t *testing.T) {
	root := t.TempDir()
	src := "export type A = | \"x\" | \"y\";\nexport type B =\n\t{ a: string; b: \"q;\" } | null;\nexport type D = 1;\nexport type C = {\n\t/** the system's home; see (a */\n\thome: string;\n\twarnings: string[];\n};\nexport type E = {\n\thome: string; // it's (here\n\twarnings: string[];\n};\n"
	write(t, filepath.Join(root, ".upstream/current/packages/p/src/a.ts"), src)
	write(t, filepath.Join(root, ".upstream/current/packages/q/src/a.ts"), "export type D = 2;\nexport interface S {\n\ta: string;\n}\n")
	write(t, filepath.Join(root, ".upstream/current/packages/r/src/a.ts"), "export type S = { extensions: string[] };\n")
	tab := tsAliases(root)
	if b := tab.lookup("p", "A"); b == nil || *b != `"x" | "y"` {
		t.Errorf("A = %v", b)
	}
	if b := tab.lookup("p", "B"); b == nil || !strings.HasSuffix(*b, "| null") {
		t.Errorf("B = %v", b)
	}
	if b := tab.lookup("p", "C"); b == nil || !strings.HasSuffix(*b, "warnings: string[];\n}") {
		t.Errorf("an apostrophe or bracket in a comment must not end or extend the body: C = %v", b)
	}
	if b := tab.lookup("p", "E"); b == nil || !strings.HasSuffix(*b, "warnings: string[];\n}") {
		t.Errorf("an apostrophe or bracket in a line comment must not end or extend the body: E = %v", b)
	}
	if b := tab.lookup("q", "S"); b != nil {
		t.Errorf("an interface of the row's package must shadow another package's alias, got %q", *b)
	}
	if b := tab.lookup("r", "S"); b == nil {
		t.Error("the alias stays visible in its own package")
	}
	if b := tab.iface("q", "S"); b == nil || *b != "{\n\ta: string;\n}" {
		t.Errorf("an interface is read from source: S = %v", b)
	}
	write(t, filepath.Join(root, ".upstream/current/packages/s/src/a.ts"), "export type G<T = JsonValue, K extends Array<string> = [], F extends () => void = () => void> = { v: T };\nexport type H<T> = T[];\n")
	if b := tab2(root).lookup("s", "G"); b == nil || *b != "{ v: T }" {
		t.Errorf("a type parameter default must not hide the alias: G = %v", b)
	}
	write(t, filepath.Join(root, ".upstream/current/packages/s/src/b.ts"), "export const MODES = [\"off\", \"on\"] as const;\nexport type Mode = (typeof MODES)[number];\n")
	if b := tab2(root).lookup("s", "Mode"); b == nil || *b != `"off" | "on"` {
		t.Errorf("(typeof MODES)[number] must read the const array: Mode = %v", b)
	}
	write(t, filepath.Join(root, ".upstream/current/packages/s/src/c.ts"), "type Code = \"x\" | \"y\";\ntype T2 = string;\ntype Hidden = \"a\";\n")
	write(t, filepath.Join(root, ".upstream/current/packages/u/src/c.ts"), "type Code = \"z\";\nexport type T2 = number;\ntype Hidden = \"b\";\n")
	if b := tab2(root).lookup("s", "Code"); b == nil || *b != `"x" | "y"` {
		t.Errorf("an unexported alias is visible in its own package: Code = %v", b)
	}
	if b := tab2(root).lookup("s", "T2"); b == nil || *b != "number" {
		t.Errorf("an exported alias wins over an unexported one: T2 = %v", b)
	}
	if b := tab2(root).lookup("v", "Hidden"); b != nil {
		t.Errorf("an unexported alias is not visible in another package: Hidden = %q", *b)
	}
	if b := tab2(root).lookup("s", "H"); b == nil || *b != "T[]" {
		t.Errorf("H = %v", b)
	}
	if tab.lookup("r", "D") != nil || *tab.lookup("p", "D") != "1" {
		t.Error("an alias declared in two packages must resolve only within its own package")
	}
}

func tab2(root string) aliasTable { return tsAliases(root) }

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBaselineFailsOnlyWhenTheGapListGrows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "baseline.tsv")
	write(t, path, "pkg:a/.#X\ta\tnot-exercised\t\npkg:a/.#Y\ta\tno-go-symbol\t\n")
	same := []*decision{{ID: "pkg:a/.#X", Gap: true, Reason: reasonExercise}, {ID: "pkg:a/.#Y", Gap: true, Reason: reasonNoSymbol}, {ID: "pkg:a/.#Z"}}
	if err := checkBaseline(path, same); err != nil {
		t.Errorf("unchanged list must pass: %v", err)
	}
	shrunk := []*decision{{ID: "pkg:a/.#X", Gap: true, Reason: reasonExercise}}
	if err := checkBaseline(path, shrunk); err != nil {
		t.Errorf("a shrinking list must pass: %v", err)
	}
	grown := append(append([]*decision{}, same...), &decision{ID: "pkg:a/.#W", Gap: true, Reason: reasonType, Detail: "boom"})
	if err := checkBaseline(path, grown); err == nil || !strings.Contains(err.Error(), "pkg:a/.#W") {
		t.Errorf("a new gap must fail naming it, got %v", err)
	}
}

func TestSummaryCountsByReasonAndPackage(t *testing.T) {
	ds := []*decision{
		{ID: "1", Pkg: "ai", Gap: true, Reason: reasonType, Disp: "pending"},
		{ID: "2", Pkg: "ai", Gap: true, Reason: reasonType, Disp: "ported"},
		{ID: "3", Pkg: "tui", Gap: true, Reason: reasonNoSymbol, Disp: "deferred"},
		{ID: "4", Pkg: "tui", Disp: "ported"},
	}
	s := summary(ds)
	for _, want := range []string{"total IDs: 4", "NOT-A-GAP: 1", "GAP: 3", "| type-mismatch | 2 | 0 | 2 |", "| no-go-symbol | 0 | 1 | 1 |", "| ported | 1 | 1 |"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary lacks %q:\n%s", want, s)
		}
	}
}

func TestCatalogRepresentationAndIntersectionRules(t *testing.T) {
	gt := goTypes(t, `
type Opts struct{ A string; B int }
func Cat(provider string) []int { return nil }
func CatTwo(provider string, n int) []int { return nil }
func CatScalar(provider string) int { return 0 }
var (
	Msgs []Opts
	Str  string
)
`)
	// V4: a per-provider catalog constant is a Go function from the provider name to the catalog slice, and only that.
	for _, tc := range []struct {
		up, fn string
		want   bool
	}{
		{`ChatModelCatalog<any, "x">`, "Cat", true},
		{`ImageModelCatalog<any, "x">`, "Cat", true},
		{`ClassifierModelCatalog<any, "x">`, "Cat", true},
		{`ModelCatalog<any, "x">`, "Cat", false},
		{`ChatModelCatalog<any, "x">`, "CatTwo", false},
		{`ChatModelCatalog<any, "x">`, "CatScalar", false},
	} {
		if got := keyedCatalog(tc.up, gt[tc.fn].(*types.Signature)); got != tc.want {
			t.Errorf("keyedCatalog(%s, %s) = %v, want %v", tc.up, tc.fn, got, tc.want)
		}
	}
	// T9c: a documented representation applies to its package and to the exact Go type only.
	c := &checker{pkg: "ai", generics: map[string]bool{}, reps: map[string]string{"ai:Msgs": "[]x.Opts"}}
	if v := c.named("Msgs", gt["Msgs"]); v.ok != yes {
		t.Errorf("documented representation: %v %s", v.ok, v.why)
	}
	if v := c.named("Msgs", gt["Str"]); v.ok == yes {
		t.Errorf("a different Go type must not match the representation")
	}
	other := &checker{pkg: "tui", generics: map[string]bool{}, reps: c.reps}
	if v := other.named("Msgs", gt["Msgs"]); v.ok == yes {
		t.Errorf("a representation is scoped to its upstream package")
	}
	// T15: an intersection needs a Go type that satisfies every operand.
	ic := &checker{pkg: "ai", generics: map[string]bool{}, renames: map[string]string{}}
	if v := ic.agree("string & string", gt["Str"]); v.ok != yes {
		t.Errorf("intersection of equal operands: %v %s", v.ok, v.why)
	}
	if v := ic.agree("string & number", gt["Str"]); v.ok == yes {
		t.Errorf("an operand the Go type does not satisfy must refute the intersection")
	}
	// T15p: the operand of a nullable intersection is parenthesised (`(A & { k: V }) | undefined`); the parentheses carry no type, and the
	// operands are still judged: Opts has A as a string.
	if v := ic.agree("(Opts & { readonly A: string }) | undefined", gt["Opts"]); v.ok != yes {
		t.Errorf("a parenthesised nullable intersection: %v %s", v.ok, v.why)
	}
	if v := ic.agree("(Opts & { readonly A: number }) | undefined", gt["Opts"]); v.ok == yes {
		t.Errorf("a parenthesised intersection keeps its operands")
	}
}

// V1 compares the value of a TypeScript string literal, not its source text: Pi writes CURSOR_MARKER as
// "\u001B_pi:c\u0007" and Go as "\x1b_pi:c\a", the same string.
func TestValueRuleDecodesTypeScriptEscapes(t *testing.T) {
	for _, tc := range []struct {
		literal string
		value   string
		want    bool
	}{
		{`"\u001B_pi:c\u0007"`, "\x1b_pi:c\a", true},
		{`'\x1b_pi:c\x07'`, "\x1b_pi:c\a", true},
		{`"a\nb\t\\"`, "a\nb\t\\", true},
		{`"\u{1F600}"`, "😀", true},
		{`"\u001B_pi:c\u0007"`, "\x1b_pi:c\x06", false},
		{`"plain"`, "plain", true},
		{`"plain"`, "other", false},
	} {
		c := types.NewConst(token.NoPos, nil, "C", types.Typ[types.UntypedString], constant.MakeString(tc.value))
		got := valueVerdict(nil, tc.literal, c).ok == yes
		if got != tc.want {
			t.Errorf("valueVerdict(%s, %q) = %v, want %v", tc.literal, tc.value, got, tc.want)
		}
	}
}

// TestObjectLiteralMembers: T12 ignores comments and readonly, finds a member promoted from an embedded struct that encoding/json
// flattens, a method member as a Go method (T12m), and a string-literal member as the JSON discriminator the type marshals (T12d).
func TestObjectLiteralMembers(t *testing.T) {
	gt := goTypes(t, `
type Base struct{ ID string `+"`json:\"id\"`"+` }
type Tagged struct{ Base `+"`json:\"base\"`"+` }
type Draft struct {
	Base
	Count int
}
type Doc struct{ Kind string }
type Opts struct {
	Position string `+"`json:\"position,omitempty\"`"+`
	WithSession func(string) error `+"`json:\"-\"`"+`
	Hidden string `+"`json:\"-\"`"+`
}
func (Doc) Initial() string { return "" }
func (Doc) Migrate(value string, from int) string { return value }
type TaskID int64
type IDC interface{ ~int64 | ~int32 }
type MixC interface{ ~int64 | ~string }
func useC[T IDC, U MixC](T, U) {}
type Hello struct{ Name string }
func (Hello) MarshalJSON() ([]byte, error) { return nil, nil }
func (Hello) Type() string { return "hello" }
var (
	D  Draft
	Tg Tagged
	Dc Doc
	Op Opts
	H  Hello
	A  any
	Ns struct{ Kind struct{ Name string } }
	TI TaskID
	Gs func(id ...string) string
	Gp func(id ...string) *string
	Gw func(id ...int) string
	Gn func(id string) string
	Gl func(id []string) string
	Gr func(id ...string) int
)`)
	c := &checker{generics: map[string]bool{}, renames: map[string]string{}, pkg: "p",
		discriminates: func(tn *types.TypeName, key, lit string) bool {
			return tn.Name() == "Hello" && key == "type" && lit == "hello"
		}}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{"{ /** the id */ readonly id: string; readonly count: number }", "D", yes},
		{"{ id: string; count: number; missing: string }", "D", no},
		{"{ id: string; count: number; readonly [rowBrand]: true }", "D", yes},  // T12k: a unique-symbol brand is no field
		{"{ id: string; count: number; readonly [rowBrand]: string }", "D", no}, // only the boolean tag is a brand
		{"{ id: string; count: number; readonly [rowKey]: true }", "D", no},     // any other computed key is a member
		{"{ id: string; count: number; signal?: AbortSignal }", "D", unknown},   // T12s needs a call with a context
		{"{ id: string }", "Tg", no},                                            // a named JSON field is not flattened
		{"{ kind: string; initial(): string; migrate?(value: string, fromVersion: number): string }", "Dc", yes},
		{"{ kind: string; migrate?(value: string): string }", "Dc", no},
		{"{ kind: string; checkpointWhen?(value: string): boolean }", "Dc", no},
		{"{ position?: string; withSession?: (ctx: string) => Promise<void> }", "Op", yes}, // T12f: a callback is a func field JSON skips
		{"{ position?: string; withSession?: (ctx: number) => Promise<void> }", "Op", no},  // T12f still agrees the function type
		{"{ position?: string; hidden?: string }", "Op", no},                               // a data member JSON skips is not carried
		{`{ type: "hello"; name: string }`, "H", yes},
		{`{ type: "bye"; name: string }`, "H", no},
		{`{ type: "hello" }`, "Dc", no},
		{`{ type: "hello"; name: string } | { type: "hello" }`, "H", yes}, // T10d with the discriminator the type writes
		{`{ type: "hello"; name: string } | { type: "bye" }`, "H", no},
		{"{ command: string; timeout?: number }", "A", no},                    // the empty interface has none of the members
		{"{ kind: string } & Record<string, unknown>", "Dc", yes},             // T15r
		{"number & { readonly [idBrand]: { readonly kind: K } }", "TI", yes},  // T15b
		{"number & { [k: string]: number }", "TI", no},                        // an index signature is not a brand
		{"number & { readonly [idBrand]: { readonly kind: K } }", "IDC", yes}, // T2c
		{"number", "MixC", no},
		{"{ kind: string } & Record<string, number>", "Dc", no},
		{"string & Record<string, unknown>", "A", no},                 // only a struct has the slot
		{`Omit<{ id: string; count: number }, "id">`, "Dc", no},       // T16: the kept member count is still missing
		{`Omit<{ id: string; kind: string }, "id" | "x">`, "Dc", yes}, // the omitted id is not judged
		{`Pick<{ id: string; kind: string; z: number }, "kind">`, "Dc", yes},
		{`Omit<Omit<{ id: string; kind: string; count: number }, "id">, "count">`, "Dc", yes}, // T16: the outer Omit still removes count from the inner Omit's members
		{`Omit<Omit<{ id: string; kind: string; count: number }, "id">, "kind">`, "Dc", no},   // count is kept by both, and Dc has none
		{`Omit<{ kind: { id: string; name: string } }, "id">`, "Ns", no},                      // the selection does not reach member types
		{"{ (): string, (id: string): string }", "Gs", yes},                                   // T12o: the overload set of getModel is one variadic Go function
		{"{ (): string; (id: string): string | undefined }", "Gp", yes},                       // T12o: a result that may be undefined is a Go pointer
		{"{ (): string, (id: string): string }", "Gw", no},                                    // T12o: the variadic element must agree with the overload's parameter
		{"{ (): string, (id: string): string }", "Gr", no},                                    // T12o: and the result with each overload's result
		{"{ (): string, (id: string): string }", "Gl", unknown},                               // T12o: a slice last parameter is not a variadic one
		{"{ (): string, (id: string): string }", "Gn", unknown},                               // T12o: a Go function that is not variadic cannot hold the set
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
	withCtx := *c
	withCtx.ctxInScope = true
	if got := withCtx.agree("{ id: string; count: number; signal?: AbortSignal }", gt["D"]); got.ok != yes {
		t.Errorf("T12s inside a call with a context: %v %s", got.ok, got.why)
	}
	sigs := goTypes(t, `
import "context"
type In struct{ ID string }
func WithCtx(ctx context.Context, in In) {}
func NoCtx(in In) {}`)
	call := callShape{Parameters: []param{{Name: "input", Type: "{ id: string; signal?: AbortSignal }"}}, Returns: "void"}
	if got := c.signature(call, sigs["WithCtx"].(*types.Signature)); got.ok != yes {
		t.Errorf("a Go call with a context carries the AbortSignal member: %v %s", got.ok, got.why)
	}
	if got := c.signature(call, sigs["NoCtx"].(*types.Signature)); got.ok == yes {
		t.Errorf("a Go call without a context does not carry the AbortSignal member")
	}
	for _, tc := range []struct{ part, name, typ string }{
		{"migrate?(value: JsonObject, fromVersion: number): T", "migrate", "(value: JsonObject, fromVersion: number) => T"},
		{"initial(): T", "initial", "() => T"},
		{"onData: (chunk: Uint8Array) => void", "onData", "(chunk: Uint8Array) => void"},
		{"count?: number", "count", "number"},
	} {
		if n, typ, ok := objectMember(tc.part); !ok || n != tc.name || typ != tc.typ {
			t.Errorf("objectMember(%q) = %q, %q, %v", tc.part, n, typ, ok)
		}
	}
}

// TestIntersectionOperandIsInherited: T15e satisfies a named intersection operand by an embedded Go type that stands for it, or by a
// field or method for every property of the upstream interface (LEAD-ANSWERS-ledger #1). A struct that lacks one of them, or hides
// it from JSON with `json:"-"`, does not satisfy the operand.
func TestIntersectionOperandIsInherited(t *testing.T) {
	gt := goTypes(t, `
type Base struct{ ID string `+"`json:\"id\"`"+` }
type Opaque struct{ X int }
type Embeds struct {
	Base
	Count int
}
type EmbedsOpaque struct {
	Opaque
	Count int
}
type Spreads struct {
	ID    string
	Count int
}
type Accessor struct{ Count int }
func (Accessor) ID() string { return "" }
type Missing struct{ Count int }
type Hidden struct {
	ID    string `+"`json:\"-\"`"+`
	Count int
}
type HiddenFunc struct {
	ID    func() string `+"`json:\"-\"`"+`
	Count int
}
var (
	E  Embeds
	EO EmbedsOpaque
	S  Spreads
	A  Accessor
	M  Missing
	H  Hidden
	HF HiddenFunc
	NN int
)`)
	c := &checker{generics: map[string]bool{}, renames: map[string]string{}, pkg: "p",
		propNames: func(name string) []string {
			if name == "Base" {
				return []string{"id"}
			}
			return nil
		}}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{"Base & { count: number }", "E", yes},
		{"Opaque & { count: number }", "EO", yes}, // only the embedded type stands for Opaque: its properties are unknown
		{"Base & { count: number }", "S", yes},
		{"Base & { count: number }", "A", yes},
		{"Base & { count: number }", "M", no},
		{"Base & { count: number }", "H", no},
		{"Base & { count: number }", "HF", yes}, // a callback field is not hidden from JSON by json:"-": it cannot be marshalled
		// T15p: a parenthesized intersection in a union with undefined is the intersection
		{"(Base & { count: number }) | undefined", "E", yes}, {"(Base & { count: number }) | undefined", "M", no},
		{"((number | undefined))", "NN", yes}, // nested parentheses around a union
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// TestTypeBoxStaticTypes: TB1 rewrites `Static<typeof S>` to the type TypeBox gives the schema; a nested schema with an exported
// static type keeps its name, and an expression outside the supported constructors leaves the body unchanged.
func TestTypeBoxStaticTypes(t *testing.T) {
	src := `const IdSchema = Type.String({ minLength: 1 });
const StrictObject = <const T extends Parameters<typeof Type.Object>[0]>(properties: T) =>
	Type.Object(properties, { additionalProperties: false });
const ServerIdSchema = Type.String({ pattern: "^[0-9a-f]{8}$" });
export type ServerId = Static<typeof ServerIdSchema>;
const HelloSchema = StrictObject({
	type: Type.Literal("hello"),
	serverId: ServerIdSchema,
	code: IdSchema,
	n: Type.Integer({ minimum: 0 }),
	tags: Type.Optional(Type.Array(Type.String({ description: "a, b (c" }))),
	mode: Type.Union([Type.Literal("a"), Type.Literal("b")]),
	extra: Type.Record(Type.String(), Type.Number()),
	raw: Type.Unsafe<JsonValue>(Type.Unknown()),
});
export type Hello = Static<typeof HelloSchema>;
const OddSchema = Type.Transform(Type.String());
export type Odd = Static<typeof OddSchema>;
`
	schemas := typeBoxSchemas{}
	readTypeBoxSchemas(src, schemas)
	body := func(s string) *string { return &s }
	aliases := map[string]*string{"ServerId": body("Static<typeof ServerIdSchema>"), "Hello": body("Static<typeof HelloSchema>"), "Odd": body("Static<typeof OddSchema>")}
	staticTypes(aliases, schemas)
	for name, want := range map[string]string{
		"ServerId": "string",
		"Hello":    `{ type: "hello"; serverId: ServerId; code: string; n: number; tags?: Array<string>; mode: "a" | "b"; extra: Record<string, number>; raw: JsonValue }`,
		"Odd":      "Static<typeof OddSchema>",
	} {
		if got := *aliases[name]; got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// TestIntersectionOperandIsInherited: T15e satisfies a named intersection operand by an embedded Go type that stands for it, or by a
// field or method for every property of the upstream interface (LEAD-ANSWERS-ledger #1). A struct that lacks one of them, or hides
// it from JSON with `json:"-"`, does not satisfy the operand.
// TestTypeBoxDuplicateSchemaIsNotGuessed: a schema constant declared twice in the scanned sources leaves `Static<typeof S>` unchanged,
// because the rule cannot tell which declaration the alias reads.
func TestTypeBoxDuplicateSchemaIsNotGuessed(t *testing.T) {
	schemas := typeBoxSchemas{}
	readTypeBoxSchemas("const DupSchema = Type.String();\n", schemas)
	readTypeBoxSchemas("const DupSchema = Type.Number();\n", schemas)
	body := "Static<typeof DupSchema>"
	aliases := map[string]*string{"Dup": &body}
	staticTypes(aliases, schemas)
	if got := *aliases["Dup"]; got != "Static<typeof DupSchema>" {
		t.Fatalf("Dup = %q, want the body unchanged", got)
	}
}

// TestUnionMemberMethodIsAGoMethod: T10d satisfies a union member's method member by a method of the Go struct; a literal member still
// needs a field or a written discriminator.
func TestUnionMemberMethodIsAGoMethod(t *testing.T) {
	gt := goTypes(t, `
type Job struct{ Kind string }
func (Job) Run() string { return "" }
type Plain struct{ Kind string }
var (
	J Job
	P Plain
)`)
	c := &checker{generics: map[string]bool{}, renames: map[string]string{}, pkg: "p"}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{`{ kind: "a"; run(): string } | { kind: "b" }`, "J", yes},
		{`{ kind: "a"; run(): string } | { kind: "b" }`, "P", no},
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// TestStructuralTyping: T9t accepts a Go type of another name for an upstream interface when it has a field or method of an agreeing
// type for every property; a missing member or a basic type refutes; an AbortSignal member is left to its own row.
func TestStructuralTyping(t *testing.T) {
	gt := goTypes(t, `
type Full struct {
	ID   string
	Cost float64
}
func (Full) Name() string { return "" }
type Short struct{ ID string }
type WrongType struct {
	ID   int
	Cost float64
	Name string
}
type WrongMethod struct {
	ID   string
	Cost float64
}
func (WrongMethod) Name() int { return 0 }
type ParamMethod struct {
	ID   string
	Cost float64
}
func (ParamMethod) Name(prefix string) string { return prefix }
type Model struct{ ID string }
type Opts struct {
	Model
	X string
}
var (
	F  Full
	Sh Short
	W  WrongType
	WM WrongMethod
	PM ParamMethod
	S  string
	O  Opts
)`)
	props := map[string][]propShape{"Model": {{Name: "id", Type: "string"}, {Name: "cost", Type: "number", Optional: true}, {Name: "name", Type: "string"},
		{Name: "signal", Type: "AbortSignal", Optional: true}, {Name: "[Symbol.asyncIterator]", Type: "() => AsyncIterator<string>"}}}
	c := &checker{generics: map[string]bool{}, renames: map[string]string{}, pkg: "p", props: func(n string) []propShape { return props[n] }, ctxInScope: true}
	noCtx := *c
	noCtx.ctxInScope = false
	if got := noCtx.agree("Model", gt["F"]); got.ok != unknown {
		t.Errorf("an AbortSignal member outside a call with a context stays undecided, got %v", got.ok)
	}
	// T16: Pick leaves the AbortSignal member out, so it needs no context in scope (agent.ToolCallHooks = Pick<AgentLoopConfig, "beforeToolCall" | "afterToolCall">).
	if got := noCtx.agree(`Pick<Model, "id" | "name">`, gt["F"]); got.ok != yes {
		t.Errorf("a Pick that leaves out the AbortSignal member is judged by its picked members, got %v (%s)", got.ok, got.why)
	}
	if got := noCtx.agree(`Pick<Model, "signal">`, gt["F"]); got.ok != unknown {
		t.Errorf("a Pick that keeps the AbortSignal member outside a call with a context stays undecided, got %v", got.ok)
	}
	if got := noCtx.agree(`Omit<Model, "signal">`, gt["F"]); got.ok != yes {
		t.Errorf("an Omit of the AbortSignal member is judged by the other members, got %v (%s)", got.ok, got.why)
	}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{"Model", "F", yes}, {"Model", "Sh", no}, {"Model", "W", no}, {"Model", "WM", no}, {"Model", "S", no}, {"Other", "F", unknown},
		{"Model", "PM", unknown},                    // a method that takes parameters is not an accessor for the data property name
		{`Omit<Model, "cost" | "name">`, "Sh", yes}, // T16 applies to the members T9t compares
		{"Model", "PM", unknown},                    // a method that takes parameters is not an accessor for the data property name
		{`Pick<Model, "id">`, "S", yes},             // T9p: a one-member Pick is the member's value
		{`Pick<Model, "cost">`, "S", no},            // the member's type must agree with the Go basic type
		{`Pick<Model, "id" | "name">`, "S", no},     // two members need a type of their own
		{"Model & { x: string }", "O", yes},         // T15e: the embedded Go Model stands for the operand by name before T9t compares members
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
	// T9m: a method member without a call row (theme.ts:434 `getBashModeBorderColor(): (str: string) => string`) is the Go method
	// itself, not its result; a data member of function type is still a parameterless accessor's result.
	gtm := goTypes(t, `
type Painter struct{}
func (Painter) Border() func(string) string { return nil }
func (Painter) Fg() func(string) string { return nil }
type BadPainter struct{}
func (BadPainter) Border() func(int) string { return nil }
func (BadPainter) Fg() func(string) string { return nil }
var (
	P  Painter
	BP BadPainter
)`)
	painter := []propShape{{Name: "border", Type: "() => (str: string) => string"}, {Name: "fg", Type: "(str: string) => string"}}
	pc := &checker{generics: map[string]bool{}, renames: map[string]string{}, pkg: "p", props: func(string) []propShape { return painter }, ctxInScope: true}
	if got := pc.agree("Brush", gtm["P"]); got.ok != yes {
		t.Errorf("a method member is the Go method of the same signature, got %v (%s)", got.ok, got.why)
	}
	if got := pc.agree("Brush", gtm["BP"]); got.ok == yes {
		t.Errorf("a method whose returned function takes another parameter type must not agree, got %v", got.ok)
	}
	// A Go form of its own: another type with every member stays undecided unless reviewed; Partial<X> needs nilable fields.
	gt2 := goTypes(t, `
type Caps struct {
	Images    string
	TrueColor bool
}
type CapOverrides struct {
	Images    *string
	TrueColor *bool
}
type CapCopy struct {
	Images    string
	TrueColor bool
}
var (
	C  Caps
	CO CapOverrides
	CC CapCopy
)`)
	capProps := []propShape{{Name: "images", Type: "string"}, {Name: "trueColor", Type: "boolean"}}
	own := gt2["C"].(*types.Named).Obj()
	oc := &checker{generics: map[string]bool{}, renames: map[string]string{}, pkg: "tui", props: func(string) []propShape { return capProps },
		resolve: func(n string) *types.TypeName {
			if n == "Caps" {
				return own
			}
			return nil
		}}
	for _, tc := range []struct {
		up, goT string
		forms   map[string][]string
		want    tri
	}{
		{"Caps", "CC", nil, unknown},
		{"Caps", "CC", map[string][]string{"tui:Caps": {"x.CapCopy"}}, yes},
		{"Caps", "CC", map[string][]string{"agent:Caps": {"x.CapCopy"}}, unknown},
		{"Partial<Caps>", "CO", nil, yes},
		{`Omit<Caps, "images">`, "CC", nil, yes}, // Omit<X> is a type of its own, so another Go type with its members stands for it
		{"Partial<Caps>", "CC", nil, unknown},
		{"Partial<Caps>", "CC", map[string][]string{"tui:Caps": {"x.CapCopy"}}, yes},
	} {
		oc.forms = tc.forms
		if got := oc.agree(tc.up, gt2[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) forms %v = %v (%s), want %v", tc.up, tc.goT, tc.forms, got.ok, got.why, tc.want)
		}
	}
	deep := *c
	deep.depth = 2
	if got := deep.agree("Model", gt["F"]); got.ok != unknown {
		t.Errorf("T9t must not recurse past depth 2, got %v", got.ok)
	}
}

// TestIndexedAccessAndKeyof: T17 reads keyof X, X["p"] and X[K] from the properties of an upstream interface.
func TestIndexedAccessAndKeyof(t *testing.T) {
	gt := goTypes(t, `
type Kind string
const (
	KindChat  Kind = "chat"
	KindImage Kind = "image"
)
type Any interface{ isModel() }
type CallPart struct{ ID string }
type TextPart struct{ Text string }
var (
	K  Kind
	A  Any
	S  string
	CP CallPart
	N  int
)`)
	props := map[string][]propShape{"ModelTypeMap": {{Name: "chat", Type: "Model"}, {Name: "image", Type: "ImageModel"}}, "Rec": {{Name: "status", Type: "string"}, {Name: "tags", Type: "string[]"}},
		"Msg": {{Name: "content", Type: "(TextPart | CallPart)[]"}, {Name: "one", Type: "TextPart | CallPart"}}, "TextPart": {{Name: "type", Type: `"text"`}}, "CallPart": {{Name: "type", Type: `"call"`}, {Name: "id", Type: "string"}},
		"Spaced": {{Name: "type", Type: ` "s" `}}}
	mt, other := "keyof ModelTypeMap", "string"
	c := &checker{generics: map[string]bool{"TType": true}, renames: map[string]string{}, pkg: "p", props: func(n string) []propShape { return props[n] },
		aliases: aliasTable{"p": {"ModelType": &mt, "Other": &other}}}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{"keyof ModelTypeMap", "K", yes}, {"keyof ModelTypeMap", "N", no},
		{"ModelTypeMap[TType]", "A", yes}, {"ModelTypeMap[keyof ModelTypeMap]", "A", yes}, {"ModelTypeMap[TType]", "S", unknown},
		{`Rec["status"]`, "S", yes}, {`Rec["status"]`, "N", no}, {`Rec["missing"]`, "S", no},
		{`Rec["tags"][number]`, "S", yes}, {`Rec["tags"][number]`, "N", no}, {`Rec["status"][number]`, "S", unknown}, {`Rec[TType][number]`, "S", unknown}, {`Rec["tag"][number]`, "S", unknown},
		{"Other[TType]", "A", unknown}, {"ModelTypeMap[U]", "A", unknown},
		{`Extract<ModelTypeMap[TType], { kind: "chat" }>`, "A", yes}, {`Exclude<ModelTypeMap[TType], { kind: "chat" }>`, "A", yes},
		{`Extract<ModelTypeMap[TType], { kind: "chat" }>`, "S", unknown}, {`Extract<ModelTypeMap[TType]>`, "A", unknown},
		{`Extract<Msg["content"][number], { type: "call" }>`, "CP", yes}, {`Extract<Msg["content"][number], { type: "text" }>`, "CP", no},
		{`Exclude<Msg["content"][number], { type: "text" }>`, "CP", yes}, {`Extract<Msg["one"], { readonly type: "call" }>`, "CP", yes},
		{`Extract<TextPart | CallPart, { type: "call" }>`, "CP", yes}, {`Extract<Msg["content"][number], { type: "none" }>`, "CP", unknown},
		{`Extract<Msg["content"][number], { type: string }>`, "CP", unknown}, {`Extract<Msg["status"][number], { type: "call" }>`, "CP", unknown},
		{`Extract<Msg["one"][number], { type: "call" }>`, "CP", unknown}, {`Extract<TextPart | Unknown, { type: "call" }>`, "CP", unknown},
		{`Extract<TextPart | CallPart, "call">`, "CP", unknown}, {`Extract<TextPart | CallPart, >`, "CP", unknown},
		{"ModelTypeMap[ModelType]", "A", yes}, {"ModelTypeMap[Other]", "A", unknown},
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
	if sel, ok := c.selectMembers("TextPart | CallPart", "{ type: string }", false); ok {
		t.Errorf("a pattern of a non-literal type is not evaluated, got %q", sel)
	}
	if sel, ok := c.selectMembers("CallPart | Unknown", `{ type: "call" }`, false); ok {
		t.Errorf("a member without recorded properties is not evaluated, got %q", sel)
	}
	if sel, ok := (&checker{pkg: "p"}).selectMembers(`Msg["one"]`, `{ type: "call" }`, true); ok {
		t.Errorf("without recorded properties nothing is selected, got %q", sel)
	}
	writes := "| { readonly type: \"a\"; readonly value: string }\n| { readonly type: \"b\" }\n| { readonly type: \"c\"; x: number } | CallPart"
	c.aliases["p"]["Write"] = &writes
	for _, tc := range []struct {
		from, pattern string
		keep          bool
		want          string
	}{
		{"Write", `{ readonly type: "a" | "c" }`, true, `{ type: "a"; value: string } | { type: "c"; x: number }`},
		{"Write", `{ type: "b" | "call" }`, true, `{ type: "b" } | CallPart`},
		{"Write", `{ type: "a" | "b" | "c" }`, false, "CallPart"},
		{"Write", `{ type: "a" | string }`, true, ""},
		{"Missing", `{ type: "a" }`, true, ""},
		{"Spaced | TextPart", `{ type: "s" }`, true, "Spaced"},
	} {
		sel, ok := c.selectMembers(tc.from, tc.pattern, tc.keep)
		if sel != tc.want || ok != (tc.want != "") {
			t.Errorf("selectMembers(%s, %s, %v) = %q, %v; want %q", tc.from, tc.pattern, tc.keep, sel, ok, tc.want)
		}
	}
	for re, in := range map[*regexp.Regexp]string{genericRe: "Extract<\n\tWrite,\n\t{ type: \"a\" }\n>", arrayRe: "(\n\t| A\n\t| B\n)[]",
		pickRe: "Pick<\n\tX,\n\t\"a\"\n>", mapLiteralRe: "{\n\t[key: string]: {\n\t\ta: X;\n\t};\n}"} {
		if !re.MatchString(in) {
			t.Errorf("%s does not match a type written over several lines: %q", re, in)
		}
	}
}

// TestDistinctiveIgnoresContextAndError: N5 matches a function by shape only when its signature is distinctive. A context.Context
// parameter and an error result are conventions the call rules map away, so func(ctx, string) (string, error) is the shape of every
// (string) => string and must not stand for one (keyText was bound to an unrelated toolCWD).
func TestDistinctiveIgnoresContextAndError(t *testing.T) {
	gt := goTypes(t, `import "context"
type Model struct{ ID string }
var (
	CtxString   func(ctx context.Context, fallback string) (string, error)
	CtxNothing  func(ctx context.Context) error
	ModelLookup func(ctx context.Context, provider string, id string) (*Model, error)
	Plain       func(a, b string) *Model
)`)
	for name, want := range map[string]bool{"CtxString": false, "CtxNothing": false, "ModelLookup": true, "Plain": true} {
		if got := distinctive(gt[name].(*types.Signature)); got != want {
			t.Errorf("distinctive(%s) = %v, want %v", name, got, want)
		}
	}
}

// TestConstArrayElements: (typeof NAME)[number] reads the string literals of an `export const NAME = [...] as const` array, and an
// element that names a string const declared once (T1c).
func TestConstArrayElements(t *testing.T) {
	consts := map[string]string{}
	readConstArrays(`export const MODES = ["off", "streaming",
	"idle",] as const;
export const LOOSE = ["a", "b"];
export const MIXED = ["a", OTHER] as const;
export const TWICE = ["a"] as const;
export const TWICE = ["b"] as const;
export const LATEST = "2025-11-25";
export const VERSIONS = [LATEST, "2025-06-18"] as const;
export const WIDE: string = "w";
export const WIDENED = [WIDE] as const;
const DUP = "a";
function f() { const DUP = "b"; }
export const DUPS = [DUP] as const;
export const NUM = 3;
export const NUMS = [NUM] as const;
`, consts)
	body := func(s string) *string { return &s }
	aliases := map[string]*string{"Mode": body("(typeof MODES)[number]"), "Loose": body("(typeof LOOSE)[number]"), "Mixed": body("(typeof MIXED)[number]"), "Twice": body("(typeof TWICE)[number]"), "Gone": body("(typeof GONE)[number]"), "Nil": nil,
		"Version": body("(typeof VERSIONS)[number]"), "Widened": body("(typeof WIDENED)[number]"), "Dups": body("(typeof DUPS)[number]"), "Nums": body("(typeof NUMS)[number]")}
	constArrayElements(aliases, consts)
	want := map[string]string{"Mode": `"off" | "streaming" | "idle"`, "Loose": "(typeof LOOSE)[number]", "Mixed": "(typeof MIXED)[number]", "Twice": "(typeof TWICE)[number]", "Gone": "(typeof GONE)[number]",
		// a const name element reads its literal (mcp protocol/types.ts:4,9), unless an annotation widens it, it is declared twice,
		// or it is not a string
		"Version": `"2025-11-25" | "2025-06-18"`, "Widened": "(typeof WIDENED)[number]", "Dups": "(typeof DUPS)[number]", "Nums": "(typeof NUMS)[number]"}
	for name, w := range want {
		if got := *aliases[name]; got != w {
			t.Errorf("%s = %q, want %q", name, got, w)
		}
	}
	if aliases["Nil"] != nil {
		t.Error("a nil alias must stay nil")
	}
}

// TestListOf: a slice or array of the element type is a value list; another element type or nil is not.
func TestListOf(t *testing.T) {
	s, i := types.Typ[types.String], types.Typ[types.Int]
	for _, tc := range []struct {
		t    types.Type
		want bool
	}{{types.NewSlice(s), true}, {types.NewArray(s, 2), true}, {types.NewArray(i, 2), false}, {types.NewSlice(i), false}, {s, false}, {nil, false}} {
		if got := listOf(tc.t, s); got != tc.want {
			t.Errorf("listOf(%v) = %v, want %v", tc.t, got, tc.want)
		}
	}
}

// TestLiteralUtility: T18 evaluates Exclude/Extract over string-literal unions, reading an alias for the first operand.
func TestLiteralUtility(t *testing.T) {
	lv := `"minimal" | "low" | "xhigh" | "max"`
	c := &checker{pkg: "ai", aliases: aliasTable{"ai": {"Level": &lv, "Obj": nil}}}
	for _, tc := range []struct {
		up, want string
		ok       bool
	}{
		{`Exclude<Level, "xhigh" | "max">`, `"minimal" | "low"`, true},
		{`Extract<Level, "low" | "max">`, `"low" | "max"`, true},
		{`Exclude<"a" | "b", "a">`, `"b"`, true},
		{`Exclude<"a", "a">`, "never", true},
		{`Exclude<Obj, "a">`, "", false},
		{`Exclude<Missing, "a">`, "", false},
		{`Extract<Level, { type: "x" }>`, "", false},
		{`Omit<Level, "low">`, "", false},
		{`Exclude<Level>`, "", false},
	} {
		got, ok := c.literalUtility(tc.up)
		if got != tc.want || ok != tc.ok {
			t.Errorf("literalUtility(%q) = %q, %v; want %q, %v", tc.up, got, ok, tc.want, tc.ok)
		}
	}
	// The result is judged by the other rules: a Go bool is not a string-literal union, and an excluded member never is.
	if v := c.agree(`Exclude<"a" | "b", "a">`, types.Typ[types.Bool]); v.ok != no {
		t.Errorf("Exclude over string literals against bool = %v (%s), want no", v.ok, v.why)
	}
	if v := c.agree(`Exclude<"a", "a">`, types.Typ[types.Bool]); v.ok == yes {
		t.Errorf("never against bool = %v, want not yes", v.ok)
	}
}

// TestSettledSibling: T3s accepts a chan struct{} accessor for Promise<T> when another parameterless method of the receiver returns
// T alone; a method with parameters, two results or an unexported method does not count.
func TestSettledSibling(t *testing.T) {
	gt := goTypes(t, `
type Watch interface {
	Closed() <-chan struct{}
	End() string
}
type Stopper interface {
	Closed() <-chan struct{}
	Stop() (string, error)
	At(i int) string
}
type Self interface {
	Closed() <-chan struct{}
}
type impl struct{}
func (*impl) Closed() <-chan struct{} { return nil }
func (*impl) Err() error { return nil }
func (*impl) end() string { return "" }
type Impl = impl
`)
	closed := types.NewChan(types.RecvOnly, types.NewStruct(nil, nil))
	for _, tc := range []struct {
		recv, up string
		want     tri
	}{
		{"Watch", "Promise<string>", yes}, {"Watch", "Promise<number>", no}, {"Stopper", "Promise<string>", no}, {"Self", "Promise<string>", no},
		{"Impl", "Promise<Error | undefined>", yes}, {"Impl", "Promise<string>", no}, {"", "Promise<string>", no},
	} {
		c := &checker{renames: map[string]string{}, pkg: "p"}
		if tc.recv != "" {
			c.recv = gt[tc.recv]
		}
		if got := c.agree(tc.up, closed); got.ok != tc.want {
			t.Errorf("%s: agree(%q) = %v (%s), want %v", tc.recv, tc.up, got.ok, got.why, tc.want)
		}
	}
}

// TestStaticOfSchemaParameter: TB2 accepts json.RawMessage or the empty interface for the value of a schema type parameter (Static<TParams>,
// StaticType<..., TParameters>); a concrete schema or another Go type is left to the other rules.
func TestStaticOfSchemaParameter(t *testing.T) {
	gt := goTypes(t, `
import "encoding/json"

var (
	R json.RawMessage
	S string
	A any
)`)
	c := &checker{generics: map[string]bool{"TParams": true}, renames: map[string]string{}, pkg: "p"}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{"Static<TParams>", "R", yes}, {`StaticType<[], "Encode", {}, {}, TParams>`, "R", yes},
		{"Static<TParams>", "A", yes}, {"Static<Concrete>", "A", unknown},
		{"Static<TParams>", "S", unknown}, {"Static<Concrete>", "R", unknown}, {`StaticType<TParams, "Encode", {}, {}, Concrete>`, "R", unknown},
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// TestFunctionAliasAgainstGoFunc: T9b judges a function-typed alias body like any other: a Go function type of the same shape agrees,
// one of another arity is refuted, and a Go type the function rules cannot judge keeps the alias name undecided.
func TestFunctionAliasAgainstGoFunc(t *testing.T) {
	gt := goTypes(t, `
type Update func(partial string)
type Big interface {
	A()
	B()
}

var (
	BI Big
	U  Update
	W func(n int)
	S string
)`)
	cb := "(partialResult: string) => void"
	c := &checker{renames: map[string]string{}, pkg: "p", aliases: aliasTable{"p": {"UpdateCallback": &cb}}}
	for _, tc := range []struct {
		goT  string
		want tri
	}{{"U", yes}, {"W", no}, {"S", unknown}, {"BI", unknown}} {
		if got := c.agree("UpdateCallback", gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(UpdateCallback, %s) = %v (%s), want %v", tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// TestCodecUnion: T10j accepts an untagged union of JSON kinds against a Go struct with its own JSON codec that holds a field for
// every member; without the codec, without a field for a kind, or with two object members, the rule stays silent.
func TestCodecUnion(t *testing.T) {
	gt := goTypes(t, `
type Inner struct{ A string }

type Deferred struct {
	Object  bool
	Enabled bool
	Window  string
	Extra   Inner
}

func (Deferred) MarshalJSON() ([]byte, error) { return nil, nil }
func (*Deferred) UnmarshalJSON([]byte) error  { return nil }

type Selection struct {
	Names  []string
	Add    []string
	Remove []string
}

func (Selection) MarshalJSON() ([]byte, error) { return nil, nil }
func (*Selection) UnmarshalJSON([]byte) error  { return nil }

type NoCodec struct {
	Enabled bool
	Window  string
}

type MarshalOnly struct {
	Enabled bool
	Window  string
}

func (MarshalOnly) MarshalJSON() ([]byte, error) { return nil, nil }

type UnmarshalOnly struct {
	Enabled bool
	Window  string
}

func (*UnmarshalOnly) UnmarshalJSON([]byte) error { return nil }

type ID struct {
	str string
	num float64
}

type KeyValue struct {
	Key  string
	Keys []string
}

func (KeyValue) MarshalJSON() ([]byte, error) { return nil, nil }
func (*KeyValue) UnmarshalJSON([]byte) error  { return nil }

func (ID) MarshalJSON() ([]byte, error) { return nil, nil }
func (*ID) UnmarshalJSON([]byte) error  { return nil }

var (
	D  Deferred
	SL Selection
	NC NoCodec
	MO MarshalOnly
	UO UnmarshalOnly
	I  ID
	KV KeyValue
)`)
	// T10j: tui keybindings.ts KeybindingsConfig is Record<string, KeyId | KeyId[] | undefined>; KeyId is an alias, which stands for its body.
	keyID := "string"
	c := &checker{renames: map[string]string{}, pkg: "p", aliases: aliasTable{"p": {"KeyId": &keyID}}}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{`boolean | { window?: "15m" | "1h" }`, "D", yes},
		{`string[] | { add?: string[]; remove?: string[] }`, "SL", yes},
		{"string | number", "I", yes},
		{`boolean | { window?: "15m" | "1h" }`, "NC", unknown},
		{`boolean | { window?: "15m" | "1h" }`, "MO", unknown},
		{`number | { window?: "15m" | "1h" }`, "D", unknown},
		{`boolean | { other?: string }`, "D", unknown},
		{`boolean | { window?: string } | { enabled?: boolean }`, "D", unknown},
		{"boolean | Other", "D", unknown},
		{`boolean | { window?: "15m" | "1h" }`, "UO", unknown},
		{"boolean | Inner", "D", unknown},
		{"KeyId | KeyId[]", "KV", yes},
		{"KeyId | KeyId[] | undefined", "KV", yes},
		{"KeyId | KeyId[]", "NC", unknown},
		{"KeyId | KeyId[]", "SL", unknown},
		{"Unknown | KeyId[]", "KV", unknown},
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// TestClosedKey: S6k's closed key is keyof X, a string-literal union, or an alias of either; an open type is not.
func TestClosedKey(t *testing.T) {
	kb, lits, open := "keyof Keybindings", `"a" | "b"`, "string"
	c := &checker{pkg: "p", aliases: aliasTable{"p": {"Keybinding": &kb, "Mode": &lits, "Name": &open}}}
	for typ, want := range map[string]bool{"Keybinding": true, "Mode": true, "keyof X": true, `"x" | "y"`: true, "Name": false, "string": false, "Missing": false} {
		if got := c.closedKey(typ); got != want {
			t.Errorf("closedKey(%q) = %v, want %v", typ, got, want)
		}
	}
	str := types.Typ[types.String]
	sig := types.NewSignatureType(nil, nil, nil, types.NewTuple(types.NewVar(0, nil, "m", str)),
		types.NewTuple(types.NewVar(0, nil, "", str), types.NewVar(0, nil, "", types.Typ[types.Bool])), false)
	c.renames = map[string]string{}
	if v := c.signature(callShape{Parameters: []param{{Name: "m", Type: "Mode"}}, Returns: "string"}, sig); v.ok != yes {
		t.Errorf("a lookup by an aliased closed key may return (T, bool): %v (%s)", v.ok, v.why)
	}
}

// TestSharesWord: N5 compares function shapes only between identifiers that share a specific word.
func TestSharesWord(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"createInMemoryTransportPair", "NewInMemoryTransportPair", true},
		{"formatSkillsForPrompt", "formatSkills", true},
		{"keyText", "toolCWD", false},
		{"getBuiltinModels", "PollOAuthDeviceCodeFlow", false},
		{"createResult", "buildResult", false},
		{"parse_session_entries", "ParseSessionEntries", true},
		{"getHTTPProxyURL", "httpProxyFor", true},
		{"isRecord", "modelDataTimestampValid", false},
		{"keyMap", "keyTable", false},
	} {
		if got := sharesWord(tc.a, tc.b); got != tc.want {
			t.Errorf("sharesWord(%q, %q) = %v (%v / %v), want %v", tc.a, tc.b, got, identWords(tc.a), identWords(tc.b), tc.want)
		}
	}
}

// TestLiteralFlags: T10f reads a union of literals and types from a struct with a bool field per literal and a field per type;
// T10e from an integer enum with <Type><Literal> constants (and True/False for a boolean member).
func TestLiteralFlags(t *testing.T) {
	gt := goTypes(t, `
type Wheel struct {
	Auto  bool
	Lines float64
}

type NoFlag struct {
	Lines float64
	Mode  string
}

type Quiet uint8

const (
	QuietFalse Quiet = iota
	QuietTrue
	QuietHeader
)

type Partial uint8

const (
	PartialTrue Partial = iota
	PartialHeader
)

type Label string

const (
	LabelHeader Label = "header"
	LabelTrue   Label = "true"
	LabelFalse  Label = "false"
)

type WheelS struct {
	Auto  string
	Lines float64
}

type Quiet2 uint8

const (
	Quiet2False = 0
	Quiet2True  = 1
	Quiet2Header = 2
)

var (
	W  Wheel
	NF NoFlag
	Q  Quiet
	P  Partial
	L  Label
	WS WheelS
	Q2 Quiet2
	Ss []string
)`)
	c := &checker{renames: map[string]string{}, pkg: "p"}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{`number | "auto"`, "W", yes}, {`boolean | "auto"`, "W", unknown}, {`number | "auto" | "max"`, "W", unknown}, {`number | "auto"`, "NF", unknown},
		{`boolean | "header"`, "Q", yes}, {`boolean | "header"`, "P", unknown},
		{`number | "header"`, "Q", unknown}, {`boolean | "header"`, "L", unknown}, {`number | "lines"`, "W", unknown},
		{`number | "auto"`, "WS", unknown}, {`boolean | "header"`, "Q2", unknown},
		{"string | string[]", "Ss", yes}, {"string[] | string", "Ss", yes}, {"number | number[]", "Ss", no}, {"string | number[]", "Ss", unknown}, {"string | string[]", "L", unknown}, {"string | string[] | number", "Ss", unknown},
		{"string | readonly string[]", "Ss", yes}, {"readonly number[] | number", "Ss", no},
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// TestAliasMemberTypes: T17a reads X["p"] of an alias from every object type the alias is made of.
func TestAliasMemberTypes(t *testing.T) {
	gt := goTypes(t, `
type Scope interface{ isScope() }

var (
	SC Scope
	S  string
	N  int
)`)
	rec := `{ readonly id: string; } & (
	| { readonly scope: { readonly kind: "session" }; readonly extra?: never }
	| ({ readonly scope: { readonly kind: "task"; readonly taskId: number } } & Base)
)`
	sub := `| (Base & { readonly status: "queued" }) | (Base & { readonly status: "done" })`
	bad := `Base | [string, number]`
	missing := `{ readonly other: string } | Nested`
	nested := `{ readonly scope: string }`
	loop := `{ readonly scope: string } | Loop`
	badNest := `{ readonly scope: string } | Bad`
	odd := `{ scope; other: string }`
	cond, cond2 := "X extends true ? { readonly scope: string } : never", "X extends true ? never : { scope: number }"
	c := &checker{renames: map[string]string{}, pkg: "p",
		aliases: aliasTable{"p": {"Rec": &rec, "Sub": &sub, "Bad": &bad, "Missing": &missing, "Nested": &nested, "Loop": &loop, "BadNest": &badNest, "Odd": &odd, "Cond": &cond, "Cond2": &cond2}},
		props: func(n string) []propShape {
			if n == "Base" {
				return []propShape{{Name: "id", Type: "string"}, {Name: "status", Type: `"base"`}}
			}
			return nil
		}}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{`Rec["scope"]`, "SC", yes}, {`Rec["scope"]`, "N", unknown}, {`Sub["status"]`, "S", yes}, {`Sub["status"]`, "N", no},
		{`Bad["id"]`, "S", unknown}, {`Missing["scope"]`, "S", yes}, {`Missing["scope"]`, "N", no}, {`Rec["nothing"]`, "S", unknown},
		{`Rec[K]`, "S", unknown}, {`Gone["scope"]`, "S", unknown}, {`Loop["scope"]`, "S", unknown}, {`BadNest["scope"]`, "S", unknown},
		{`Rec["extra"]`, "S", unknown}, {`Cond["scope"]`, "S", yes}, {`Cond["scope"]`, "N", no}, {`Cond2["scope"]`, "N", yes},
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
	for _, n := range [][2]string{{"Rec", "extra"}, {"Odd", "scope"}} {
		if typs, _ := c.aliasMemberTypes(n[0], n[1], 0); len(typs) != 0 {
			t.Errorf("%s[%q] types = %q, want none (never, or a member without a type)", n[0], n[1], typs)
		}
	}
	if typs, _ := c.aliasMemberTypes("Sub", "status", 0); len(typs) != 3 {
		t.Errorf("Sub status types = %q, want the two literals and Base's", typs)
	}
}

// TestSignalOnly: T15s drops only an operand whose members are all AbortSignals.
func TestSignalOnly(t *testing.T) {
	for in, want := range map[string]bool{
		"{ signal?: AbortSignal }": true, "{ readonly signal: AbortSignal; abort: AbortSignal }": true, "{ signal?: AbortSignal; name: string }": false,
		"{}": false, "Interaction": false, "{ signal }": false, "{ signal?: AbortSignal; }": true, "{ signal?: AbortSignal }[]": false,
	} {
		if got := signalOnly(in); got != want {
			t.Errorf("signalOnly(%q) = %v, want %v", in, got, want)
		}
	}
	gt := goTypes(t, `
type Prompt interface{ Kind() string }

var P Prompt`)
	for _, consumers := range []bool{true, false} {
		c := &checker{renames: map[string]string{}, pkg: "p", ctxConsumers: func(types.Type) bool { return consumers }}
		want := map[bool]tri{true: yes, false: unknown}[consumers]
		if got := c.agree("{ signal?: AbortSignal } & Prompt", gt["P"]); got.ok != want {
			t.Errorf("consumers take a context = %v: got %v (%s), want %v", consumers, got.ok, got.why, want)
		}
	}
}

// TestUnparen: only parentheses that enclose the whole type are removed.
func TestUnparen(t *testing.T) {
	for in, want := range map[string]string{
		" ( () => Promise<X> ) ": "() => Promise<X>", `typeof import("x")`: `typeof import("x")`, "(a: A) => (B)": "(a: A) => (B)", "X": "X",
	} {
		if got := unparen(in); got != want {
			t.Errorf("unparen(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSubtypeReduction: T10w reduces a union of a type and its subtypes to that type before judging it.
func TestSubtypeReduction(t *testing.T) {
	gt := goTypes(t, `
type Info struct{ ID string }

var (
	I Info
	N int
)`)
	full := "Info & Meta"
	c := &checker{renames: map[string]string{}, pkg: "p", aliases: aliasTable{"p": {"Full": &full}},
		props: func(n string) []propShape {
			switch n {
			case "Info":
				return []propShape{{Name: "id", Type: "string"}}
			case "Ext":
				return []propShape{{Name: "id", Type: "string"}, {Name: "x", Type: "number"}}
			case "OptID":
				return []propShape{{Name: "id", Type: "string", Optional: true}, {Name: "x", Type: "number"}}
			case "NumID":
				return []propShape{{Name: "id", Type: "number"}, {Name: "x", Type: "number"}}
			case "Other":
				return []propShape{{Name: "x", Type: "number"}}
			case "Named":
				return []propShape{{Name: "name", Type: "string"}}
			case "Twin":
				return []propShape{{Name: "id", Type: "string"}}
			}
			return nil
		}}
	for _, tc := range []struct {
		members string
		want    []string
	}{
		{"Info|Full", []string{"Info"}}, {"Full|Info", []string{"Info"}}, {"Info|Info & Meta", []string{"Info"}}, {"Info|Ext", []string{"Info"}},
		{"Info|OptID", []string{"Info", "OptID"}}, {"Info|NumID", []string{"Info", "NumID"}}, {"Info|Other", []string{"Info", "Other"}},
		{"Ext|Other", []string{"Other"}}, {"Meta & X|Info", []string{"Meta & X", "Info"}}, {"{ id: string }|Ext", []string{"{ id: string }", "Ext"}},
		{"Info|Info", []string{"Info", "Info"}}, {"Missing|Info", []string{"Missing", "Info"}}, {"Info|Named", []string{"Info", "Named"}},
		{"A & { id: string }|{ id: string }", []string{"{ id: string }"}}, {"Info | Full", []string{"Info "}},
		// Two names for the same shape are subtypes of each other; the union reduces to one of them, not to nothing.
		{"Info|Twin", []string{"Info"}}, {"Twin|Info", []string{"Twin"}},
	} {
		if got := c.reduceSubtypes(strings.Split(tc.members, "|")); !slices.Equal(got, tc.want) {
			t.Errorf("reduceSubtypes(%s) = %q, want %q", tc.members, got, tc.want)
		}
	}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{{"Info | Full", "I", yes}, {"Info | Full | undefined", "I", yes}, {"Info | Full", "N", no}} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// TestReadInterfaces: interfaces read from source are `Extended & { members }`; braces in strings and comments do not close them.
func TestReadInterfaces(t *testing.T) {
	out := aliasTable{}
	readInterfaces("export interface A { a: string; b: \"x\" }\ninterface B<T = string> extends A, C<D, E> { /* } */ c: \"}\"; // }\n d: number }\n"+
		"interface Dup { x: string }\nexport interface Dup { y: string }\ninterface Open { a: string\n", out, "iface:p")
	want := map[string]string{"A": `{ a: string; b: "x" }`, "B": "A & C<D, E> & { /* } */ c: \"}\"; // }\n d: number }"}
	for n, w := range want {
		if b := out["iface:p"][n]; b == nil || *b != w {
			t.Errorf("interface %s = %v, want %q", n, b, w)
		}
	}
	if b, ok := out["iface:p"]["Dup"]; !ok || b != nil {
		t.Errorf("a twice-declared interface reads nil, got %v", b)
	}
	if _, ok := out["iface:p"]["Open"]; ok {
		t.Error("an unclosed interface is not read")
	}
	for in, want := range map[string]int{"{ a }": 4, "{ '}' }": 6, "{ \"\\\"}\" }": 8, "{ /* }": -1, "{ // }": -1, "{ a ": -1, "{ { } }": 6} {
		if got := closingBrace(in); got != want {
			t.Errorf("closingBrace(%q) = %d, want %d", in, got, want)
		}
	}
	one, two, other := "{ one }", "{ two }", "{ other }"
	tab := aliasTable{"iface:p1": {"X": &one}, "iface:p2": {"X": &two, "Y": &other}, "p2": {"Z": &other}}
	if b := tab.iface("p1", "X"); b != &one {
		t.Error("the row package's interface comes first")
	}
	if tab.iface("p3", "X") != nil || tab.iface("p3", "Y") != &other || tab.iface("p2", "Z") != nil {
		t.Error("another package's interface is read only when it is the only one, and aliases are not interfaces")
	}
	if tab.lookup("p3", "Y") != nil {
		t.Error("alias lookup does not read interfaces")
	}
}

// TestMemberNames: T10d collects property names through objects, ledger interfaces, source interfaces, aliases, unions and
// intersections, and fails on any other type.
func TestMemberNames(t *testing.T) {
	ifc, union, loop, base := "Base & { c: \"k\" }", "| { a: string } | { b: string }", "Loop & { a: string }", "{ shadow: string }"
	c := &checker{pkg: "p", aliases: aliasTable{"iface:p": {"Ifc": &ifc, "Both": &ifc}, "p": {"U": &union, "Loop": &loop, "Both": &base}},
		propNames: func(n string) []string {
			if n == "Base" {
				return []string{"p", "q"}
			}
			return nil
		}}
	for _, tc := range []struct {
		in    string
		want  []string
		ok    bool
		lits  string
		litOf string
	}{
		{`{ a: string; b: "x" }`, []string{"a", "b"}, true, "x", "b"}, {"Base", []string{"p", "q"}, true, "", ""},
		{"Ifc", []string{"p", "q", "c"}, true, "k", "c"}, {"(U)", []string{"a", "b"}, true, "", ""}, {"Both", []string{"p", "q", "c"}, true, "", ""},
		{"Missing", nil, false, "", ""}, {"Base[]", nil, false, "", ""}, {"Loop", nil, false, "", ""}, {"Base & Missing", []string{"p", "q"}, false, "", ""},
		{"Missing | Base", nil, false, "", ""},
		{`Omit<{ a: string; b: "x"; c?: never }, "b">`, []string{"a"}, true, "x", "b"}, {`Pick<{ a: string; b: string }, "b" | "z">`, []string{"b"}, true, "", ""},
		{`Omit<Missing, "a">`, nil, false, "", ""}, {`Omit<{ a: string }, string>`, nil, false, "", ""}, {`Omit<{ a: string }>`, nil, false, "", ""},
	} {
		var names []string
		lits := map[string]map[string]bool{}
		ok := c.memberNames(tc.in, 0, &names, lits)
		if ok != tc.ok || !slices.Equal(names, tc.want) {
			t.Errorf("memberNames(%q) = %q, %v; want %q, %v", tc.in, names, ok, tc.want, tc.ok)
		}
		if tc.lits != "" && !lits[tc.litOf][tc.lits] {
			t.Errorf("memberNames(%q) literals = %v, want %s=%s", tc.in, lits, tc.litOf, tc.lits)
		}
	}
	gt := goTypes(t, `
type Rec struct{ A string }

var R Rec`)
	st := gt["R"].Underlying().(*types.Struct)
	if _, ok := c.taggedStruct([]string{"Missing", "{ a: string }"}, gt["R"], st); ok {
		t.Error("T10d does not judge a union with a member whose properties are unknown")
	}
	if v, ok := c.taggedStruct([]string{"{ a: string }"}, gt["R"], st); !ok || v.ok != yes {
		t.Errorf("T10d: a struct with every member property, got %v %v", v, ok)
	}
}

// TestStructuralOwnTypeParameters: T9g judges an interface's members with the interface's own type parameters in scope, so
// Pick<Def, "parameters"> with `parameters: TParams` is any Go type for the parameter (T11).
func TestStructuralOwnTypeParameters(t *testing.T) {
	gt := goTypes(t, `
type Info struct {
	Name       string
	Parameters []byte
}

var I Info`)
	props := func(n string) []propShape {
		if n == "Def" {
			return []propShape{{Name: "name", Type: "string"}, {Name: "parameters", Type: "TParams"}, {Name: "extra", Type: "number"}}
		}
		return nil
	}
	for _, tc := range []struct {
		params []string
		gen    map[string]bool
		want   tri
	}{
		{[]string{"TParams"}, nil, yes}, {[]string{"TParams"}, map[string]bool{"Row": true}, yes}, {[]string{"Other"}, nil, unknown}, {nil, nil, unknown},
	} {
		c := &checker{renames: map[string]string{}, pkg: "p", props: props, generics: tc.gen, typeParams: func(n string) []string {
			if n == "Def" {
				return tc.params
			}
			return nil
		}}
		if got := c.agree(`Pick<Def, "name" | "parameters">`, gt["I"]); got.ok != tc.want {
			t.Errorf("type parameters %v: got %v (%s), want %v", tc.params, got.ok, got.why, tc.want)
		}
		if tc.gen != nil && !c.generics["Row"] || c.generics["TParams"] {
			t.Errorf("the row's generics are copied, not changed: %v", c.generics)
		}
	}
}

// TestTypeParamsOf: the type parameters of an interface or class come from the row package's declaration, else the first one.
func TestTypeParamsOf(t *testing.T) {
	entry := func(id, name, kind, role string, params ...string) *upstreamEntry {
		e := &upstreamEntry{ID: id, Name: name, Kind: kind, Role: role}
		for _, p := range params {
			e.Shape.TypeParameters = append(e.Shape.TypeParameters, typeParam{Name: p})
		}
		return e
	}
	d := &detector{l: &ledger{entries: []*upstreamEntry{
		entry("pkg:a/.#Def::property:x", "Def", "property", "property", "Prop"),
		entry("pkg:a/.#Def", "Def", "type-alias", "", "Alias"),
		entry("pkg:a/.#Def", "Def", "interface", "", "A"),
		entry("pkg:b/.#Def", "Def", "class", "", "B"),
	}}}
	for pkg, want := range map[string]string{"a": "A", "b": "B", "c": "A"} {
		if got := d.typeParamsOf(pkg, "Def"); !slices.Equal(got, []string{want}) {
			t.Errorf("typeParamsOf(%s) = %q, want %s", pkg, got, want)
		}
	}
	if got := d.typeParamsOf("a", "Missing"); got != nil {
		t.Errorf("an unknown name has no type parameters, got %q", got)
	}
}

// TestInvocationContextIsTheGoContext: S1c reads an upstream parameter `context: Context` as Go's context.Context only in a package
// whose Context comes from chord (chord itself, durable); a package that also imports pi-ai's Context (coding-agent) or declares its
// own (ai) is not rewritten, because the name alone does not say which Context a call takes.
func TestInvocationContextIsTheGoContext(t *testing.T) {
	root := t.TempDir()
	w := func(p, s string) { write(t, filepath.Join(root, ".upstream/current/packages", p), s) }
	w("chord/src/types.ts", "export interface Context {\n\treadonly abortSignal: AbortSignal | undefined;\n}\n")
	w("durable/src/a.ts", "import type { AttachedReplicatedState, Context, Draft } from \"@earendil-works/chord\";\n")
	w("durable/src/b.ts", "import type {\n\tContext,\n\tJsonValue,\n} from \"@earendil-works/chord\";\n")
	w("coding-agent/src/a.ts", "import type { Context } from \"@earendil-works/chord\";\n")
	w("coding-agent/src/b.ts", "import { type Context, type Model } from \"@earendil-works/pi-ai\";\n")
	w("ai/src/types.ts", "export interface Context {\n\tsystemPrompt?: string;\n}\n")
	w("env/src/a.ts", "import type { Logger } from \"@earendil-works/chord\";\n")
	tab := tsAliases(root)
	for pkg, want := range map[string]bool{"chord": true, "durable": true, "coding-agent": false, "ai": false, "env": false, "nope": false} {
		if got := tab.invocationContext(pkg); got != want {
			t.Errorf("invocationContext(%s) = %v, want %v", pkg, got, want)
		}
	}
	c := &checker{aliases: tab, pkg: "durable"}
	if !c.env().SignalBag("Context") || c.env().SignalBag("Model") {
		t.Error("S1c: durable's Context is the signal-carrying invocation context, no other type name is")
	}
	c.pkg = "ai"
	if c.env().SignalBag("Context") {
		t.Error("S1c must leave ai's own Context alone")
	}

	sigs := goTypes(t, `
import "context"
type Draft struct{ ID string }
func Submit(ctx context.Context, d Draft) error { return nil }
func WithAbortSignal(parent context.Context, signal context.Context) context.Context { return parent }`)
	c.pkg = "chord"
	submit := callShape{Parameters: []param{{Name: "draft", Type: "Draft"}, {Name: "context", Type: "Context"}}, Returns: "Promise<void>"}
	if got := c.signature(submit, sigs["Submit"].(*types.Signature)); got.ok == no {
		t.Errorf("S1c: the Context parameter is the leading context.Context: %v %s", got.ok, got.why)
	}
	with := callShape{Parameters: []param{{Name: "signal", Type: "AbortSignal"}, {Name: "context", Type: "Context"}}, Returns: "Context"}
	if got := c.signature(with, sigs["WithAbortSignal"].(*types.Signature)); got.ok == no {
		t.Errorf("S1c fallback: a Go function that takes a Context value of its own reads the parameter as ordinary: %v %s", got.ok, got.why)
	}
}

// TestSourceProps: an interface the ledger does not list has the members of its source block and of the interfaces it extends.
func TestSourceProps(t *testing.T) {
	base, ext, bad, loop, listed := "{ a: string; \"q.x\"?: number; m?(): void; n(): void; junk }", "Base & { b: boolean }", "Missing & { c: string }",
		"Loop & { a: string }", "{ shadow: string }"
	d := &detector{l: &ledger{entries: []*upstreamEntry{{ID: "pkg:p/.#Listed", Name: "Listed", Kind: "interface"}}},
		aliases: aliasTable{"iface:p": {"Base": &base, "Ext": &ext, "Bad": &bad, "Loop": &loop, "Listed": &listed}}}
	want := []propShape{{Name: "a", Type: "string"}, {Name: "q.x", Type: "number", Optional: true}, {Name: "m", Type: "() => void", Optional: true},
		{Name: "n", Type: "() => void"}}
	if got := d.propShapes("p", "Base"); !reflect.DeepEqual(got, want) {
		t.Errorf("Base = %+v, want %+v", got, want)
	}
	if got := d.propShapes("p", "Ext"); !reflect.DeepEqual(got, append(slices.Clone(want), propShape{Name: "b", Type: "boolean"})) {
		t.Errorf("Ext = %+v", got)
	}
	for _, n := range []string{"Bad", "Loop", "Listed", "Missing"} {
		if got := d.propShapes("p", n); len(got) != 0 {
			t.Errorf("%s = %+v, want none", n, got)
		}
	}
	base = "{ changed: string }"
	if got := d.propShapes("p", "Base"); !reflect.DeepEqual(got, want) {
		t.Errorf("a second read comes from the cache, got %+v", got)
	}
	// A chain deeper than the read limit has no members, and a member of it read on its own afterwards has all of its own.
	for i := range 6 {
		body := fmt.Sprintf("{ f%d: string }", i)
		if i > 0 {
			body = fmt.Sprintf("L%d & %s", i-1, body)
		}
		d.aliases["iface:p"][fmt.Sprintf("L%d", i)] = &body
	}
	if got := d.propShapes("p", "L1"); len(got) != 2 {
		t.Errorf("L1 = %+v, want f0 and f1", got)
	}
	if got := d.propShapes("p", "L5"); len(got) != 0 {
		t.Errorf("L5 is deeper than the read limit, got %+v", got)
	}
	if got := d.propShapes("p", "L2"); len(got) != 3 {
		t.Errorf("L2 read on its own = %+v, want f0..f2", got)
	}
}

// TestConditionalTypes: T20 splits a conditional type at its top level and judges both non-never branches; `infer R` binds R to
// the checked type.
func TestConditionalTypes(t *testing.T) {
	for in, want := range map[string][4]string{
		"A extends B ? X : Y":                            {"A", "B", "X", "Y"},
		"A extends (x: B) => C ? X | Z : Y":              {"A", "(x: B) => C", "X | Z", "Y"},
		"A extends B ? C extends D ? X : Z : Y":          {"A", "B", "C extends D ? X : Z", "Y"},
		"A extends B ? X : C extends D ? Z : Y":          {"A", "B", "X", "C extends D ? Z : Y"},
		"A extends B ? { a?: X; b: Y } : never":          {"A", "B", "{ a?: X; b: Y }", "never"},
		"[D] extends [never] ? { d?: never } : { d: D }": {"[D]", "[never]", "{ d?: never }", "{ d: D }"},
		"Map<A extends B ? X : Y, Z>":                    {},
		"X extends infer K extends string ? K : never":   {"X", "infer K extends string", "K", "never"},
		"A extends B": {},
		"A ? X : Y":   {},
	} {
		c, e, y, n, ok := splitConditional(in)
		if got := [4]string{c, e, y, n}; got != want || ok != (want[0] != "") {
			t.Errorf("splitConditional(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
	gt := goTypes(t, `
type Rec struct{ A string; B int }

var (
	R Rec
	S string
	E any
)`)
	c := &checker{renames: map[string]string{}, pkg: "p"}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{"T extends string ? { a: string } : { b: number }", "R", yes}, {"T extends string ? { a: string } : { c: number }", "R", no},
		{"T extends string ? { a: string } : never", "R", yes}, {"T extends string ? never : { c: number }", "R", no},
		{"T extends string ? never : never", "R", unknown}, {"Rec2 extends infer X ? X extends Rec2 ? string : never : never", "S", yes},
		{"string extends infer X ? { a: X } : never", "R", yes}, {"number extends infer X ? { a: X } : never", "R", no}, {"{ readonly [K in keyof T]: T[K] }", "R", unknown}, {"{ -readonly [K in keyof T]: T[K] }", "R", unknown},
		{"{ -readonly [K in keyof T]: T[K] }", "E", unknown}, {"{ readonly [K in keyof T]: T[K] }", "E", unknown}, {"{ a: string }", "E", no},
		{"(T extends string ? { a: string } : never)", "R", yes}, {"{ a: string; z?: never }", "R", yes}, {"{ a: string; z: number }", "R", no},
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
	if got := stripTypeNoise("{\n\ta: X; // note\n\tb: \"https://x\"; // two\n\tc: \"see //x\";\n}"); got != "{\n\ta: X;\n\tb: \"https://x\";\n\tc: \"see //x\";\n}" {
		t.Errorf("trailing line comments are removed and a URL literal is kept, got %q", got)
	}
	// A comment after a member holds an apostrophe: image-resize-core.ts ImageResizeOptions.maxBytes.
	if got := stripTypeNoise("{\n\ta?: number; // Default: 2000\n\tb?: number; // Default: 4.5MB (below Anthropic's 5MB limit)\n}"); got != "{\n\ta?: number;\n\tb?: number;\n}" {
		t.Errorf("a trailing comment with an apostrophe is removed, got %q", got)
	}
	// image-resize-core.ts:4-9: a trailing comment may hold a quote ("Anthropic's"); the comment still ends at the line end, and a
	// quoted literal, escaped quotes included, keeps its `//`.
	for body, want := range map[string]string{
		"{\n\ta?: number; // Default: 2000\n\tb?: number; // below Anthropic's 5MB limit\n\tc?: number; // Default: 80\n}": "{\n\ta?: number;\n\tb?: number;\n\tc?: number;\n}",
		"{ a: 'it\\'s //x'; // note \"q\n\tb: `//t`; }":                                                                    "{ a: 'it\\'s //x';\n\tb: `//t`; }",
		"{ a: X } // last": "{ a: X }",
	} {
		if got := stripTypeNoise(body); got != want {
			t.Errorf("stripTypeNoise(%q) = %q, want %q", body, got, want)
		}
	}
	if got := gapTSV([]*decision{{ID: "i", Gap: true, Detail: "a\n\tb  c"}}, true); got != "i\t\t\t\ta b c\n" {
		t.Errorf("a detail stays on one line, got %q", got)
	}
}

// TestPrimitiveUnionsAndBooleanLiterals: T10p accepts the empty interface for a union of primitives and byte buffers; T10c and T13
// count a boolean literal as a JSON value.
func TestPrimitiveUnionsAndBooleanLiterals(t *testing.T) {
	gt := goTypes(t, `
import "encoding/json"

var (
	E any
	J json.RawMessage
	N int
)`)
	c := &checker{renames: map[string]string{}, pkg: "p"}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{"null | number | bigint | string | Uint8Array", "E", yes}, {"boolean | Buffer<ArrayBufferLike>", "E", yes}, {"number | Foo", "E", unknown}, {"number | bigint", "N", unknown},
		{"false | { a: string }", "J", yes}, {"true | { a: string }", "E", yes}, {"false | Foo", "J", unknown},
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// TestTaggedEventIsItsDatum: T9w accepts, for an event whose members are a literal `type` tag and one datum (Pi extensions/types.ts
// MessageEndEvent { type: "message_end"; message: AgentMessage }, ExtensionRunner.emitMessageEnd runner.ts:1144), the datum's Go type.
// A tag that is not a literal, a second datum, an optional datum, a Go type of another datum and a datum the rules cannot decide are not accepted by T9w.
func TestTaggedEventIsItsDatum(t *testing.T) {
	gt := goTypes(t, `
type Message struct{ Role string }

type Other struct{ N int }

var (
	M Message
	O Other
)`)
	msg := []propShape{{Name: "role", Type: "string"}}
	props := map[string][]propShape{
		"Message":  msg,
		"EndEvent": {{Name: "type", Type: `"message_end"`}, {Name: "message", Type: "Message"}},
		"OpenTag":  {{Name: "type", Type: "string"}, {Name: "message", Type: "Message"}},
		"TwoData":  {{Name: "type", Type: `"x"`}, {Name: "message", Type: "Message"}, {Name: "extra", Type: "number"}},
		"OptData":  {{Name: "type", Type: `"x"`}, {Name: "message", Type: "Message", Optional: true}},
		"Unknown":  {{Name: "type", Type: `"x"`}, {Name: "message", Type: "Mystery"}},
	}
	c := &checker{generics: map[string]bool{}, renames: map[string]string{}, pkg: "p", props: func(n string) []propShape { return props[n] }}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{"EndEvent", "M", yes}, {"EndEvent", "O", no}, {"OpenTag", "M", no}, {"TwoData", "M", no}, {"OptData", "M", no}, {"Unknown", "M", no},
	} {
		// Each rejected case falls through to T9t, which finds no Go field for the event's members.
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// TestNullableBooleanIsATriStateEnum: T3n accepts, for `boolean | null` (Pi openai-responses-shared.ts:135 strict), a Go named integer
// type with exactly three constants named ...False, ...True and ...Null. A two-state enum, an enum with a fourth constant, a constant of
// another name or two False constants, a non-integer type, and a union without null are not accepted by T3n.
func TestNullableBooleanIsATriStateEnum(t *testing.T) {
	gt := goTypes(t, `
type Strict int

const (
	StrictFalse Strict = iota
	StrictTrue
	StrictNull
)

type Two int

const (
	TwoFalse Two = iota
	TwoTrue
)

type Four int

const (
	FourFalse Four = iota
	FourTrue
	FourNull
	FourAuto
)

type Odd int

const (
	OddFalse Odd = iota
	OddTrue
	OddAuto
)

type Dup int

const (
	DupFalse Dup = iota
	DupOffFalse
	DupTrue
	DupNull
)

type Str string

const (
	StrFalse Str = "f"
	StrTrue  Str = "t"
	StrNull  Str = "n"
)

var (
	S Strict
	W Two
	F Four
	R Str
	D Dup
	O Odd
)`)
	c := &checker{renames: map[string]string{}, pkg: "p"}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{"boolean | null | undefined", "S", yes}, {"boolean | null", "S", yes}, {"true | false | null", "S", yes},
		{"boolean | null", "W", no}, {"boolean | null", "F", no}, {"boolean | null", "R", no}, {"boolean | null", "D", no}, {"boolean | null", "O", no},
		{"boolean | undefined", "S", no},
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// TestNullableUnionIsAChangeWrapper: T3c accepts, for an optional `T | null` (Pi durable harness/types.ts:322 AgentChange.model), a Go
// instantiation Change[T] of a generic struct whose package declares SetTo[T](T) Change[T] and Cleared[T]() Change[T], when T agrees with the
// union without null. A generic struct without both constructors, a constructor returning another type, a wrapper of the wrong T, and a union
// without null are not accepted by T3c.
func TestNullableUnionIsAChangeWrapper(t *testing.T) {
	gt := goTypes(t, `
type Change[T any] struct {
	state int
	value T
}

func SetTo[T any](value T) Change[T] { return Change[T]{state: 2, value: value} }
func Cleared[T any]() Change[T]      { return Change[T]{state: 1} }

type Half[T any] struct{ value T }

func Unset[T any]() Half[T] { return Half[T]{} }

type Plain struct{ Provider, ModelId string }

var (
	C Change[string]
	N Change[int]
	H Half[string]
	P Plain
)`)
	c := &checker{renames: map[string]string{}, pkg: "p"}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{"string | null", "C", yes}, {"string | null | undefined", "C", yes},
		{"string | null", "N", no}, {"string", "C", no}, {"string | null", "H", no}, {"string | null", "P", no},
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// TestNeverIsTheEmptyStruct: T9v accepts, for `never` (Pi durable harness/generation.ts:49 Record<string, never>, an object with no entries), the
// empty struct, the zero-size element of a key-only Go map. A struct with a field, a string and an any are not accepted by T9v.
func TestNeverIsTheEmptyStruct(t *testing.T) {
	gt := goTypes(t, `
type Empty struct{}

type Keys map[string]struct{}

type One struct{ A int }

var (
	E Empty
	K Keys
	O One
	S string
	A any
)`)
	c := &checker{renames: map[string]string{}, pkg: "p"}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{"never", "E", yes}, {"Record<string, never>", "K", yes}, {"never", "O", unknown}, {"never", "S", unknown}, {"never", "A", unknown},
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// TestReviewedTypes: a reviewed exception turns only an undecided verdict into a pass, only for its recorded Go target, and a
// reviewed entry that no row used or that has no reason fails the run.
func TestReviewedTypes(t *testing.T) {
	d := &detector{reviewed: map[string]reviewedType{"a": {Go: "x.go#A", Reason: "Pi a.ts:1"}, "b": {Go: "x.go#B", Reason: "Pi b.ts:1"}}}
	for _, tc := range []struct {
		id, target string
		in, want   tri
	}{
		{"a", "x.go#A", unknown, yes}, {"a", "x.go#A", no, no}, {"a", "x.go#Other", unknown, unknown}, {"c", "x.go#A", unknown, unknown},
	} {
		if got := d.reviewedVerdict(tc.id, tc.target, verdict{ok: tc.in}); got.ok != tc.want {
			t.Errorf("reviewedVerdict(%s, %s, %v) = %v, want %v", tc.id, tc.target, tc.in, got.ok, tc.want)
		}
	}
	if err := d.checkReviewed(); err == nil || !strings.Contains(err.Error(), "b is stale") {
		t.Errorf("an unused exception fails the run, got %v", err)
	}
	d.reviewedVerdict("b", "x.go#B", verdict{ok: unknown})
	if err := d.checkReviewed(); err != nil {
		t.Errorf("every exception used, got %v", err)
	}
	d.reviewed["a"] = reviewedType{Go: "x.go#A", Reason: " "}
	if err := d.checkReviewed(); err == nil || !strings.Contains(err.Error(), "a has no reason") {
		t.Errorf("an exception without a reason fails the run, got %v", err)
	}
}

// TestOneOfStruct: T10o accepts a struct of nilable fields that holds every union member, with a string-keyed map field for the
// declaration-merging slot of an interface declared empty.
func TestOneOfStruct(t *testing.T) {
	gt := goTypes(t, `
type A struct{ X int }
type B struct{ Y int }
type One struct {
	A      *A
	B      *B
	Custom map[string]any
}
type NoMap struct {
	A *A
	B *B
}
type IntMap struct {
	A *A
	B *B
	M map[int]any
}
type Mixed struct {
	A *A
	B *B
	N int
}
type Priv struct {
	A *A
	B *B
	n int
}

var (
	O One
	M NoMap
	I IntMap
	X Mixed
	P Priv
)`)
	msg, slot, full, deep, loop := "| A | B", "{\n\t// apps extend this\n}", "{ a: A }", "Msg", "Loop | A"
	c := &checker{renames: map[string]string{}, pkg: "p",
		aliases: aliasTable{"p": {"Msg": &msg, "Deep": &deep, "Loop": &loop}, "iface:p": {"Slot": &slot, "Full": &full}}}
	for _, tc := range []struct {
		up, goT string
		want    bool
	}{
		{"Msg | Slot[keyof Slot]", "O", true}, {"A | B", "P", true}, {"Msg | Slot[keyof Slot]", "M", false}, {"Msg | Slot[keyof Slot]", "I", false},
		{"Loop", "O", false}, {"Msg | Full[keyof Full]", "O", false}, {"Msg | Slot[keyof Full]", "O", false}, {"A | C", "O", false}, {"A | B", "X", false},
	} {
		if got := c.agree(tc.up, gt[tc.goT]).ok == yes; got != tc.want {
			t.Errorf("agree(%q, %s) yes = %v, want %v", tc.up, tc.goT, got, tc.want)
		}
	}
	if !c.oneOfStruct([]string{"A", "B"}, gt["O"].Underlying().(*types.Struct)) || c.oneOfStruct([]string{"Deep", "B"}, gt["M"].Underlying().(*types.Struct)) {
		t.Error("an alias of a union is expanded two levels deep, no further")
	}
	empty := goTypes(t, "type E struct{ n *int }\n\nvar E0 E")
	if c.oneOfStruct([]string{"A"}, empty["E0"].Underlying().(*types.Struct)) {
		t.Error("a struct without exported fields holds nothing")
	}
}

// TestTupleUnionsAndMappedParameters: T5u reads a union of tuple types as Go []any; T11m reads a mapped type over a type parameter's
// keys as the empty interface.
func TestTupleUnionsAndMappedParameters(t *testing.T) {
	gt := goTypes(t, `
import "iter"

type Op []any
type P struct{ A string }
type Words []string

var (
	O Op
	W Words
	E any
	A [2]any
	Seq iter.Seq[string]
	PS  P
)`)
	op, wire, loop := "| readonly [\"r\", number]\n| readonly [\"s\", string, number]", "readonly [\"d\"] | Op", "Loop | [\"x\"]"
	c := &checker{renames: map[string]string{}, pkg: "p", generics: map[string]bool{"T": true},
		aliases: aliasTable{"p": {"Op": &op, "Wire": &wire, "Loop": &loop}}}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{"readonly [\"r\", number] | [\"s\", string]", "O", yes}, {"Op | Wire", "O", yes}, {"Op | Wire", "W", unknown}, {"Op | string", "O", unknown},
		{"Loop | Op", "O", unknown}, {"Op | Wire", "A", unknown}, {"[\"r\"] & Brand | [\"s\"]", "O", unknown},
		{"{ -readonly [K in keyof T]: T[K] }", "E", yes}, {"{ [K in keyof U]: U[K] }", "E", unknown}, {"{ [K in Keys]: string }", "E", unknown},
		{"{ [K in keyof T]: T[K] }", "O", unknown},
		{"[string, ...string[]]", "W", yes}, {"[string, ...number[]]", "W", unknown}, {"[string, string, ...string[]]", "W", unknown}, {"[number, ...number[]]", "W", no},
		{"AsyncIterator<string>", "Seq", yes}, {"Iterator<string>", "Seq", yes}, {"AsyncIterator<number>", "Seq", no},
		{"AsyncIterator<string, any, any>", "Seq", yes}, {"Iterator<string, undefined, unknown>", "Seq", yes}, {"AsyncIterator<string, number>", "Seq", unknown},
		{"AsyncIterable<number, void>", "Seq", no}, {"Iterable<string, any, number>", "Seq", unknown},
		{`Pick<{ a: string; b: number }, "a">`, "PS", yes}, {`Pick<{ a: string; b: number }, "a" | "z">`, "PS", unknown},
		{`"auto" | { type: "tool" }`, "E", yes}, {`"auto" | { type: "tool" }`, "W", unknown},
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// TestCallerKeyedMap: T11k reads a mapped type over a type parameter's keys (S or S["k"]) as a Go map from string to the value
// type; a non-parameter key source, a map not keyed by string, or a disagreeing value stays with the other rules.
func TestCallerKeyedMap(t *testing.T) {
	gt := goTypes(t, `type (
	H  struct{ N int }
	M  map[string]H
	MI map[int]H
	MO map[string]int
)`)
	c := &checker{generics: map[string]bool{"S": true}, renames: map[string]string{}, pkg: "p"}
	phase := `{ readonly [P in S["phase"]]: H<P, Extract<S, { phase: P }>>; }`
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{phase, "M", yes}, {`{ [P in S]: H<P>, }`, "M", yes}, {phase, "MI", unknown}, {`{ [P in S["phase"]]: string }`, "MO", no},
		{`{ [P in Q["phase"]]: H<P> }`, "M", unknown}, {`{ [P in S["phase"]]: P }`, "MO", yes},
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
	if c.generics["P"] {
		t.Error("the mapped variable stays local to the mapped type")
	}
}

// TestLiteralAgainstInterface: T12b finds a Go interface method for every member of an upstream object literal, data members and
// method members (`release(context: Context): void`, named before the parameter list) alike.
func TestLiteralAgainstInterface(t *testing.T) {
	gt := goTypes(t, `
import "context"

type Att interface {
	Release(context.Context) error
	Name() string
}`)
	c := &checker{generics: map[string]bool{}, renames: map[string]string{}, pkg: "p"}
	for _, tc := range []struct {
		up   string
		want tri
	}{
		{`{ release(context: Context): void; readonly name: string; }`, yes}, {`{ release?(context: Context): void }`, yes},
		{`{ vanish(): void }`, unknown}, {`{ readonly name: string; other: number }`, unknown},
	} {
		if got := c.agree(tc.up, gt["Att"]); got.ok != tc.want {
			t.Errorf("agree(%q, Att) = %v (%s), want %v", tc.up, got.ok, got.why, tc.want)
		}
	}
}

// T9i: a generic alias of a function type is its body with the arguments in place of the type parameters

// A9: a generic alias of a function type is its body with the arguments in place of the type parameters
// (coding-agent types.ts:1548 ExtensionHandler<E, R = undefined>).
func TestInstantiatedFunctionAlias(t *testing.T) {
	gt := goTypes(t, `
type Ctx struct{}
var (
	Silent func(e int, ctx Ctx) error
	Valued func(e int, ctx Ctx) (string, error)
	WrongE func(e string, ctx Ctx) error
)`)
	body := "(event: E, ctx: Ctx) => Promise<R | void> | R | void"
	params := "E,R"
	c := &checker{generics: map[string]bool{}, renames: map[string]string{}, pkg: "p", ctxInScope: true,
		aliases: aliasTable{"p": {"Handler": &body}, paramsPrefix + "p": {"Handler": &params}}}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{"Handler<number, undefined>", "Silent", yes}, // R = undefined: the handler returns nothing
		{"Handler<number, undefined>", "Valued", no},  // a value where Pi returns none
		{"Handler<number, string>", "Valued", yes},    // R = string: the handler returns a string
		{"Handler<string, undefined>", "WrongE", yes}, // E is substituted into the parameter
		{"Handler<number, undefined>", "WrongE", no},  // and checked against the Go parameter
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// TestUniqueSymbolBrandIsNotAGoField: a member keyed by a `declare const x: unique symbol` brand exists only in the type system, so
// the Go struct needs no field for it; a computed key that is not such a brand, and a plain missing member, stay gaps (T12).
func TestUniqueSymbolBrandIsNotAGoField(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".upstream/current/packages/ai/src/types.ts"), "declare const transcriptContextBrand: unique symbol;\nexport const notASymbol = Symbol();\n")
	tab := tsAliases(root)
	if !tab.isBrand("[transcriptContextBrand]") || tab.isBrand("[notASymbol]") || tab.isBrand("transcriptContextBrand") || tab.isBrand("[other]") {
		t.Fatalf("isBrand: brand=%v non-brand=%v bare=%v unknown=%v", tab.isBrand("[transcriptContextBrand]"), tab.isBrand("[notASymbol]"), tab.isBrand("transcriptContextBrand"), tab.isBrand("[other]"))
	}
	gt := goTypes(t, `type Transcript struct{ Messages []string }`)
	c := &checker{aliases: tab}
	for up, want := range map[string]tri{
		"{ messages: string[]; readonly [transcriptContextBrand]: true }": yes,
		"{ messages: string[]; [notASymbol]: true }":                      no,
		"{ messages: string[]; extra: true }":                             no,
	} {
		if got := c.agree(up, gt["Transcript"]); got.ok != want {
			t.Errorf("agree(%q) = %v (%s), want %v", up, got.ok, got.why, want)
		}
	}
}

// TestModuleAugmentedKeyof: an interface a `declare module` block augments has an open property set, so keyof of it is no closed union.
func TestModuleAugmentedKeyof(t *testing.T) {
	out := aliasTable{}
	readAugmented(`declare module "@x/tui" {
	interface Keys extends AppKeys {}
	export interface More { a: string }
}
interface Outside { b: string }
declare module "broken" {
	interface Never {}
`, out)
	for name, want := range map[string]bool{"Keys": true, "More": true, "Outside": false, "Never": false} {
		if _, got := out[augmentedKey][name]; got != want {
			t.Errorf("%s augmented = %v, want %v", name, got, want)
		}
	}
	c := &checker{aliases: out, props: func(string) []propShape { return []propShape{{Name: "a", Type: "string"}} }}
	if got := (checkerEnv{c}).PropertyNames("Keys"); got != nil {
		t.Errorf("an augmented interface lists %v", got)
	}
	if got := (checkerEnv{c}).PropertyNames("Outside"); len(got) != 1 {
		t.Errorf("a plain interface lists %v", got)
	}
	gt := goTypes(t, "type K = string\ntype N int\nvar (\n\tS K\n\tI N\n)")
	for _, tc := range []struct {
		up, goVar string
		want      tri
	}{{"keyof Keys", "S", yes}, {"keyof Keys", "I", no}, {"keyof Outside", "S", no}} {
		if got := c.agree(tc.up, gt[tc.goVar]).ok; got != tc.want {
			t.Errorf("agree(%q, %s) = %v, want %v", tc.up, tc.goVar, got, tc.want)
		}
	}
}

// TestImplementedGoFormStandsForUpstreamType (T9i): a Go type that implements the interface that is the upstream type's own Go form
// stands for it, however many members Pi's object type has that the Go interface carries as methods of another name; a type that does
// not implement the interface still goes through T9t.
func TestImplementedGoFormStandsForUpstreamType(t *testing.T) {
	gt := goTypes(t, `
type Tool interface{ Name() string; Run() error }
type Impl struct{}
func (*Impl) Name() string { return "" }
func (*Impl) Run() error { return nil }
type Half struct{}
func (*Half) Name() string { return "" }
var (
	I  Tool
	P  Impl
	H  Half
)`)
	props := map[string][]propShape{"Tool": {{Name: "name", Type: "string"}, {Name: "description", Type: "string"}, {Name: "run", Type: "() => void"}}}
	own := gt["I"].(*types.Named).Obj()
	c := &checker{generics: map[string]bool{}, renames: map[string]string{}, pkg: "p", props: func(n string) []propShape { return props[n] },
		resolve: func(string) *types.TypeName { return own }, ctxInScope: true}
	if got := c.agree("Tool", gt["P"]); got.ok != yes {
		t.Errorf("a type implementing the Go form (pointer receiver) stands for the upstream type, got %v", got.ok)
	}
	if got := c.agree("Tool", gt["H"]); got.ok != no {
		t.Errorf("a type that lacks a method of the Go form is judged by its members, got %v", got.ok)
	}
}

// TestGenericAliasInstance: A9 reads a generic alias instance as its body with the type arguments substituted; a wrong arity or a
// recursive alias stays undecided.
func TestGenericAliasInstance(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, ".upstream/current/packages/fx/src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	ts := "export type HookResult<T> = T | undefined | Promise<T | undefined>;\nexport type Loop<T> = Loop<T[]>;\n" +
		"export type Keyed<const K extends string, V = number> = Record<K, V>;\nexport type Wrap<T, U> = T;\n" +
		"export type Tag<const K extends string> = K;\nexport type Plain = Gizmo;\nexport type Trail<T,> = T;\nexport type List<T> = T[];\n"
	if err := os.WriteFile(filepath.Join(src, "types.ts"), []byte(ts), 0o644); err != nil {
		t.Fatal(err)
	}
	far := filepath.Join(root, ".upstream/current/packages/zz/src")
	if err := os.MkdirAll(far, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(far, "far.ts"), []byte("export type Far<T> = T;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gt := goTypes(t, `type (
	I int
	M map[string]bool
	S []int
)`)
	c := &checker{renames: map[string]string{}, pkg: "fx", aliases: tsAliases(root)}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{"HookResult<number>", "P", yes}, {"HookResult<string>", "P", no}, {"HookResult<number, string>", "P", unknown},
		{"Wrap<number, string>", "I", yes}, {"Wrap<number>", "I", unknown}, {"Tag<number>", "I", yes}, {"Plain<number>", "I", unknown},
		{"Far<number>", "I", yes}, {"Trail<number>", "I", yes},
		{"number | Promise<number>", "P", yes}, {"number | Promise<string>", "P", unknown}, {"Promise<number> | Promise<string>", "P", unknown},
		{"Loop<number>", "I", unknown}, {"Keyed<string, boolean>", "M", yes}, {"Keyed<string, number>", "M", no},
		// A substituted argument keeps its own grouping: List<number | undefined> is (number | undefined)[], not number | undefined[].
		{"List<number | undefined>", "S", yes},
	} {
		goT := gt[tc.goT]
		if tc.goT == "P" {
			goT = types.NewPointer(gt["I"])
		}
		if got := c.agree(tc.up, goT); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// TestErasedGenericInterface: T9e reads a Go interface Any<X> as the generic upstream X when the package's generic X or XOf
// implements it; another name, a non-generic X, a generic that does not implement it, a method-less Any<X>, or a struct Any<X> stays undecided.
// TestSameNamedAliasOfAnyIsNotTheUpstreamType: a Go alias with the upstream name decides the row only when it names a real type;
// `type Callback = any` has none of the upstream shape, so it stays undecided, while an alias of a named type still agrees.
func TestSameNamedAliasOfAnyIsNotTheUpstreamType(t *testing.T) {
	gt := goTypes(t, `type (
	Real struct{ N int }
	Callback = any
	Payload = Real
)
var (
	CB Callback
	PL Payload
)`)
	c := &checker{generics: map[string]bool{}, renames: map[string]string{}, pkg: "p"}
	if got := c.agree("Callback", gt["CB"]); got.ok == yes {
		t.Errorf("agree(Callback, alias of any) = yes, want undecided or no")
	}
	if got := c.agree("Payload", gt["PL"]); got.ok != yes {
		t.Errorf("agree(Payload, alias of Real) = %v (%s), want yes", got.ok, got.why)
	}
}

func TestErasedGenericInterface(t *testing.T) {
	gt := goTypes(t, `type (
	AnyTok interface{ Def() int }
	Tok[T any] interface {
		AnyTok
		Value() T
	}
	AnyBox interface{ Box() }
	BoxOf[T any] struct{ v T }
	AnyLone interface{ Lone() }
	Lone struct{}
	AnyBad interface{ Bad() }
	Bad[T any] struct{}
	AnyS struct{}
	S[T any] struct{}
	AnyEmpty interface{}
	Empty[T any] struct{ v T }
)
func (*BoxOf[T]) Box() {}
func (Lone) Lone() {}
func (S[T]) Def() int { return 0 }`)
	c := &checker{generics: map[string]bool{}, renames: map[string]string{}, pkg: "p"}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{"Tok", "AnyTok", yes}, {"Tok<string>", "AnyTok", yes}, {"Box", "AnyBox", yes}, {"Tok", "AnyBox", unknown},
		{"Lone", "AnyLone", unknown}, {"Bad", "AnyBad", unknown}, {"S", "AnyS", unknown},
		{"Empty", "AnyEmpty", unknown},
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// TestSealedUnionResult: T9s reads a Go sealed union interface (only unexported methods) as an upstream member type whose Go type
// implements it; an interface with an exported method, the empty interface, a non-member, or an unresolved name stays undecided.
func TestSealedUnionResult(t *testing.T) {
	gt := goTypes(t, `type (
	Node interface{ isNode() }
	Open interface {
		isNode()
		Name() string
	}
	Empty interface{}
	StackN struct{}
	ScrollN struct{}
	Other struct{}
)
func (StackN) isNode() {}
func (StackN) Name() string { return "" }
func (*ScrollN) isNode() {}`)
	goName := map[string]string{"StackLayout": "StackN", "ScrollLayout": "ScrollN", "OtherT": "Other"}
	c := &checker{generics: map[string]bool{}, renames: map[string]string{}, pkg: "p", resolve: func(name string) *types.TypeName {
		if g, ok := goName[name]; ok {
			return namedOf(gt[g]).Obj()
		}
		return nil
	}}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{"StackLayout", "Node", yes}, {"ScrollLayout", "Node", yes}, {"OtherT", "Node", unknown}, {"StackLayout", "Open", unknown},
		{"StackLayout", "Empty", unknown}, {"Nope", "Node", unknown}, {"StackN", "Node", yes}, // no ledger row: the Go package's type of the name
		{"Other", "Node", unknown},
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// TestQuotedKeyTable: T12q reads an object type whose keys no Go field can spell as a map from string to the agreeing value type;
// an identifier key, a disagreeing value or an empty object leaves the rule to the others.
func TestQuotedKeyTable(t *testing.T) {
	gt := goTypes(t, `type (
	Def struct {
		Keys []string `+"`json:\"defaultKeys\"`"+`
		Description string `+"`json:\"description\"`"+`
	}
	Table map[string]Def
	Ints map[int]Def
)`)
	c := &checker{generics: map[string]bool{}, renames: map[string]string{}, pkg: "p"}
	def := `{ readonly defaultKeys: string[]; readonly description: string; }`
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{`{ readonly "tui.up": ` + def + `; readonly "tui.down": ` + def + `; }`, "Table", yes},
		{`{ readonly "tui.up": ` + def + `; readonly "tui.down": { readonly defaultKeys: number; readonly description: string; }; }`, "Table", no},
		{`{ readonly "tui.up": ` + def + `; readonly "up": ` + def + `; }`, "Table", unknown},
		{`{ readonly "tui.up": ` + def + `; readonly up: ` + def + `; }`, "Table", unknown},
		{`{ readonly "tui.up": ` + def + `; }`, "Ints", unknown},
		{`{ }`, "Table", unknown},
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// T5n and T6i: faux.ts models is `[Model<string>, ...Model<string>[]]` (one or more models) and an event stream's
// [Symbol.asyncIterator]() returns AsyncIterator<T, any, any>: Go holds the first in a slice and yields the second as iter.Seq.
func TestNonEmptyArrayAndIteratorResults(t *testing.T) {
	gt := goTypes(t, `
import "iter"
type M struct{}
var (
	Models  []*M
	One     *M
	Seq     iter.Seq[*M]
	SeqInt  iter.Seq[int]
	Ints    []int
)`)
	c := &checker{generics: map[string]bool{}, renames: map[string]string{}, pkg: "p", ctxInScope: true}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{"[number, ...number[]]", "Ints", yes},
		{"[number, ...number[]]", "One", no},       // not an array
		{"[number, ...string[]]", "Ints", unknown}, // two element types: a real tuple, no rule
		{"AsyncIterator<number, any, any>", "SeqInt", yes},
		{"AsyncIterator<string, any, any>", "SeqInt", no}, // the yielded type is compared
		{"Iterator<number>", "SeqInt", yes},
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// T10n (lg-help-2 5265409c00 T10e, lost in a later merge): EventEmitter's eventName is `keyof EventEmitterEventMap | "data" | "paste" | E` and eventNames() returns `string | symbol`
// (@types/node events.d.ts); Go's event name is a string type, since Go has no symbols.
func TestOpenNameUnionIsAStringType(t *testing.T) {
	gt := goTypes(t, `
type Event string
var (
	E  Event
	S  string
	I  int
	IF interface{ Name() }
)`)
	c := &checker{generics: map[string]bool{"E": true}, renames: map[string]string{}, pkg: "p", ctxInScope: true}
	for _, tc := range []struct {
		up, goT string
		want    tri
	}{
		{`keyof EventEmitterEventMap | "data" | "paste" | E`, "E", yes},
		{`string | symbol`, "E", yes},
		{`string | symbol`, "S", yes},
		{`"data" | "paste"`, "E", yes},    // an existing literal-union rule or this one: either way the string type agrees
		{`string | symbol`, "I", unknown}, // a number is not a name: the union rules stay undecided
		{`string | number`, "E", unknown}, // a member that is not a name keeps the union rules
	} {
		if got := c.agree(tc.up, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// T7w: client.ts onConnectionStateChange(listener: (change: ConnectionStateChange) => void) keeps the listener in a Set and removes it
// by identity (client.ts:142-145); the Go port takes *ConnectionStateChangeListener built by NewConnectionStateChangeListener(func).
func TestListenerWrapperIsTheCallback(t *testing.T) {
	gt := goTypes(t, `
type L struct{ call func(string) }
func NewL(call func(string)) *L { return &L{call: call} }
type NoCtor struct{ call func(string) }
type OtherCtor struct{ call func(string) }
func NewOtherCtor(call func(int)) *OtherCtor { return nil }
type Two struct{ call, done func(string) }
func NewTwo(call func(string)) *Two { return nil }
type Ints struct{ call func(int) }
func NewInts(call func(int)) *Ints { return nil }
type Extra struct {
	call func(string)
	once bool
}
func NewExtra(call func(string)) *Extra { return nil }
type BadRet struct{ call func(string) }
func NewBadRet(call func(string)) *L { return nil }
type TwoArgs struct{ call func(string) }
func NewTwoArgs(call func(string), n int) *TwoArgs { return nil }
var (
	PL     *L
	VL     L
	PNo    *NoCtor
	POther *OtherCtor
	PTwo   *Two
	PInts  *Ints
	PExtra *Extra
	PBad   *BadRet
	PTwoA  *TwoArgs
)`)
	c := &checker{renames: map[string]string{}, pkg: "p", ctxInScope: true}
	for _, tc := range []struct {
		goT  string
		want tri
	}{
		{"PL", yes},
		{"PExtra", yes},     // non-function state beside the callback (a once flag) keeps the wrapper
		{"VL", unknown},     // a struct value has no identity
		{"PNo", unknown},    // no New<X> constructor takes the callback
		{"POther", unknown}, // the constructor takes another function type
		{"PTwo", unknown},   // two callbacks: which one the upstream function is cannot be told
		{"PBad", unknown},   // New<X> builds another type
		{"PTwoA", unknown},  // New<X> takes more than the callback
		{"PInts", no},       // the wrapped callback disagrees with the upstream function
	} {
		if got := c.agree(`(change: string) => void`, gt[tc.goT]); got.ok != tc.want {
			t.Errorf("agree(listener, %s) = %v (%s), want %v", tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// TestWrapperArmUnion: T10x accepts, for `X | W` where W's only required member has type X (Pi tui stack.ts:17 StackChild = Component |
// StackEntry, StackEntry = { component: Component } plus optional layout members), the Go form of W in either member order. A wrapper
// with a second required member, a wrapper of another type, a member the wrapper does not hold and a Go struct without the wrapped member are not accepted.
func TestWrapperArmUnion(t *testing.T) {
	gt := goTypes(t, `
type Component interface{ Render(width int) []string }

type Entry struct {
	Component Component
	Grow      *int
}

type Bare struct{ Grow *int }

var (
	E Entry
	B Bare
)`)
	props := map[string][]propShape{
		"Entry":  {{Name: "component", Type: "Component"}, {Name: "grow", Type: "number", Optional: true}},
		"TwoReq": {{Name: "component", Type: "Component"}, {Name: "grow", Type: "number"}},
		"Holder": {{Name: "component", Type: "Widget"}, {Name: "grow", Type: "number", Optional: true}},
	}
	c := &checker{generics: map[string]bool{}, renames: map[string]string{}, pkg: "p", props: func(n string) []propShape { return props[n] }}
	for _, tc := range []struct {
		up, goT string
		want    bool
	}{
		{"Component | Entry", "E", true}, {"Entry | Component", "E", true},
		{"Component | TwoReq", "E", false}, {"Component | Holder", "E", false}, {"Gadget | Entry", "E", false},
		{"Component | Entry", "B", false},
	} {
		if got := c.agree(tc.up, gt[tc.goT]); (got.ok == yes) != tc.want {
			t.Errorf("agree(%q, %s) = %v (%s), want accepted %v", tc.up, tc.goT, got.ok, got.why, tc.want)
		}
	}
}

// TestTypeBoxSchemasIgnoreExamples: coding-agent's examples/extensions/tool-override.ts declares its own `readSchema`, the name of
// src/core/tools/read.ts's schema; counted, the duplicate made `Static<typeof readSchema>` undecidable (ReadToolInput).
func TestTypeBoxSchemasIgnoreExamples(t *testing.T) {
	root := t.TempDir()
	write := func(rel, text string) {
		t.Helper()
		path := filepath.Join(root, ".upstream/current/packages", rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("pkg/src/read.ts", "const readSchema = Type.Object({ path: Type.String() });\nexport type ReadInput = Static<typeof readSchema>;\n")
	write("pkg/examples/extensions/override.ts", "const readSchema = Type.Object({ path: Type.String() });\n")
	aliases := tsAliases(root)
	body := aliases["pkg"]["ReadInput"]
	if body == nil || !strings.Contains(*body, "path") {
		t.Fatalf("ReadInput = %v, want its schema's members despite the example's duplicate schema name", body)
	}
}

// harness.ts:46,409 `import { Harness as HarnessType } from "./types.ts"; export type Harness = HarnessType;` re-exports the interface under its own name:
// the local name HarnessType is the imported Harness, so a Go type named Harness agrees with it.
func TestImportAsRenameNamesTheImportedType(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".upstream/current/packages/p/src/a.ts"), "import type { Harness as HarnessType, Other } from \"./t.ts\";\nexport type Harness = HarnessType;\n")
	tab := tsAliases(root)
	if got := tab[importAsKey+"p"]["HarnessType"]; got == nil || *got != "Harness" {
		t.Fatalf("import-as table = %v, want HarnessType -> Harness", got)
	}
	if _, ok := tab[importAsKey+"p"]["Other"]; ok {
		t.Error("a plain import is not a rename")
	}
	// client-tui.ts:51 `import { Transcript, type Transcript as TranscriptService }`: an inline type modifier is not part of the name.
	write(t, filepath.Join(root, ".upstream/current/packages/q/src/b.ts"), "import { Transcript, type Transcript as TranscriptService } from \"./t.ts\";\n")
	if got := tsAliases(root)[importAsKey+"q"]["TranscriptService"]; got == nil || *got != "Transcript" {
		t.Fatalf("inline type import-as = %v, want TranscriptService -> Transcript", got)
	}
	c := &checker{pkg: "p", aliases: tab}
	if !c.sameName("HarnessType", "Harness") {
		t.Error("HarnessType must be the same name as Harness")
	}
	if c.sameName("HarnessType", "Session") {
		t.Error("an import rename must not make unrelated names agree")
	}
}
