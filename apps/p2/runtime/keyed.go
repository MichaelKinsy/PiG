package runtime

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// Ports the keyed registry helpers of apps/p2/src/runtime/chord.ts (`keyedRegistry`, `serveKeyed`): a registry whose
// contributions are records of values by key, composed into one table. A duplicate key is reported (a registry
// conflict) and the first plugin by name keeps it. Go carries a record as its entries in the order the contributor
// lists them (a Go map has no order), so Keyed is that list.

// Keyed is one contribution to a keyed registry: values by key, in the contributor's order.
type Keyed[V any] []registries.RecordEntry[V]

// KeyedTable is a keyed registry's composed value (`ReadonlyMap<string, V>`): read-only, in insertion order.
type KeyedTable[V any] struct {
	keys   []string
	values map[string]V
}

// Len is the number of keys.
func (table *KeyedTable[V]) Len() int {
	if table == nil {
		return 0
	}
	return len(table.keys)
}

// Get is the value of key.
func (table *KeyedTable[V]) Get(key string) (V, bool) {
	if table == nil {
		var zero V
		return zero, false
	}
	value, ok := table.values[key]
	return value, ok
}

// Keys are the keys in insertion order (table order, then each contribution's own order).
func (table *KeyedTable[V]) Keys() []string {
	if table == nil {
		return nil
	}
	return append([]string(nil), table.keys...)
}

// KeyedRegistry is a declared keyed registry (`KeyedRegistry<V>`; its defining facet serves it with Serve on every host
// kind that reads it).
type KeyedRegistry[V any] struct {
	Registry registries.Registry[any, any]
}

// NewKeyedRegistry declares a keyed registry (version 1).
func NewKeyedRegistry[V any](name string) KeyedRegistry[V] {
	registry, err := registries.DefineRegistry[any, any](name, registries.DefineRegistryOptions{Version: 1})
	if err != nil {
		panic(err)
	}
	return KeyedRegistry[V]{Registry: registry}
}

// Serve serves the registry on a host: every contribution's keys, a duplicate reported and the first plugin by name
// kept.
func (keyed KeyedRegistry[V]) Serve(facet host.FacetHost) error {
	return facet.Serve(keyed.Registry, registries.RegistryProvider[Keyed[V], *KeyedTable[V]]{
		Compose: func(table registries.RegistryTable[Keyed[V]], context registries.ComposeContext) (*KeyedTable[V], error) {
			items := registries.KeyedEntries(table, func(value Keyed[V]) []registries.RecordEntry[V] { return value }, context.Report)
			composed := &KeyedTable[V]{values: make(map[string]V, items.Len())}
			for key, item := range items.All() {
				composed.keys = append(composed.keys, key)
				composed.values[key] = item.Value
			}
			return composed, nil
		},
	}.Erase(), host.ServeOptions{})
}

// Contribute adds this facet's entries (`self.contribute(registry, { key: value })`).
func (keyed KeyedRegistry[V]) Contribute(self interface {
	Contribute(registries.Registry[any, any], any) error
}, entries ...registries.RecordEntry[V]) error {
	return self.Contribute(keyed.Registry, Keyed[V](entries))
}

// Table is a registry value source's current table (the value `host.registry` and `self.registry` give).
func (keyed KeyedRegistry[V]) Table(source *registries.RegistryValueSource) (*KeyedTable[V], error) {
	table, ok := source.Value().(*KeyedTable[V])
	if !ok {
		return nil, fmt.Errorf("registry %s: value is %T", keyed.Registry.Name, source.Value())
	}
	return table, nil
}

// Entry is one key of a contribution.
func Entry[V any](key string, value V) registries.RecordEntry[V] {
	return registries.RecordEntry[V]{Key: key, Value: value}
}
