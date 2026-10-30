package host

import (
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next/host/facet.ts. Facets (§16): a plugin's setup for one host, typed by the app's principal
// type. A setup only declares: the types and registries it serves, its contributions to registries (slice 8) and the
// host's presence lists; it has no documents, routines or natives, so a facet cannot fail after `load`.
//
// Forced Go differences. The principal type parameter `P` of Facet, FacetHost and defineFacet is type-level only (owner
// class a: TypeScript type-level checks; Go carries principals as data.Principal, and a facet is checked by nothing at
// runtime in Pi either). `serve`'s overloads are one method taking the implementation untyped, as Pi's implementation
// signature does (DESIGN-go-layout.md section 5): a type is a *contracts.Contract with its factory, a registry is a
// registries.Registry[any, any] (convert a typed token with registries.Registry[any, any](token): the type parameters are
// unused by its fields) with a registries.RegistryProvider[any, any] (RegistryProvider.Erase). `serve` and `contribute`
// throw in Pi: they return the error. A setup returns the error its throw would be. A Chord watchable contribution is
// `any`, as registries' contribute takes it.

// FacetHost is what a setup serves types and registries with (valid only while the setup runs), and the host's presence
// lists.
type FacetHost interface {
	// Serve serves a type on this host with a factory (`Serve(Type *contracts.Contract, factory any, options
	// ServeOptions)`: a remote type's factory gets the principal its object is for), or serves a registry (`Serve(R
	// registries.Registry[any, any], provider registries.RegistryProvider[any, any], ServeOptions{})`: its defining
	// plugin's facet, the one provider (D1): `compose` builds the value from the table, `accepts` adapts older versions;
	// a second serve of a registry is refused at `load`). It returns an error outside the facet's setup.
	Serve(Type any, implementation any, options ServeOptions) error
	// References is `host.references(Type)`: the references to objects of Type on this host (§19), for factories to
	// derive from.
	References(Type *contracts.Contract) state.SourceHandle
}

// ServeOptions is what `host.serve` takes besides the type and the factory.
type ServeOptions struct {
	// Linger is how long, in milliseconds, a live object with params stays live after its last reference is released (0
	// by default, nil): a reference within that time reuses it, with its documents and dependencies.
	Linger *float64
}

// FacetSelf is what a facet's setup declares with besides the host: its contributions to registries (R21).
type FacetSelf interface {
	// Contribute contributes value to registry R while this plugin is loaded: a plain value, or a Chord watchable of one
	// (followed: its change recomposes). At most one per registry. It returns an error outside the facet's setup, and
	// for a second contribution to R.
	Contribute(R registries.Registry[any, any], value any) error
}

// servedKind tells what a servedEntry served.
type servedKind string

const (
	// servedType is a served type: its contract, factory and options.
	servedType servedKind = "type"
	// servedRegistry is a served registry: its provider.
	servedRegistry servedKind = "registry"
)

// servedEntry is what a setup served, by kind: a type (contract, factory and options), or a registry (registry and
// provider).
type servedEntry struct {
	kind servedKind
	// contract, factory and options are set for a type.
	contract *contracts.Contract
	// factory is the factory as the setup served it, of either kind (the host dispatches on its contract's kind).
	factory any
	options ServeOptions
	// registry and provider are set for a registry.
	registry registries.Registry[any, any]
	provider registries.RegistryProvider[any, any]
}

// facetContribution is a contribution a setup declared.
type facetContribution struct {
	registry registries.Registry[any, any]
	value    any
}

// Facet is a plugin's facet for hosts of the app's principal type (type only: nothing is checked at runtime).
type Facet struct {
	// Setup declares the facet's types, registries and contributions; its error is its throw.
	Setup func(host FacetHost, self FacetSelf) error
}

// DefineFacet defines a facet; base code binds the principal type once, next to its principal type (type-level in Pi).
func DefineFacet(setup func(host FacetHost, self FacetSelf) error) Facet {
	return Facet{Setup: setup}
}

// facetSetUp is what setUp returns: the types and registries a facet served, and its contributions, in order.
type facetSetUp struct {
	served        []servedEntry
	contributions []facetContribution
}

// setUp runs a facet's setup and returns the types and registries it served and its contributions, in order; references
// is the host's.
func setUp(name string, facet Facet, references func(Type *contracts.Contract) state.SourceHandle) (facetSetUp, error) {
	scope := &facetScope{name: name, open: true, references: references}
	err := func() error {
		defer func() { scope.open = false }()
		return facet.Setup(scope, scope)
	}()
	return facetSetUp{served: scope.served, contributions: scope.contributions}, err
}

// facetScope is the host and the self a setup gets: both valid only while it runs.
type facetScope struct {
	name          string
	open          bool
	references    func(Type *contracts.Contract) state.SourceHandle
	served        []servedEntry
	contributions []facetContribution
}

func (f *facetScope) assertOpen(what string) error {
	if !f.open {
		return fmt.Errorf("%s: %s is valid only in the facet's setup", f.name, what)
	}
	return nil
}

// Serve implements FacetHost.
func (f *facetScope) Serve(Type any, implementation any, options ServeOptions) error {
	if err := f.assertOpen("host.serve"); err != nil {
		return err
	}
	// Served types and registries are dispatched by their kind; a factory matches its own contract, a provider its own
	// registry.
	switch served := Type.(type) {
	case registries.Registry[any, any]:
		provider, ok := implementation.(registries.RegistryProvider[any, any])
		if !ok {
			return fmt.Errorf("%s: host.serve of registry %s takes a registries.RegistryProvider[any, any], not %T", f.name, served.Name, implementation)
		}
		f.served = append(f.served, servedEntry{kind: servedRegistry, registry: served, provider: provider})
	case *contracts.Contract:
		f.served = append(f.served, servedEntry{kind: servedType, contract: served, factory: implementation, options: options})
	default:
		return errors.New(f.name + ": host.serve takes a contract or a registry, not " + fmt.Sprintf("%T", Type))
	}
	return nil
}

// References implements FacetHost.
func (f *facetScope) References(Type *contracts.Contract) state.SourceHandle {
	return f.references(Type)
}

// Contribute implements FacetSelf.
func (f *facetScope) Contribute(registry registries.Registry[any, any], value any) error {
	if err := f.assertOpen("self.contribute"); err != nil {
		return err
	}
	for _, each := range f.contributions {
		if each.registry.Name == registry.Name {
			return fmt.Errorf("%s: registry %s is contributed to twice", f.name, registry.Name)
		}
	}
	f.contributions = append(f.contributions, facetContribution{registry: registry, value: value})
	return nil
}
