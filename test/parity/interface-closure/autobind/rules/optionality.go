package rules

import (
	"fmt"
	"go/types"
	"reflect"
	"regexp"
	"strings"
)

// Finding is the result of one rule of this file or of unions.go, with the rule that fired and the reason. Result uses the registry's
// Tri: Yes means the rule proves the Go shape equivalent, No refutes it and Unknown cannot decide; No and Unknown stay gaps.
type Finding struct {
	Result Tri
	// Rule is the identifier of the rule that decided (O1.., U1..).
	Rule string
	// Why explains a No or Unknown, and carries the evidence of a Yes.
	Why string
	// Notes lists observations that do not change the result, such as Go constants with no upstream literal.
	Notes []string
}

func triName(t Tri) string {
	switch t {
	case Yes:
		return "yes"
	case No:
		return "no"
	}
	return "unknown"
}

// Verdict converts the finding to the registry's verdict; the rule id leads the reason so a gap report names the rule.
func (f Finding) Verdict() Verdict { return Verdict{OK: f.Result, Why: f.Rule + ": " + f.Why} }

func fYes(rule, format string, args ...any) Finding {
	return Finding{Result: Yes, Rule: rule, Why: fmt.Sprintf(format, args...)}
}

func fNo(rule, format string, args ...any) Finding {
	return Finding{Result: No, Rule: rule, Why: fmt.Sprintf(format, args...)}
}

func fUnknown(rule, format string, args ...any) Finding {
	return Finding{Result: Unknown, Rule: rule, Why: fmt.Sprintf(format, args...)}
}

var (
	ouStringLit = regexp.MustCompile(`^"[^"]*"$|^'[^']*'$`)
	ouNumberLit = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	ouIdentRe   = regexp.MustCompile(`^[A-Za-z_][\w.]*$`)
)

// ouSplitTop splits s on sep outside brackets, parentheses, braces, angle brackets and string literals.
func ouSplitTop(s, sep string) []string {
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
		case strings.ContainsRune("([{<", r):
			depth++
		case r == '>' && i > 0 && runes[i-1] == '=':
			// arrow
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

// ouStripParens removes parentheses that enclose the whole of s.
func ouStripParens(s string) string {
	s = strings.TrimSpace(s)
	for strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") && ouClosesAtEnd(s) {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

func ouClosesAtEnd(s string) bool {
	depth := 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i == len(s)-1
			}
		}
	}
	return false
}

// OptionState rules (family 2): `x?: T` and `T | undefined` against *T, omitempty and a presence wrapper; `T | null` against a pointer
// or other nullable Go type.
//
//	O1  a required property is a Go value: no pointer, no presence wrapper, no omitempty or omitzero (they would drop a value upstream sends)
//	O2  an optional property has a Go unset state: a pointer, a presence wrapper, omitempty or omitzero, or a non-serialized nilable type
//	O3  a nullable property has a Go null state: a pointer, a nilable type without omitempty, or a wrapper
//	O4  an optional and nullable property has three Go states: a wrapper, a pointer to pointer, or json.RawMessage with omitempty
//	O5  a result `T | undefined` is *T, a nilable T or (T, bool); a required result has no absent state
//	O6  a parameter `x?: T` is a pointer, a nilable type or the variadic tail; a required parameter has none of them

// OptionState is the optionality and nullability of an upstream property, parameter or result.
type OptionState struct {
	// Optional is true when the declaration is `x?:` or the type has an undefined member.
	Optional bool
	// Nullable is true when the type has a null member.
	Nullable bool
	// Base is the type with its undefined and null members removed.
	Base string
}

// ParseOptionality splits an upstream type string into its base type and its undefined and null members. declaredOptional is the
// `?` of the declaration. A parenthesised function type keeps its parentheses in Base so that the base stays unambiguous.
func ParseOptionality(declaredOptional bool, typ string) OptionState {
	o := OptionState{Optional: declaredOptional}
	typ = strings.TrimSpace(typ)
	var kept []string
	for _, m := range ouSplitTop(typ, "|") {
		switch m = strings.TrimSpace(m); m {
		case "undefined":
			o.Optional = true
		case "null":
			o.Nullable = true
		case "":
		default:
			kept = append(kept, m)
		}
	}
	o.Base = strings.Join(kept, " | ")
	if len(kept) == 1 {
		o.Base = ouStripParens(kept[0])
		if o.Base != kept[0] && hasArrow(o.Base) {
			o.Base = kept[0]
		}
	}
	return o
}

