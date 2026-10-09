package rules

import (
	"go/types"
	"regexp"
	"slices"
	"strings"
)

// Functions and options rule family.
//
// Upstream calls are recorded as parameter and result text; Go calls are go/types signatures. The rules are:
//
//	S1  an AbortSignal parameter is a leading context.Context; an options bag whose only member is a signal is the context too
//	S2  the other parameters correspond one to one, in order, and their types agree (FuncEnv.Agree)
//	S2a a Record<string, unknown> parameter (optionally | undefined) is a Go any parameter, which accepts every record value; a result is not judged this way
//	S2g a predicate over one unknown input (`f(value: unknown): boolean`, a type guard) is a Go func(value X) bool
//	S3  a Go variadic tail takes the trailing optional or rest parameters; a trailing array, required or optional, is the variadic slice itself
//	S5a an upstream result of type any or unknown is any one Go value: the caller reads it through the Go type that refines it
//	S5t a generic call whose callback returns the call's type parameter T: Go's callback returns an error and the caller captures the value
//	S4g an upstream function-typed property read through a Go getter (no parameters, one function result) is checked against that function
//	S4  a parameter count that differs otherwise is a gap: omitted optional parameters lose a capability, extra Go parameters are a
//	    different signature
//	S5  a Promise<T> or PromiseLike<T> result is the value T with an optional trailing error; void, undefined and never are no value
//	S5b an object-literal result is the Go return values in field order; a field declared as a method (`remote(): T`) is a Go func() T
//	S5c a single Go channel result is judged against the whole Promise<T>
//	S6  T | undefined and T | null are T, *T or (T, bool)
//	S6k a T looked up by a closed key (a type parameter, keyof X or a literal union) may be (T, bool): Go reports the miss that
//	    TypeScript's key type rules out at compile time
//	S7v a Go variadic call stands for overloads that differ by trailing required parameters: the shorter one is satisfied and the tail takes the rest
//	S7  an overload whose parameters are a prefix of a wider overload that the Go signature satisfies is covered by it
//	S8  an options-bag parameter is spread over Go positional parameters named after its members; every member, optional or not,
//	    needs one, since an omitted optional member loses a capability as an omitted optional parameter does (S4)
//	S9  a trailing optional options bag is a Go variadic of functional options func(*T), where T has a field for every member
//	S9p trailing optional positional parameters are a Go variadic of a functional option type with one With...<Param> setter each
//	S9s trailing optional positional parameters are an optional Go options struct with a field (or pointer field) per parameter
//	S3w a required upstream parameter that the variadic functional option type sets with one With...<Param> function is carried by that option
//	S10 an overload set is covered when every overload is satisfied by some Go function of the candidate set
//	S11 a callback is a Go func type with the same call rules; a one-method interface stands for a function type

// TypeParam is a generic parameter of a declaration or call signature.
type TypeParam struct {
	Name string `json:"name"`
	// Constraint is the `extends` bound of a type parameter read from a generic function type's text; empty when unbounded or unrecorded.
	Constraint string `json:"constraint,omitempty"`
}

// Param is one upstream parameter as recorded by the inventory.
type Param struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Optional bool   `json:"optional"`
	Rest     bool   `json:"rest"`
}

// Call is one upstream call or construct signature.
type Call struct {
	TypeParameters []TypeParam `json:"typeParameters"`
	Parameters     []Param     `json:"parameters"`
	Returns        string      `json:"returns"`
}

// FuncEnv is what the engine supplies to the function rules.
type FuncEnv struct {
	// Agree decides whether an upstream value type and a Go type agree (the type-shape families own it).
	Agree func(up string, t types.Type) Verdict
	// SignalBag reports whether the named upstream type is an options bag whose only member is an AbortSignal.
	SignalBag func(name string) bool
	// ContextParam reports whether an upstream parameter type travels in Go's context.Context: chord's Context (S1c) or the coding-agent
	// extension context (S1e).
	ContextParam func(typ string) bool
	// Bag lists the members of an upstream options-bag type.
	Bag func(typ string) ([]Param, bool)
	// ClosedKey reports whether an upstream parameter type is a closed set of keys (`keyof X`, a string-literal union or an alias of
	// one), which TypeScript checks at compile time and Go checks at run time.
	ClosedKey func(typ string) bool
	// Alias returns the body of an upstream type alias visible from the package of the row.
	Alias func(name string) (string, bool)
	// ErrorUnionPinned is set only when the Go declaration of the call documents and a test pins all three outcomes of a
	// `boolean | string` result as its error return (S5s).
	ErrorUnionPinned bool
}

// ParseFuncType reads `(a: A, b?: B) => R` into a call.
func ParseFuncType(up string) (Call, bool) {
	up, typeParams := CutTypeParams(strings.TrimSpace(up))
	up = strings.TrimPrefix(up, "new ")
	if !strings.HasPrefix(up, "(") {
		return Call{}, false
	}
	depth := 0
	for i, r := range up {
		switch {
		case r == '>' && i > 0 && up[i-1] == '=':
			// the arrow of a nested function type closes nothing
		case strings.ContainsRune("([{<", r):
			depth++
		case strings.ContainsRune(")]}>", r):
			depth--
			if depth == 0 && r == ')' {
				rest := strings.TrimSpace(up[i+1:])
				ret, ok := strings.CutPrefix(rest, "=>")
				if !ok {
					return Call{}, false
				}
				call := Call{Returns: strings.TrimSpace(ret), TypeParameters: typeParams}
				if inner := strings.TrimSpace(up[1:i]); inner != "" {
					for _, p := range SplitTop(inner, ",") {
						if strings.TrimSpace(p) == "" {
							continue // the trailing comma of a parameter list written over several lines
						}
						name, typ, _ := strings.Cut(p, ":")
						pr := Param{Name: strings.TrimSpace(name), Type: boundTypeParams(strings.TrimSpace(typ), typeParams)}
						if n, ok := strings.CutSuffix(pr.Name, "?"); ok {
							pr.Name, pr.Optional = n, true
						}
						if n, ok := strings.CutPrefix(pr.Name, "..."); ok {
							pr.Name, pr.Rest = n, true
						}
						call.Parameters = append(call.Parameters, pr)
					}
				}
				return call, true
			}
		}
	}
	return Call{}, false
}

