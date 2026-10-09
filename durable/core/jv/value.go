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
	keys     []string
	vals     []any
	index    map[string]int
	indexKey bool // some key is a canonical array index
}

const indexThreshold = 12

// NewObject returns an empty object.
func NewObject() *Object { return &Object{} }

// NewObjectCap returns an empty object with room for n members.
func NewObjectCap(n int) *Object {
	return &Object{keys: make([]string, 0, n), vals: make([]any, 0, n)}
}

// Len returns the number of members.
func (o *Object) Len() int { return len(o.keys) }

func (o *Object) find(key string) int {
	if o.index != nil {
		if at, ok := o.index[key]; ok {
			return at
		}
		return -1
	}
	for at, k := range o.keys {
		if k == key {
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
	o.keys = append(o.keys, key)
	o.vals = append(o.vals, value)
	if IsIndexKey(key) {
		o.indexKey = true
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
	o.keys = append(o.keys[:at], o.keys[at+1:]...)
	o.vals = append(o.vals[:at], o.vals[at+1:]...)
	if o.index != nil {
		delete(o.index, key)
		for i := at; i < len(o.keys); i++ {
			o.index[o.keys[i]] = i
		}
	}
	if o.indexKey {
		o.indexKey = slices.ContainsFunc(o.keys, IsIndexKey)
	}
	return true
}

// Keys returns the keys in ECMAScript enumeration order. The result is shared
// with the object and must not be modified.
func (o *Object) Keys() []string {
	if !o.indexKey {
		return o.keys
	}
	return o.orderedKeys()
}

// Entries calls fn for each member in enumeration order until fn returns false.
func (o *Object) Entries(fn func(key string, value any) bool) {
	if !o.indexKey {
		for at, k := range o.keys {
			if !fn(k, o.vals[at]) {
				return
			}
		}
		return
	}
	for _, k := range o.orderedKeys() {
		v, _ := o.Get(k)
		if !fn(k, v) {
			return
		}
	}
}

func (o *Object) orderedKeys() []string {
	var idx []string
	rest := make([]string, 0, len(o.keys))
	for _, k := range o.keys {
		if IsIndexKey(k) {
			idx = append(idx, k)
		} else {
			rest = append(rest, k)
		}
	}
	// insertion sort by numeric value: index keys are rare and few
	for i := 1; i < len(idx); i++ {
		for j := i; j > 0 && indexLess(idx[j], idx[j-1]); j-- {
			idx[j], idx[j-1] = idx[j-1], idx[j]
		}
	}
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
		c := NewObjectCap(len(v.keys))
		c.keys = append(c.keys, v.keys...)
		c.indexKey = v.indexKey
		for _, item := range v.vals {
			c.vals = append(c.vals, Clone(item))
		}
		if v.index != nil {
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
		if !ok || len(x.keys) != len(y.keys) {
			return false
		}
		if x == y {
			return true
		}
		for at, k := range x.keys {
			other, present := y.Get(k)
			if !present || !Equal(x.vals[at], other) {
				return false
			}
		}
		return true
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
	}
	return false
}

// Shallow returns a copy that shares the member values.
func (o *Object) Shallow() *Object {
	c := &Object{
		keys:     append(make([]string, 0, len(o.keys)+1), o.keys...),
		vals:     append(make([]any, 0, len(o.vals)+1), o.vals...),
		indexKey: o.indexKey,
	}
	if o.index != nil {
		c.index = make(map[string]int, len(o.index)+1)
		maps.Copy(c.index, o.index)
	}
	return c
}

// ValueAt returns the member at position i of insertion order. It exists for
// walkers that already hold the position.
func (o *Object) ValueAt(i int) any { return o.vals[i] }

// KeyAt returns the key at position i of insertion order.
func (o *Object) KeyAt(i int) string { return o.keys[i] }
