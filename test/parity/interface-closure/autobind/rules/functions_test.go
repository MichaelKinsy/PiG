package rules

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"slices"
	"strings"
	"testing"
)

// stubImporter provides a context package without invoking the go tool: the rules only look at the package path and name.
type stubImporter struct{ pkg *types.Package }

func (s stubImporter) Import(path string) (*types.Package, error) {
	if path != "context" {
		return nil, fmt.Errorf("unexpected import %q", path)
	}
	return s.pkg, nil
}

func contextStub(t *testing.T) *types.Package {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "context.go", "package context\ntype Context interface{ Err() error }\n", 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := (&types.Config{}).Check("context", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

// check type-checks a snippet and returns its package scope.
func checkCall(t *testing.T, src string) *types.Scope {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", "package x\nimport \"context\"\nvar _ context.Context\n"+src, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := (&types.Config{Importer: stubImporter{contextStub(t)}}).Check("x", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pkg.Scope()
}

func sigOf(t *testing.T, scope *types.Scope, name string) *types.Signature {
	t.Helper()
	obj := scope.Lookup(name)
	if obj == nil {
		t.Fatalf("no %s", name)
	}
	return obj.Type().Underlying().(*types.Signature)
}

// testEnv agrees the TypeScript primitives with the Go types they stand for and everything else by name.
func testEnv(scope *types.Scope) FuncEnv {
	var env FuncEnv
	env.Agree = func(up string, t types.Type) Verdict {
		up = strings.TrimSpace(up)
		switch {
		case up == "string" || up == "number" || up == "boolean":
			want := map[string]types.BasicInfo{"string": types.IsString, "number": types.IsNumeric, "boolean": types.IsBoolean}[up]
			if b, ok := t.Underlying().(*types.Basic); ok && b.Info()&want != 0 {
				return Accept("")
			}
			return Refute("T: %s against %s", up, typeLabel(t))
		case up == "AbortSignal":
			if IsContext(t) {
				return Accept("")
			}
			return Refute("T: AbortSignal against %s", typeLabel(t))
		case strings.HasSuffix(up, "| undefined"):
			inner := strings.TrimSpace(strings.TrimSuffix(up, "| undefined"))
			if p, ok := t.(*types.Pointer); ok {
				t = p.Elem()
			}
			return env.Agree(inner, t)
		case IsFuncType(up):
			return Callback(env, up, t)
		case strings.HasSuffix(up, "[]"):
			if s, ok := t.Underlying().(*types.Slice); ok {
				return env.Agree(strings.TrimSuffix(up, "[]"), s.Elem())
			}
			return Refute("T: %s against %s", up, typeLabel(t))
		}
		if n, ok := types.Unalias(t).(*types.Named); ok && n.Obj().Name() == up {
			return Accept("")
		}
		return Undecided("T: no rule for %s", up)
	}
	// The main package's SignalBag also reports chord's invocation Context (S1c) in a package that imports it.
	env.SignalBag = func(name string) bool { return name == "{ signal: AbortSignal }" || name == "Context" }
	env.Bag = func(typ string) ([]Param, bool) {
		switch strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(typ), "| undefined")) {
		case "StrictOptions":
			return []Param{{Name: "label", Type: "string"}}, true
		case "RunOptions":
			return []Param{{Name: "timeoutMs", Type: "number", Optional: true}, {Name: "label", Type: "string", Optional: true}}, true
		}
		return nil, false
	}
	return env
}

func call(returns string, params ...Param) Call { return Call{Parameters: params, Returns: returns} }

func TestSignatureRules(t *testing.T) {
	scope := checkCall(t, `
type Opts struct{ TimeoutMs int; Label string }
type Option func(*Opts)
type Bad struct{ Other int }
type BadOption func(*Bad)
type Wrong struct{ TimeoutMs string; Label string }
type WrongOption func(*Wrong)
type Handler interface{ Handle(string) }
func Plain(a string, b int) (string, error)                       { return "", nil }
func Ctx(ctx context.Context, a string) error                     { return nil }
func NoCtx(a string) error                                        { return nil }
func Fewer(a string)                                              {}
func Variadic(a string, rest ...int)                              {}
func AnyTail(a string, rest ...any)                               {}
func Func(a string, opts ...Option) error                         { return nil }
func FuncBad(a string, opts ...BadOption) error                   { return nil }
func FuncWrong(a string, opts ...WrongOption) error               { return nil }
func Spread(timeoutMs int, label string) error                    { return nil }
func None() error                                                 { return nil }
func BagValue(a string, label string) error                       { return nil }
func BagOther(a string, other string) error                       { return nil }
func Opt(a string) (*string, bool)                                { return nil, false }
func HookOpt(a string) (*string, error)                           { return nil, nil }
func Pair() (int, string)                                         { return 0, "" }
func Accessor() (int, func() string)                                { return 0, nil }
func AccessorValue() (int, string)                                  { return 0, "" }
func AccessorArgs() (int, func(int) string)                         { return 0, nil }
func Callback(cb func(string) error)                              {}
func Handled(h Handler)                                           {}
func Slice(items ...string)                                       {}
func Prompt(a string, items ...string)                            {}
func OnlyErr() error                                              { return nil }
func Plain1(a string) (string, error)                             { return "", nil }
func NoErr(a string) string                                       { return "" }
func GuardBool(m Opts) bool                                       { return false }
func GuardNoBool(m Opts) string                                   { return "" }
func AnyParam(a string, params any) error                          { return nil }
func StrParam(a string, params string) error                       { return nil }
func AnyResult(a string) any                                       { return nil }
`)
	env := testEnv(scope)
	str, num := Param{Name: "a", Type: "string"}, Param{Name: "b", Type: "number"}
	for _, tc := range []struct {
		name string
		up   Call
		fn   string
		want Tri
		why  string
	}{
		{"S2 one to one with promise result", call("Promise<string>", str, num), "Plain", Yes, ""},
		{"S1 signal is the leading context", call("Promise<void>", Param{Name: "signal", Type: "AbortSignal"}, str), "Ctx", Yes, ""},
		{"S1 optional signal", call("Promise<void>", Param{Name: "signal", Type: "AbortSignal | undefined"}, str), "Ctx", Yes, ""},
		{"S1 signal-only bag", call("Promise<void>", Param{Name: "o", Type: "{ signal: AbortSignal }"}, str), "Ctx", Yes, ""},
		{"S1c chord Context passed last is the leading context", call("Promise<void>", str, Param{Name: "context", Type: "Context"}), "Ctx", Yes, ""},
		{"S1c optional chord Context", call("Promise<void>", str, Param{Name: "context", Type: "Context | undefined"}), "Ctx", Yes, ""},
		{"S1c chord Context without a Go context", call("Promise<void>", str, Param{Name: "context", Type: "Context"}), "NoCtx", No, "S1"},
		{"S1c another Context type is an ordinary parameter", call("Promise<void>", str, Param{Name: "context", Type: "TranscriptContext"}), "Ctx", No, "S4"},
		{"S1 signal without context", call("Promise<void>", Param{Name: "signal", Type: "AbortSignal"}, str), "NoCtx", No, "S1"},
		{"S4 omitted parameter", call("void", str, num), "Fewer", No, "S4"},
		{"S4 extra Go parameter", call("string", str), "Plain", No, "S4"},
		{"S3 optional tail is variadic", call("void", str, Param{Name: "rest", Type: "number", Rest: true}), "Variadic", Yes, ""},
		{"S3 required parameter in variadic tail", call("void", str, num), "Variadic", No, "S3"},
		{"S3d required parameters land in an erased any tail", call("void", str, num, Param{Name: "key", Type: "string"}), "AnyTail", Yes, ""},
		{"S3 required array is the variadic slice", call("void", Param{Name: "items", Type: "string[]"}), "Slice", Yes, ""},
		{"S3 an optional trailing array is the variadic slice", call("void", str, Param{Name: "items", Type: "string[]", Optional: true}), "Prompt", Yes, ""},
		{"S3 an optional trailing array of another element is a gap", call("void", str, Param{Name: "items", Type: "number[]", Optional: true}), "Prompt", No, "T"},
		{"S5 void against a value", call("void", str, num), "Plain", No, "S5"},
		{"S5 value against no result", call("string", str), "Fewer", No, "S5"},
		{"S6 undefined is (T, bool)", call("string | undefined", str), "Opt", Yes, ""},
		{"S5h HookResult<T> is an optional value with the Go error", call("HookResult<string>", str), "HookOpt", Yes, ""},
		{"S5h a HookResult against no result is a gap", call("HookResult<string>", str), "Fewer", No, "S5"},
		{"S5b object result is the return values in order", call("{ n: number; s: string }"), "Pair", Yes, ""},
		{"S5b a method field is a Go func returning its type", call("{ n: number; remote(): string }"), "Accessor", Yes, ""},
		{"S5b a method field against a plain value is a gap", call("{ n: number; remote(): string }"), "AccessorValue", No, "S5b"},
		{"S5b a method field is a func with no parameters", call("{ n: number; remote(): string }"), "AccessorArgs", No, "S5b"},
		{"S8 options bag spread over positional parameters", call("Promise<void>", Param{Name: "o", Type: "RunOptions"}), "Spread", Yes, ""},
		// Dropping an optional bag, or an optional member of a bag, loses a capability exactly as an omitted optional parameter does (S4).
		{"S8 an omitted optional bag is a gap", call("Promise<void>", Param{Name: "o", Type: "StrictOptions", Optional: true}), "None", No, "S4"},
		// The engine lists a named interface or class (a TUI, a theme) as a bag of optional members; such a required parameter must not vanish.
		{"S8 a required bag of optional members is a gap", call("Promise<void>", Param{Name: "o", Type: "RunOptions"}), "None", No, "S4"},
		{"S8 required members of a required bag need a parameter", call("Promise<void>", Param{Name: "o", Type: "StrictOptions"}), "None", No, "S4"},
		// S8b: the counts agree, so the bag is not at its own position: the one-member bag lands on that member's value.
		{"S8b a one-member bag is its member's Go value", call("Promise<void>", str, Param{Name: "o", Type: "StrictOptions"}), "BagValue", Yes, ""},
		{"S8b a Go parameter not named after the member stays undecided", call("Promise<void>", str, Param{Name: "o", Type: "StrictOptions"}), "BagOther", Unknown, "T:"},
		{"S9 functional options cover the bag", call("Promise<void>", str, Param{Name: "o", Type: "RunOptions", Optional: true}), "Func", Yes, ""},
		{"S9 member without a field", call("Promise<void>", str, Param{Name: "o", Type: "RunOptions", Optional: true}), "FuncBad", No, "S9"},
		{"S9 member of another type", call("Promise<void>", str, Param{Name: "o", Type: "RunOptions", Optional: true}), "FuncWrong", No, "T:"},
		{"S2a a Go any parameter takes a record", call("void", str, Param{Name: "params", Type: "Record<string, unknown> | undefined"}), "AnyParam", Yes, ""},
		{"S2a a Go string parameter is not decided by S2a", call("void", str, Param{Name: "params", Type: "Record<string, unknown>"}), "StrParam", Unknown, "no rule"},
		{"S2a only a record of unknown", call("void", str, Param{Name: "params", Type: "Record<string, number>"}), "AnyParam", Unknown, "no rule"},
		{"S2a does not widen a result", call("Record<string, unknown>", str), "AnyResult", Unknown, "no rule"},
		{"S2g a type guard is a Go func of the inspected value returning bool", call("message is Handler", Param{Name: "message", Type: "unknown"}), "GuardBool", Yes, ""},
		{"S2g the guard's result must still be a boolean", call("message is Handler", Param{Name: "message", Type: "unknown"}), "GuardNoBool", No, "boolean against string"},
		{"S2g a boolean over one unknown is the predicate the inventory records", call("boolean", Param{Name: "message", Type: "unknown"}), "GuardBool", Yes, ""},
		{"S2g a second parameter is not a guard", call("boolean", Param{Name: "message", Type: "unknown"}, Param{Name: "n", Type: "number"}), "GuardBool", No, "S4"},
		{"S2g a guard of a typed input is not changed", call("boolean", Param{Name: "message", Type: "string"}), "GuardBool", No, "string against"},
		{"S11 callback is a func type", call("void", Param{Name: "cb", Type: "(s: string) => Promise<void>"}), "Callback", Yes, ""},
		{"S11 callback with a different arity", call("void", Param{Name: "cb", Type: "(s: string, n: number) => void"}), "Callback", No, "S4"},
		{"S11 one-method interface is a function type", call("void", Param{Name: "h", Type: "(s: string) => void"}), "Handled", Yes, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Signature(env, tc.up, sigOf(t, scope, tc.fn))
			if got.OK != tc.want || !strings.Contains(got.Why, tc.why) {
				t.Fatalf("Signature = %+v, want %v containing %q", got, tc.want, tc.why)
			}
		})
	}
}

