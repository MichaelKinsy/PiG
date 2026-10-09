// Package daemon is pi-env's daemon: it gives a Pi Durable host an execution environment on this machine over a
// framed protocol on stdin and stdout, usually started through ssh as `pi-env serve --token <hex>`.
//
// Ports packages/env/daemon/src/main.rs
//
// Go mapping: the Rust crate is a Go package, and Serve runs one connection. The daemon reuses
// durable/env/node for `exec` and `watch`, which the Rust crate re-implements as ports of Durable's
// NodeExecutionEnv; file operations are Go standard library calls with Node's error codes from internal/nodeerrno.
// The wire protocol is packages/env/docs/protocol.md unchanged.
package daemon
