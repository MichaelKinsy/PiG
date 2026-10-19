// Package jv is the ordered JSON value model of the Durable core's session
// layer.
//
// A value is nil, bool, float64, string, *Object or []any. An Object keeps
// insertion order and enumerates in ECMAScript property order (canonical array
// index keys ascending first, then the other keys in insertion order), so a
// value stringifies exactly as JSON.stringify prints the object JSON.parse
// built. A string holds well-formed UTF-8 except that a lone UTF-16 surrogate
// is held as its three-byte generalized encoding (WTF-8), because JSON text can
// carry one and JSON.stringify prints it back as a \udXXX escape.
//
// The package uses no reflection and no encoding/json, and keeps no
// package-level mutable state.
package jv

import (
	"maps"
	"slices"
)

// Object is a JSON object with ECMAScript key order.
type Object struct {
	keys   []string // insertion order; a slot Delete retired holds a stale key and a tombstone in vals
	vals   []any
	index  map[string]int // live key -> slot; nil while the object is small, and then no slot is retired
	nIndex int            // live keys that are canonical array indexes
	dead   int            // retired slots
}

// tombstone marks a slot Delete retired. Retiring leaves the other slots where they are, so deleting members oldest
// first costs O(1) each instead of renumbering every later member; the slots are squeezed out once half are retired.
type tombstone struct{}

const indexThreshold = 12

// firstMembers is the room the first Set gives an object created without a capacity.
const firstMembers = 4

// object4 is an object with the room of its first members: one allocation instead of the object and its two slices.
type object4 struct {
	Object
	keys [firstMembers]string
	vals [firstMembers]any
}

// NewObject returns an empty object with room for its first few members.
func NewObject() *Object {
	o := &object4{}
	o.Object.keys, o.Object.vals = o.keys[:0], o.vals[:0]
	return &o.Object
}

// NewObjectCap returns an empty object with room for n members.
func NewObjectCap(n int) *Object {
	if n <= firstMembers {
		return NewObject()
	}
	return &Object{keys: make([]string, 0, n), vals: make([]any, 0, n)}
}

// Len returns the number of members.
func (o *Object) Len() int { return len(o.keys) - o.dead }

func (o *Object) find(key string) int {
	if o.index != nil {
		if at, ok := o.index[key]; ok {
			return at
		}
		return -1
	}
	// Most members of a small object differ from key in length: compare it before the call TinyGo makes for a string comparison.
	n := len(key)
	for at, k := range o.keys {
		if len(k) == n && k == key {
			return at
		}
	}
	return -1
}

// Get returns the member named key.
func (o *Object) Get(key string) (any, bool) {
	at := o.find(key)
	if at < 0 {
		return nil, false
	}
	return o.vals[at], true
}

// Has reports whether key is a member.
func (o *Object) Has(key string) bool { return o.find(key) >= 0 }

// Set replaces the value of an existing member in place, or appends a new
// member. It matches assignment to an own data property.
func (o *Object) Set(key string, value any) {
	if at := o.find(key); at >= 0 {
		o.vals[at] = value
		return
	}
	if o.keys == nil {
		// Most objects built member by member hold a handful of members: one allocation each instead of 1, 2, 4 growth steps.
		o.keys = make([]string, 0, firstMembers)
		o.vals = make([]any, 0, firstMembers)
	}
	o.keys = append(o.keys, key)
	o.vals = append(o.vals, value)
	if IsIndexKey(key) {
		o.nIndex++
	}
	if o.index != nil {
		o.index[key] = len(o.keys) - 1
	} else if len(o.keys) > indexThreshold {
		o.index = make(map[string]int, len(o.keys)*2)
		for at, k := range o.keys {
			o.index[k] = at
		}
	}
}

// Delete removes key and reports whether it was a member.
func (o *Object) Delete(key string) bool {
	at := o.find(key)
	if at < 0 {
		return false
	}
	if IsIndexKey(key) {
		o.nIndex--
	}
	if o.index == nil {
		o.keys = append(o.keys[:at], o.keys[at+1:]...)
		o.vals = append(o.vals[:at], o.vals[at+1:]...)
		return true
	}
	delete(o.index, key)
	o.vals[at] = tombstone{}
	o.dead++
	if o.dead*2 > len(o.keys) {
		o.compact()
	}
	return true
}

// compact squeezes the retired slots out into fresh slices (a slice Keys returned earlier stays as it was) and numbers
// the index again.
func (o *Object) compact() {
	live := len(o.keys) - o.dead
	keys := make([]string, 0, live+1)
	vals := make([]any, 0, live+1)
	for at, k := range o.keys {
		if _, gone := o.vals[at].(tombstone); gone {
			continue
		}
		o.index[k] = len(keys)
		keys = append(keys, k)
		vals = append(vals, o.vals[at])
	}
	o.keys, o.vals, o.dead = keys, vals, 0
}

