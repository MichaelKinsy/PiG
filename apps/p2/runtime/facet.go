package runtime

import (
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/src/runtime/chord.ts (`forViews`, `defineFacet`): a remote type's factory never runs for a view principal unless
// the type is `ForViews`: frames reach nothing by default, whatever a factory would allow (apps/p2/docs/rooms.md §1).

var viewTypes sync.Map

// ForViews declares that view principals (a custom view's frame) may hold objects of typ; its factory decides the rest.
func ForViews(typ *contracts.Contract) *contracts.Contract {
	viewTypes.Store(typ, struct{}{})
	return typ
}

// DefineFacet is host.DefineFacet for a facet of p2's hosts: every remote type it serves refuses a view principal with
// `forbidden`, unless the type is ForViews.
func DefineFacet(setup func(facet host.FacetHost, self host.FacetSelf) error) host.Facet {
	return host.DefineFacet(func(facet host.FacetHost, self host.FacetSelf) error {
		return setup(guardedFacet{facet}, self)
	})
}

// guardedFacet wraps the factories of remote types served through it.
type guardedFacet struct{ host.FacetHost }

func (guarded guardedFacet) Serve(typ any, implementation any, options host.ServeOptions) error {
	contract, isType := typ.(*contracts.Contract)
	if !isType || contract.Kind != contracts.KindRemote {
		return guarded.FacetHost.Serve(typ, implementation, options)
	}
	if _, allowed := viewTypes.Load(contract); allowed {
		return guarded.FacetHost.Serve(typ, implementation, options)
	}
	factory, isFactory := implementation.(host.RemoteFactory)
	if !isFactory {
		return guarded.FacetHost.Serve(typ, implementation, options)
	}
	return guarded.FacetHost.Serve(typ, host.RemoteFactory(func(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		if who, err := FromChord(principal); err == nil {
			if _, view := who.(ViewPrincipal); view {
				return contracts.Implementation{}, Forbidden("views reach only their room")
			}
		}
		return factory(params, principal, self)
	}), options)
}
