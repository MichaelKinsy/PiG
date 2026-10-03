# Experimental server routing

`NewServer(host, options)` creates an inert protocol-v8 server. `Start` starts its listeners in order. A startup failure closes previously started listeners and routed state. `Accept` admits an already-authorized ordered byte connection, including a connection supplied by a relay adapter.

Each connection owns its frame decoder, handshake deadline, active request contexts, server-service attachment, and service-state encoders. The first message must be hello. A protocol failure fences further dispatch before the terminal frame is written. Request cancellation requires both request ID and complete target identity. Untyped host errors reach the error observer but cross the wire only as `Internal server error`.

`SessionRouter` serializes attachment transitions and service-call admission per client. Accepted calls can execute concurrently. Releasing an attachment waits for its admitted calls, then releases its lease. Handle termination invalidates the hosted Session and releases its attachments. A later attach opens a new handle. Client identity is a comparable Go type parameter; the server uses connection pointers.

A service subscription has one `chord.ServiceStateEncoder`. State dictionaries remain independent by keyed instance and member. Subscription updates received before the initial snapshot wait behind the response. Omitted method results remain distinct from explicit JSON null.

`CreateUnixServer` composes the server with one native Unix listener. `UnixServerOptions` has one flat set of listener and server options. `GetUnixSocketPath` validates a canonical lowercase UUIDv4 before deriving the route. The listener binds a private staging path, hard-links its inode into the public route, and removes the staging link. Shutdown preserves a replacement inode. A stale socket is removed only after a conservative liveness probe and a second identity check.

`UnixByteConnection` admits writes against the pending-byte limit before queuing them. Writes retain their bytes and completion errors. Close fences new sends, settles queued writes, sends the optional final frame, half-closes the socket, and waits for the socket-close event or the configured grace. `MarkClosed` records an actual socket-close event. Unix socket permission changes are omitted on Windows, matching the pinned transport.

`Close` joins owned server work and caches its result. `Closed` broadcasts completion, and `ClosedError` retains its result for every observer. Error and connection-count observers must return promptly and must not wait for server shutdown from their own callback stack.

Package `routingtest` ports the server package's `./testing` subpath for routing and client tests. `TestServerHost` resolves seeded Sessions and opens a scripted `TestHarness` per open. `CreateTestServerServices` routes only `pi.session-management` attach and detach. `ProtocolTestClient` records decoded server messages and waits for them by predicate over any `WireChannel`; `ConnectUnixTestClient` connects one to a Unix socket. `CreateTestServer` creates an unstarted server with the default test identity.
