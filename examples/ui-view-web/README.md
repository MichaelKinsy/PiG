# ui-view-web: a reference web client for plugin views

A static page that draws the views extensions mount (`ui.view.*`) and raises their intents, over the `ui_view` carrier of RPC mode ([docs/ui-view-carrier.md](../../docs/ui-view-carrier.md)). A browser cannot speak lines on stdin and stdout, so `bridge.mjs` (Node, standard library only) starts one `pig --mode rpc` per page, relays its lines as Server-Sent Events and the page's lines by POST, and serves the page on 127.0.0.1. Pig has no web server; nothing here is part of it.

```sh
cd examples/ui-view-web
node bridge.mjs -- -e ./demo-extension.mjs      # prints http://127.0.0.1:<port>/?token=<token>
# open the printed address and type /ui-demo
```

`--pig <path>` names the pig to start (default `$PIG_BIN`, then `pig` on the PATH), `--port <n>` the port (default: any free one); everything after `--` goes to pig.

| File | What it is |
|---|---|
| `bridge.mjs` | the relay: `GET /events` starts a pig and streams its lines, `POST /send` writes one line to it; both need the token of the printed address and this origin |
| `index.html`, `client.js` | the page: attach, the client's rule for numbers, gaps and resync, drawing, a11y and focus, intents and their cancel, reports |
| `demo-extension.mjs` | `/ui-demo`: one panel with every view node kind that carries an id |
| `bridge_test.go` | `TestBridgeRelaysTheCarrier` drives the bridge with an HTTP client and always runs; `TestPageDrawsFocusesAndRaisesIntents` drives the page in headless Chromium, needs `CHROMIUM_BIN` and is skipped without it |

When the page reads slower than pig writes, the bridge stops reading pig's output, so pig's own queue bound and `ui_view_gap` apply; the bridge buffers nothing without bound.