// Keys returns the keys in ECMAScript enumeration order. The result is shared
// with the object and must not be modified.
func (o *Object) Keys() []string {
	if o.nIndex == 0 && o.dead == 0 {
		return o.keys
	}
	return o.orderedKeys()
}

// Entries calls fn for each member in enumeration order until fn returns false.
func (o *Object) Entries(fn func(key string, value any) bool) {
	if o.nIndex == 0 {
		o.Members(fn)
		return
	}
	for _, k := range o.orderedKeys() {
		v, _ := o.Get(k)
		if !fn(k, v) {
			return
		}
	}
}

// Members calls fn for each member in insertion order, index keys not first, until fn returns false. fn may Set a key
// that is already a member.
func (o *Object) Members(fn func(key string, value any) bool) {
	for at, k := range o.keys {
		v := o.vals[at]
		if o.dead > 0 {
			if _, gone := v.(tombstone); gone {
				continue
			}
		}
		if !fn(k, v) {
			return
		}
	}
}

func (o *Object) orderedKeys() []string {
	var idx []string
	rest := make([]string, 0, o.Len())
	for at, k := range o.keys {
		if o.dead > 0 {
			if _, gone := o.vals[at].(tombstone); gone {
				continue
			}
		}
		if IsIndexKey(k) {
			idx = append(idx, k)
		} else {
			rest = append(rest, k)
		}
	}
	slices.SortFunc(idx, func(a, b string) int {
		switch {
		case indexLess(a, b):
			return -1
		case indexLess(b, a):
			return 1
		}
		return 0
	})
	return append(idx, rest...)
}

func indexLess(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

// IsIndexKey reports whether key is a canonical ECMAScript array index: a
// decimal integer without leading zeros in 0..2^32-2.
func IsIndexKey(key string) bool {
	n := len(key)
	if n == 0 || n > 10 {
		return false
	}
	if key[0] == '0' {
		return n == 1
	}
	var v uint64
	for i := range n {
		c := key[i]
		if c < '0' || c > '9' {
			return false
		}
		v = v*10 + uint64(c-'0')
	}
	return v <= 4294967294
}

// Clone returns a deep copy.
func Clone(value any) any {
	switch v := value.(type) {
	case *Object:
		c := NewObjectCap(v.Len())
		c.nIndex = v.nIndex
		for at, k := range v.keys {
			if v.dead > 0 {
				if _, gone := v.vals[at].(tombstone); gone {
					continue
				}
			}
			c.keys = append(c.keys, k)
			c.vals = append(c.vals, Clone(v.vals[at]))
		}
		if v.dead > 0 {
			c.reindex()
		} else if v.index != nil {
			c.index = make(map[string]int, len(v.index))
			maps.Copy(c.index, v.index)
		}
		return c
	case []any:
		c := make([]any, len(v))
		for i, item := range v {
			c[i] = Clone(item)
		}
		return c
	}
	return value
}

// Equal reports deep equality. Object key order is ignored, as in a deep
// comparison of the decoded values.
func Equal(a, b any) bool {
	switch x := a.(type) {
	case *Object:
		y, ok := b.(*Object)
		if !ok || x.Len() != y.Len() {
			return false
		}
		if x == y {
			return true
		}
		equal := true
		x.Members(func(k string, v any) bool {
			other, present := y.Get(k)
			equal = present && Equal(v, other)
			return equal
		})
		return equal
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !Equal(x[i], y[i]) {
				return false
			}
		}
		return true
	case nil:
		return b == nil
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	case float64:
		y, ok := b.(float64)
		return ok && x == y
	case Raw:
		y, ok := b.(Raw)
		return ok && string(x) == string(y)
	}
	return false
}

// Shallow returns a copy that shares the member values.
func (o *Object) Shallow() *Object {
	c := &Object{nIndex: o.nIndex}
	if o.dead > 0 {
		c.keys, c.vals = make([]string, 0, o.Len()+1), make([]any, 0, o.Len()+1)
		o.Members(func(k string, v any) bool {
			c.keys = append(c.keys, k)
			c.vals = append(c.vals, v)
			return true
		})
		c.reindex()
		return c
	}
	c.keys = append(make([]string, 0, len(o.keys)+1), o.keys...)
	c.vals = append(make([]any, 0, len(o.vals)+1), o.vals...)
	if o.index != nil {
		c.index = make(map[string]int, len(o.index)+1)
		maps.Copy(c.index, o.index)
	}
	return c
}

// reindex builds the index of an object whose slots are dense, when it is large enough to want one.
func (o *Object) reindex() {
	if len(o.keys) <= indexThreshold {
		return
	}
	o.index = make(map[string]int, len(o.keys)*2)
	for at, k := range o.keys {
		o.index[k] = at
	}
}

// Raw is a pre-serialized JSON value: valid JSON text that Append splices
// verbatim. It lets a caller hand a large stored record (a model message) to a
// record without parsing it. Equal and Clone treat it as opaque bytes.
type Raw []byte