func TestParseFuncType(t *testing.T) {
	c, ok := ParseFuncType("(a: string, b?: (x: number) => void, ...rest: number[]) => Promise<void>")
	if !ok || len(c.Parameters) != 3 || c.Returns != "Promise<void>" {
		t.Fatalf("got %+v ok=%v", c, ok)
	}
	if !c.Parameters[1].Optional || c.Parameters[1].Type != "(x: number) => void" || !c.Parameters[2].Rest || c.Parameters[2].Name != "rest" {
		t.Fatalf("parameters %+v", c.Parameters)
	}
	if _, ok := ParseFuncType("string"); ok {
		t.Fatal("a plain type is not a function type")
	}
	if c, ok := ParseFuncType("(\n\tmodel: Model,\n\toptions?: Options,\n) => Promise<void>"); !ok || len(c.Parameters) != 2 {
		t.Fatalf("a trailing comma adds no parameter, got %+v", c.Parameters)
	}
}

func TestOverloadRules(t *testing.T) {
	scope := checkCall(t, `
func Short(a string)                {}
func Wide(a string, b int)          {}
func Narrow(a string, b int, c int) {}
`)
	env := testEnv(scope)
	str := Param{Name: "a", Type: "string"}
	opt := Param{Name: "b", Type: "number", Optional: true}
	short := call("void", str)
	wide := call("void", str, opt)
	// S7: the one-parameter overload is a prefix of the wider one, which Wide satisfies.
	if v := Overload(env, short, []Call{short, wide}, sigOf(t, scope, "Wide")); v.OK != Yes {
		t.Fatalf("prefix overload: %+v", v)
	}
	// Without the wider sibling Wide does not satisfy the short overload.
	if v := Overload(env, short, []Call{short}, sigOf(t, scope, "Wide")); v.OK != No {
		t.Fatalf("lone overload: %+v", v)
	}
	// A required extra parameter is not a prefix extension.
	if PrefixOverload(short, call("void", str, Param{Name: "b", Type: "number"})) {
		t.Fatal("required extra parameter must not make a prefix overload")
	}
	// S10: each overload is satisfied by a different Go function.
	vs := OverloadSet(env, []Call{short, call("void", str, Param{Name: "b", Type: "number"})}, []*types.Signature{sigOf(t, scope, "Short"), sigOf(t, scope, "Wide")})
	if vs[0].OK != Yes || vs[1].OK != Yes {
		t.Fatalf("overload set: %+v", vs)
	}
	// S10: an overload no function takes stays a gap with a reason.
	vs = OverloadSet(env, []Call{short, call("void", str, Param{Name: "b", Type: "number"}, Param{Name: "c", Type: "number"}, Param{Name: "d", Type: "number"})}, []*types.Signature{sigOf(t, scope, "Short"), sigOf(t, scope, "Narrow")})
	if vs[0].OK != Yes || vs[1].OK != No || !strings.Contains(vs[1].Why, "S4") {
		t.Fatalf("uncovered overload: %+v", vs)
	}
	if vs := OverloadSet(env, []Call{short}, nil); vs[0].OK != No || !strings.Contains(vs[0].Why, "S10") {
		t.Fatalf("no candidates: %+v", vs)
	}
}