// boundTypeParams replaces a parameter type that is exactly a bounded type parameter by its bound (`key: K` with `K extends BaseKey` is
// `key: BaseKey`): a Go value of the bound's type is what the function takes. An unbounded or composite type is left as written.
func boundTypeParams(typ string, params []TypeParam) string {
	for _, tp := range params {
		if tp.Constraint != "" && typ == tp.Name {
			return tp.Constraint
		}
	}
	return typ
}

// FuncSignature returns the signature that stands for a Go callback type (S11): a func type, or an interface with one method.
func FuncSignature(t types.Type) *types.Signature {
	if sig, ok := t.Underlying().(*types.Signature); ok {
		return sig
	}
	return singleMethod(t)
}

func singleMethod(t types.Type) *types.Signature {
	it, ok := t.Underlying().(*types.Interface)
	if !ok || it.NumMethods() != 1 {
		return nil
	}
	return it.Method(0).Type().(*types.Signature)
}

// Callback applies S11 to an upstream function type and a Go type.
func Callback(env FuncEnv, up string, t types.Type) Verdict {
	sig := FuncSignature(t)
	if sig == nil {
		return Undecided("S11: function type %q against Go type %s", up, typeLabel(t))
	}
	call, ok := ParseFuncType(up)
	if !ok {
		return Undecided("S11: cannot parse function type %q", up)
	}
	return Signature(env, call, sig)
}

// signalBagRest reads an inline object type of an AbortSignal member and at most one other member (`{ signal: AbortSignal;
// force?: boolean }`) and returns the other member as a parameter.
func signalBagRest(typ string) ([]Param, bool) {
	body := strings.TrimSpace(typ)
	if !strings.HasPrefix(body, "{") || !strings.HasSuffix(body, "}") {
		return nil, false
	}
	var rest []Param
	signal := false
	for _, part := range SplitTop(strings.ReplaceAll(body[1:len(body)-1], ";", ","), ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		name, typ, ok := strings.Cut(part, ":")
		if !ok {
			return nil, false
		}
		name, typ = strings.TrimSpace(name), strings.TrimSpace(typ)
		opt := strings.HasSuffix(name, "?")
		if IsSignalType(typ) {
			signal = true
			continue
		}
		rest = append(rest, Param{Name: strings.TrimSuffix(name, "?"), Type: typ, Optional: opt})
	}
	return rest, signal && len(rest) <= 1
}

// Signature applies S1-S9 to an upstream call and a Go signature. When that fails, S1b retries with each inline bag of an
// AbortSignal and at most one option replaced by that option: the Go call takes the context and the option itself.
func Signature(env FuncEnv, up Call, sig *types.Signature) Verdict {
	up.Returns = promiseLikeAsPromise(up.Returns) // S5l
	up = typeGuard(up)
	if fv, ok := optionCarried(env, up, sig); ok {
		return fv // S3w
	}
	v := signature(env, up, sig)
	if fv, ok := accessorSignature(env, up, sig); ok && v.OK != Yes {
		return fv // S4g
	}
	if v.OK != Yes {
		if cv, ok := closureResult(env, up, sig); ok {
			return cv // S5t
		}
		if isFetchCall(up) && isHTTPRoundTrip(sig) {
			return Accept("S4f") // S4f: fetch(input, init?) is one *http.Request, which carries the URL and every RequestInit field
		}
	}
	flat := Call{TypeParameters: up.TypeParameters, Returns: up.Returns}
	changed := false
	for _, p := range up.Parameters {
		if rest, ok := signalBagRest(p.Type); ok {
			flat.Parameters = append(flat.Parameters, Param{Name: p.Name, Type: "AbortSignal"})
			flat.Parameters = append(flat.Parameters, rest...)
			changed = true
			continue
		}
		flat.Parameters = append(flat.Parameters, p)
	}
	if changed {
		if fv := signature(env, flat, sig); fv.OK == Yes {
			return fv // S1b
		}
	}
	return v
}

// closureResult applies S5t: a generic upstream call `f<T>(cb: (...) => T | Promise<T>): Promise<T>` hands the value of T from the
// callback to the caller. Go has no generic methods, so the callback returns at most an error and the caller keeps the value in
// a variable the closure captures. The call is judged again with T's result replaced by void in the callback and in the call.
func closureResult(env FuncEnv, up Call, sig *types.Signature) (Verdict, bool) {
	ret := strings.TrimSpace(up.Returns)
	if inner, ok := strings.CutPrefix(ret, "Promise<"); ok {
		ret = strings.TrimSuffix(inner, ">")
	}
	isParam := false
	for _, tp := range up.TypeParameters {
		isParam = isParam || tp.Name == ret
	}
	if !isParam {
		return Verdict{}, false
	}
	changed := Call{TypeParameters: up.TypeParameters, Returns: "void"}
	found := false
	for _, p := range up.Parameters {
		if cb, ok := ParseFuncType(p.Type); ok && onlyResultOf(cb.Returns, ret) {
			p.Type = p.Type[:strings.LastIndex(p.Type, "=>")] + "=> void"
			found = true
		}
		changed.Parameters = append(changed.Parameters, p)
	}
	if !found {
		return Verdict{}, false
	}
	v := Signature(env, changed, sig)
	return v, v.OK == Yes
}

// onlyResultOf reports whether a result is the type parameter name, or the Promise of it, or either alternative of the two.
func onlyResultOf(returns, name string) bool {
	for _, m := range SplitTop(strings.TrimSpace(returns), "|") {
		if m != name && m != "Promise<"+name+">" {
			return false
		}
	}
	return true
}

