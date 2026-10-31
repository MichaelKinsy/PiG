package http

import (
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Go-only helpers of the package.

// must is the value of a declaration that cannot fail with the constants given.
func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

// recoverAs turns a recovered panic into the error a JavaScript `throw` of it would be (plugin code: a hook, a route, a
// wrap, a socket handler). Use as `defer recoverAs(&err)`.
func recoverAs(err *error) {
	if recovered := recover(); recovered != nil {
		if thrown, ok := recovered.(error); ok {
			*err = thrown
		} else {
			*err = errors.New(fmt.Sprint(recovered))
		}
	}
}

// jsonString is JSON.stringify of a string.
func jsonString(text string) string {
	out, err := chordjson.Marshal(text)
	if err != nil {
		return `""`
	}
	return string(out)
}

// StringSet is a set of strings in insertion order (a JavaScript `Set<string>`).
type StringSet struct {
	order []string
	seen  map[string]struct{}
}

// NewStringSet is a set of values.
func NewStringSet(values ...string) *StringSet {
	set := &StringSet{seen: map[string]struct{}{}}
	for _, value := range values {
		set.Add(value)
	}
	return set
}

// Add adds value.
func (s *StringSet) Add(value string) {
	if _, ok := s.seen[value]; ok {
		return
	}
	s.seen[value] = struct{}{}
	s.order = append(s.order, value)
}

// Has is whether value is in the set.
func (s *StringSet) Has(value string) bool {
	_, ok := s.seen[value]
	return ok
}

// Size is how many values the set holds.
func (s *StringSet) Size() int { return len(s.order) }

// Values are its values in insertion order.
func (s *StringSet) Values() []string { return append([]string(nil), s.order...) }
