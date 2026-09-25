# Experimental Radius transport

This package provides the opt-in Radius relay library from the pinned Pi sources `experimental/radius-auth.ts` and `experimental/radius-relay.ts`.
It does not register commands, enable experimental mode, or select a hosted endpoint.

## Selection and authentication

Supply the gateway explicitly to `NewRadiusRelayAuthResolver`.
Supply either a token, a token file, or a lazy `CreateRuntime` callback.
Configure that runtime without model-network refresh and with OAuth refresh directed to the selected gateway.
Do not use the built-in Earendil Radius runtime as a substitute for a PiG-owned gateway (D64).
The existing `RequestAuthRuntime` supports an explicit `radius` provider configuration with `oauth: "radius"` and the selected `baseUrl`.
`TestRadiusAuthRefreshesFiveMinuteCredentialAtLocalGateway` exercises that composition against a local gateway.

The resolver checks context cancellation before `PI_OFFLINE` presence.
Offline mode performs no token-file or runtime access.
The resolver trims explicit tokens using JavaScript whitespace semantics and reads a token file anew for every attempt.
The resolver creates a stored-auth runtime once and resolves credentials anew for every attempt.
Stored OAuth requires five minutes of remaining validity, including after refresh.

## Ownership and async contract

`RadiusRelayHost.Start` owns one reconnect loop.
`RadiusRelayHost.Close` cancels and joins opening, serving, pending writes, and retry waits.
The host accepts independent virtual connections in arrival order.
Pong and unknown or rejected connection-close replies reserve their order and bytes immediately without waiting for socket writes.
One owned writer drains control and data submissions in that order while the reader continues delivering inbound data, closes, and opens.
Control-write failures close the socket with the transport-error code and report the failure on the reader.
Shutdown rejects queued writes and joins the active write and all completion callbacks.
A local virtual close writes its non-nil final chunk before its close control.
A remote close or host failure reports one terminal callback per active connection in insertion order.

`CreateRadiusClientTransportFactory` awaits authentication and the WebSocket handshake.
The returned transport delivers raw binary messages without the host multiplexing envelope.
`Send` copies each submission and awaits its ordered write.
Pending submissions share the upstream four-frame byte budget.
Native socket writes provide backpressure instead of browser `bufferedAmount` polling.
`Close` initiates local shutdown without synthesizing a remote callback.
Wait on `Done` to join transport cleanup.

Callbacks execute on the transport reader, not the TUI loop.
Do not wait for that reader's shutdown from its own callback.
Marshal any UI mutation through the UI owner's executor.

`RadiusClientReconnect` observes an established client.
It retries after one second, doubles failed-attempt delays to thirty seconds, and restores the last selected Session after reconnect succeeds.
A disconnected attachment reset preserves the selection.
An explicit detach while connected clears it.
A failed reattachment disconnects the client and retries.
`Dispose` removes listeners, cancels pending work, disconnects, and joins the retry loop.

## Evidence

Run `go test -race -count=3 ./internal/experimental`.
The suite includes the relevant pinned upstream relay cases, malformed controls and envelopes, token rotation, local OAuth refresh, a native WebSocket echo gateway, ordered bidirectional backpressure, reconnect selection, and cancellation/cleanup tests.
`TestPinnedUpstreamRadiusSource` executes the actual pinned TypeScript modules with local dependency adapters and rejects implicit endpoints or live sockets.
The compiler adapter uses the repository's existing TypeScript installation and installs nothing.

Run `go test ./internal/experimental -run '^$' -bench 'Benchmark(RelayDataFrame|RadiusClientSend|RadiusHostControl)$' -benchmem` to measure framing, client-send, and host-control allocation costs.
These benchmarks do not claim a speedup or complete CLI/server parity.
The server's accept adapter and the established client's reconnect adapter remain caller-owned library boundaries.