// accessorSignature applies S4g: an upstream function-typed property is read through a Go getter, a method with no parameters
// whose one result is the function. The upstream call is then checked against that function's signature. It applies only
// when the upstream call takes parameters, so a parameterless upstream method is never matched to a getter.
func accessorSignature(env FuncEnv, up Call, sig *types.Signature) (Verdict, bool) {
	if len(up.Parameters) == 0 || sig.Params().Len() != 0 || sig.Results().Len() != 1 {
		return Verdict{}, false
	}
	fn, ok := sig.Results().At(0).Type().Underlying().(*types.Signature)
	if !ok {
		return Verdict{}, false
	}
	v := Signature(env, up, fn)
	return v, v.OK == Yes
}

func signature(env FuncEnv, up Call, sig *types.Signature) Verdict {
	var want []Param
	signal := false
	for _, p := range up.Parameters {
		if IsSignalType(p.Type) || env.SignalBag != nil && env.SignalBag(strings.TrimSpace(strings.TrimSuffix(p.Type, "| undefined"))) ||
			env.ContextParam != nil && env.ContextParam(strings.TrimSpace(strings.TrimSuffix(p.Type, "| undefined"))) {
			signal = true // S1, S1c, S1e
			continue
		}
		want = append(want, p)
	}
	var got []types.Type
	var vars []*types.Var
	for v := range sig.Params().Variables() {
		got = append(got, v.Type())
		vars = append(vars, v)
	}
	ctx := len(got) > 0 && IsContext(got[0])
	if ctx {
		got = got[1:]
		vars = vars[1:]
	}
	if signal && !ctx {
		return Refute("S1: upstream takes an AbortSignal, Go signature has no context.Context")
	}
	variadic := sig.Variadic() && len(got) > 0
	fixed := len(got)
	if variadic {
		fixed--
	}
	pairs := min(len(want), fixed)
	if len(want) < fixed || len(want) > fixed && !variadic {
		if fv := flattened(env, want, vars, variadic); fv.OK != No {
			return All(fv, Result(env, up.Returns, sig.Results()))
		}
		return Refute("S4: upstream takes %d parameters, Go takes %d", len(want), len(got))
	}
	vs := make([]Verdict, 0, len(want)+1)
	for i := range pairs {
		vs = append(vs, paramVerdict(env, want[i], got[i]))
	}
	if env.Bag != nil && !variadic && All(vs...).OK != Yes {
		// S8b: the counts agree but an options bag is not the Go parameter at its position, as a one-member bag lands on that member's
		// value (`install(source, { local?: boolean })` against `Install(source string, local bool)`): spread the bag as S8 does.
		if fv := flattened(env, want, vars, false); fv.OK == Yes {
			return All(fv, keyedResult(env, up, sig.Results()))
		}
	}
	if variadic {
		tail := got[len(got)-1] // the variadic parameter is a slice
		elem := tail
		if s, ok := tail.(*types.Slice); ok {
			elem = s.Elem()
		}
		rest := want[pairs:]
		switch {
		case len(rest) == 1 && !rest[0].Rest && arrayRe.MatchString(strings.TrimSpace(rest[0].Type)):
			vs = append(vs, paramVerdict(env, rest[0], tail)) // S3: a trailing array, optional as `images?: ImageContent[]` too, is the variadic slice (lead ruling for Agent.prompt)
		case len(rest) == 1 && rest[0].Optional && !rest[0].Rest && functionalOptions(env, rest[0], elem) != nil:
			vs = append(vs, *functionalOptions(env, rest[0], elem)) // S9
		case optionSetters(env, rest, elem) != nil:
			vs = append(vs, *optionSetters(env, rest, elem)) // S9p
		case optionStruct(env, rest, elem) != nil:
			vs = append(vs, *optionStruct(env, rest, elem)) // S9s
		default:
			for _, p := range rest {
				if emptyInterface(elem) {
					continue // S3d
				}
				if !p.Optional && !p.Rest {
					return Refute("S3: required upstream parameter %s lands in a Go variadic tail", p.Name)
				}
				vs = append(vs, paramVerdict(env, p, elem))
			}
		}
	}
	vs = append(vs, keyedResult(env, up, sig.Results()))
	return All(vs...)
}

// keyedResult applies Result, and S6k when the call takes a closed key: a non-nullable T may be the Go pair (T, bool).
func keyedResult(env FuncEnv, up Call, results *types.Tuple) Verdict {
	v := Result(env, up.Returns, results)
	if v.OK != Unknown || results.Len() != 2 {
		return v
	}
	if b, ok := results.At(1).Type().Underlying().(*types.Basic); !ok || b.Info()&types.IsBoolean == 0 {
		return v
	}
	for _, p := range up.Parameters {
		typ := strings.TrimSpace(p.Type)
		closed := env.ClosedKey != nil && env.ClosedKey(typ)
		for _, tp := range up.TypeParameters {
			closed = closed || tp.Name == typ
		}
		if closed {
			return wrapResult(env.Agree(up.Returns, results.At(0).Type())) // S6k
		}
	}
	return v
}

func paramVerdict(env FuncEnv, p Param, t types.Type) Verdict {
	typ := p.Type
	if p.Rest {
		typ = strings.TrimSuffix(typ, "[]")
	}
	if typ == guardInput {
		return Accept("S2g: a type guard's unknown input is the Go value it inspects")
	}
	if iface, ok := t.Underlying().(*types.Interface); ok && iface.Empty() && recordOfUnknown(typ) {
		return Accept("S2a: a Go any parameter accepts every Record<string, unknown> value")
	}
	v := env.Agree(typ, t)
	if v.OK != Yes && p.Optional {
		if mt, ok := soleOptionalMember(typ); ok && env.Agree(mt, t).OK == Yes {
			return Accept("S1o") // an options bag of one optional member is the Go parameter of that member; zero is absent
		}
	}
	if v.OK != Yes {
		v.Why = "parameter " + p.Name + ": " + v.Why
		return v
	}
	return Accept("")
}

