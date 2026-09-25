// Package subprocess implements pig's out-of-process extension transport:
// extensions communicate over a local stream socket with length-prefixed JSON
// framing. The socket is AF_UNIX, except that a Node extension on Windows gets
// a named pipe, because Node's net module cannot reach AF_UNIX there. The
// extension finds its address in PIG_EXT_SOCKET; framing and handshake are
// the same on both.
//
// # Architecture
//
// The subprocess host spawns extension binaries, connects via those sockets,
// translates subprocess registrations into [extension.Extension] structs, and
// feeds them to the existing [inproc.Runner]. This is additive: it sits ABOVE
// the in-process runner, not beside it. The inproc runner remains the dispatch
// engine; the subprocess layer is a transport adapter.
//
//	┌─────────────────────────────────────────────────────────┐
//	│             inproc.Runner (dispatch engine)              │
//	│   Consumes []extension.Extension: doesn't care how     │
//	│   they were created (compiled-in vs subprocess)          │
//	└─────────────────────┬───────────────────────────────────┘
//	                      │ feeds []extension.Extension
//	┌─────────────────────┴───────────────────────────────────┐
//	│             subprocess.Host (this package)               │
//	│   Spawns binaries, manages sockets, translates          │
//	│   register messages → extension.Extension structs       │
//	└─────────────────────────────────────────────────────────┘
//
// # Wire Protocol
//
// Messages are length-prefixed JSON: 4 bytes big-endian uint32 length followed
// by that many bytes of UTF-8 JSON. Message types:
//
//   - register (ext→host): declares tools, commands, shortcuts, event handlers
//   - ready (host→ext): acknowledges registration, sends context (cwd, theme)
//   - notify (bidirectional): fire-and-forget (no response expected)
//   - request (host→ext): expects a response (tool_call, event dispatch)
//   - response (ext→host): reply to a request
//   - call (ext→host): extension calls host method (ui.notify, sendMessage, etc.)
//   - call_result (host→ext): reply to a call
//   - shutdown (host→ext): graceful shutdown signal
//
// # pig-specific: no upstream equivalent
//
// This entire package is pig-specific. Upstream pi uses TypeScript extensions
// loaded via jiti in-process. The subprocess protocol exists to support compiled
// extensions in Go, Rust, or any language that can speak Unix socket + JSON.
package subprocess
