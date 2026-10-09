package client

import "testing"

// Ports packages/client/src/errors.ts ClientDisposedError: fixed message and `name`.
func TestClientDisposedErrorCarriesUpstreamNameAndMessage(t *testing.T) {
	e := NewClientDisposedError()
	if e.Error() != "Client is disposed" || e.Name() != "ClientDisposedError" {
		t.Fatalf("error=%q name=%q", e.Error(), e.Name())
	}
}
