package host

// Ports packages/chord/src/registry.ts. The host builds and installs the tables and implements RegistryView (host.ts
// registry, buildTables, installTables); an object registers entries with Self.Register (object.ts register).

import "iter"

// Registry conflict policies (registry.ts:5 `conflict: "error" | "last"`).
const (
	// ConflictError rejects two objects registering one key.
	ConflictError = "error"
	// ConflictLast keeps the later registration.
	ConflictLast = "last"
)

// Registry is a registry token: plain data, as upstream's is. The host keys a registry's tables by Name, so two
// tokens with one name share a table, as upstream's do. V, the type of its values, is carried by the type parameter
// (upstream's never-set `value?: V`).
type Registry[V any] struct {
	Name string
	// Conflict is ConflictError or ConflictLast.
	Conflict string
}

// RegistryOptions are DefineRegistry's options; an empty Conflict is ConflictError (upstream's omitted conflict).
type RegistryOptions struct {
	Conflict string
}

// DefineRegistry is registry.ts defineRegistry(name, options = {}): conflict defaults to "error". Options are optional,
// as upstream's are.
func DefineRegistry[V any](name string, options ...RegistryOptions) Registry[V] {
	conflict := ConflictError
	if len(options) > 0 && options[0].Conflict != "" {
		conflict = options[0].Conflict
	}
	return Registry[V]{Name: name, Conflict: conflict}
}

// registryStore is one installed table: its keys in Map insertion order and their values.
type registryStore struct {
	keys   []string
	values map[string]any
}

// RegistryTable is registry.ts RegistryTable<V> = ReadonlyMap<string, V>: one code version's table, by key in insertion
// order. It is a typed view of an untyped store, so the host keeps every registry's tables untyped and a view hands
// them out typed without a copy. The zero RegistryTable is the empty table.
type RegistryTable[V any] struct{ store *registryStore }

// NewRegistryTable is a table of values under keys, in the order of keys (the host's Map insertion order). The table
// keeps keys and values; callers must not change them afterwards.
func NewRegistryTable(keys []string, values map[string]any) RegistryTable[any] {
	return RegistryTable[any]{store: &registryStore{keys: keys, values: values}}
}

// tableAs is the typed view of an untyped table. A registry's entries are registered through its Registry[V], so
// every value is a V.
func tableAs[V any](table RegistryTable[any]) RegistryTable[V] {
	return RegistryTable[V](table)
}

// Get is ReadonlyMap.get: the value under key, and whether there is one.
func (table RegistryTable[V]) Get(key string) (V, bool) {
	var zero V
	if table.store == nil {
		return zero, false
	}
	value, ok := table.store.values[key]
	if !ok {
		return zero, false
	}
	typed, _ := value.(V)
	return typed, true
}

// Has is ReadonlyMap.has.
func (table RegistryTable[V]) Has(key string) bool {
	if table.store == nil {
		return false
	}
	_, ok := table.store.values[key]
	return ok
}

// Len is ReadonlyMap.size.
func (table RegistryTable[V]) Len() int {
	if table.store == nil {
		return 0
	}
	return len(table.store.keys)
}

// Keys is ReadonlyMap.keys(), in insertion order, as a fresh slice.
func (table RegistryTable[V]) Keys() []string {
	if table.store == nil {
		return nil
	}
	return append([]string(nil), table.store.keys...)
}

// All is ReadonlyMap.entries(): key and value pairs in insertion order.
func (table RegistryTable[V]) All() iter.Seq2[string, V] {
	return func(yield func(string, V) bool) {
		if table.store == nil {
			return
		}
		for _, key := range table.store.keys {
			typed, _ := table.store.values[key].(V)
			if !yield(key, typed) {
				return
			}
		}
	}
}

// RegistryView is registry.ts RegistryView<V>, a host's view of one registry.
type RegistryView[V any] interface {
	// Table is the table of ctx's code version, or of the current code version when ctx is nil (upstream's omitted ctx).
	Table(ctx Ctx) RegistryTable[V]
	// Get is Table(ctx).Get(key).
	Get(key string, ctx Ctx) (V, bool)
	// Subscribe calls listener with the current table immediately and whenever a new table is installed; the result
	// unsubscribes.
	Subscribe(listener func(table RegistryTable[V], codeVersion int)) func()
}

// Registration is registry.ts Registration: one object's registration of value under key in registry. A
// Registry[V] converts to the Registry[any] it holds with Registry[any](registry): the token is plain data.
type Registration struct {
	Registry Registry[any]
	Key      string
	Value    any
}
