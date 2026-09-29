# Experimental client

This package implements the pinned Pi client request, connection, and subscription lifecycle over the native protocol codec. A client requires an explicit transport factory. The package does not select a gateway or enable a stable CLI command.

`NewClient(ClientOptions)` creates a disconnected client. `Connect(ctx, ClientOptions)` creates and connects a client. Instance `Connect` and `Reconnect` await a validated server hello. `Request` waits for a correlated service result. Cancellation sends a target-fenced cancel and keeps the response correlation until the server responds or the connection ends. A response without a matching request fails the connection.

`ByteTransportFactory` and `ByteTransport.Send` expose the synchronous admission prefix and asynchronous completion separately. Both return promptly. Their completion callback runs exactly once. This preserves transport write ordering and pending-byte admission before an unawaited send returns. Connection callbacks run on the transport dispatcher. Promise-style continuations are published after the complete incoming chunk, including coalesced and reentrant messages.

Connection and attachment callbacks use identity-bearing listener objects because Go functions are not comparable. Reusing one listener object preserves JavaScript Set registration/removal behavior. Callback additions and removals during a notification affect the live iteration. Listener failures reach `OnListenerError`; a failure in that diagnostic callback does not change protocol state.

`Connect` awaits failed-startup disposal and joins the attempt and native transport before returning its error. Instance `Dispose` remains callback-safe; external owners use `WaitClosed` to join it without making a reader callback wait on itself.

`BeginInvoke` returns an owned `chord.ServiceInvocation` only after actual validation, correlation registration and send admission. `Wait(waitContext)` cancels only the observer; the original operation context remains unchanged. Presentation shutdown can stop awaiting a Background-context operation without inventing cancellation of that operation. The runtime service-source adapter invokes `extension.CallInitiated` after this admission boundary and before awaiting the response. `RemoteService.BeginCall` retains normal access/member checks and requires an explicit initiating transport; it does not infer entry by starting a blocking call in a goroutine.

`SubscribeService` returns a snapshot and buffers updates until `ServiceSubscription.Start`. State dictionaries belong to the subscription decoder. Listener delivery is ordered and owned by the subscription. `Dispose` removes the listener before sending an unsubscribe. It joins admitted delivery after a successful unsubscribe. Callers dispose bindings/subscriptions before disposing their client. Client disposal rejects requests and removes connection/attachment/subscription registrations without inventing application cancellation.

`CreateClientServiceTransport` accepts the narrow `ServiceTransportClient` contract so a decorator can interpose a service operation without replacing connection state or the transport. This is the adapter used by Chord bindings.

`CreateUnixTransportFactory` creates a fresh Unix socket transport for each attempt. It copies admitted sends and applies the upstream four-frame pending-byte budget before returning. One owned writer preserves invocation order. Local close suppresses remote terminal callbacks and releases queued writes. `WaitClosed(ctx)` joins Go-owned attempts, native I/O, and admitted subscription delivery after disposal; it stays separate from callback-safe `Dispose` so a callback never waits for itself.

`DiscoverUnixServers` checks canonical server-addressed socket entries, runs at most sixteen joined probes, applies the caller's per-probe timeout, and returns routes in server-ID order. It leaves stale/unresponsive sockets on disk. Unexpected filesystem or transport failures propagate. Unix transport remains unsupported on Windows, as upstream.

`OpenClientRuntime`, `RunClient` and the client TUI in `internal/experimental` compose this client; the experimental suite runs it against real servers.