// ParseResultOptionality is ParseOptionality for a return type: a Promise<T> result is judged by T, because the Go function returns T
// and an error (the function rules own the error).
func ParseResultOptionality(typ string) OptionState {
	typ = ouStripParens(typ)
	if inner, ok := strings.CutPrefix(typ, "Promise<"); ok && strings.HasSuffix(inner, ">") && len(ouSplitTop(typ, "|")) == 1 {
		return ParseOptionality(false, inner[:len(inner)-1])
	}
	return ParseOptionality(false, typ)
}

func hasArrow(s string) bool {
	depth := 0
	for i := 0; i+1 < len(s); i++ {
		switch s[i] {
		case '(', '[', '{', '<':
			depth++
		case ')', ']', '}':
			depth--
		case '>':
			if i > 0 && s[i-1] == '=' {
				continue
			}
			depth--
		case '=':
			if s[i+1] == '>' && depth == 0 {
				return true
			}
		}
	}
	return false
}

// GoMember is the Go side of a property or parameter: its type and, for a struct field, its raw struct tag.
type GoMember struct {
	Type types.Type
	// Tag is the raw struct tag, for example `json:"name,omitempty"`. Empty for a non-field.
	Tag string
}

// OptionalOptions tunes the optionality rules.
type OptionalOptions struct {
	// Wrappers names the generic presence wrappers (for example Opt[T]); a nil map uses DefaultWrappers.
	Wrappers map[string]bool
	// ZeroMeansUnset accepts an untagged scalar field for an optional property when the Go documentation says the zero value is
	// the unset state. It is false by default because the zero value of a string, number or bool is also a value upstream can send.
	ZeroMeansUnset bool
}

// DefaultWrappers are the names of the generic presence wrappers the rules recognise.
var DefaultWrappers = map[string]bool{"Opt": true, "Option": true, "Optional": true, "Maybe": true, "Nullable": true}

type goKind int

const (
	kindValue    goKind = iota // basic type or struct
	kindPointer                // *T
	kindNilable                // slice, map, interface, func, chan
	kindWrapper                // Opt[T]
	kindPtrToPtr               // **T
	kindRawJSON                // json.RawMessage
)

type jsonTag struct {
	present   bool
	skip      bool
	omitempty bool
	omitzero  bool
}

func (t jsonTag) omits() bool { return t.omitempty || t.omitzero }

func parseJSONTag(raw string) jsonTag {
	v, ok := reflect.StructTag(raw).Lookup("json")
	if !ok {
		return jsonTag{}
	}
	name, opts, _ := strings.Cut(v, ",")
	t := jsonTag{present: true, skip: name == "-" && opts == ""}
	for opt := range strings.SplitSeq(opts, ",") {
		switch opt {
		case "omitempty":
			t.omitempty = true
		case "omitzero":
			t.omitzero = true
		}
	}
	return t
}

func isRawJSONType(t types.Type) bool {
	var obj *types.TypeName
	switch x := t.(type) {
	case *types.Alias:
		obj = x.Obj()
	case *types.Named:
		obj = x.Obj()
	}
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	p, n := obj.Pkg().Path(), obj.Name()
	return p == "encoding/json" && n == "RawMessage" || p == "encoding/json/jsontext" && n == "Value"
}

func classify(t types.Type, wrappers map[string]bool) goKind {
	if isRawJSONType(t) {
		return kindRawJSON
	}
	if n, ok := types.Unalias(t).(*types.Named); ok && n.TypeArgs().Len() > 0 && wrappers[n.Obj().Name()] {
		return kindWrapper
	}
	switch u := types.Unalias(t).Underlying().(type) {
	case *types.Pointer:
		if _, ok := types.Unalias(u.Elem()).Underlying().(*types.Pointer); ok {
			return kindPtrToPtr
		}
		return kindPointer
	case *types.Slice, *types.Map, *types.Interface, *types.Signature, *types.Chan:
		return kindNilable
	}
	return kindValue
}

func isNonSerialized(t types.Type) bool {
	switch types.Unalias(t).Underlying().(type) {
	case *types.Signature, *types.Chan:
		return true
	}
	return false
}

func isScalar(t types.Type) bool {
	_, ok := types.Unalias(t).Underlying().(*types.Basic)
	return ok
}

// CheckOptionalProperty applies O1-O4 to an upstream property and the Go member that stands for it.
func CheckOptionalProperty(up OptionState, m GoMember, opts OptionalOptions) Finding {
	wrappers := opts.Wrappers
	if wrappers == nil {
		wrappers = DefaultWrappers
	}
	kind := classify(m.Type, wrappers)
	tag := parseJSONTag(m.Tag)
	switch {
	case !up.Optional && !up.Nullable:
		return checkRequired(kind, tag)
	case up.Optional && !up.Nullable:
		return checkOptional(kind, tag, m.Type, opts)
	case !up.Optional && up.Nullable:
		return checkNullable(kind, tag, m.Type)
	}
	return checkOptionalNullable(kind, tag)
}