func TestIsAsync(t *testing.T) {
	for up, want := range map[string]bool{
		"Promise<void>": true, "Promise<string> | string": true, "AsyncIterable<number>": true, "AsyncGenerator<X, void>": true,
		"PromiseLike<void>": true, "void": false, "string[]": false,
	} {
		if got := IsAsync(up); got != want {
			t.Errorf("IsAsync(%q) = %v, want %v", up, got, want)
		}
	}
}

// TestS5cChannelResultKeepsThePromise: a Go channel result is judged against the whole Promise<T>, so the type rules see the
// asynchronous delivery; another result shape is judged against T.
func TestS5cChannelResultKeepsThePromise(t *testing.T) {
	var seen []string
	env := FuncEnv{Agree: func(up string, _ types.Type) Verdict { seen = append(seen, up); return Accept("") }}
	ch := types.NewTuple(types.NewVar(0, nil, "", types.NewChan(types.RecvOnly, types.Typ[types.String])))
	str := types.NewTuple(types.NewVar(0, nil, "", types.Typ[types.String]))
	Result(env, "Promise<Colors>", ch)
	Result(env, "Promise<Colors>", str)
	Result(env, "Colors | Promise<Colors>", ch)
	if want := []string{"Promise<Colors>", "Colors", "Colors"}; !slices.Equal(seen, want) {
		t.Errorf("Agree saw %q, want %q", seen, want)
	}
}

