// Package contract ports apps/p2/plugins/proxy-marker/contract.ts.
package contract

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// ProxyMarkerHeader is the header a trusted proxy marks its requests with (re-exported from the host, as Pi re-exports it
// from `@p2/app/v1/server`).
const ProxyMarkerHeader = hosts.ProxyMarkerHeader

// ProxyMarker is the plugin's marker object (`p2.proxy-marker`): it exposes nothing.
var ProxyMarker = func() *contracts.Contract {
	contract, err := contracts.Local("p2.proxy-marker", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{}})
	if err != nil {
		panic(err)
	}
	return contract
}()
