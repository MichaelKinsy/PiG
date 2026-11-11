// Package registries ports packages/chord/src/next/registries: an extension point between plugins (design §17). The
// defining plugin shares a token (DefineRegistry) and serves it with Compose; facets and host-lifetime objects
// contribute; readers follow the composed value (RegistryValue). The record, contribution and value types are what
// Chord's layers (host, attempts) build registries from.
//
// Forced Go differences from upstream, each also noted on the identifier:
//   - A `throw` is a returned error: Compose and the adapters return (value, error); a panic inside them is recovered
//     as the error a throw would be.
//   - `unknown` is `any`; a registry the record holds is Registry[any, any]. RegistryProvider.Erase is upstream's cast
//     of a RegistryProvider[C, V] to RegistryProvider<unknown, unknown>.
//   - An adapter's `old: never` parameter is `any`: the plugin asserts its older contribution type.
//   - `Contribution<C> = C | Watchable<C> | NativeWatchable<C>` has no Go form (a union of a type parameter and
//     interfaces): the host's contribute takes `any` and classifies it into a ContributionContent.
//   - `index.ts` is designed out: Go packages export what the file groups list, there is no re-export barrel.
//   - Record<string, I> in KeyedEntries is an ordered []RecordEntry[I] (Object.entries order is the caller's), and its
//     ReadonlyMap result is a KeyedItems, an insertion-ordered read-only map.
//   - An absent `type` is the empty string (a type is a nonempty name).
package registries

import (
	"context"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next/registries/registry.ts (tokens, tables, provider, value) and index.ts (designed out).

// Registry is a registry token: its name (as type names, `[a-z0-9@/._~-]`), the version the bundling plugin was built
// against, the contribution type C and the composed value type V. Shared: every contributor and reader bundles its own
// copy. C and V are type-level only (upstream's `~types`).
type Registry[C, V any] struct {
	Kind    string // always "registry"
	Name    string
	Version int
}

// RegistryEntry is one contribution in a registry's table: its plugin, its object type for an object contributor, its
// version.
type RegistryEntry[C any] struct {
	Plugin string
	// Type is the contributing object's type; empty for a facet (upstream's absent `type`).
	Type string
	// Version is the registry version its contributor was built against (an older one adapted to C).
	Version int
	Value   C
}

// RegistryTable is a registry's table: one entry per live contributor, sorted by plugin name, then type (a facet's
// first).
type RegistryTable[C any] []RegistryEntry[C]

// RegistryConflict is a conflict Compose resolved with its fallback: reported as `registry_conflict`, once per conflict
// episode.
type RegistryConflict struct {
	Message string
	Plugins []string
}

// ComposeContext is what Compose gets besides the table.
type ComposeContext struct {
	// Report reports a conflict Compose resolved (the first by plugin name wins, for example).
	Report func(conflict RegistryConflict)
}

// RegistryAccept holds the adapters of one older version, the provider's code: Contribution maps an older contribution
// to C (once per contribution value), Value maps the composed value back for a reader of that version (once per
// composition). Either may be nil. Contribution's parameter is upstream's `never`: the plugin asserts the old type.
type RegistryAccept[C, V any] struct {
	Contribution func(old any) (C, error)
	Value        func(current V) (any, error)
}

// RegistryAccepts are the adapters of older versions, by version.
type RegistryAccepts[C, V any] map[int]RegistryAccept[C, V]

// RegistryProvider is what the defining plugin's facet serves a registry with (`host.serve(R, provider)`).
type RegistryProvider[C, V any] struct {
	// Compose composes the table: pure and synchronous, always a value (a fallback built in), conflicts reported. It
	// runs when the table changed and someone reads or follows the value; an error is the owner's bug
	// (`registry_failed`, the last good composition stays).
	Compose func(table RegistryTable[C], context ComposeContext) (V, error)
	Accepts RegistryAccepts[C, V]
}

// RegistryValue is a registry's composed value as a reader of one version sees it (`self.registry`, `host.registry`): a
// native watchable; Value is the last good composition (a later failing Compose keeps it), a watch gets each new one.
type RegistryValue[V any] interface {
	chord.NativeWatchable[V]
	// Observe attaches a synchronous observer of its compositions (as Durable's `observeXXX`): Advance runs inside the
	// batch that composed, so it only records; an attached observer counts as watching, so the composition runs for it.
	// It returns the failure when there is no value to start at (upstream's throw).
	Observe(observer state.NativeObserver[V], ctx context.Context) (state.NativeObservation[V], error)
}

// DefineRegistryOptions are the options of DefineRegistry.
type DefineRegistryOptions struct {
	// Version is the version this plugin is built against.
	Version int
}

// maxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER, the largest version `Number.isSafeInteger` accepts.
const maxSafeInteger = 1<<53 - 1

// DefineRegistry declares a registry token: name and the version this plugin is built against. It returns the error
// upstream throws for a bad name or version.
func DefineRegistry[C, V any](name string, options DefineRegistryOptions) (Registry[C, V], error) {
	if err := contracts.CheckName(name, "registry"); err != nil {
		return Registry[C, V]{}, err
	}
	// Declaration policy: JavaScript callers reach this check; a Go int is integral already.
	if options.Version < 1 || options.Version > maxSafeInteger {
		return Registry[C, V]{}, fmt.Errorf("%s.version: expected a positive integer", name)
	}
	return Registry[C, V]{Kind: "registry", Name: name, Version: options.Version}, nil
}

// Erase is upstream's cast of a RegistryProvider<C, V> to RegistryProvider<unknown, unknown>: the form a record holds.
// Entries and values cross as `any` and are asserted back to C and V; the table handed to Compose is a copy.
func (provider RegistryProvider[C, V]) Erase() RegistryProvider[any, any] {
	var erased RegistryProvider[any, any]
	if provider.Compose != nil {
		compose := provider.Compose
		erased.Compose = func(table RegistryTable[any], context ComposeContext) (any, error) {
			typed := make(RegistryTable[C], len(table))
			for i, entry := range table {
				value, err := assertAs[C](entry.Value, "contribution of "+entry.Plugin)
				if err != nil {
					return nil, err
				}
				typed[i] = RegistryEntry[C]{Plugin: entry.Plugin, Type: entry.Type, Version: entry.Version, Value: value}
			}
			return compose(typed, context)
		}
	}
	if provider.Accepts != nil {
		erased.Accepts = make(RegistryAccepts[any, any], len(provider.Accepts))
		for version, accept := range provider.Accepts {
			var erasedAccept RegistryAccept[any, any]
			if adapter := accept.Contribution; adapter != nil {
				erasedAccept.Contribution = func(old any) (any, error) { return adapter(old) }
			}
			if adapter := accept.Value; adapter != nil {
				erasedAccept.Value = func(current any) (any, error) {
					value, err := assertAs[V](current, "composed value")
					if err != nil {
						return nil, err
					}
					return adapter(value)
				}
			}
			erased.Accepts[version] = erasedAccept
		}
	}
	return erased
}

// assertAs is `value as T`: an `undefined` (nil) is T's zero value; any other value must be a T.
func assertAs[T any](value any, what string) (T, error) {
	if value == nil {
		var zero T
		return zero, nil
	}
	typed, ok := value.(T)
	if !ok {
		var zero T
		return zero, errors.New(what + fmt.Sprintf(": %T is not %T", value, zero))
	}
	return typed, nil
}
