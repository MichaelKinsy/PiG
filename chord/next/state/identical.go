package state

import (
	"reflect"
	"unsafe"
)

// Ports the `===` Pi applies to JSON and native values (derive.ts `after !== before`, native-source.ts
// `shown.value === start.value`, writable.ts `value === this.#value`).

// Identical is JavaScript's `===` on a Go value: equal numbers, strings and booleans; the same pointer, map, channel or
// function value (a closure is its own identity, as a JavaScript function object is); the same slice (data pointer and
// length: JavaScript has one identity for an array, a Go slice header is a view of its backing array); equal values of a
// comparable struct or array type, which have no identity of their own. Values of different dynamic types, of an
// uncomparable struct or array type, and NaN are never identical. Two nil interface values are identical.
func Identical[T any](a, b T) bool {
	switch x := any(a).(type) {
	case float64:
		y, ok := any(b).(float64)
		return ok && x == y
	case string:
		y, ok := any(b).(string)
		return ok && x == y
	case bool:
		y, ok := any(b).(bool)
		return ok && x == y
	}
	left, right := reflect.ValueOf(any(a)), reflect.ValueOf(any(b))
	if !left.IsValid() || !right.IsValid() {
		return !left.IsValid() && !right.IsValid()
	}
	if left.Type() != right.Type() {
		return false
	}
	switch left.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Chan, reflect.UnsafePointer:
		return left.UnsafePointer() == right.UnsafePointer()
	case reflect.Slice:
		return left.UnsafePointer() == right.UnsafePointer() && left.Len() == right.Len()
	case reflect.Func:
		return funcIdentity(any(a)) == funcIdentity(any(b))
	default:
		return left.Comparable() && left.Equal(right)
	}
}

// funcIdentity is the closure object of a function value. A func value is pointer-shaped, so the data word of the
// interface holding it is the closure itself; reflect's Pointer reports only the code, which two closures of one
// function literal share.
func funcIdentity(function any) unsafe.Pointer {
	return (*[2]unsafe.Pointer)(unsafe.Pointer(&function))[1] //nolint:gosec // G103: deliberate read of the interface data word, the only way to tell closures of one literal apart.
}
