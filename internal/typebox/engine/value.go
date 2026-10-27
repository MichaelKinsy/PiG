// Package engine is TypeBox 1.3.27's type-system engine (build/type/{action,engine,extends,script} and the
// parts of build/type/types and build/system the engine uses), ported once for the host. The extension SDKs
// (Go, Rust, Python) call it through one blocking host request at schema-build time instead of carrying
// ports of it, so Type.Pick, Type.Mapped, Type.Script and the rest return, byte for byte, what Pi's `Type`
// returns: the enumerable members in the order JavaScript enumerates them and the hidden members (`~kind`,
// `~optional`, ...) the later calls read.
//
// The engine works on JavaScript values, because TypeBox's own code does: a schema is an object that has
// hidden (non-enumerable) members, a context is a plain object of named schemas, and a script parser's
// intermediate results are arrays, booleans, strings and schemas in one list. Value is that model: nil is
// null, [Undef] is undefined, bool, float64, string, *big.Int (bigint), []any (array), *Obj (object) and
// *Opaque (a function or other value the engine only passes through).
package engine

import (
	"fmt"
	"math/big"
	"slices"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Undef is JavaScript's undefined.
var Undef = &undefined{}

type undefined struct{}

// Opaque is a value the engine never looks into: a function of the extension process (a refinement's check) or an
// Object.prototype member read through `in`. It has `typeof` "function", so no guard takes it for an object.
type Opaque struct {
	// Raw is the wire text it came in as (empty for a prototype member); it is written back as it was.
	Raw string
}

// Error is a JavaScript exception: the name of the error constructor and the message `error.message` has.
type Error struct{ Name, Message string }

func (e *Error) Error() string { return e.Name + ": " + e.Message }

func throw(name, format string, args ...any) {
	panic(&Error{Name: name, Message: fmt.Sprintf(format, args...)})
}

func throwError(message string) { panic(&Error{Name: "Error", Message: message}) }

// unreachable is system/unreachable: `throw Error('Unreachable')`.
func unreachable() { throwError("Unreachable") }

type prop struct {
	value      any
	enumerable bool
}

// Obj is a JavaScript object: own members in insertion order, each enumerable or hidden. Keys and EnumKeys list
// them as JavaScript does (array-index keys ascending first, the rest in insertion order).
type Obj struct {
	names []string
	props map[string]*prop
}

// NewObj returns an object whose enumerable members are the key, value pairs, in that order (a later pair for a key
// replaces the earlier one in place).
func NewObj(pairs ...any) *Obj {
	o := &Obj{props: make(map[string]*prop, len(pairs)/2)}
	for i := 0; i+1 < len(pairs); i += 2 {
		o.Set(pairs[i].(string), pairs[i+1])
	}
	return o
}

// Set defines an enumerable member: replaced in place when it exists, appended otherwise.
func (o *Obj) Set(name string, value any) { o.define(name, value, true) }

// SetHidden defines a non-enumerable member.
func (o *Obj) SetHidden(name string, value any) { o.define(name, value, false) }

func (o *Obj) define(name string, value any, enumerable bool) {
	if p, ok := o.props[name]; ok {
		p.value, p.enumerable = value, enumerable
		return
	}
	if o.props == nil {
		o.props = map[string]*prop{}
	}
	o.props[name] = &prop{value, enumerable}
	o.names = append(o.names, name)
}

// Has reports an own member, hidden or not.
func (o *Obj) Has(name string) bool { _, ok := o.props[name]; return ok }

// Get returns an own member (hidden ones too).
func (o *Obj) Get(name string) (any, bool) {
	p, ok := o.props[name]
	if !ok {
		return nil, false
	}
	return p.value, true
}

// Enumerable reports whether an own member exists and is enumerable.
func (o *Obj) Enumerable(name string) bool { p, ok := o.props[name]; return ok && p.enumerable }

// Keys is Object.getOwnPropertyNames: every own member name, hidden ones included.
func (o *Obj) Keys() []string {
	keys := slices.Clone(o.names)
	jsstring.SortOwnKeys(keys, func(k string) string { return k })
	return keys
}

// EnumKeys is Object.keys.
func (o *Obj) EnumKeys() []string {
	keys := make([]string, 0, len(o.names))
	for _, name := range o.names {
		if o.props[name].enumerable {
			keys = append(keys, name)
		}
	}
	jsstring.SortOwnKeys(keys, func(k string) string { return k })
	return keys
}

// Value is an own member's value, undefined when absent (the read `obj[name]` of a member that exists or not).
func (o *Obj) Value(name string) any {
	if p, ok := o.props[name]; ok {
		return p.value
	}
	return Undef
}

// inObjectPrototype reports whether key is one of the members `key in {}` finds through Object.prototype. A switch rather than a
// package-level set: a set is built by an initializer on every start of the program.
func inObjectPrototype(key string) bool {
	switch key {
	case "constructor", "__defineGetter__", "__defineSetter__", "hasOwnProperty", "__lookupGetter__", "__lookupSetter__", "isPrototypeOf",
		"propertyIsEnumerable", "toString", "valueOf", "__proto__", "toLocaleString":
		return true
	}
	return false
}

// isObject is Guard.IsObject: typeof "object" and not null (arrays count).
func isObject(v any) bool {
	switch v.(type) {
	case *Obj, []any:
		return true
	}
	return false
}

// isObjectNotArray is Guard.IsObjectNotArray.
func isObjectNotArray(v any) bool { _, ok := v.(*Obj); return ok }

func isArray(v any) bool { _, ok := v.([]any); return ok }

func isString(v any) bool { _, ok := v.(string); return ok }

func isBool(v any) bool { _, ok := v.(bool); return ok }

// isNumber is Guard.IsNumber: a finite number.
func isNumber(v any) bool {
	f, ok := v.(float64)
	return ok && f == f && f-f == 0
}

func isBigInt(v any) bool { _, ok := v.(*big.Int); return ok }

func isUndefined(v any) bool { return v == any(Undef) }

// isUnsafeKey is Guard.IsUnsafePropertyKey.
func isUnsafeKey(key string) bool {
	return key == "__proto__" || key == "constructor" || key == "prototype"
}

// jsEqual is `===` over the values the engine holds.
func jsEqual(a, b any) bool {
	switch x := a.(type) {
	case float64:
		y, ok := b.(float64)
		return ok && x == y
	case *big.Int:
		y, ok := b.(*big.Int)
		return ok && x.Cmp(y) == 0
	case *Opaque:
		y, ok := b.(*Opaque)
		return ok && x == y
	case []any:
		y, ok := b.([]any)
		return ok && len(x) == len(y) && (len(x) == 0 || &x[0] == &y[0])
	}
	return a == b
}

// hasKey is Guard.HasPropertyKey: `key in value` (an own member for the unsafe names, whose prototype members are
// ignored), which throws for a value that is not an object.
func hasKey(value any, key string) bool {
	switch v := value.(type) {
	case *Obj:
		if v.Has(key) {
			return true
		}
		return !isUnsafeKey(key) && inObjectPrototype(key)
	case []any:
		if key == "length" {
			return true
		}
		if n, ok := jsstring.ArrayIndex(key); ok {
			return int(n) < len(v)
		}
		return !isUnsafeKey(key) && (inObjectPrototype(key) || inArrayPrototype(key))
	}
	throw("TypeError", "Cannot use 'in' operator to search for '%s' in %s", key, toDisplay(value))
	return false
}

// inArrayPrototype reports whether key is one of the members `key in []` finds through Array.prototype.
func inArrayPrototype(key string) bool {
	switch key {
	case "at", "concat", "copyWithin", "entries", "every", "fill", "filter", "find", "findIndex", "findLast", "findLastIndex", "flat", "flatMap",
		"forEach", "includes", "indexOf", "join", "keys", "lastIndexOf", "map", "pop", "push", "reduce", "reduceRight", "reverse", "shift", "slice",
		"some", "sort", "splice", "toLocaleString", "toReversed", "toSorted", "toSpliced", "unshift", "values", "with":
		return true
	}
	return false
}

func toDisplay(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return numberString(x)
	}
	return "undefined"
}

