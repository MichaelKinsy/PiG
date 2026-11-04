// Package http ports packages/chord/src/next-node/http (Chord v2's HTTP component, slice 10 §2): a Web-standard `fetch`
// handler built from the routes, wraps and sockets plugins contribute to the registry `Http`, with the security baseline on
// by default, Chord's own listener, an upgrade adapter and the request conversion for apps that mount it. `index.ts` is
// designed out: Go imports the package, and what it exported is exported here. JavaScript's Fetch and DOM types are Go's
// net/http types; the WebSocket `ws` is gorilla/websocket behind WebSocket (websocket.go).
//
// Where Go's net/http server differs from Node's observably, each file says so under "Forced Go differences" (node.go lists
// the listener's).
package http
