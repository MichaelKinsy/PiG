package host

// Ports packages/chord/src/websocket.ts.

// WebSocketServerLike is a WebSocket server (e.g. `ws`'s WebSocketServer), structurally: Chord does not depend on a
// WebSocket library (Pi websocket.ts:6-9). On registers listener for event; acceptWebSockets registers "connection".
type WebSocketServerLike[Request any] interface {
	On(event string, listener func(socket WebSocketLike, request Request))
}

// WebSocketAcceptOptions are AcceptOptions without principal, kind and name: WebSocket peers are always accepted as
// clients, so they have no roots, no code versions, no reload votes and are not addressable (Pi websocket.ts:11-15).
type WebSocketAcceptOptions[Request any] struct {
	LinkOptions
	// Restamp is AcceptOptions.Restamp: on invalidatePrincipal a new principal keeps the link, false closes it.
	Restamp func(principal Principal) (Principal, bool)
	// MaxCalls is AcceptOptions.MaxCalls: the calls the client may have in progress on its link (nil: no limit); further
	// ones are answered at once with unavailable (reason too_many_calls).
	MaxCalls *int
	// Principal is the authenticator: the principal of the connecting client, or false to refuse it. An error is a
	// failed authentication. It may block; acceptWebSockets calls it off the server's connection listener, as Pi
	// resolves the authenticator's result asynchronously.
	Principal func(request Request) (Principal, bool, error)
}

// AcceptWebSockets accepts every connection of server on host as a client link stamped with the authenticated
// principal (Pi websocket.ts:17-34). The socket is wrapped at once, so frames sent while authenticating are queued, not
// lost; a refused client's link closes with "unauthorized" and a failed authentication's with "authentication failed".
func AcceptWebSockets[Request any](server WebSocketServerLike[Request], host *HostImpl, options WebSocketAcceptOptions[Request]) {
	server.On("connection", func(socket WebSocketLike, request Request) {
		// Large payloads go out deflated; clients may not send packed frames (a small message could inflate to anything).
		refuse := false
		link := NewWebSocketLink(socket, WebSocketLinkOptions{Compress: true, AcceptPacked: &refuse})
		// Pi's Promise.resolve(options.principal(request)).then(...) settles after the listener returns; the
		// authenticator may block, so it runs as a task the host owns (Close drains it).
		host.goTask(func() {
			principal, ok, err := options.Principal(request)
			switch {
			case err != nil:
				link.Close("authentication failed")
			case !ok:
				link.Close("unauthorized")
			default:
				// Accept fails only for a host link without a name (Pi host.ts:729), never for this client link; an
				// error is still surfaced, where Pi would leave an unhandled rejection.
				if err := host.Accept(link, AcceptOptions{LinkOptions: options.LinkOptions, Restamp: options.Restamp, MaxCalls: options.MaxCalls, Principal: principal, Kind: "client"}); err != nil {
					host.report(err)
				}
			}
		})
	})
}
