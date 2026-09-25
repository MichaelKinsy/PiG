package chord

import (
	"slices"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// orderedMap is a JavaScript Map: iteration follows insertion order, a Set of
// an existing key keeps its position, and Delete followed by Set moves the
// key to the end. Upstream keyed directories, observers and providers iterate
// Maps and Sets, so observer start and disposal order follow insertion order
// rather than Go's randomized map order.
type orderedMap[K comparable, V any] struct {
	index map[K]V
	keys  []K
}

func newOrderedMap[K comparable, V any]() *orderedMap[K, V] {
	return &orderedMap[K, V]{index: map[K]V{}}
}

func (m *orderedMap[K, V]) Get(key K) (V, bool) {
	value, ok := m.index[key]
	return value, ok
}

func (m *orderedMap[K, V]) Set(key K, value V) {
	if _, ok := m.index[key]; !ok {
		m.keys = append(m.keys, key)
	}
	m.index[key] = value
}

func (m *orderedMap[K, V]) Delete(key K) {
	if _, ok := m.index[key]; !ok {
		return
	}
	delete(m.index, key)
	m.keys = slices.DeleteFunc(m.keys, func(candidate K) bool { return candidate == key })
}

func (m *orderedMap[K, V]) Len() int { return len(m.keys) }

// Keys returns a snapshot of the keys in insertion order.
func (m *orderedMap[K, V]) Keys() []K { return slices.Clone(m.keys) }

// Values returns a snapshot of the values in insertion order.
func (m *orderedMap[K, V]) Values() []V {
	values := make([]V, len(m.keys))
	for index, key := range m.keys {
		values[index] = m.index[key]
	}
	return values
}

func (m *orderedMap[K, V]) Clear() {
	clear(m.index)
	m.keys = nil
}

// localeCompareKeys sorts keys like upstream's
// left.localeCompare(right) with the default (root) collation.
func localeCompareKeys(keys []string) {
	collator := collate.New(language.Und)
	slices.SortStableFunc(keys, func(left, right string) int { return collator.CompareString(left, right) })
}