// guardInput marks the unknown parameter of a type guard once typeGuard has rewritten the call.
const guardInput = "unknown#guard"

// typeGuard applies S2g: a predicate over an unknown value, `f(value: unknown): boolean` or `f(value: unknown): value is T`, is a Go
// func(value X) bool. The inventory records a type predicate as boolean, so the two read alike. The unknown input is checked at run
// time upstream and Go checks the value of the type the guard inspects; the predicate's narrowing is compile-time only
// (LEAD-RULINGS-1520 unblock-types 2 reads type guards as language mechanics). Only a call with one unknown parameter and a boolean
// result is read this way; the call is returned with the input marked.
func typeGuard(up Call) Call {
	ret := strings.TrimSpace(up.Returns)
	if _, rest, isPredicate := strings.Cut(ret, " is "); isPredicate {
		if strings.TrimSpace(rest) == "" {
			return up
		}
	} else if ret != "boolean" {
		return up
	}
	if len(up.Parameters) != 1 || strings.TrimSpace(up.Parameters[0].Type) != "unknown" || up.Parameters[0].Optional || up.Parameters[0].Rest {
		return up
	}
	p := up.Parameters[0]
	p.Type = guardInput
	up.Parameters, up.Returns = []Param{p}, "boolean"
	return up
}

// recordOfUnknown reports whether typ is Record<string, unknown>, alone or with undefined or null.
func recordOfUnknown(typ string) bool {
	for _, part := range SplitTop(typ, "|") {
		switch strings.Join(strings.Fields(part), "") {
		case "Record<string,unknown>", "undefined", "null":
		default:
			return false
		}
	}
	return true
}

// soleOptionalMember returns the type of the only member of an inline object type `{ name?: T }`.
func soleOptionalMember(typ string) (string, bool) {
	body := strings.TrimSpace(typ)
	if !strings.HasPrefix(body, "{") || !strings.HasSuffix(body, "}") {
		return "", false
	}
	var parts []string
	for _, part := range SplitTop(strings.ReplaceAll(body[1:len(body)-1], ";", ","), ",") {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) != 1 {
		return "", false
	}
	name, mt, ok := strings.Cut(parts[0], ":")
	if !ok || !strings.HasSuffix(strings.TrimSpace(name), "?") {
		return "", false
	}
	return strings.TrimSpace(mt), true
}

// functionalOptions applies S9. It returns nil when elem is not a functional option (a func taking a pointer to a struct) or the
// upstream parameter is not a recognised options bag, so that the caller falls back to the plain variadic rule.
func functionalOptions(env FuncEnv, bag Param, elem types.Type) *Verdict {
	sig := FuncSignature(elem)
	if sig == nil || sig.Params().Len() != 1 || sig.Results().Len() != 0 || env.Bag == nil {
		return nil
	}
	ptr, ok := sig.Params().At(0).Type().(*types.Pointer)
	if !ok {
		return nil
	}
	st, ok := ptr.Elem().Underlying().(*types.Struct)
	if !ok {
		return nil
	}
	members, ok := env.Bag(bag.Type)
	if !ok {
		return nil
	}
	var vs []Verdict
	for _, m := range members {
		field := structField(st, m.Name)
		if field == nil {
			v := Refute("S9: options member %s has no field in the Go options struct", m.Name)
			return &v
		}
		if m.Type != "" {
			vs = append(vs, env.Agree(m.Type, field.Type()))
		}
	}
	v := All(vs...)
	return &v
}

// optionSetters applies S9p: trailing optional positional parameters (`constructor(text, theme = t, outputPad = 1)`) are a Go variadic of
// a named functional option type, func(*T), when the option's package declares, for every such parameter, one function named
// With...<Param> that takes a value agreeing with the parameter and returns the option. It returns nil when a parameter is required
// or a rest parameter, the element is not such an option type, or a parameter has no setter, so the caller falls back to the plain
// variadic rule.
func optionSetters(env FuncEnv, rest []Param, elem types.Type) *Verdict {
	named, ok := types.Unalias(elem).(*types.Named)
	if !ok || named.Obj().Pkg() == nil || len(rest) == 0 {
		return nil
	}
	sig := FuncSignature(elem)
	if sig == nil || sig.Params().Len() != 1 || sig.Results().Len() != 0 {
		return nil
	}
	if _, ok := sig.Params().At(0).Type().(*types.Pointer); !ok {
		return nil
	}
	vs := make([]Verdict, 0, len(rest))
	for _, p := range rest {
		if !p.Optional || p.Rest {
			return nil
		}
		setter := optionSetter(named, p.Name)
		if setter == nil {
			return nil
		}
		vs = append(vs, env.Agree(p.Type, setter.Params().At(0).Type()))
	}
	v := All(vs...)
	if v.OK == Yes {
		v = Accept("S9p: the trailing optional parameters are functional options set by their With functions")
	}
	return &v
}

// optionSetter finds the function that sets the upstream parameter name on the functional option type named: exactly one exported function
// of its package called With...<Name> that takes one value and returns named. It returns nil when there is none or more than one, because
// the rule cannot tell which stands for the parameter.
func optionSetter(named *types.Named, name string) *types.Signature {
	scope := named.Obj().Pkg().Scope()
	var setter *types.Signature
	for _, fname := range scope.Names() {
		fn, ok := scope.Lookup(fname).(*types.Func)
		if !ok || !fn.Exported() || !strings.HasPrefix(fname, "With") || !strings.HasSuffix(fname, upperFirst(name)) {
			continue
		}
		fs := fn.Type().(*types.Signature)
		if fs.Recv() == nil && fs.Params().Len() == 1 && fs.Results().Len() == 1 && types.Identical(fs.Results().At(0).Type(), named) {
			if setter != nil {
				return nil
			}
			setter = fs
		}
	}
	return setter
}