// get is the read `value[key]`: undefined for a member that is not there, a throw for null and undefined.
func get(value any, key string) any {
	switch v := value.(type) {
	case *Obj:
		if p, ok := v.props[key]; ok {
			return p.value
		}
		if inObjectPrototype(key) {
			return &Opaque{}
		}
		return Undef
	case []any:
		if key == "length" {
			return float64(len(v))
		}
		if n, ok := jsstring.ArrayIndex(key); ok && int(n) < len(v) {
			return v[n]
		}
		if inArrayPrototype(key) || inObjectPrototype(key) {
			return &Opaque{}
		}
		return Undef
	case string:
		if key == "length" {
			return float64(jsstring.Length(v))
		}
		return Undef
	case nil, *undefined:
		throw("TypeError", "Cannot read properties of %s (reading '%s')", toDisplay(value), key)
	}
	return Undef
}

// elem returns an array element, undefined past the end.
func elem(a []any, i int) any {
	if i >= 0 && i < len(a) {
		return a[i]
	}
	return Undef
}

// ownKeys is Guard.Keys: Object.getOwnPropertyNames of an object or array.
func ownKeys(value any) []string {
	switch v := value.(type) {
	case *Obj:
		return v.Keys()
	case []any:
		keys := make([]string, 0, len(v)+1)
		for i := range v {
			keys = append(keys, fmt.Sprint(i))
		}
		return append(keys, "length")
	case nil, *undefined:
		throw("TypeError", "Cannot convert undefined or null to object")
	}
	return nil
}

