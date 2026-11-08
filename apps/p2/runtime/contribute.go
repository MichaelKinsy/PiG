package runtime

import (
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/src/runtime/contribute.ts: a facet's registration-only object.

// Contribute serves a host-lifetime local object named name whose factory only contributes (slots, routes, tools,
// renderers, commands...) through its StoreSelf and exposes nothing. Its contributions come and go with the object
// (reload, removal). Contributions that need nothing at runtime belong to the facet's own Contribute instead.
func Contribute(facet host.FacetHost, name string, register func(self host.StoreSelf) error) error {
	contract, err := contracts.Local(name, contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}})
	if err != nil {
		return err
	}
	return facet.Serve(contract, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		if err := register(self.(host.StoreSelf)); err != nil {
			return contracts.Implementation{}, err
		}
		return contracts.Implementation{}, nil
	}, host.ServeOptions{})
}
