package schema

// Ports packages/schema/src/checks.ts
//
// Check factories: each returns a closure that captures only its arguments, never the compiler, so a compiled schema keeps no compile-time state alive.

import (
	"bytes"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/schema/internal/ecmaregex"
)

func always(any) bool { return true }
func never(any) bool  { return false }

func isString(v any) bool {
	_, ok := v.(string)
	return ok
}

// isNumber is Number.isFinite.
// a `number` is finite, as in TypeBox (Pi main 55a5c6914). JSON has no NaN or Infinity, so only a value built in code can be one.
func isNumber(v any) bool {
	n, ok := asNumber(v)
	return ok && isFinite(n)
}

func isIntegerValue(v any) bool {
	n, ok := asNumber(v)
	return ok && isInteger(n)
}

func isBoolean(v any) bool {
	_, ok := v.(bool)
	return ok
}

func isNull(v any) bool { return v == nil }

func isArray(v any) bool {
	_, ok := v.([]any)
	return ok
}

func isBytes(v any) bool {
	_, ok := v.([]byte)
	return ok
}

// typeChecks maps a `type` name to its check; `bytes` ([]byte) extends JSON Schema for values that never pass through JSON.
var typeChecks = map[string]Check{
	"string":  isString,
	"number":  isNumber,
	"integer": isIntegerValue,
	"boolean": isBoolean,
	"null":    isNull,
	"array":   isArray,
	"object":  isObject,
	"bytes":   isBytes,
}

// guard says whether a combined object or array check also checks the type: not at all, strictly, or passing other types.
type guard int

const (
	guardNone guard = iota
	guardStrict
	guardLoose
)

// and returns a check of all of parts.
func and(parts []Check) Check {
	switch len(parts) {
	case 0:
		return always
	case 1:
		return parts[0]
	case 2:
		a, b := parts[0], parts[1]
		return func(v any) bool { return a(v) && b(v) }
	case 3:
		a, b, c := parts[0], parts[1], parts[2]
		return func(v any) bool { return a(v) && b(v) && c(v) }
	}
	return func(v any) bool {
		for _, part := range parts {
			if !part(v) {
				return false
			}
		}
		return true
	}
}

// checkSlot holds a check that is set later: the indirection a recursive reference goes through.
type checkSlot struct{ check Check }

func indirect(slot *checkSlot) Check { return func(v any) bool { return slot.check(v) } }

// scopeStack is the dynamic scope: the base URIs of the schema resources being evaluated, innermost last.
type scopeStack struct{ uris []string }

// scopedCheck runs check with uri pushed onto the dynamic scope (only for schemas with `$dynamicRef`).
func scopedCheck(scope *scopeStack, uri string, check Check) Check {
	return func(v any) bool {
		scope.uris = append(scope.uris, uri)
		ok := check(v)
		scope.uris = scope.uris[:len(scope.uris)-1]
		return ok
	}
}

// typeCheck is any of the named types.
func typeCheck(types []any) Check {
	checks := make([]Check, len(types))
	for i, t := range types {
		checks[i] = never
		if name, ok := t.(string); ok {
			if check, ok := typeChecks[name]; ok {
				checks[i] = check
			}
		}
	}
	if len(checks) == 1 {
		return checks[0]
	}
	return func(v any) bool {
		for _, check := range checks {
			if check(v) {
				return true
			}
		}
		return false
	}
}

// bytesCheck is a []byte within the length bounds.
func bytesCheck(lo, hi float64) Check {
	return func(v any) bool {
		b, ok := v.([]byte)
		return ok && float64(len(b)) >= lo && float64(len(b)) <= hi
	}
}

// lengthOk compares the code point count of s with the bounds. A string of at most max bytes has at most max code points, so a string within a bound and a zero minimum needs no count.
func lengthOk(s string, lo, hi float64) bool {
	if lo <= 0 && float64(len(s)) <= hi {
		return true
	}
	n := float64(codePoints(s))
	if n < lo {
		return false
	}
	return n <= hi
}