// optionCarried applies S3w (LEAD-ANSWERS-ledger 3: a constructor parameter Go cannot place positionally is a functional option): a required
// upstream parameter (Pi image.ts:30 `constructor(base64Data, mimeType, theme: ImageTheme, options?, dimensions?)`) that the variadic functional
// option type sets with one With...<Param> function is carried by that option, wherever it sits in the upstream list. The remaining parameters
// are judged against the Go signature as before. It decides only a call with at least one carried parameter.
func optionCarried(env FuncEnv, up Call, sig *types.Signature) (Verdict, bool) {
	if !sig.Variadic() || sig.Params().Len() == 0 {
		return Verdict{}, false
	}
	tail, ok := sig.Params().At(sig.Params().Len() - 1).Type().(*types.Slice)
	if !ok {
		return Verdict{}, false
	}
	named, ok := types.Unalias(tail.Elem()).(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return Verdict{}, false
	}
	if fs := FuncSignature(tail.Elem()); fs == nil || fs.Params().Len() != 1 || fs.Results().Len() != 0 {
		return Verdict{}, false
	}
	var kept []Param
	var carried []Verdict
	for _, p := range up.Parameters {
		setter := optionSetter(named, p.Name)
		if p.Optional || p.Rest || setter == nil {
			kept = append(kept, p)
			continue
		}
		carried = append(carried, paramVerdict(env, p, setter.Params().At(0).Type()))
	}
	if len(carried) == 0 {
		return Verdict{}, false
	}
	reduced := Call{TypeParameters: up.TypeParameters, Parameters: kept, Returns: up.Returns}
	return All(append(carried, Signature(env, reduced, sig))...), true
}

// optionStruct applies S9s: trailing optional positional parameters (`sshArguments(target, strictHostKeys = true, knownHostsFile =
// target.knownHostsFile)`) are one optional Go options struct passed as a variadic tail, with a field named after each parameter whose
// type, or its pointer element (the pointer is the omitted parameter that takes the default), agrees with it. It returns nil when a
// parameter is required or a rest parameter, the element is not a struct, or a parameter has no field, so the caller falls back to the
// plain variadic rule.
func optionStruct(env FuncEnv, rest []Param, elem types.Type) *Verdict {
	st, ok := elem.Underlying().(*types.Struct)
	if !ok || len(rest) == 0 {
		return nil
	}
	vs := make([]Verdict, 0, len(rest))
	for _, p := range rest {
		if !p.Optional || p.Rest {
			return nil
		}
		field := structField(st, p.Name)
		if field == nil {
			return nil
		}
		t := field.Type()
		if ptr, ok := t.(*types.Pointer); ok {
			t = ptr.Elem()
		}
		vs = append(vs, env.Agree(p.Type, t))
	}
	v := All(vs...)
	if v.OK == Yes {
		v = Accept("S9s: the trailing optional parameters are the fields of an optional Go options struct")
	}
	return &v
}

// structField finds the field standing for an upstream member: its name with an upper-case first letter, a json tag, or the name
// folded over case and underscores.
func structField(st *types.Struct, name string) *types.Var {
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "_", "")) }
	var folded *types.Var
	for i := range st.NumFields() {
		f := st.Field(i)
		tag := jsonName(st.Tag(i))
		switch {
		case f.Name() == name || f.Name() == upperFirst(name) || tag == name:
			return f
		case folded == nil && norm(f.Name()) == norm(name):
			folded = f
		}
	}
	return folded
}

func jsonName(tag string) string {
	const key = `json:"`
	_, after, ok := strings.Cut(tag, key)
	if !ok {
		return ""
	}
	v := after
	v, _, _ = strings.Cut(v, `"`)
	v, _, _ = strings.Cut(v, ",")
	return v
}

// flattenedName reports S8 name agreement: the Go parameter has the property's name (ignoring case and underscores) or is its last
// camel-case word, for example runtime for modelRuntime.
func flattenedName(prop, goName string) bool {
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "_", "")) }
	p, g := norm(prop), norm(goName)
	return p == g || len(g) > 0 && strings.HasSuffix(p, g) && prop[len(prop)-len(goName)] >= 'A' && prop[len(prop)-len(goName)] <= 'Z'
}

// flattened applies S8: an upstream options-bag parameter is spread over Go positional parameters named after its members. Every
// Go parameter must stand for one member or plain parameter, and every member and plain parameter must have a Go parameter: the
// engine lists a named interface or class as a bag of members it marks optional, so an optionality exemption would let a required
// TUI or theme parameter vanish.
func flattened(env FuncEnv, want []Param, params []*types.Var, variadic bool) Verdict {
	var flat []Param
	spread := false
	for _, p := range want {
		if env.Bag != nil && !p.Rest {
			if ms, ok := env.Bag(p.Type); ok {
				spread = true
				flat = append(flat, ms...)
				continue
			}
		}
		flat = append(flat, p)
	}
	if !spread {
		return Refute("S4: upstream takes %d parameters, Go takes %d", len(want), len(params))
	}
	used := make([]bool, len(flat))
	vs := []Verdict{}
	for i, g := range params {
		t := g.Type()
		if variadic && i == len(params)-1 {
			if s, ok := t.(*types.Slice); ok {
				t = s.Elem()
			}
		}
		match := -1
		for j, f := range flat {
			if !used[j] && flattenedName(f.Name, g.Name()) {
				match = j
				break
			}
		}
		if match < 0 {
			return Refute("S8: Go parameter %s has no upstream parameter or options member", g.Name())
		}
		used[match] = true
		if flat[match].Type != "" {
			vs = append(vs, paramVerdict(env, flat[match], t))
		}
	}
	for j, f := range flat {
		if !used[j] {
			return Refute("S8: upstream parameter %s has no Go parameter", f.Name)
		}
	}
	return All(vs...)
}

