// Package tui is the trusted side of a terminal client (slice 11 §4, decision "Slice 11, P8"; p2's `tui-client.ts`,
// `tui-sandbox.ts`, `tui-bootstrap.mjs`, `tui-filter.ts`): the launcher, which signs in with a token, downloads the terminal
// kind's base bundle and plugin scripts, and runs them in a sandbox (a Node child process, Pi's bootstrap), always. Ports
// packages/chord/src/node/tui; the sandbox's own runtime (the bootstrap, `src/tui`) is Pi's TypeScript, vendored (D601).
package tui