// stringCheck is the string keywords: length bounds, pattern and format.
func stringCheck(strict bool, lo, hi float64, re *ecmaregex.Regexp, format FormatCheck) Check {
	if re != nil && lo == 0 && math.IsInf(hi, 1) && format == nil {
		if strict {
			return func(v any) bool { s, ok := v.(string); return ok && re.MatchString(s) }
		}
		return func(v any) bool { s, ok := v.(string); return !ok || re.MatchString(s) }
	}
	if re == nil && format == nil {
		if strict {
			return func(v any) bool { s, ok := v.(string); return ok && lengthOk(s, lo, hi) }
		}
		return func(v any) bool { s, ok := v.(string); return !ok || lengthOk(s, lo, hi) }
	}
	return func(v any) bool {
		s, ok := v.(string)
		if !ok {
			return !strict
		}
		return lengthOk(s, lo, hi) && (re == nil || re.MatchString(s)) && (format == nil || format(s))
	}
}

// numberCheck is the number keywords. mode 0 passes other types, 1 requires a number, 2 an integer.
func numberCheck(mode int, lo, hi, xmin, xmax float64, multipleOf *float64) Check {
	return func(v any) bool {
		n, ok := asNumber(v)
		if !ok {
			return mode == 0
		}
		// mode 1 and 2 require a finite number (Pi main 55a5c6914).
		if mode != 0 && !isFinite(n) {
			return false
		}
		if mode == 2 && !isInteger(n) {
			return false
		}
		return n >= lo && n <= hi && n > xmin && n < xmax && (multipleOf == nil || isMultiple(n, *multipleOf))
	}
}

// isMultiple reports whether v is a multiple of d, exactly for decimals: 0.0075 / 0.0001 is 74.99999999999999 in floating point.
func isMultiple(v, d float64) bool {
	q := v / d
	if isInteger(q) {
		return true
	}
	if math.IsInf(q, 0) || math.IsNaN(q) {
		return math.Mod(v, d) == 0
	}
	vc, ve := decimal(v)
	dc, de := decimal(d)
	if ve >= de {
		scaled := new(big.Int).Mul(vc, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(ve-de)), nil))
		return new(big.Int).Rem(scaled, dc).Sign() == 0
	}
	divisor := new(big.Int).Mul(dc, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(de-ve)), nil))
	return new(big.Int).Rem(vc, divisor).Sign() == 0
}

// decimal splits |x| into an integer coefficient and a power of ten from its JavaScript string.
func decimal(x float64) (*big.Int, int) {
	mantissa, exponent, _ := strings.Cut(numberString(math.Abs(x)), "e")
	point := strings.IndexByte(mantissa, '.')
	fraction := 0
	if point >= 0 {
		fraction = len(mantissa) - point - 1
	}
	coefficient, _ := new(big.Int).SetString(strings.Replace(mantissa, ".", "", 1), 10)
	power := 0
	if exponent != "" {
		//portlint:allow numbers exponent is the tail of Number#toString's "e+21"/"e-7" form, a signed decimal integer that Atoi reads as checks.ts decimal's Number(exponent) does
		power, _ = strconv.Atoi(exponent)
	}
	return coefficient, power - fraction
}

// guardObject is inner for objects; other values pass unless strict.
func guardObject(inner Check, strict bool) Check {
	if strict {
		return func(v any) bool { return isObject(v) && inner(v) }
	}
	return func(v any) bool { return !isObject(v) || inner(v) }
}

// guardArray is inner for arrays; other values pass unless strict.
func guardArray(inner Check, strict bool) Check {
	if strict {
		return func(v any) bool { return isArray(v) && inner(v) }
	}
	return func(v any) bool { return !isArray(v) || inner(v) }
}

// property is a declared property in an object check.
type property struct {
	key      string
	check    Check
	required bool
}

// declaredObject is an object with declared properties. The declared names are looked up in the value, and `closed` rejects any member not declared (the members are counted, so unknown keys are found without allocating).
func declaredObject(properties []property, closed bool, g guard) Check {
	return func(v any) bool {
		o, isObj := asObject(v)
		if g != guardNone && !isObj {
			return g == guardLoose
		}
		present := 0
		for i := range properties {
			p := &properties[i]
			x, ok := o.get(p.key)
			if !ok {
				if p.required {
					return false
				}
				continue
			}
			present++
			if !p.check(x) {
				return false
			}
		}
		return !closed || o.len() <= present
	}
}

// patternCheck is a `patternProperties` entry.
type patternCheck struct {
	re    *ecmaregex.Regexp
	check Check
}

