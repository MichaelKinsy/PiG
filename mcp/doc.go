// Package mcp is a small, standalone Model Context Protocol client. It does
// not depend on an MCP SDK or on other PiG packages.
//
// The package provides a transport-neutral client core, stdio and Streamable
// HTTP transports, and an in-memory transport for tests (package mcptest).
// A transport owns framing and I/O and delivers individual JSON-RPC messages
// to [Client]; the client owns request correlation, initialization, timeouts,
// cancellation, server requests, and protocol-level helpers.
//
// Async mapping from upstream: an awaited Promise is a blocking call that
// returns its value and error; an AbortSignal is a context.Context; every
// intentionally unawaited Promise is a goroutine the client or transport owns
// and drains on Close.
package mcp
