// Package surfaces is the server side of Chord's web and terminal surfaces (slice 11 §1, §4; decisions "Slice 11, P3" and
// "P5"): the base bundles built from the app's entry at start, the plugins' client scripts published as registered scripts
// and one combined file, the scripts objects (`WebScripts`, `TuiScripts`) showing the current set, and the routes serving them.
// The app kit creates them (`StartServer`'s Web and Tui options); by hand, WebSurface and TuiSurface. Ports
// packages/chord/src/node/surfaces.
//
// The client side of both surfaces (the page's runtime, the terminal's runtime, React bindings) is TypeScript that runs in a
// browser or in the terminal's sandbox: it is vendored byte for byte under client (D601).
package surfaces