// openObject is an object with declared properties, required keys not declared, pattern properties and additional properties.
func openObject(properties []property, keys []string, extraRequired []string, patterns []patternCheck, additional Check, g guard) Check {
	var known map[string]struct{}
	if len(patterns) > 0 || additional != nil {
		known = make(map[string]struct{}, len(keys))
		for _, k := range keys {
			known[k] = struct{}{}
		}
	}
	return func(v any) bool {
		o, isObj := asObject(v)
		if g != guardNone && !isObj {
			return g == guardLoose
		}
		for i := range properties {
			p := &properties[i]
			x, ok := o.get(p.key)
			if !ok {
				if p.required {
					return false
				}
				continue
			}
			if !p.check(x) {
				return false
			}
		}
		for _, k := range extraRequired {
			if !o.has(k) {
				return false
			}
		}
		if known == nil {
			return true
		}
		pass := true
		o.each(func(k string, x any) bool {
			_, matched := known[k]
			for _, pattern := range patterns {
				if pattern.re.MatchString(k) {
					matched = true
					if !pattern.check(x) {
						pass = false
						return false
					}
				}
			}
			if !matched && additional != nil && !additional(x) {
				pass = false
				return false
			}
			return true
		})
		return pass
	}
}

// propertyNamesCheck requires every property name to pass check (v is an object).
func propertyNamesCheck(check Check) Check {
	return func(v any) bool {
		o, _ := asObject(v)
		pass := true
		o.each(func(k string, _ any) bool {
			pass = check(k)
			return pass
		})
		return pass
	}
}

// propertyCountCheck is `minProperties` and `maxProperties` (v is an object).
func propertyCountCheck(lo, hi float64) Check {
	return func(v any) bool {
		o, _ := asObject(v)
		n := float64(o.len())
		return n >= lo && n <= hi
	}
}

// dependency is a `dependentRequired` entry.
type dependency struct {
	key  string
	list []any
}

// dependentRequiredCheck is `dependentRequired` (v is an object).
func dependentRequiredCheck(entries []dependency) Check {
	return func(v any) bool {
		o, _ := asObject(v)
		for _, entry := range entries {
			if !o.has(entry.key) {
				continue
			}
			for _, name := range entry.list {
				if !o.has(propertyKey(name)) {
					return false
				}
			}
		}
		return true
	}
}

// propertyKey is the property key a JavaScript value names.
func propertyKey(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return jsToString(v)
}

// dependentCheck is a `dependentSchemas` entry.
type dependentCheck struct {
	key   string
	check Check
}

// dependentSchemasCheck is `dependentSchemas` (v is an object).
func dependentSchemasCheck(entries []dependentCheck) Check {
	return func(v any) bool {
		o, _ := asObject(v)
		for _, entry := range entries {
			if o.has(entry.key) && !entry.check(v) {
				return false
			}
		}
		return true
	}
}

// listCheck is the common array: guard, length bounds, prefix items and the items after them, in one closure.
func listCheck(g guard, lo, hi float64, prefix []Check, rest Check) Check {
	n := len(prefix)
	return func(v any) bool {
		list, ok := v.([]any)
		if !ok {
			return g == guardLoose
		}
		length := len(list)
		if float64(length) < lo || float64(length) > hi {
			return false
		}
		m := min(length, n)
		for i := range m {
			if !prefix[i](list[i]) {
				return false
			}
		}
		if rest == nil {
			return true
		}
		for i := n; i < length; i++ {
			if !rest(list[i]) {
				return false
			}
		}
		return true
	}
}

// itemsCheck is the prefix items and the items after them (v is an array).
func itemsCheck(prefix []Check, rest Check) Check {
	return listCheck(guardNone, 0, math.Inf(1), prefix, rest)
}

// lengthCheck is `minItems` and `maxItems` (v is an array).
func lengthCheck(lo, hi float64) Check {
	return func(v any) bool {
		list, _ := v.([]any)
		n := float64(len(list))
		return n >= lo && n <= hi
	}
}

// containsCheck is `contains` with its bounds (v is an array).
func containsCheck(check Check, lo, hi float64) Check {
	return func(v any) bool {
		list, _ := v.([]any)
		n := 0.0
		for _, item := range list {
			if check(item) {
				n++
				if n > hi {
					return false
				}
			}
		}
		return n >= lo
	}
}

// uniqueCheck is `uniqueItems` (v is an array).
func uniqueCheck(v any) bool {
	list, _ := v.([]any)
	for i := 1; i < len(list); i++ {
		for j := range i {
			if deepEqual(list[i], list[j]) {
				return false
			}
		}
	}
	return true
}