// PrefixOverload reports whether a's parameters are a prefix of b's, b's extra parameters are optional, and the results agree.
func PrefixOverload(a, b Call) bool {
	if len(a.Parameters) > len(b.Parameters) || strings.TrimSpace(a.Returns) != strings.TrimSpace(b.Returns) {
		return false
	}
	for i, p := range a.Parameters {
		if strings.TrimSpace(p.Type) != strings.TrimSpace(b.Parameters[i].Type) {
			return false
		}
	}
	for _, p := range b.Parameters[len(a.Parameters):] {
		if !p.Optional && !p.Rest {
			return false
		}
	}
	return true
}

// Overload applies S7: it judges own against sig and, failing that, accepts it when a wider overload among siblings, of which own is
// a prefix, is satisfied by sig.
func Overload(env FuncEnv, own Call, siblings []Call, sig *types.Signature) Verdict {
	v := Signature(env, own, sig)
	if v.OK == Yes {
		return v
	}
	for _, wide := range siblings {
		if PrefixOverload(own, wide) && Signature(env, wide, sig).OK == Yes {
			return Accept("")
		}
	}
	if sig.Variadic() {
		for _, short := range siblings {
			if VariadicSpan(env, short, own, sig) {
				return Accept("S7v: the variadic tail of the Go call spans overloads %d and %d parameters", len(short.Parameters), len(own.Parameters))
			}
		}
	}
	return v
}

// VariadicSpan applies S7v: a Go variadic call stands for a family of overloads that differ by trailing required parameters. The
// shorter overload is satisfied by sig, its parameter types begin own's, and own is satisfied when its extra parameters are
// optional, which is how the variadic tail takes them; results are judged as own states them.
func VariadicSpan(env FuncEnv, short, own Call, sig *types.Signature) bool {
	if len(short.Parameters) >= len(own.Parameters) || Signature(env, short, sig).OK != Yes {
		return false
	}
	relaxed := Call{TypeParameters: own.TypeParameters, Returns: own.Returns}
	for i, p := range own.Parameters {
		if i < len(short.Parameters) {
			if strings.TrimSpace(p.Type) != strings.TrimSpace(short.Parameters[i].Type) {
				return false
			}
		} else {
			p.Optional = true
		}
		relaxed.Parameters = append(relaxed.Parameters, p)
	}
	return Signature(env, relaxed, sig).OK == Yes
}

// OverloadSet applies S10: it pairs every overload with the first Go signature that satisfies it (S7 included) and returns one
// verdict per overload. A Go signature may satisfy several overloads. An overload no signature satisfies gets the verdict of the
// first candidate that could not decide, else of the first candidate, so the reason names a real mismatch.
func OverloadSet(env FuncEnv, overloads []Call, sigs []*types.Signature) []Verdict {
	out := make([]Verdict, len(overloads))
	for i, own := range overloads {
		out[i] = Refute("S10: no Go function takes the parameters of overload %d", i)
		for j, sig := range sigs {
			v := Overload(env, own, overloads, sig)
			if v.OK == Yes {
				out[i] = v
				break
			}
			if j == 0 || v.OK == Unknown && out[i].OK == No {
				out[i] = v
			}
		}
	}
	return out
}

// IsAsync reports whether an upstream result is a Promise or an async iterable, so that the Go call must take a context and
// return an error (or an iterator that yields errors).
func IsAsync(returns string) bool {
	for _, m := range SplitTop(strings.TrimSpace(returns), "|") {
		if strings.HasPrefix(m, "Promise<") || strings.HasPrefix(m, "PromiseLike<") || strings.HasPrefix(m, "AsyncIterable<") || strings.HasPrefix(m, "AsyncGenerator<") ||
			strings.HasPrefix(m, "AsyncIterableIterator<") {
			return true
		}
	}
	return false
}

// Result applies S5, S5b and S6 to an upstream result and the Go results.
func Result(env FuncEnv, returns string, results *types.Tuple) Verdict {
	returns = expandHookResult(strings.TrimSpace(returns))
	v := result(env, returns, results)
	if v.OK == Yes {
		return v
	}
	return v
}

// result applies S5, S5b and S6 to an expanded upstream result and the Go results.
func result(env FuncEnv, returns string, results *types.Tuple) Verdict {
	if results.Len() == 1 {
		if _, ok := results.At(0).Type().Underlying().(*types.Chan); ok && strings.HasPrefix(returns, "Promise<") {
			return wrapResult(env.Agree(returns, results.At(0).Type())) // S5c: a channel is the Go form of the Promise itself
		}
	}
	var kept []string
	for _, m := range SplitTop(returns, "|") {
		if inner, ok := cutPromise(m); ok {
			m = inner // S5: PromiseLike<T> is the thenable `await` accepts, the same asynchronous delivery of T
		} else if inner, ok := cutMaybePromise(m); ok {
			m = inner // S5m: Awaitable<T> and MaybePromise<T> are T | Promise<T>, which Go spells as the synchronous T
		}
		for _, alt := range SplitTop(m, "|") { // the Promise's own alternatives join the union
			if alt = strings.TrimSpace(alt); !containsString(kept, alt) {
				kept = append(kept, alt)
			}
		}
	}
	if allNothing(kept) {
		kept = []string{"void"} // S5v: a union of void and undefined arms is one empty result
	}
	returns = strings.Join(kept, " | ")
	var values []types.Type
	for v := range results.Variables() {
		values = append(values, v.Type())
	}
	hadError := false
	if n := len(values); n > 0 && isErrorType(values[n-1]) {
		values = values[:n-1]
		hadError = true
	}
	if v, ok := okErrorResult(env, returns, values, hadError); ok {
		return v // S13
	}
	if returns == "boolean | string" && hadError && len(values) == 0 && env.ErrorUnionPinned {
		return Accept("S5s: a result that is true on success or the failure message is the Go error (nil is true, its text is the message)")
	}
	if returns == "Error" && hadError && len(values) == 0 {
		return Accept("S5g: an upstream Error result is the Go error value") // the Go error is the value, not the failure arm
	}
	switch returns {
	case "void", "undefined", "never":
		if len(values) == 0 {
			return Accept("")
		}
		return Refute("S5: upstream returns nothing, Go returns %s", typeLabel(values[0]))
	}
	if strings.HasPrefix(returns, "{") && len(values) >= 2 {
		if v, ok := tupleResult(env, returns, values); ok {
			return v // S5b
		}
	}
	nullable := strings.Contains(returns, "undefined") || strings.Contains(returns, "null")
	switch len(values) {
	case 0:
		return Refute("S5: upstream returns %s, Go returns no value", returns)
	case 1:
		if returns == "any" || returns == "unknown" {
			return Accept("S5a: an unconstrained upstream result is read through its Go refinement %s", typeLabel(values[0]))
		}
		return wrapResult(env.Agree(returns, values[0]))
	case 2:
		if b, ok := values[1].Underlying().(*types.Basic); ok && b.Info()&types.IsBoolean != 0 && nullable {
			return wrapResult(env.Agree(returns, values[0])) // S6
		}
	}
	return Undecided("S5: upstream returns %s, Go returns %d values", returns, len(values))
}

