package rules

// Family DataShapes: plain-data interfaces and aliases to structs with JSON tags; Record<K,V> to map[K]V; arrays and tuples; Date, number and bigint mapping. Extension points: RegisterType, RegisterMember.
//
// Rules in this file (labels repeat in the verdict text):
//
//	DS1 number is a sync/atomic integer (Int32, Int64, Uint32, Uint64), whose Load and Store carry the value.
//	DS2 a fetch function (an object type of call signatures returning Promise<Response>) is a *http.Client, an http.RoundTripper,
//	    or a Go function from *http.Request to (*http.Response, error).
//	DS3 an ordered record (Record<string, V>, index signature) is a named slice of two-field key/value structs with both JSON methods.
//	DS4 a property inherited from an upstream base interface is a member of a Go struct field that holds the base.
//	DS5 the members every JS Error carries (message, name, cause, stack) map onto a Go error type's Error, Name, Unwrap/Cause, Stack.
//	DS6 a fixed tuple [A, B] is a Go array [2]T whose element type agrees with every position.
//	DS7 bigint is a Go 64-bit integer or *math/big.Int.
//
// Add rules to this file only. Each rule is a small type with a Name, registered in init under DataShapes with the matching Register
// function, and has table tests in datashapes_test.go that include a case it must refuse. Name rules by a short label that
// the verdict text repeats, so a gap report points at the rule that decided.

import (
	"go/types"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

func init() {
	RegisterType(DataShapes, atomicNumber{})
	RegisterType(DataShapes, fetchFunction{})
	RegisterType(DataShapes, orderedRecord{})
	RegisterType(DataShapes, fixedTuple{})
	RegisterType(DataShapes, bigintRule{})
	RegisterMember(DataShapes, inheritedThroughField{})
	RegisterMember(DataShapes, errorBaseMember{})
}

// splitTopLevel splits s on sep outside brackets, parentheses, braces, angle brackets and string literals.
func splitTopLevel(s, sep string) []string {
	var parts []string
	depth, start := 0, 0
	var quote rune
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case quote != 0:
			switch r {
			case '\\':
				i++
			case quote:
				quote = 0
			}
		case r == '"' || r == '\'' || r == '`':
			quote = r
		case r == '>' && i > 0 && runes[i-1] == '=':
		case strings.ContainsRune("([{<", r):
			depth++
		case strings.ContainsRune(")]}>", r):
			depth--
		case depth == 0 && strings.HasPrefix(string(runes[i:]), sep):
			parts = append(parts, strings.TrimSpace(string(runes[start:i])))
			start = i + len([]rune(sep))
			i = start - 1
		}
	}
	return append(parts, strings.TrimSpace(string(runes[start:])))
}

// carried returns the members of an upstream union that carry a type: undefined, null and void carry none. A rule applies only when
// exactly one member remains.
func carried(up string) (string, bool) {
	var kept []string
	for _, m := range splitTopLevel(strings.TrimSpace(up), "|") {
		switch m {
		case "", "undefined", "null", "void":
		default:
			kept = append(kept, m)
		}
	}
	if len(kept) != 1 {
		return "", false
	}
	m := kept[0]
	for strings.HasPrefix(m, "(") && strings.HasSuffix(m, ")") && len(splitTopLevel(m[1:len(m)-1], "|")) > 0 && !strings.Contains(m, "=>") {
		m = strings.TrimSpace(m[1 : len(m)-1])
	}
	return m, true
}

func derefType(t types.Type) types.Type {
	for {
		p, ok := types.Unalias(t).(*types.Pointer)
		if !ok {
			return t
		}
		t = p.Elem()
	}
}

func named(t types.Type) *types.Named {
	n, _ := types.Unalias(t).(*types.Named)
	return n
}

func isNamed(t types.Type, path, name string) bool {
	n := named(t)
	return n != nil && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == path && n.Obj().Name() == name
}

// hasMethod reports whether *t (and so t) has an exported method of that name.
func hasMethod(t types.Type, name string) *types.Func {
	base := derefType(t)
	ms := types.NewMethodSet(types.NewPointer(base))
	if types.IsInterface(base) {
		ms = types.NewMethodSet(base) // a pointer to an interface has no methods
	}
	for sel := range ms.Methods() {
		if sel.Obj().Name() == name {
			f, _ := sel.Obj().(*types.Func)
			return f
		}
	}
	return nil
}

type atomicNumber struct{}

func (atomicNumber) Name() string { return "DS1" }

func (atomicNumber) Type(_ Env, up string, t types.Type) (Verdict, bool) {
	m, ok := carried(up)
	if !ok || m != "number" {
		return Verdict{}, false
	}
	for _, name := range []string{"Int32", "Int64", "Uint32", "Uint64"} {
		if isNamed(derefType(t), "sync/atomic", name) {
			return Accept("DS1: upstream number is a sync/atomic.%s read with Load and written with Store", name), true
		}
	}
	return Verdict{}, false
}

