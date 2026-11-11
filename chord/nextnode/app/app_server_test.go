package app

import "testing"

// Ports app.ts `Server = hostKind("server")`: the kit's server host has one fixed address, kind `server`, with no params, that remote
// types place themselves on (`hostAddress: () => Server()`); every call gives the same one.
func TestServerAddressIsTheKitsWellKnownServerAddress(t *testing.T) {
	address := Server.Address(nil)
	if address.Kind != "server" || address.Key != "server" {
		t.Fatalf("address = %+v, want kind and key server", address)
	}
	if again := Server.Address(nil); again.Key != address.Key || again.Kind != address.Kind {
		t.Fatalf("a second call gave %+v, want %+v", again, address)
	}
}