// cutPromise reads `Promise<T>` or `PromiseLike<T>` as T.
func cutPromise(m string) (string, bool) {
	for _, prefix := range []string{"Promise<", "PromiseLike<"} {
		if inner, ok := strings.CutPrefix(m, prefix); ok {
			return strings.TrimSuffix(inner, ">"), true
		}
	}
	return "", false
}

// cutMaybePromise reads `Awaitable<T>` or `MaybePromise<T>` (the aliases `T | Promise<T>` of tui autocomplete.ts and server types.ts).
func cutMaybePromise(m string) (string, bool) {
	for _, prefix := range []string{"Awaitable<", "MaybePromise<"} {
		if inner, ok := strings.CutPrefix(strings.TrimSpace(m), prefix); ok && strings.HasSuffix(inner, ">") {
			return strings.TrimSuffix(inner, ">"), true
		}
	}
	return "", false
}

// allNothing reports whether every alternative of a result union is void, undefined or never.
func allNothing(alts []string) bool {
	for _, a := range alts {
		if a != "void" && a != "undefined" && a != "never" {
			return false
		}
	}
	return len(alts) > 0
}

// accessorResult judges an upstream result field declared as a no-argument method (`remote(): T`) against a Go func() T.
func accessorResult(env FuncEnv, typ string, value types.Type) Verdict {
	sig, ok := value.Underlying().(*types.Signature)
	if !ok || sig.Params().Len() != 0 || sig.Results().Len() != 1 {
		return Verdict{Why: "S5b: method field " + strings.TrimSpace(typ) + " against Go type " + typeLabel(value) + ", not a func with no parameters and one result", OK: No}
	}
	return env.Agree(typ, sig.Results().At(0).Type())
}

// tupleResult applies S5b: an upstream object literal result with n fields is n Go return values of the same types, in order.
func tupleResult(env FuncEnv, returns string, values []types.Type) (Verdict, bool) {
	inner := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(returns), "{"), "}")
	var vs []Verdict
	for _, part := range SplitTop(strings.ReplaceAll(inner, ";", ","), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, typ, ok := strings.Cut(part, ":")
		if !ok || len(vs) >= len(values) {
			return Verdict{}, false
		}
		if strings.HasSuffix(strings.TrimSpace(name), "()") {
			// A method field `remote(): T` is a no-argument Go func returning T: the caller invokes it for the value (env sshConnection).
			vs = append(vs, accessorResult(env, typ, values[len(vs)]))
			continue
		}
		vs = append(vs, env.Agree(typ, values[len(vs)]))
	}
	if len(vs) != len(values) {
		return Verdict{}, false
	}
	return All(vs...), true
}

func wrapResult(v Verdict) Verdict {
	if v.OK != Yes {
		v.Why = "result: " + v.Why
	}
	return v
}

// IsContext reports whether t is context.Context.
func IsContext(t types.Type) bool {
	n, _ := types.Unalias(t).(*types.Named)
	return n != nil && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "context" && n.Obj().Name() == "Context"
}

func typeLabel(t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string { return p.Name() })
}

func containsString(list []string, s string) bool {
	return slices.Contains(list, s)
}

// okErrorResult applies S13: Result<V, E>, an alias of { ok: true; value: V } | { ok: false; error: E }, is the Go results (V, error);
// a void V is the single result error. The Go function must end in an error: without one the failure arm has nowhere to go.
func okErrorResult(env FuncEnv, returns string, values []types.Type, hadError bool) (Verdict, bool) {
	args, ok := strings.CutPrefix(returns, "Result<")
	if !ok || !strings.HasSuffix(args, ">") || env.Alias == nil {
		return Verdict{}, false
	}
	body, ok := env.Alias("Result")
	if !ok || !isOkErrorUnion(body) {
		return Verdict{}, false
	}
	parts := SplitTop(strings.TrimSuffix(args, ">"), ",")
	if len(parts) != 2 {
		return Verdict{}, false
	}
	if !hadError {
		if v, ok := resultStruct(env, parts, values); ok {
			return v, true // S13r
		}
		return Refute("S13: upstream returns %s, Go has no error result for the failure arm", returns), true
	}
	switch v := strings.TrimSpace(parts[0]); v {
	case "void", "undefined":
		if len(values) != 0 {
			return Refute("S13: upstream returns Result<void, ...>, Go returns %s besides the error", typeLabel(values[0])), true
		}
		return Accept("S13"), true
	default:
		if len(values) != 1 {
			return Refute("S13: upstream returns Result<%s, ...>, Go returns %d values besides the error", v, len(values)), true
		}
		return wrapResult(env.Agree(v, values[0])), true
	}
}