// TestS6kKeyedLookup: a non-nullable result looked up by a closed key may be the Go pair (T, bool); an open key, a non-bool second
// value or a decided result are left alone.
func TestS6kKeyedLookup(t *testing.T) {
	str := types.Typ[types.String]
	pair := func(second types.Type) *types.Tuple {
		return types.NewTuple(types.NewVar(0, nil, "", str), types.NewVar(0, nil, "", second))
	}
	env := FuncEnv{
		Agree: func(up string, t types.Type) Verdict {
			switch up = strings.TrimSpace(up); {
			case (up == "Def" || up == "Keybinding") && types.Identical(t, str), up == "boolean" && types.Identical(t, types.Typ[types.Bool]):
				return Accept("")
			}
			return Refute("T: %s", up)
		},
		ClosedKey: func(typ string) bool { return typ == "Keybinding" },
	}
	key := func(typ string) Call { return Call{Parameters: []Param{{Name: "k", Type: typ}}, Returns: "Def"} }
	generic := Call{TypeParameters: []TypeParam{{Name: "TId"}}, Parameters: []Param{{Name: "id", Type: "TId"}}, Returns: "Def"}
	for _, tc := range []struct {
		name string
		up   Call
		res  *types.Tuple
		want Tri
	}{
		{"closed key", key("Keybinding"), pair(types.Typ[types.Bool]), Yes},
		{"type parameter key", generic, pair(types.Typ[types.Bool]), Yes},
		{"open key", key("string"), pair(types.Typ[types.Bool]), Unknown},
		{"second value not a bool", key("Keybinding"), pair(types.Typ[types.Int]), Unknown},
		{"wrong value type", Call{Parameters: []Param{{Name: "k", Type: "Keybinding"}}, Returns: "Other"}, pair(types.Typ[types.Bool]), No},
		{"decided object result is kept", Call{Parameters: []Param{{Name: "k", Type: "Keybinding"}}, Returns: "{ n: Def; ok: boolean }"}, pair(types.Typ[types.Bool]), Yes},
	} {
		if got := keyedResult(env, tc.up, tc.res); got.OK != tc.want {
			t.Errorf("%s: got %v (%s), want %v", tc.name, got.OK, got.Why, tc.want)
		}
	}
	sig := types.NewSignatureType(nil, nil, nil, types.NewTuple(types.NewVar(0, nil, "k", str)), pair(types.Typ[types.Bool]), false)
	if got := Signature(env, key("Keybinding"), sig); got.OK != Yes {
		t.Errorf("Signature must apply S6k: got %v (%s)", got.OK, got.Why)
	}
}

// TestS1bSignalBagOption: an inline bag of an AbortSignal and one option is the Go context and that option when the bag itself
// does not match; a bag with two options, or a Go call without a context, does not.
func TestS1bSignalBagOption(t *testing.T) {
	scope := checkCall(t, `
func Suggest(ctx context.Context, prefix string, force bool) []string { return nil }
func NoCtx(prefix string, force bool) []string                       { return nil }
func Suggest2(ctx context.Context, prefix string, force bool, limit int) []string { return nil }
func SuggestVar(ctx context.Context, prefix string, force ...bool) []string      { return nil }
`)
	env := testEnv(scope)
	str := Param{Name: "prefix", Type: "string"}
	for _, tc := range []struct {
		name, bag, fn string
		want          Tri
	}{
		{"signal and one option", "{ signal: AbortSignal; force?: boolean }", "Suggest", Yes},
		{"readonly members", "{ readonly signal: AbortSignal; readonly force?: boolean }", "Suggest", Yes},
		{"two options", "{ signal: AbortSignal; force?: boolean; limit?: number }", "Suggest", Unknown},
		{"no signal", "{ force?: boolean }", "Suggest", Unknown},
		{"no context", "{ signal: AbortSignal; force?: boolean }", "NoCtx", Unknown},
		{"wrong option type", "{ signal: AbortSignal; force?: string }", "Suggest", Unknown},
		{"two options never flatten", "{ signal: AbortSignal; force?: boolean; limit?: number }", "Suggest2", No},
		{"an optional option is a variadic tail", "{ signal: AbortSignal; force?: boolean }", "SuggestVar", Yes},
		{"a required option is not", "{ signal: AbortSignal; force: boolean }", "SuggestVar", No},
	} {
		up := Call{Parameters: []Param{str, {Name: "options", Type: tc.bag}}, Returns: "string[]"}
		if got := Signature(env, up, sigOf(t, scope, tc.fn)); got.OK != tc.want {
			t.Errorf("%s: got %v (%s), want %v", tc.name, got.OK, got.Why, tc.want)
		}
	}
	if _, ok := signalBagRest("{ signal: AbortSignal; force }"); ok {
		t.Error("a member without a type is not read")
	}
}

// S13: Result<V, E> (durable/src/env/index.ts: { ok: true; value: V } | { ok: false; error: E }) is the Go results (V, error).
func TestOkErrorResult(t *testing.T) {
	scope := checkCall(t, `
func Void() error                       { return nil }
func Value() (string, error)            { return "", nil }
func ValueNoErr() string                { return "" }
func Extra() (string, int, error)       { return "", 0, nil }
func VoidValue() (string, error)        { return "", nil }
func Wrong() (int, error)               { return 0, nil }
type Result[V, E any] struct {
	Ok    bool
	Value V
	Error E
}
type Other struct{ Ok bool }
func Data() Result[string, error]       { return Result[string, error]{} }
func Partial() Other                    { return Other{} }
`)
	env := testEnv(scope)
	agree := env.Agree
	env.Agree = func(up string, t types.Type) Verdict {
		if up == "FileError" && types.Identical(t, types.Universe.Lookup("error").Type()) {
			return Accept("FileError is the Go error") // the failure arm of a Go Result struct is judged by its type argument
		}
		return agree(up, t)
	}
	env.Alias = func(name string) (string, bool) {
		if name == "Result" {
			return "{ ok: true; value: TValue } | { ok: false; error: TError }", true
		}
		return "", false
	}
	for _, tc := range []struct {
		name, returns, fn string
		want              Tri
		why               string
	}{
		{"void result is error alone", "Result<void, FileError>", "Void", Yes, ""},
		{"value result is (V, error)", "Result<string, FileError>", "Value", Yes, ""},
		{"promised result", "Promise<Result<string, FileError>>", "Value", Yes, ""},
		{"Go Result struct carries both arms", "Result<string, FileError>", "Data", Yes, ""},
		{"a struct without the arms is not Result", "Result<string, FileError>", "Partial", No, "S13"},
		// Mutations: the failure arm needs an error result, the value must agree, and a void result carries no value.
		{"no error result", "Result<string, FileError>", "ValueNoErr", No, "S13"},
		{"value of another type", "Result<string, FileError>", "Wrong", No, "result"},
		{"extra Go value", "Result<string, FileError>", "Extra", No, "S13"},
		{"void result with a Go value", "Result<void, FileError>", "VoidValue", No, "S13"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Result(env, tc.returns, sigOf(t, scope, tc.fn).Results())
			if got.OK != tc.want || !strings.Contains(got.Why, tc.why) {
				t.Fatalf("Result = %+v, want %v containing %q", got, tc.want, tc.why)
			}
		})
	}
	// Result is only the ok/error union when the alias says so: another Result alias is an ordinary named type.
	env.Alias = func(string) (string, bool) { return "{ code: number }", true }
	if got := Result(env, "Result<void, FileError>", sigOf(t, scope, "Void").Results()); got.OK == Yes {
		t.Fatalf("a Result alias that is not the ok/error union must not be read as (V, error): %+v", got)
	}
	env.Alias = nil
	if got := Result(env, "Result<void, FileError>", sigOf(t, scope, "Void").Results()); got.OK == Yes {
		t.Fatalf("without the alias body the rule cannot decide: %+v", got)
	}
}

