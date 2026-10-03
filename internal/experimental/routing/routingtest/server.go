package routingtest

// Ports packages/server/src/testing/server.ts.

import "github.com/MichaelKinsy/PiG/internal/experimental/routing"

// defaultTestServerId is the server identity CreateTestServer uses when the options omit one.
const defaultTestServerId = "00000000-0000-4000-8000-000000000001"

// TestServerOptions selects the test server's listeners and limits. A nil Host selects a new TestServerHost, and a nil ServerId selects 00000000-0000-4000-8000-000000000001. OnConnectionCountChanged is accepted and, as upstream, not forwarded to the server.
type TestServerOptions struct {
	Host                     *routing.ServerHost
	ServerId                 *string
	Listeners                []routing.ServerListener
	MaxFrameLength           *float64
	HandshakeTimeoutMs       *float64
	OnConnectionCountChanged func(int)
	OnError                  func(error)
}

// TestServer is an unstarted server and the host it routes to.
type TestServer struct {
	Server *routing.Server
	Host   routing.ServerHost
}

// CreateTestServer creates an unstarted Server with deterministic defaults for transport conformance tests. It returns the server's option validation error.
func CreateTestServer(options TestServerOptions) (TestServer, error) {
	host := NewTestServerHost().ServerHost()
	if options.Host != nil {
		host = *options.Host
	}
	serverID := defaultTestServerId
	if options.ServerId != nil {
		serverID = *options.ServerId
	}
	server, err := routing.NewServer(host, routing.ServerOptions{
		Listeners:          options.Listeners,
		MaxFrameLength:     options.MaxFrameLength,
		HandshakeTimeoutMs: options.HandshakeTimeoutMs,
		ServerId:           serverID,
		OnError:            options.OnError,
	})
	if err != nil {
		return TestServer{}, err
	}
	return TestServer{Server: server, Host: host}, nil
}