func checkRequired(kind goKind, tag jsonTag) Finding {
	switch {
	case kind == kindWrapper:
		return fNo("O1", "required property is a presence wrapper, which adds an absent state")
	case kind == kindPointer || kind == kindPtrToPtr:
		return fNo("O1", "required property is a pointer, which adds a nil state upstream does not have")
	case tag.omits():
		return fNo("O1", "required property has omitempty or omitzero, which drops a zero value upstream always sends")
	}
	return fYes("O1", "required property is a Go value without an omit option")
}

func checkOptional(kind goKind, tag jsonTag, t types.Type, opts OptionalOptions) Finding {
	switch kind {
	case kindPtrToPtr:
		return fNo("O2", "optional but not nullable property is a pointer to pointer, which adds a null state")
	case kindWrapper:
		if tag.skip || hasJSONMethods(t) {
			return fYes("O2", "optional property is a presence wrapper that is not serialized or encodes itself")
		}
		return fUnknown("O2", "presence wrapper without MarshalJSON and UnmarshalJSON marshals as an object, so absent is not an absent key")
	case kindPointer:
		return fYes("O2", "optional property is a pointer")
	}
	if tag.omits() {
		v := fYes("O2", "optional property has omitempty or omitzero")
		if kind == kindValue && isScalar(t) {
			v.Notes = append(v.Notes, "the zero value of the Go type cannot be sent as a value")
		}
		return v
	}
	if kind == kindNilable || kind == kindRawJSON {
		switch {
		case tag.skip || isNonSerialized(t):
			return fYes("O2", "optional property is a non-serialized nilable type")
		case tag.present:
			return fNo("O2", "optional property of nilable type marshals nil as null; add omitempty or omitzero")
		}
		return fUnknown("O2", "optional property of nilable type has no json tag, so the unset encoding is not stated")
	}
	if opts.ZeroMeansUnset && isScalar(t) {
		return fYes("O2", "optional scalar property whose zero value is documented as unset")
	}
	return fNo("O2", "optional property is a Go value with no unset state: use *T, a presence wrapper or omitempty")
}

func checkNullable(kind goKind, tag jsonTag, t types.Type) Finding {
	switch {
	case kind == kindWrapper && (tag.skip || hasJSONMethods(t)):
		return fYes("O3", "nullable property is a presence wrapper that is not serialized or encodes itself")
	case kind == kindWrapper:
		return fUnknown("O3", "presence wrapper without MarshalJSON and UnmarshalJSON marshals as an object, so null is not null")
	case tag.omits():
		return fNo("O3", "nullable property has omitempty or omitzero, which turns null into an absent key")
	case kind == kindPointer, kind == kindNilable, kind == kindRawJSON:
		return fYes("O3", "nullable property has a Go nil state")
	case kind == kindPtrToPtr:
		return fNo("O3", "nullable but required property is a pointer to pointer, which adds an absent state")
	}
	return fNo("O3", "nullable property is a Go value with no null state")
}

func checkOptionalNullable(kind goKind, tag jsonTag) Finding {
	switch {
	case kind == kindWrapper:
		return fUnknown("O4", "a presence wrapper has one unset state, so it cannot keep null apart from absent")
	case kind == kindPtrToPtr && tag.skip:
		return fYes("O4", "optional nullable property is a non-serialized pointer to pointer")
	case kind == kindPtrToPtr && tag.present:
		return fNo("O4", "encoding/json decodes null into the outer pointer of **T, so null reads back as absent")
	case kind == kindPtrToPtr:
		return fUnknown("O4", "pointer to pointer has no json tag: if serialized, null decodes as absent")
	case kind == kindRawJSON && tag.omits():
		return fYes("O4", "json.RawMessage with omitempty keeps null (the bytes null) apart from absent (nil)")
	case kind == kindPointer, kind == kindNilable, kind == kindRawJSON:
		return fUnknown("O4", "a single nil state cannot keep null apart from absent")
	}
	return fNo("O4", "optional nullable property is a Go value with neither state")
}

