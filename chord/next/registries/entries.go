package registries

import (
	"iter"
	"slices"
)

// Ports packages/chord/src/next/registries/entries.ts (table helpers for `compose`: a map by key and an ordered list;
// both keep each item's plugin, so a use site reports the contributor of an entry that throws).

// TableItem is an item taken from a table, with the contributor it came from; Type is empty for a facet.
type TableItem[I any] struct {
	Value  I
	Plugin string
	Type   string
}

// RecordEntry is one `[key, value]` of upstream's `Object.entries(record)`: Go carries a keyed record as its entries in
// the order the caller wants them taken (a Go map has no order).
type RecordEntry[I any] struct {
	Key   string
	Value I
}

// KeyedItems is upstream's `ReadonlyMap<string, TableItem<I>>`: read-only, in insertion order.
type KeyedItems[I any] struct {
	keys  []string
	items map[string]TableItem[I]
}

// Len is the number of keys.
func (keyed *KeyedItems[I]) Len() int { return len(keyed.keys) }

// Get is the item of key, if any.
func (keyed *KeyedItems[I]) Get(key string) (TableItem[I], bool) {
	item, ok := keyed.items[key]
	return item, ok
}

// All iterates the keys with their items in insertion order.
func (keyed *KeyedItems[I]) All() iter.Seq2[string, TableItem[I]] {
	return func(yield func(string, TableItem[I]) bool) {
		for _, key := range keyed.keys {
			if !yield(key, keyed.items[key]) {
				return
			}
		}
	}
}

// KeyedEntries is a map of every entry's keyed items (entriesOf, e.g. its routes), in table order: a key taken already
// is reported through report (Compose's) naming both plugins, and the first by plugin name keeps it.
func KeyedEntries[C, I any](
	table RegistryTable[C],
	entriesOf func(value C) []RecordEntry[I],
	report func(conflict RegistryConflict),
) *KeyedItems[I] {
	keyed := &KeyedItems[I]{items: map[string]TableItem[I]{}}
	for _, entry := range table {
		for _, item := range entriesOf(entry.Value) {
			if taken, ok := keyed.items[item.Key]; ok {
				report(RegistryConflict{
					Message: `"` + item.Key + `" is contributed twice`,
					Plugins: []string{taken.Plugin, entry.Plugin},
				})
				continue
			}
			keyed.keys = append(keyed.keys, item.Key)
			keyed.items[item.Key] = TableItem[I]{Value: item.Value, Plugin: entry.Plugin, Type: entry.Type}
		}
	}
	return keyed
}

// OrderedEntries lists every entry's items (itemsOf) in one list: in table order, or sorted by orderOf (a phase's
// index, for example; nil for none), table order kept among equals.
func OrderedEntries[C, I any](
	table RegistryTable[C],
	itemsOf func(value C) []I,
	orderOf func(item I) float64,
) []TableItem[I] {
	var items []TableItem[I]
	for _, entry := range table {
		for _, value := range itemsOf(entry.Value) {
			items = append(items, TableItem[I]{Value: value, Plugin: entry.Plugin, Type: entry.Type})
		}
	}
	if orderOf != nil {
		// A stable sort: equal orders keep table order. The comparator is upstream's `a - b`: a NaN difference is a tie.
		slices.SortStableFunc(items, func(a, b TableItem[I]) int {
			difference := orderOf(a.Value) - orderOf(b.Value)
			switch {
			case difference < 0:
				return -1
			case difference > 0:
				return 1
			}
			return 0
		})
	}
	return items
}
