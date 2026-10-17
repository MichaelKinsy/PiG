package yaml12

// Go values for the JavaScript objects the library's named tags build.

// Date is a JavaScript Date; MS is its time value, NaN for an invalid date.
type Date struct{ MS float64 }

// Set is a JavaScript Set in insertion order.
type Set struct{ Values []any }

// Map is a JavaScript Map in insertion order.
type Map struct{ Keys, Values []any }

// Symbol is a JavaScript symbol with the given description.
type Symbol string

// Object is a JavaScript object ParseOrdered built: Keys are its own string keys in insertion order (a repeated key keeps its first position), and
// Values holds each key's value.
type Object struct {
	Keys   []string
	Values map[string]any
}