// objectValues is Object.values.
func objectValues(value any) []any {
	switch v := value.(type) {
	case *Obj:
		keys := v.EnumKeys()
		out := make([]any, len(keys))
		for i, k := range keys {
			out[i] = v.props[k].value
		}
		return out
	case []any:
		return slices.Clone(v)
	case nil, *undefined:
		throw("TypeError", "Cannot convert undefined or null to object")
	}
	return nil
}

// spreadInto is `{...target, ...source}` for one source: the enumerable members of source copied in (an array's
// elements by index, a string's characters; null, undefined and the other primitives add nothing).
func spreadInto(target *Obj, source any) {
	switch s := source.(type) {
	case *Obj:
		for _, k := range s.EnumKeys() {
			target.Set(k, s.props[k].value)
		}
	case []any:
		for i, e := range s {
			target.Set(fmt.Sprint(i), e)
		}
	case string:
		units := jsstring.ToUTF16(s)
		for i, u := range units {
			target.Set(fmt.Sprint(i), jsstring.FromUTF16([]uint16{u}))
		}
	}
}

// spread is `{...a, ...b, ...}`.
func spread(sources ...any) *Obj {
	o := &Obj{}
	for _, s := range sources {
		spreadInto(o, s)
	}
	return o
}

// with is `{...base, [key]: value}`.
func with(base any, key string, value any) *Obj {
	o := spread(base)
	o.Set(key, value)
	return o
}

// ---- system/memory ----

// Create is Memory.Create(hidden, enumerable, options): `{...enumerable, ...options}` with the hidden members defined non-enumerable.
func Create(hidden *Obj, enumerable *Obj, options any) *Obj {
	result := spread(enumerable, options)
	for _, k := range hidden.EnumKeys() {
		result.SetHidden(k, hidden.props[k].value)
	}
	return result
}

// kindOf creates the schema of `kind` the common way: Memory.Create({'~kind': kind}, enumerable, options).
func kindOf(kind string, enumerable *Obj, options any) *Obj {
	return Create(NewObj("~kind", kind), enumerable, options)
}

