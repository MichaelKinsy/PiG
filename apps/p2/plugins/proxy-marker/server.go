// Package proxymarker is p2's proxy-marker plugin (apps/p2/plugins/proxy-marker/server.ts). Marker classification lives in
// the host's origin checks (hosts.RequestOrigin), including when this facet is not loaded. This facet deliberately
// contributes NO authenticators: a proxy header can only remove device trust.
package proxymarker

import (
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/proxy-marker/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Server is the plugin's server facet: it serves `ProxyMarker`, which has no members.
var Server = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return facet.Serve(contract.ProxyMarker, host.LocalFactory(func(_ *delta.JsonObject, _ host.Self) (contracts.Implementation, error) {
		return contracts.Implementation{}, nil
	}), host.ServeOptions{})
})