// S4g: a function-typed property read through a Go getter. Pi agent.ts `convertToLlm`, `onResponse` and scroll-view.ts
// `scrollbarTrackStyle` are properties holding a function; Go reads them through a method that returns the function.
func TestS4gGetterReturnsTheFunctionProperty(t *testing.T) {
	scope := checkCall(t, `
func Style() func(text string) string              { return nil }
func Style2() func(a, b string) string             { return nil }
func Plain(text string) string                     { return "" }
func Count() int                                   { return 0 }
func Hook() func(data any, n int) error            { return nil }
`)
	env := testEnv(scope)
	for _, tc := range []struct {
		up   Call
		fn   string
		want Tri
	}{
		{Call{Parameters: []Param{{Name: "text", Type: "string"}}, Returns: "string"}, "Style", Yes},
		{Call{Parameters: []Param{{Name: "text", Type: "string"}}, Returns: "string"}, "Style2", No}, // arity of the returned function
		{Call{Parameters: []Param{{Name: "text", Type: "string"}}, Returns: "string"}, "Count", No},  // the result is not a function
		{Call{Parameters: []Param{{Name: "text", Type: "string"}}, Returns: "string"}, "Plain", Yes}, // a direct match is unchanged
		{Call{Returns: "string"}, "Style", No},                                                       // a parameterless upstream call is never a getter read
	} {
		got := Signature(env, tc.up, sigOf(t, scope, tc.fn))
		if got.OK != tc.want {
			t.Errorf("%v against %s: got %v (%s), want %v", tc.up, tc.fn, got.OK, got.Why, tc.want)
		}
	}
}

// S7v: Pi faux.ts getModel() and getModel(modelId) are two overloads; Go's GetModel(id ...string) is one variadic call.
func TestS7vVariadicSpansOverloads(t *testing.T) {
	scope := checkCall(t, `
func GetModel(id ...string) string        { return "" }
func Fixed(id string) string              { return "" }
func Count(id ...int) string              { return "" }
func Both(a string, rest ...string) string { return "" }
`)
	env := testEnv(scope)
	none := call("string")
	one := call("string | undefined", Param{Name: "modelId", Type: "string"})
	two := call("string", Param{Name: "a", Type: "string"}, Param{Name: "b", Type: "string"})
	for _, tc := range []struct {
		own  Call
		fn   string
		want Tri
	}{
		{one, "GetModel", Yes},  // the empty overload is satisfied; the tail takes modelId
		{none, "GetModel", Yes}, // unchanged
		{two, "Fixed", No},      // not variadic: nothing spans the overloads
		{one, "Count", No},      // the tail's type is not the parameter's
		{two, "Both", Yes},      // a leading parameter plus a tail, against the one-parameter overload
	} {
		got := Overload(env, tc.own, []Call{none, one, two}, sigOf(t, scope, tc.fn))
		if got.OK != tc.want {
			t.Errorf("%v against %s: got %v (%s), want %v", tc.own, tc.fn, got.OK, got.Why, tc.want)
		}
	}
}

// S5t: telemetry/src/index.ts startSpan<T>(options, callback: (span) => T | Promise<T>): Promise<T> and the sqlite
// transaction<T>(callback: () => T): T hand a value out of the callback; Go's callback returns an error and the caller
// captures the value.
func TestS5tCallbackResultIsCapturedByTheClosure(t *testing.T) {
	scope := checkCall(t, `
func StartSpan(name string, cb func(n int) error) error              { return nil }
func WrongCallback(name string, cb func(n int) error) string          { return "" }
func Get(name string) error                                           { return nil }
func Plain(name string, cb func(n int)) error                         { return nil }
`)
	env := testEnv(scope)
	generic := []TypeParam{{Name: "T"}}
	withCb := call("Promise<T>", Param{Name: "name", Type: "string"}, Param{Name: "callback", Type: "(n: number) => T | Promise<T>"})
	withCb.TypeParameters = generic
	noCb := call("Promise<T>", Param{Name: "name", Type: "string"})
	noCb.TypeParameters = generic
	concrete := call("Promise<number>", Param{Name: "name", Type: "string"}, Param{Name: "callback", Type: "(n: number) => number"})
	concrete.TypeParameters = generic
	for _, tc := range []struct {
		up   Call
		fn   string
		want Tri
	}{
		{withCb, "StartSpan", Yes},    // the callback returns an error; the value is captured
		{withCb, "WrongCallback", No}, // the call itself must still return only an error
		{noCb, "Get", No},             // no callback carries T: a generic result with no Go value is a real gap
		{concrete, "StartSpan", No},   // a concrete callback result is not T: S5t does not apply, and the Go value is missing
	} {
		got := Signature(env, tc.up, sigOf(t, scope, tc.fn))
		if got.OK != tc.want {
			t.Errorf("%v against %s: got %v (%s), want %v", tc.up, tc.fn, got.OK, got.Why, tc.want)
		}
	}
}

