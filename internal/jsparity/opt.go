package jsparity

// Opt is an object member that JavaScript can leave undefined (JSON.stringify omits it), set to null, or set to a value.
type Opt[T any] struct {
	state optState
	value T
}

type optState uint8

const (
	optAbsent optState = iota
	optNull
	optSet
)

// Some is a member set to v.
func Some[T any](v T) Opt[T] { return Opt[T]{state: optSet, value: v} }

// Null is a member set to null.
func Null[T any]() Opt[T] { return Opt[T]{state: optNull} }

// Get returns the value and whether the member is set to one. An absent or null member returns the zero value and false.
func (o Opt[T]) Get() (T, bool) { return o.value, o.state == optSet }

// IsNull reports whether the member is null.
func (o Opt[T]) IsNull() bool { return o.state == optNull }

// Present reports whether JSON.stringify writes the member: it is null or set.
func (o Opt[T]) Present() bool { return o.state != optAbsent }

// AppendOpt appends the member's JSON: null when it is null, and appendValue's output when it is set. An absent member appends what JSON.stringify
// writes for undefined inside an array, null; an object writer checks Present and leaves the member out instead.
func AppendOpt[T any](dst []byte, o Opt[T], appendValue func([]byte, T) []byte) []byte {
	if o.state != optSet {
		return append(dst, "null"...)
	}
	return appendValue(dst, o.value)
}