var (
	fetchObjectRe = regexp.MustCompile(`^\{\s*(?:\([^{}]*\)\s*:\s*Promise<Response>\s*;?\s*)+\}$`)
	// fetchFuncRe is a fetch-shaped function type: (input, init?) => Promise<Response>.
	fetchFuncRe = regexp.MustCompile(`^\(\s*input\s*:\s*(?:RequestInfo|string|URL|Request)[^()]*(?:,\s*init\s*\?\s*:\s*RequestInit)?\s*\)\s*=>\s*Promise<Response>$`)
)

type fetchFunction struct{}

func (fetchFunction) Name() string { return "DS2" }

func (fetchFunction) Type(_ Env, up string, t types.Type) (Verdict, bool) {
	m, ok := carried(up)
	if !ok || m != "typeof fetch" && !fetchObjectRe.MatchString(m) && !fetchFuncRe.MatchString(m) {
		return Verdict{}, false
	}
	base := derefType(t)
	if isNamed(base, "net/http", "Client") || isNamed(base, "net/http", "RoundTripper") {
		return Accept("DS2: upstream fetch is Go's net/http %s, whose Do or RoundTrip sends a request and returns the response", named(base).Obj().Name()), true
	}
	if hasDoRequest(t) {
		return Accept("DS2: upstream fetch is a Go type whose Do(*http.Request) (*http.Response, error) sends the request"), true
	}
	if sig, ok := t.Underlying().(*types.Signature); ok && sig.Params().Len() == 1 && sig.Results().Len() == 2 &&
		isNamed(derefType(sig.Params().At(0).Type()), "net/http", "Request") &&
		isNamed(derefType(sig.Results().At(0).Type()), "net/http", "Response") && isErrorType(sig.Results().At(1).Type()) {
		return Accept("DS2: upstream fetch is func(*http.Request) (*http.Response, error)"), true
	}
	return Verdict{}, false
}

// hasDoRequest reports whether t has the method Do(*http.Request) (*http.Response, error), as an http.Client does.
func hasDoRequest(t types.Type) bool {
	do := hasMethod(t, "Do")
	if do == nil {
		return false
	}
	sig, ok := do.Type().(*types.Signature)
	return ok && sig.Params().Len() == 1 && sig.Results().Len() == 2 &&
		isNamed(derefType(sig.Params().At(0).Type()), "net/http", "Request") &&
		isNamed(derefType(sig.Results().At(0).Type()), "net/http", "Response") && isErrorType(sig.Results().At(1).Type())
}

func isErrorType(t types.Type) bool { return types.Identical(t, types.Universe.Lookup("error").Type()) }

var (
	recordRe    = regexp.MustCompile(`^(?:Partial<)?Record<\s*string\s*,\s*(.+?)\s*>>?$`)
	indexSigRe  = regexp.MustCompile(`^\{\s*\[\w+:\s*string\]:\s*(.+?);?\s*\}$`)
	nullSuffixR = regexp.MustCompile(`\s*\|\s*(null|undefined)`)
)

type orderedRecord struct{}

func (orderedRecord) Name() string { return "DS3" }

func (orderedRecord) Type(env Env, up string, t types.Type) (Verdict, bool) {
	m, ok := carried(up)
	if !ok {
		return Verdict{}, false
	}
	var value string
	if g := recordRe.FindStringSubmatch(m); g != nil {
		value = g[1]
	} else if g := indexSigRe.FindStringSubmatch(m); g != nil {
		value = g[1]
	} else {
		return Verdict{}, false
	}
	t = derefType(t) // an optional ordered record is a pointer to the slice type
	slice, ok := t.Underlying().(*types.Slice)
	if !ok || named(t) == nil {
		return Verdict{}, false
	}
	if hasMethod(t, "MarshalJSON") == nil || hasMethod(t, "UnmarshalJSON") == nil {
		return Verdict{}, false
	}
	entry, ok := slice.Elem().Underlying().(*types.Struct)
	if !ok || entry.NumFields() != 2 || !isStringKind(entry.Field(0).Type()) {
		return Verdict{}, false
	}
	// A null value is an explicit JSON null; the Go value field is a pointer then, and the value type agrees without it.
	v := env.Agree(strings.TrimSpace(nullSuffixR.ReplaceAllString(value, "")), derefType(entry.Field(1).Type()))
	if v.OK != Yes {
		return Verdict{}, false
	}
	return Accept("DS3: upstream Record<string, V> is the insertion-ordered %s: a slice of {%s, %s} with MarshalJSON and UnmarshalJSON writing an object",
		named(t).Obj().Name(), entry.Field(0).Name(), entry.Field(1).Name()), true
}

func isStringKind(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsString != 0
}

type fixedTuple struct{}

func (fixedTuple) Name() string { return "DS6" }