// Clone is Memory.Clone: a deep copy. A schema object (one that has `~kind` or `~unsafe`) keeps its hidden
// members; any other object keeps only its enumerable members; the unsafe key names are dropped.
func Clone(value any) any {
	switch v := value.(type) {
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = Clone(e)
		}
		return out
	case *Obj:
		result := &Obj{}
		if v.Has("~kind") || v.Has("~unsafe") {
			for _, k := range v.Keys() {
				if isUnsafeKey(k) {
					continue
				}
				p := v.props[k]
				result.define(k, Clone(p.value), p.enumerable)
			}
			return result
		}
		for _, k := range v.EnumKeys() {
			if isUnsafeKey(k) {
				continue
			}
			result.Set(k, Clone(v.props[k].value))
		}
		return result
	}
	return value
}

// Update is Memory.Update(current, hidden, enumerable): a clone of current with the hidden members defined
// non-enumerable and the enumerable ones defined enumerable (in place when they exist).
func Update(current any, hidden, enumerable any) any {
	result := Clone(current)
	hiddenKeys := keysOf(hidden)
	target, isObj := result.(*Obj)
	if len(hiddenKeys) > 0 && !isObj && !isArray(result) {
		throw("TypeError", "Object.defineProperty called on non-object")
	}
	enumKeys := keysOf(enumerable)
	if len(enumKeys) > 0 && !isObj && !isArray(result) {
		throw("TypeError", "Object.defineProperty called on non-object")
	}
	if !isObj {
		// An array: TypeBox's Clone of one is `map`, which drops every member that is not an element, so the members
		// a later update defines never survive; the engine keeps the array itself.
		return result
	}
	for _, k := range hiddenKeys {
		target.SetHidden(k, get(hidden, k))
	}
	for _, k := range enumKeys {
		target.Set(k, get(enumerable, k))
	}
	return result
}

// keysOf is Object.keys of the hidden/enumerable argument of Update and Discard: an object's, an array's indices,
// a throw for null and undefined.
func keysOf(value any) []string {
	switch v := value.(type) {
	case *Obj:
		return v.EnumKeys()
	case []any:
		keys := make([]string, len(v))
		for i := range v {
			keys[i] = fmt.Sprint(i)
		}
		return keys
	case string:
		keys := make([]string, jsstring.Length(v))
		for i := range keys {
			keys[i] = fmt.Sprint(i)
		}
		return keys
	case nil, *undefined:
		throw("TypeError", "Cannot convert undefined or null to object")
	}
	return nil
}

// Discard is Memory.Discard(value, keys): a copy without the named members, hidden ones kept as hidden, values cloned.
func Discard(value any, keys ...string) any {
	result := &Obj{}
	switch v := value.(type) {
	case *Obj:
		for _, k := range v.Keys() {
			if slices.Contains(keys, k) {
				continue
			}
			p := v.props[k]
			result.define(k, Clone(p.value), p.enumerable)
		}
	case []any:
		for i, e := range v {
			if !slices.Contains(keys, fmt.Sprint(i)) {
				result.Set(fmt.Sprint(i), Clone(e))
			}
		}
		if !slices.Contains(keys, "length") {
			result.SetHidden("length", float64(len(v)))
		}
	case nil, *undefined:
		throw("TypeError", "Cannot convert undefined or null to object")
	}
	return result
}

// Assign is Memory.Assign(left, right): `{...left, ...right}`.
func Assign(left, right any) *Obj { return spread(left, right) }

// asArray reads value as the array a method like `.map` needs; anything else throws as the method call would.
func asArray(value any, method string) []any {
	if a, ok := value.([]any); ok {
		return a
	}
	switch value.(type) {
	case nil, *undefined:
		throw("TypeError", "Cannot read properties of %s (reading '%s')", toDisplay(value), method)
	}
	throw("TypeError", "%s.%s is not a function", toDisplay(value), method)
	return nil
}

// mapTypes is `types.map(fn)`.
func mapTypes(types any, fn func(any) any) []any {
	in := asArray(types, "map")
	out := make([]any, len(in))
	for i, t := range in {
		out[i] = fn(t)
	}
	return out
}
