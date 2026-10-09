package client_test

// pi: packages/client/src/errors.ts

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/client"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// packages/client/src/errors.ts:3-27: ServerError(error) keeps the server's code and message, DisconnectedError(message, cause) keeps its cause
// (Unwrap, as D103 maps `cause`); each class's constructor sets `name`.
func TestClientErrorClassesCarryNameCodeAndCause(t *testing.T) {
	cause := errors.New("socket reset")
	server := client.NewServerError(protocol.ProtocolError{Code: "invalid_request", Message: "bad call"})
	disconnected := client.NewDisconnectedError("Client is disconnected", cause)
	if server.Name() != "ServerError" || server.Code != "invalid_request" || server.Error() != "bad call" {
		t.Errorf("ServerError name=%q code=%q message=%q", server.Name(), server.Code, server.Error())
	}
	if disconnected.Name() != "DisconnectedError" || disconnected.Error() != "Client is disconnected" || !errors.Is(disconnected, cause) {
		t.Errorf("DisconnectedError name=%q message=%q unwrap=%v", disconnected.Name(), disconnected.Error(), errors.Unwrap(disconnected))
	}
}