func (fixedTuple) Type(env Env, up string, t types.Type) (Verdict, bool) {
	m, ok := carried(up)
	if !ok {
		return Verdict{}, false
	}
	m = strings.TrimSpace(strings.TrimPrefix(m, "readonly "))
	if !strings.HasPrefix(m, "[") || !strings.HasSuffix(m, "]") {
		return Verdict{}, false
	}
	elems := splitTopLevel(m[1:len(m)-1], ",")
	for i, e := range elems {
		if e == "" || strings.HasPrefix(e, "...") {
			return Verdict{}, false
		}
		if label, typ, ok := strings.Cut(e, ":"); ok && regexp.MustCompile(`^\w+$`).MatchString(strings.TrimSpace(label)) {
			e = strings.TrimSpace(typ) // a labelled element [name: string]
		} else if ok2 := regexp.MustCompile(`^\w+\?\s*:`).MatchString(e); ok2 {
			return Verdict{}, false // an optional element has no fixed length
		}
		elems[i] = e
	}
	arr, ok := t.Underlying().(*types.Array)
	if !ok || int(arr.Len()) != len(elems) {
		return Verdict{}, false
	}
	for _, e := range elems {
		if v := env.Agree(e, arr.Elem()); v.OK != Yes {
			return Verdict{}, false
		}
	}
	return Accept("DS6: upstream tuple of %d is the Go array [%d]%s", len(elems), len(elems), types.TypeString(arr.Elem(), nil)), true
}

type bigintRule struct{}

func (bigintRule) Name() string { return "DS7" }

func (bigintRule) Type(_ Env, up string, t types.Type) (Verdict, bool) {
	m, ok := carried(up)
	if !ok || m != "bigint" {
		return Verdict{}, false
	}
	base := derefType(t)
	if isNamed(base, "math/big", "Int") {
		return Accept("DS7: upstream bigint is *math/big.Int"), true
	}
	if b, ok := base.Underlying().(*types.Basic); ok {
		switch b.Kind() {
		case types.Int64, types.Uint64, types.Int, types.Uint:
			return Accept("DS7: upstream bigint is the Go 64-bit integer %s", b.Name()), true
		}
	}
	return Verdict{}, false
}

// inheritedThroughField is DS4: an upstream interface that extends B has B's properties; the Go struct that holds B as a field,
// embedded or named, has them through that field. The rule needs the upstream owner and B to both list the property, so a
// field that merely happens to share a name never matches.
type inheritedThroughField struct{}

func (inheritedThroughField) Name() string { return "DS4" }

func (inheritedThroughField) Member(env Env, prop Property, owner *types.TypeName) (types.Object, bool) {
	st, ok := owner.Type().Underlying().(*types.Struct)
	if !ok || !contains(env.PropertyNames(owner.Name()), prop.Name) {
		return nil, false
	}
	for holder := range st.Fields() {
		n := named(derefType(holder.Type()))
		if n == nil || n.Obj() == owner {
			continue
		}
		inner, ok := n.Underlying().(*types.Struct)
		if !ok || !contains(env.PropertyNames(n.Obj().Name()), prop.Name) {
			continue
		}
		for j := 0; j < inner.NumFields(); j++ {
			if f := inner.Field(j); f.Exported() && fieldStands(f, inner.Tag(j), prop.Name) {
				return f, true
			}
		}
	}
	return nil, false
}

// holdsError reports whether the struct behind t has a named (not embedded) field of type error.
func holdsError(t types.Type) bool {
	st, ok := derefType(t).Underlying().(*types.Struct)
	if !ok {
		return false
	}
	for f := range st.Fields() {
		if !f.Embedded() && isErrorType(f.Type()) {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	return slices.Contains(list, s)
}

// fieldStands reports whether a Go field stands for an upstream property: its JSON tag is the property name, or its name is the
// property name with the first letter upper-cased or equal after folding case (Id/ID, Url/URL).
func fieldStands(f *types.Var, tag, prop string) bool {
	name, _, _ := strings.Cut(reflect.StructTag(tag).Get("json"), ",")
	if name == prop {
		return true
	}
	return fold(f.Name()) == fold(prop)
}

func fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// errorBaseMember is DS5: an upstream class that extends Error inherits message, name, cause and stack. A Go type that implements error
// has the message as Error(), the cause as Unwrap or Cause, and the name and stack as members of those names. The rule applies
// only to a Go error type and only to those four properties.
type errorBaseMember struct{}

func (errorBaseMember) Name() string { return "DS5" }

func (errorBaseMember) Member(_ Env, prop Property, owner *types.TypeName) (types.Object, bool) {
	if hasMethod(owner.Type(), "Error") == nil {
		return nil, false
	}
	switch prop.Name {
	case "message":
		if f := hasMethod(owner.Type(), "Error"); f != nil {
			return f, true
		}
	case "cause":
		if f := hasMethod(owner.Type(), "Cause"); f != nil {
			return f, true
		}
		// Unwrap stands for the cause only on a type that holds one: an error-typed field of its own. Unwrap that returns an embedded
		// base error spells upstream class inheritance (McpAuthRequiredError extends McpHttpError), whose Error.cause stays unset.
		if f := hasMethod(owner.Type(), "Unwrap"); f != nil && holdsError(owner.Type()) {
			return f, true
		}
	}
	return nil, false
}
