package contracts

import "iter"

// NamedMap is the Go form of a JavaScript Map keyed by member or variant name: it keeps insertion order. Setting an
// existing name keeps its position; deleting a name and setting it again moves it to the end. The zero value is an empty
// map and every read is safe on a nil *NamedMap. A NamedMap held by contract data is immutable by convention.
type NamedMap[V any] struct {
	names  []string
	values map[string]V
}

// NewNamedMap returns an empty map with room for capacity names.
func NewNamedMap[V any](capacity int) *NamedMap[V] {
	return &NamedMap[V]{names: make([]string, 0, capacity), values: make(map[string]V, capacity)}
}

// Len is the number of names.
func (m *NamedMap[V]) Len() int {
	if m == nil {
		return 0
	}
	return len(m.names)
}

// Get returns the value of name.
func (m *NamedMap[V]) Get(name string) (V, bool) {
	if m == nil {
		var zero V
		return zero, false
	}
	value, ok := m.values[name]
	return value, ok
}

// Value returns the value of name, the zero value when there is none.
func (m *NamedMap[V]) Value(name string) V {
	value, _ := m.Get(name)
	return value
}

// Has reports whether name is set.
func (m *NamedMap[V]) Has(name string) bool {
	_, ok := m.Get(name)
	return ok
}

// Keys lists the names in order. The slice is shared with the map and must not be modified.
func (m *NamedMap[V]) Keys() []string {
	if m == nil {
		return nil
	}
	return m.names
}

// All iterates the names and values in order.
func (m *NamedMap[V]) All() iter.Seq2[string, V] {
	return func(yield func(string, V) bool) {
		if m == nil {
			return
		}
		for _, name := range m.names {
			if !yield(name, m.values[name]) {
				return
			}
		}
	}
}

// Set sets name to value; a new name goes last.
func (m *NamedMap[V]) Set(name string, value V) {
	if m.values == nil {
		m.values = map[string]V{}
	}
	if _, ok := m.values[name]; !ok {
		m.names = append(m.names, name)
	}
	m.values[name] = value
}

// Delete removes name and reports whether it was set.
func (m *NamedMap[V]) Delete(name string) bool {
	if _, ok := m.values[name]; !ok {
		return false
	}
	delete(m.values, name)
	for i, existing := range m.names {
		if existing == name {
			m.names = append(m.names[:i:i], m.names[i+1:]...)
			break
		}
	}
	return true
}

// Clone returns an independent map with the same names, order and values.
func (m *NamedMap[V]) Clone() *NamedMap[V] {
	clone := NewNamedMap[V](m.Len())
	for name, value := range m.All() {
		clone.Set(name, value)
	}
	return clone
}
