// Package contract ports apps/p2/plugins/radius/contract.ts, relay-access.ts and brain.ts: p2 as a Radius brain. The brain
// keeps one outbound WebSocket to its relay in Radius and serves what Radius multiplexes over it (see brain.go).
package contract

import (
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// RadiusService is the Radius connection (`p2.radius-service`, server, host-internal); its relay credentials never leave
// the `RadiusConnections` native.
var RadiusService = func() *contracts.Contract {
	contract, err := contracts.Local("p2.radius-service", contracts.LocalDeclaration{Version: 2, Params: contracts.Params{}, Members: []contracts.LocalEntry{}})
	if err != nil {
		panic(err)
	}
	return contract
}()

// Connection is the open `RadiusService`'s relay and brain.
type Connection struct {
	Relay *RelayAccess
	Brain *Brain
	// Port is the server's port.
	Port int
}

// RadiusConnectionsParts is what `RadiusService` contributes to RadiusConnections once open: Connection is read at each use.
type RadiusConnectionsParts struct {
	Connection func() Connection
}

// RadiusConnections is the registry of the open Radius connection (`p2.radius-connections`): its value is `RadiusService`'s
// contribution, nil while it is not open.
var RadiusConnections = p2runtime.NewNativeRegistry("p2.radius-connections")

// ServeRadiusConnections serves the registry (the radius plugin's facet; test hosts).
func ServeRadiusConnections(facet host.FacetHost) error {
	return p2runtime.ServeNative[RadiusConnectionsParts](facet, RadiusConnections)
}
