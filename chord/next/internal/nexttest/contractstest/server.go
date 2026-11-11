// Package contractstest holds the test helpers of packages/chord/test/next/support.ts that need the contracts package: kept apart from
// nexttest so the helpers that do not need it (counters, collectors) build without it.
package contractstest

import (
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// ServerKind is support.ts's `Server`: the host kind of the test servers, where every test type but `PlacedNotes`
// lives.
var ServerKind = func() *contracts.HostKind {
	kind, err := contracts.NewHostKind("server")
	if err != nil {
		panic(err)
	}
	return kind
}()

// Server is support.ts's `Server()`: the address of every test type served on the server.
func Server() contracts.Address { return ServerKind.Address(delta.NewJsonObject(0)) }

// ServerAddress is support.ts's `hostAddress: () => Server()`: a contract's HostAddress that names the server.
func ServerAddress(*delta.JsonObject) (contracts.Address, error) { return Server(), nil }

// ServerAddressWire is support.ts's SERVER_ADDRESS: `Server()` as a hold names it on the wire.
func ServerAddressWire() *delta.JsonObject {
	return delta.JsonObjectOf("kind", "server", "params", delta.NewJsonObject(0))
}
