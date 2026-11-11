package app

import (
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/src/node/app/info.ts
//
// The start-data objects (slice 13 §1; decision "The plugin model, points 3, 4, 5 and 8" (8)): base code shrinks to the API, the
// bootstrap and one facet serving process values as core objects plugins `get`. `AppInfo` on the server (the data directory, the
// app's name, its API version and its owner: decision "The kit's owner is a principal the app chooses"), `WorkerInfo` on a worker
// (its address and the app's start data for it).
//
// Go forced differences: the generic principal parameter of `appInfoFacet<P>` is designed out (a principal is a JSON object).

// AppInfo is what the server knows of the app: its data directory, its name, its newest API version, its owner principal.
var AppInfo = mustLocal("chord.app.info", contracts.LocalDeclaration{
	Version: 2,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("dataDir", contracts.Str()), contracts.Mem("name", contracts.Str()),
		contracts.Mem("version", contracts.Str()), contracts.Mem("owner", contracts.Unknown()),
	},
})

// WorkerInfo is what a worker knows of itself: its address (kind, params, key) and the app's start data for it.
var WorkerInfo = mustLocal("chord.app.worker-info", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("address", contracts.Obj(
			contracts.Prop("kind", contracts.Str()), contracts.Prop("key", contracts.Str()), contracts.Prop("params", contracts.Unknown()),
		)),
		contracts.Mem("start", contracts.Unknown()),
	},
})

// AppInfoValues is what AppInfoFacet serves.
type AppInfoValues struct {
	DataDir string
	Name    string
	Version string
	// Owner is the owner principal.
	Owner data.Principal
}

// AppInfoFacet is the facet serving AppInfo with info.
func AppInfoFacet(info AppInfoValues) host.Facet {
	return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		return h.Serve(AppInfo, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"dataDir": constantMember(self, info.DataDir),
					"name":    constantMember(self, info.Name),
					"version": constantMember(self, info.Version),
					// The same object at every get: its principal key is computed once.
					"owner": constantMember(self, info.Owner),
				},
			}}, nil
		}, host.ServeOptions{})
	})
}

// WorkerInfoValues is what WorkerInfoFacet serves.
type WorkerInfoValues struct {
	// Address is the worker's address: kind, key and params (a JSON object).
	Kind, Key string
	Params    delta.JsonValue
	// Start is the app's start data for the worker (JSON).
	Start delta.JsonValue
}

// WorkerInfoFacet is the facet serving WorkerInfo with the worker's address and start data.
func WorkerInfoFacet(info WorkerInfoValues) host.Facet {
	return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		return h.Serve(WorkerInfo, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"address": constantMember(self, delta.JsonObjectOf("kind", info.Kind, "key", info.Key, "params", info.Params)),
					"start":   constantMember(self, info.Start),
				},
			}}, nil
		}, host.ServeOptions{})
	})
}

// constantMember is a state member that never changes (upstream's `constant(value)`): a derive of no source. Go's Self has no
// constant of its own (the host takes a *state.Constant only where a member is given as a value, not through Derive).
func constantMember(self host.Self, value delta.JsonValue) chord.Watchable[delta.JsonValue] {
	return self.Derive(nil, func([]any) (delta.JsonValue, error) { return value, nil })
}