// S5v: ExtensionHandler<E, undefined> returns `Promise<undefined | void> | undefined | void` (types.ts:1548): every arm is empty,
// so the Go handler returns no value (an error only).
func TestS5vEmptyResultUnion(t *testing.T) {
	scope := checkCall(t, `
func Nothing() error     { return nil }
func Value() (int, error) { return 0, nil }
`)
	env := testEnv(scope)
	for _, tc := range []struct {
		returns, fn string
		want        Tri
	}{
		{"Promise<undefined | void> | undefined | void", "Nothing", Yes},
		{"undefined | void", "Nothing", Yes},
		{"Promise<undefined | void> | undefined | void", "Value", No}, // a value is not an empty result
		{"Promise<number | void> | number | void", "Nothing", No},     // a number arm needs a Go value
	} {
		got := Result(env, tc.returns, sigOf(t, scope, tc.fn).Results())
		if got.OK != tc.want {
			t.Errorf("%s against %s: got %v (%s), want %v", tc.returns, tc.fn, got.OK, got.Why, tc.want)
		}
	}
}

// S5m: tui autocomplete.ts:255 `type Awaitable<T> = T | Promise<T>` and server types.ts:14 `type MaybePromise<T> = T | Promise<T>`:
// the Go call is synchronous (or returns an error) and yields T.
func TestS5mMaybePromiseResult(t *testing.T) {
	scope := checkCall(t, `
func Items() []string               { return nil }
func Nothing() error                { return nil }
func ValueErr() (string, error)     { return "", nil }
`)
	env := testEnv(scope)
	for _, tc := range []struct {
		returns, fn string
		want        Tri
	}{
		{"Awaitable<string[]>", "Items", Yes},
		{"MaybePromise<void>", "Nothing", Yes},
		{"MaybePromise<string>", "ValueErr", Yes},
		{"MaybePromise<string>", "Nothing", No}, // a string must still come back
		{"Awaitable<number>", "Items", No},      // and agree with T
	} {
		got := Result(env, tc.returns, sigOf(t, scope, tc.fn).Results())
		if got.OK != tc.want {
			t.Errorf("%s against %s: got %v (%s), want %v", tc.returns, tc.fn, got.OK, got.Why, tc.want)
		}
	}
}

// S5p: telemetry testing.ts `[Symbol.asyncDispose](): PromiseLike<void>`: a PromiseLike result is awaited like a Promise, so the Go call
// returns the value (here none) with an optional error; a value it names must still agree.
func TestS5pPromiseLikeResult(t *testing.T) {
	scope := checkCall(t, `
func Nothing() error            { return nil }
func Items() []string           { return nil }
func Bare()                     {}
`)
	env := testEnv(scope)
	for _, tc := range []struct {
		returns, fn string
		want        Tri
	}{
		{"PromiseLike<void>", "Nothing", Yes},
		{"PromiseLike<void>", "Bare", Yes},
		{"PromiseLike<string[]>", "Items", Yes},
		{"PromiseLike<string>", "Nothing", No}, // a string must still come back
	} {
		got := Result(env, tc.returns, sigOf(t, scope, tc.fn).Results())
		if got.OK != tc.want {
			t.Errorf("%s against %s: got %v (%s), want %v", tc.returns, tc.fn, got.OK, got.Why, tc.want)
		}
	}
}

// S5a: validation.ts:317 validateToolArguments returns `any` and system-one-shared.ts:23 payload returns `unknown`; Go returns the
// concrete map the value is.
func TestS5aUnconstrainedResultTakesAnyGoValue(t *testing.T) {
	scope := checkCall(t, `
func Map() map[string]any   { return nil }
func Nothing() error        { return nil }
func Two() (int, string)    { return 0, "" }
`)
	env := testEnv(scope)
	for _, tc := range []struct {
		returns, fn string
		want        Tri
	}{
		{"any", "Map", Yes},
		{"unknown", "Map", Yes},
		{"any", "Nothing", No}, // a value must come back
		{"string", "Map", No},  // other result types keep their rules
	} {
		got := Result(env, tc.returns, sigOf(t, scope, tc.fn).Results())
		if got.OK == Yes != (tc.want == Yes) {
			t.Errorf("%s against %s: got %v (%s), want %v", tc.returns, tc.fn, got.OK, got.Why, tc.want)
		}
	}
}

// TestS1oSoleOptionalMember: an optional options bag of one optional member is the Go parameter of that member.
func TestS1oSoleOptionalMember(t *testing.T) {
	scope := checkCall(t, `
func Call(name string, id string) string { return name + id }
func Count(name string, n int) string   { return name }
`)
	env := testEnv(scope)
	str := Param{Name: "name", Type: "string"}
	for _, tc := range []struct {
		name, bag, fn string
		optional      bool
		want          Tri
	}{
		{"one optional member", "{ id?: string }", "Call", true, Yes},
		{"member type differs", "{ id?: string }", "Count", true, No},
		{"required member", "{ id: string }", "Call", true, No},
		{"two members", "{ id?: string; tag?: string }", "Call", true, No},
		{"required bag", "{ id?: string }", "Call", false, No},
		{"not an object", "string[]", "Call", true, No}, {"an array of bags", "{ id?: string }[]", "Call", true, No},
	} {
		up := Call{Parameters: []Param{str, {Name: "options", Type: tc.bag, Optional: tc.optional}}, Returns: "string"}
		if got := Signature(env, up, sigOf(t, scope, tc.fn)); (got.OK == Yes) != (tc.want == Yes) {
			t.Errorf("%s: got %v (%s), want %v", tc.name, got.OK, got.Why, tc.want)
		}
	}
	for _, typ := range []string{"{ id? }", "{ id?: string }[]", "{ id?: string } & Base"} {
		if _, ok := soleOptionalMember(typ); ok {
			t.Errorf("%s is not a one-member bag", typ)
		}
	}
	if mt, ok := soleOptionalMember("{ id?: string; }"); !ok || mt != "string" {
		t.Errorf("a trailing separator: got %q %v", mt, ok)
	}
}

// S5e: an upstream function that returns an Error value (durable/src/env/index.ts toError) is the Go function that returns error alone;
// a Go function that returns nothing, or a value besides the error, does not return it.
func TestErrorValueResult(t *testing.T) {
	scope := checkCall(t, `
func Make() error          { return nil }
func Nothing()             {}
func Pair() (string, error) { return "", nil }
`)
	env := testEnv(scope)
	for _, tc := range []struct {
		fn   string
		want Tri
	}{{"Make", Yes}, {"Nothing", No}, {"Pair", No}} {
		got := Result(env, "Error", sigOf(t, scope, tc.fn).Results())
		if (tc.want == Yes) != (got.OK == Yes) {
			t.Errorf("%s: Result = %+v, want accepted=%v", tc.fn, got, tc.want == Yes)
		}
	}
}