// CheckOptionalResult applies O5 to the results of a Go function against an upstream return type. A trailing error result belongs to
// the function rule (Promise and exceptions) and is ignored here.
func CheckOptionalResult(up OptionState, results *types.Tuple, opts OptionalOptions) Finding {
	wrappers := opts.Wrappers
	if wrappers == nil {
		wrappers = DefaultWrappers
	}
	var rs []types.Type
	for v := range results.Variables() {
		rs = append(rs, v.Type())
	}
	if n := len(rs); n > 0 && isError(rs[n-1]) {
		rs = rs[:n-1]
	}
	if len(rs) == 0 {
		return fUnknown("O5", "the Go function returns no value")
	}
	hasBool := len(rs) == 2 && isBool(rs[1])
	if len(rs) > 2 || len(rs) == 2 && !hasBool {
		return fUnknown("O5", "the Go function returns %d values", len(rs))
	}
	kind := classify(rs[0], wrappers)
	absent := kind == kindPointer || kind == kindNilable || kind == kindRawJSON || kind == kindWrapper
	switch {
	case up.Optional && up.Nullable:
		if kind == kindWrapper || kind == kindPtrToPtr {
			return fYes("O5", "result keeps null and undefined apart")
		}
		return fUnknown("O5", "a single absent state cannot keep null apart from undefined")
	case up.Optional || up.Nullable:
		if hasBool || absent && len(rs) == 1 {
			return fYes("O5", "result has an absent state")
		}
		return fNo("O5", "result of an optional or nullable upstream type has no absent state")
	}
	if hasBool {
		return fNo("O5", "required result is (T, bool), which adds an absent state")
	}
	if kind == kindPointer && len(rs) == 1 {
		return fNo("O5", "required result is a pointer, which adds a nil state")
	}
	return fYes("O5", "required result is a Go value")
}

// CheckOptionalParam applies O6 to one upstream parameter and the Go parameter that stands for it. variadic is true for the Go
// variadic tail, whose type is the slice of the element.
func CheckOptionalParam(up OptionState, t types.Type, variadic bool, opts OptionalOptions) Finding {
	wrappers := opts.Wrappers
	if wrappers == nil {
		wrappers = DefaultWrappers
	}
	kind := classify(t, wrappers)
	switch {
	case variadic:
		if up.Optional {
			return fYes("O6", "optional parameter is the variadic tail")
		}
		return fNo("O6", "required parameter is a variadic tail, which may be empty")
	case up.Optional || up.Nullable:
		if kind == kindPointer || kind == kindNilable || kind == kindWrapper || kind == kindRawJSON {
			return fYes("O6", "optional or nullable parameter has a Go nil or absent state")
		}
		return fNo("O6", "optional or nullable parameter is a Go value with no absent state")
	}
	if kind == kindPointer || kind == kindWrapper {
		return fNo("O6", "required parameter is a pointer or wrapper, which adds an absent state")
	}
	return fYes("O6", "required parameter is a Go value")
}

// hasJSONMethods reports whether *t has both MarshalJSON and UnmarshalJSON, the only way a presence wrapper can encode absent as
// an absent key and null as null instead of marshalling its own struct fields.
func hasJSONMethods(t types.Type) bool {
	ms := types.NewMethodSet(types.NewPointer(t))
	return ms.Lookup(nil, "MarshalJSON") != nil && ms.Lookup(nil, "UnmarshalJSON") != nil
}

func isError(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Pkg() == nil && n.Obj().Name() == "error"
}

func isBool(t types.Type) bool {
	b, ok := types.Unalias(t).Underlying().(*types.Basic)
	return ok && b.Info()&types.IsBoolean != 0
}

// presenceWrapper is rule O2w: an upstream type that is optional or nullable, but not both, agrees with a generic presence wrapper
// (Opt[T]) when its base type agrees with the wrapped type and the wrapper has MarshalJSON and UnmarshalJSON. A type rule does not see
// the field tag, so a wrapper that would marshal its own struct fields (absent as an object, not an absent key) stays undecided, and so
// does an optional and nullable type, whose three states a two-state wrapper cannot hold. A wrapper whose payload disagrees is refuted.
// It stays silent for a required upstream type, where the wrapper would add an absent state (CheckOptionalProperty refutes that as O1).
// There is no type rule for **T: encoding/json decodes null into its outer pointer, so only CheckOptionalProperty, which sees the tag,
// can accept it (O4, non-serialized fields only).
type presenceWrapper struct{}

func (presenceWrapper) Name() string { return "O2w" }

func (presenceWrapper) Type(env Env, up string, t types.Type) (Verdict, bool) {
	state := ParseOptionality(false, up)
	if state.Optional == state.Nullable || state.Base == "" {
		return Verdict{}, false
	}
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.TypeArgs().Len() == 0 || !DefaultWrappers[n.Obj().Name()] {
		return Verdict{}, false
	}
	inner := env.Agree(state.Base, n.TypeArgs().At(0))
	switch {
	case inner.OK == No:
		return Refute("O2w: %s wraps a type that disagrees with %s: %s", n.Obj().Name(), state.Base, inner.Why), true
	case inner.OK == Yes && hasJSONMethods(n):
		return Accept("O2w: %s is a presence wrapper with its own JSON encoding of a type that agrees with %s", n.Obj().Name(), state.Base), true
	}
	return Verdict{}, false
}

func init() {
	RegisterType(Optionality, presenceWrapper{})
}