// resultStruct applies S13r: the upstream function returns the Result data itself ({ ok: true; value } | { ok: false; error }), which Go
// spells as the one struct result `Result[V, E]` with the fields Ok, Value and Error. The type arguments are judged against the upstream
// ones; no error result is needed because the failure arm is the returned value, not a thrown one.
func resultStruct(env FuncEnv, parts []string, values []types.Type) (Verdict, bool) {
	if len(values) != 1 {
		return Verdict{}, false
	}
	named, ok := values[0].(*types.Named)
	if !ok || named.Obj().Name() != "Result" || named.TypeArgs().Len() != 2 {
		return Verdict{}, false
	}
	st, ok := named.Underlying().(*types.Struct)
	if !ok || st.NumFields() != 3 {
		return Verdict{}, false
	}
	for i, want := range []string{"Ok", "Value", "Error"} {
		if st.Field(i).Name() != want {
			return Verdict{}, false
		}
	}
	if b, ok := st.Field(0).Type().Underlying().(*types.Basic); !ok || b.Kind() != types.Bool {
		return Verdict{}, false
	}
	var vs []Verdict
	for i, p := range parts {
		vs = append(vs, wrapResult(env.Agree(strings.TrimSpace(p), named.TypeArgs().At(i))))
	}
	return All(vs...), true
}

// isOkErrorUnion reports whether an alias body is exactly the two-arm union { ok: true; value: _ } | { ok: false; error: _ }.
func isOkErrorUnion(body string) bool {
	arms := SplitTop(strings.TrimSpace(body), "|")
	if len(arms) != 2 {
		return false
	}
	have := func(arm, want string) bool {
		arm = strings.Join(strings.Fields(strings.NewReplacer("{", " ", "}", " ", ";", " ", ",", " ").Replace(arm)), " ")
		return strings.HasPrefix(arm, want)
	}
	return have(arms[0], "ok: true value:") && have(arms[1], "ok: false error:")
}

// emptyInterface reports whether t is `any`: an erased variadic tail (`args ...any`) carries the owner and key arguments that a
// generic upstream overload takes as required parameters (S3d), because Go methods cannot have type parameters; the typed generic
// helpers (Snapshot[T], WatchDoc[T]) fix their types.
func emptyInterface(t types.Type) bool {
	i, ok := t.Underlying().(*types.Interface)
	return ok && i.NumMethods() == 0 && i.NumEmbeddeds() == 0
}

// expandHookResult reads durable's HookResult<T> (harness/types.ts: `T | undefined | Promise<T | undefined>`) as the Promise of an
// optional T, which S5 and S6 then judge (S5h).
func expandHookResult(returns string) string {
	inner, ok := strings.CutPrefix(returns, "HookResult<")
	if !ok || !strings.HasSuffix(inner, ">") {
		return returns
	}
	inner = strings.TrimSuffix(inner, ">")
	depth := 0
	for _, r := range inner {
		switch r {
		case '<', '(', '{', '[':
			depth++
		case '>', ')', '}', ']':
			depth--
			if depth < 0 {
				return returns
			}
		}
	}
	if depth != 0 {
		return returns
	}
	return "Promise<" + inner + " | undefined>"
}

// isFetchCall reports whether up is the WHATWG fetch signature (input, init?: RequestInit) => Promise<Response>.
func isFetchCall(up Call) bool {
	if len(up.Parameters) != 2 || strings.TrimSpace(up.Returns) != "Promise<Response>" {
		return false
	}
	in, init := up.Parameters[0], up.Parameters[1]
	if !init.Optional || strings.TrimSpace(init.Type) != "RequestInit" {
		return false
	}
	for _, alt := range SplitTop(strings.TrimSpace(in.Type), "|") {
		switch strings.TrimSpace(alt) {
		case "string", "URL", "Request", "RequestInfo":
		default:
			return false
		}
	}
	return true
}

// isHTTPRoundTrip reports whether sig is func(*net/http.Request) (*net/http.Response, error).
func isHTTPRoundTrip(sig *types.Signature) bool {
	if sig.Params().Len() != 1 || sig.Results().Len() != 2 || !isErrorType(sig.Results().At(1).Type()) {
		return false
	}
	return isHTTPType(sig.Params().At(0).Type(), "Request") && isHTTPType(sig.Results().At(0).Type(), "Response")
}

func isHTTPType(t types.Type, name string) bool {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "net/http" && named.Obj().Name() == name
}

// promiseLikeRe matches the PromiseLike type name in an upstream result.
var promiseLikeRe = regexp.MustCompile(`\bPromiseLike<`)

// promiseLikeAsPromise applies S5l: PromiseLike<T> is the thenable Pi awaits exactly as it awaits Promise<T> (lib.es5.d.ts), so a call
// that returns it is judged as one that returns Promise<T>: a Go call that takes a context and returns the value and an error.
func promiseLikeAsPromise(returns string) string {
	return promiseLikeRe.ReplaceAllString(returns, "Promise<")
}

// IsBoolStringUnion reports whether an upstream result is `boolean | string`, in either order, optionally awaited as a Promise.
func IsBoolStringUnion(returns string) bool {
	returns = strings.TrimSpace(returns)
	if inner, ok := strings.CutPrefix(returns, "Promise<"); ok {
		returns = strings.TrimSpace(strings.TrimSuffix(inner, ">"))
	}
	arms := SplitTop(returns, "|")
	if len(arms) != 2 {
		return false
	}
	a, b := strings.TrimSpace(arms[0]), strings.TrimSpace(arms[1])
	return a == "boolean" && b == "string" || a == "string" && b == "boolean"
}