// primitiveKey is a primitive as a Set holds it: SameValueZero identity.
type primitiveKey struct {
	kind   byte
	text   string
	number float64
}

func keyOf(v any) (primitiveKey, bool) {
	if n, ok := asNumber(v); ok {
		if math.IsNaN(n) {
			return primitiveKey{kind: 'N'}, true
		}
		return primitiveKey{kind: 'n', number: n + 0}, true
	}
	switch x := v.(type) {
	case nil:
		return primitiveKey{kind: 'z'}, true
	case bool:
		if x {
			return primitiveKey{kind: 'b', number: 1}, true
		}
		return primitiveKey{kind: 'b'}, true
	case string:
		return primitiveKey{kind: 's', text: x}, true
	}
	return primitiveKey{}, false
}

// enumCheck is `enum`: up to 8 primitives by a scan, more by a set, objects and arrays by deep equality.
func enumCheck(values []any) Check {
	var primitives, containers []any
	for _, x := range values {
		if isContainer(x) {
			containers = append(containers, x)
		} else {
			primitives = append(primitives, x)
		}
	}
	if len(containers) == 0 && len(primitives) <= 8 {
		return func(v any) bool {
			for _, p := range primitives {
				if strictEquals(p, v) {
					return true
				}
			}
			return false
		}
	}
	set := make(map[primitiveKey]struct{}, len(primitives))
	for _, p := range primitives {
		if key, ok := keyOf(p); ok {
			set[key] = struct{}{}
		}
	}
	return func(v any) bool {
		if key, ok := keyOf(v); ok {
			_, has := set[key]
			return has
		}
		if !isContainer(v) {
			return false
		}
		for _, o := range containers {
			if deepEqual(v, o) {
				return true
			}
		}
		return false
	}
}

// constCheck is `const`.
func constCheck(value any) Check {
	if !isContainer(value) {
		return func(v any) bool { return strictEquals(v, value) }
	}
	return func(v any) bool { return deepEqual(v, value) }
}

// anyCheck is `anyOf`.
func anyCheck(checks []Check) Check {
	return func(v any) bool {
		for _, check := range checks {
			if check(v) {
				return true
			}
		}
		return false
	}
}

// oneCheck is `oneOf`.
func oneCheck(checks []Check) Check {
	return func(v any) bool {
		n := 0
		for _, check := range checks {
			if check(v) {
				n++
				if n > 1 {
					return false
				}
			}
		}
		return n == 1
	}
}

// notCheck is `not`.
func notCheck(check Check) Check { return func(v any) bool { return !check(v) } }

// ifCheck is `if`, `then` and `else`.
func ifCheck(cond, then, otherwise Check) Check {
	return func(v any) bool {
		if cond(v) {
			return then(v)
		}
		return otherwise(v)
	}
}

// taggedCheck is a union of objects tagged by a const on key: the tag selects the one branch that can match.
func taggedCheck(key string, tags []any, checks []Check) Check {
	return func(v any) bool {
		o, ok := asObject(v)
		if !ok {
			return false
		}
		tag, present := o.get(key)
		if !present {
			return false
		}
		for i, t := range tags {
			if strictEquals(t, tag) {
				return checks[i](v)
			}
		}
		return false
	}
}

// Equal is JSON equality as `const`, `enum` and `uniqueItems` compare (Pi's `equal`, Pi main af79b0c3d; TypeBox's `Value.Equal`): the same primitives, arrays with equal items in order, objects with the same keys and equal values (key order does not matter).
func Equal(a, b any) bool { return deepEqual(a, b) }

// deepEqual is JSON equality: same primitives, arrays with equal items in order, objects with the same keys and equal values.
func deepEqual(a, b any) bool {
	if strictEquals(a, b) {
		return true
	}
	switch x := a.(type) {
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !deepEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	case []byte:
		y, ok := b.([]byte)
		return ok && bytes.Equal(x, y)
	}
	x, ok := asObject(a)
	if !ok {
		return false
	}
	y, ok := asObject(b)
	if !ok || x.len() != y.len() {
		return false
	}
	equal := true
	x.each(func(k string, xv any) bool {
		yv, present := y.get(k)
		equal = present && deepEqual(xv, yv)
		return equal
	})
	return equal
}