// TestS5sBooleanOrStringResultIsTheGoError: Pi tui-alt-screen.ts:198 `copySelection?: (text: string) => Promise<boolean | string>`
// returns true on success and the failure message otherwise (tui-alt-screen.ts:1473-1479); the Go func(string) error is that result only
// when the Go declaration documents and a test pins all three outcomes (env.ErrorUnionPinned). A Go function with no error, one that also
// returns a value, a bare bool, and an unpinned error are not the failure-message form.
func TestS5sBooleanOrStringResultIsTheGoError(t *testing.T) {
	scope := checkCall(t, `
func Copy(string) error            { return nil }
func Nothing(string)               {}
func WithValue() (string, error)   { return "", nil }
func Flag() bool                   { return true }
`)
	for _, tc := range []struct {
		fn     string
		pinned bool
		want   bool
	}{{"Copy", true, true}, {"Copy", false, false}, {"Nothing", true, false}, {"WithValue", true, false}, {"Flag", true, false}} {
		env := testEnv(scope)
		env.ErrorUnionPinned = tc.pinned
		got := Result(env, "Promise<boolean | string>", sigOf(t, scope, tc.fn).Results())
		if (got.OK == Yes) != tc.want {
			t.Errorf("%s pinned=%v: Result = %+v, want accepted=%v", tc.fn, tc.pinned, got, tc.want)
		}
	}
}

// S4f: fetch(input, init?) is the Go function over one *http.Request (the URL and every RequestInit field are request fields); another
// shape of Go function, or another upstream signature, is not fetch.
func TestFetchIsAnHTTPRoundTrip(t *testing.T) {
	httpPkg := types.NewPackage("net/http", "http")
	named := func(name string) *types.Named {
		tn := types.NewTypeName(token.NoPos, httpPkg, name, nil)
		return types.NewNamed(tn, types.NewStruct(nil, nil), nil)
	}
	req, resp := types.NewPointer(named("Request")), types.NewPointer(named("Response"))
	errT := types.Universe.Lookup("error").Type()
	sig := func(params, results []*types.Var) *types.Signature {
		return types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), types.NewTuple(results...), false)
	}
	v := func(t types.Type) *types.Var { return types.NewVar(token.NoPos, nil, "", t) }
	good := sig([]*types.Var{v(req)}, []*types.Var{v(resp), v(errT)})
	noErr := sig([]*types.Var{v(req)}, []*types.Var{v(resp)})
	twoParams := sig([]*types.Var{v(req), v(req)}, []*types.Var{v(resp), v(errT)})
	if !isHTTPRoundTrip(good) || isHTTPRoundTrip(noErr) || isHTTPRoundTrip(twoParams) {
		t.Fatal("only func(*http.Request) (*http.Response, error) is a round trip")
	}
	fetch := Call{Parameters: []Param{{Name: "input", Type: "RequestInfo | URL"}, {Name: "init", Type: "RequestInit", Optional: true}}, Returns: "Promise<Response>"}
	if !isFetchCall(fetch) {
		t.Fatal("the WHATWG fetch signature is fetch")
	}
	other := fetch
	other.Returns = "Promise<string>"
	required := Call{Parameters: []Param{{Name: "input", Type: "string"}, {Name: "init", Type: "RequestInit"}}, Returns: "Promise<Response>"}
	if isFetchCall(other) || isFetchCall(required) {
		t.Fatal("another result or a required init is not fetch")
	}
	env := testEnv(types.NewScope(nil, token.NoPos, token.NoPos, ""))
	if got := Signature(env, fetch, good); got.OK != Yes {
		t.Fatalf("Signature = %+v, want accepted", got)
	}
	if got := Signature(env, fetch, noErr); got.OK == Yes {
		t.Fatalf("a round trip without an error result must not be accepted: %+v", got)
	}
}

// TestOptionSetters: S9p accepts trailing optional positional parameters (Pi user-message.ts:19-24 `constructor(text, markdownTheme =
// ..., outputPad = 1, ...)`) against a Go variadic of a functional option type func(*T) when every parameter has one With...<Param>
// function returning the option whose argument agrees. A parameter of another type, a required parameter, a parameter without a
// setter, two setters for one parameter, a setter that returns another option type and an option over a value rather than a
// pointer are not accepted by S9p.
func TestOptionSetters(t *testing.T) {
	scope := checkCall(t, `
type Msg struct{ pad int; theme string }
type MsgOption func(*Msg)
func WithMsgOutputPad(n int) MsgOption { return nil }
func WithMsgTheme(s string) MsgOption  { return nil }
func WithMsgLabel(s string) Other      { return nil }
func NewMsg(text string, opts ...MsgOption) {}
type Other func(*Msg)
func WithOtherOutputPad(n int) Other   { return nil }
func WithSecondOutputPad(n int) Other  { return nil }
func NewOther(text string, opts ...Other) {}
type ByValue func(Msg)
func WithByValueOutputPad(n int) ByValue { return nil }
func NewByValue(text string, opts ...ByValue) {}
`)
	env := testEnv(scope)
	str := Param{Name: "text", Type: "string"}
	theme := Param{Name: "theme", Type: "string", Optional: true}
	pad := Param{Name: "outputPad", Type: "number", Optional: true}
	for _, tc := range []struct {
		name string
		up   Call
		fn   string
		want bool
	}{
		{"every optional parameter has its setter", call("void", str, theme, pad), "NewMsg", true},
		{"a parameter of another type", call("void", str, theme, Param{Name: "outputPad", Type: "boolean", Optional: true}), "NewMsg", false},
		{"a required parameter without a setter", call("void", str, Param{Name: "transformers", Type: "number"}), "NewMsg", false},
		{"a parameter without a setter", call("void", str, pad, Param{Name: "transformers", Type: "string[]", Optional: true}), "NewMsg", false},
		{"two setters for one parameter", call("void", str, pad), "NewOther", false},
		{"a setter of another option type", call("void", str, Param{Name: "label", Type: "string", Optional: true}), "NewMsg", false},
		{"an option that does not take a pointer", call("void", str, pad), "NewByValue", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Signature(env, tc.up, sigOf(t, scope, tc.fn))
			if (got.OK == Yes) != tc.want {
				t.Fatalf("Signature = %+v, want accepted %v", got, tc.want)
			}
		})
	}
}

