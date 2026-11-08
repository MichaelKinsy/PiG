// Package client holds the vendored TypeScript of Chord's client runtimes (D601): Pi's sources of the page's and the terminal's
// runtimes, the React bindings and the test fixtures under pi/, with manifest.json recording the Pi commit and each file's hash
// (automation/gen/vendor-chord-surfaces-client.sh regenerates both), and the terminal sandbox's bootstrap.
// pig divergence (D601): the page runtime, the React bindings and the terminal sandbox runtime are Pi's TypeScript shipped unchanged,
// not Go ports (docs/parity/divergences/D601.md).
package client

import _ "embed"

// TuiBootstrapTemplate is the terminal sandbox's bootstrap (Pi's `BOOTSTRAP` of node/tui/sandbox.ts) with the two values it
// interpolates left as `@MAX_PIPE_FRAME@` and `@SANDBOX_BUILTINS@`.
//
//go:embed tui/bootstrap.cjs.in
var TuiBootstrapTemplate string
