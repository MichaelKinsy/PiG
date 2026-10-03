package harness

import "slices"

// OrderedMap is a JavaScript Map with string keys: Set keeps an existing key's position, Delete removes it, and a re-added key appends.
type OrderedMap[T any] struct {
	keys   []string
	values map[string]T
}

// NewOrderedMap returns an empty map.
func NewOrderedMap[T any]() *OrderedMap[T] { return &OrderedMap[T]{values: map[string]T{}} }

// Set stores value at key.
func (items *OrderedMap[T]) Set(key string, value T) {
	if _, exists := items.values[key]; !exists {
		items.keys = append(items.keys, key)
	}
	items.values[key] = value
}

// Get returns the value at key.
func (items *OrderedMap[T]) Get(key string) (T, bool) {
	value, ok := items.values[key]
	return value, ok
}

// Has reports whether key is present.
func (items *OrderedMap[T]) Has(key string) bool {
	_, ok := items.values[key]
	return ok
}

// Delete removes key.
func (items *OrderedMap[T]) Delete(key string) {
	if _, exists := items.values[key]; !exists {
		return
	}
	delete(items.values, key)
	items.keys = slices.DeleteFunc(items.keys, func(candidate string) bool { return candidate == key })
}

// Len returns the number of keys.
func (items *OrderedMap[T]) Len() int { return len(items.keys) }

// Keys returns the keys in insertion order.
func (items *OrderedMap[T]) Keys() []string { return slices.Clone(items.keys) }

// Values returns the values in key order.
func (items *OrderedMap[T]) Values() []T {
	values := make([]T, 0, len(items.keys))
	for _, key := range items.keys {
		values = append(values, items.values[key])
	}
	return values
}