// TestOptionStruct: S9s accepts trailing optional positional parameters (Pi env/src/ssh.ts:77-81 `sshArguments(target, strictHostKeys =
// true, knownHostsFile = target.knownHostsFile)`) against one optional Go options struct in a variadic tail, with a field or pointer
// field named after each parameter whose type agrees. A parameter of another type, a required parameter, a rest parameter and a parameter
// without a field are not accepted by S9s.
func TestOptionStruct(t *testing.T) {
	scope := checkCall(t, `
type ArgsOptions struct{ StrictHostKeys *bool; KnownHostsFile *string }
type Plain struct{ StrictHostKeys bool }
func Args(target string, options ...ArgsOptions) {}
func PlainArgs(target string, options ...Plain) {}
`)
	env := testEnv(scope)
	target := Param{Name: "target", Type: "string"}
	strict := Param{Name: "strictHostKeys", Type: "boolean", Optional: true}
	known := Param{Name: "knownHostsFile", Type: "string", Optional: true}
	for _, tc := range []struct {
		name string
		up   Call
		fn   string
		want bool
	}{
		{"pointer fields stand for the parameters", call("void", target, strict, known), "Args", true},
		{"a plain field stands for the parameter", call("void", target, strict), "PlainArgs", true},
		{"a parameter of another type", call("void", target, strict, Param{Name: "knownHostsFile", Type: "number", Optional: true}), "Args", false},
		{"a required parameter", call("void", target, Param{Name: "strictHostKeys", Type: "boolean"}), "Args", false},
		{"a rest parameter", call("void", target, Param{Name: "strictHostKeys", Type: "boolean", Optional: true, Rest: true}), "Args", false},
		{"a parameter without a field", call("void", target, strict, Param{Name: "port", Type: "boolean", Optional: true}), "Args", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Signature(env, tc.up, sigOf(t, scope, tc.fn))
			if (got.OK == Yes) != tc.want {
				t.Fatalf("Signature = %+v, want accepted %v", got, tc.want)
			}
		})
	}
}

// TestS3wRequiredParameterIsCarriedByItsOption: Pi image.ts:30 `constructor(base64Data, mimeType, theme: ImageTheme, options?, dimensions?)`
// takes a required theme, which Go cannot place before the variadic options; LEAD-ANSWERS-ledger 3 ports it as the With...Theme option. A
// required parameter without a setter, a setter of another type, two setters, a setter of another option type and a constructor with no
// variadic option are not accepted by S3w.
func TestS3wRequiredParameterIsCarriedByItsOption(t *testing.T) {
	scope := checkCall(t, `
type Pic struct{}
type PicOption func(*Pic)
func WithPicTheme(s string) PicOption { return nil }
func NewPic(data string, opts ...PicOption) {}
type Two func(*Pic)
func WithTwoTheme(s string) Two  { return nil }
func WithOtherTheme(s string) Two { return nil }
func NewTwo(data string, opts ...Two) {}
func NewPlain(data string, theme string) {}
`)
	env := testEnv(scope)
	data := Param{Name: "data", Type: "string"}
	for _, tc := range []struct {
		name string
		up   Call
		fn   string
		want bool
	}{
		{"the required parameter has its option", call("void", data, Param{Name: "theme", Type: "string"}), "NewPic", true},
		{"the option takes another type", call("void", data, Param{Name: "theme", Type: "number"}), "NewPic", false},
		{"a required parameter without an option", call("void", data, Param{Name: "label", Type: "string"}), "NewPic", false},
		{"two options for one parameter", call("void", data, Param{Name: "theme", Type: "string"}), "NewTwo", false},
		{"no variadic option", call("void", data, Param{Name: "theme", Type: "string"}), "NewPlain", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Signature(env, tc.up, sigOf(t, scope, tc.fn))
			if (got.OK == Yes) != tc.want {
				t.Fatalf("Signature = %+v, want accepted %v", got, tc.want)
			}
		})
	}
}

// Pi tui keys.ts:195-236 `ctrl: <K extends BaseKey>(key: K): `ctrl+${K}` => ...`: a generic function type is a function type, its parameter typed by a
// bounded type parameter is typed by the bound, and its type parameters are recorded. An unbounded or composite parameter type is left as written, and a
// non-generic type has no type parameters.
func TestGenericFunctionTypeIsParsedWithBoundedTypeParameters(t *testing.T) {
	text := "<K extends BaseKey>(key: K) => `ctrl+${K}`"
	if !IsFuncType(text) {
		t.Fatalf("IsFuncType(%q) = false", text)
	}
	call, ok := ParseFuncType(text)
	if !ok || len(call.Parameters) != 1 || call.Parameters[0].Type != "BaseKey" || call.Returns != "`ctrl+${K}`" {
		t.Fatalf("ParseFuncType(%q) = %+v, %v; want one parameter typed by its bound BaseKey", text, call, ok)
	}
	if len(call.TypeParameters) != 1 || call.TypeParameters[0].Name != "K" || call.TypeParameters[0].Constraint != "BaseKey" {
		t.Fatalf("type parameters = %+v, want K extends BaseKey", call.TypeParameters)
	}
	if c, _ := ParseFuncType("<T>(value: T, list: T[]) => T"); c.Parameters[0].Type != "T" || c.Parameters[1].Type != "T[]" || len(c.TypeParameters) != 1 {
		t.Errorf("unbounded generic parsed as %+v", c)
	}
	if rest, params := CutTypeParams("(a: string) => void"); rest != "(a: string) => void" || params != nil {
		t.Errorf("CutTypeParams of a plain function type = %q, %v", rest, params)
	}
	if !IsFuncType("<K>(key: K) => K") || IsFuncType("<K> string") {
		t.Errorf("IsFuncType generic handling wrong")
	}
}
