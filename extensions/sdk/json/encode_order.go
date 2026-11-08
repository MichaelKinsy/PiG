package json

import (
	"reflect"
	"slices"
	"strings"
)

// pig additive (key-order): a map the encoder writes in a member order the caller knows.
//
// A Go map has no insertion order, so the encoder writes its members in byte order. A value the extension host sent an extension and the extension hands back
// unchanged (an entry of a turn boundary, a message of the context) must keep the member order it was sent in, as the JavaScript object it is in Pi keeps it.

// KeyOrder names the member order of the maps a value holds.
type KeyOrder interface {
	// Keys returns the order in which the map[string]any whose reflect.Value.Pointer is m writes its members, and whether it knows that order. Each key is
	// named at most once. The slice is read, never kept or changed. A key the map does not hold is skipped, and a member the order does not name follows the
	// others in the order an unordered map writes them.
	Keys(m uintptr) ([]string, bool)
}

// MarshalJSOrdered is [MarshalJS] with the member order order gives each map[string]any it knows: members the order names come first, in its order, then the
// rest as [MarshalJS] writes them. Any other map is written as [MarshalJS] writes it.
func MarshalJSOrdered(v any, order KeyOrder) ([]byte, error) {
	e := newEncodeState()
	defer putEncodeState(e)

	if err := e.marshal(v, encOpts{escapeHTML: true, jsNumbers: true, order: order}); err != nil {
		return nil, err
	}
	return append([]byte(nil), e.Bytes()...), nil
}

var mapStringAnyType = reflect.TypeFor[map[string]any]()

// orderedMapOf returns v as a map[string]any with its member order when opts carries an order that knows it.
func orderedMapOf(v reflect.Value, opts encOpts) (map[string]any, []string, bool) {
	if opts.order == nil || v.Type() != mapStringAnyType || v.Len() < 2 {
		return nil, nil, false
	}
	keys, ok := opts.order.Keys(v.Pointer())
	if !ok {
		return nil, nil, false
	}
	return v.Interface().(map[string]any), keys, true
}

// writeOrderedMap writes the members of m, between the braces the caller writes, in the order of keys and then the others in byte order.
func writeOrderedMap(e *encodeState, m map[string]any, keys []string, opts encOpts) {
	written := 0
	member := func(key string, value any) {
		if written > 0 {
			_ = e.WriteByte(',')
		}
		written++
		e.Write(appendString(e.AvailableBuffer(), key, opts.stringEscapes()))
		_ = e.WriteByte(':')
		e.reflectValue(reflect.ValueOf(value), opts)
	}
	for _, key := range keys {
		if value, ok := m[key]; ok {
			member(key, value)
		}
	}
	if written == len(m) {
		return
	}
	var rest []string
	for key := range m {
		if !slices.Contains(keys, key) {
			rest = append(rest, key)
		}
	}
	compare := strings.Compare
	if opts.jsNumbers {
		compare = compareJSKeys
	}
	slices.SortFunc(rest, compare)
	for _, key := range rest {
		member(key, m[key])
	}
}
