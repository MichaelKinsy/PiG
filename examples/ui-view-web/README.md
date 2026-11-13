# ui-view-web: a reference web client for plugin views

A static page that draws the views extensions mount (`ui.view.*`) and raises their intents, over the `ui_view` carrier ([docs/ui-view-carrier.md](../../docs/ui-view-carrier.md)). A browser cannot speak lines on a pipe or a Unix socket, so `bridge.mjs` (Node, standard library only) relays pig's lines as Server-Sent Events and the page's lines by POST, and serves the page on 127.0.0.1. Pig has no web server; nothing here is part of it.

The bridge has two modes.

## A pig for every page

```sh
cd examples/ui-view-web
node bridge.mjs -- -e ./demo-extension.mjs      # prints http://127.0.0.1:<port>/?token=<token>
# open the printed address and type /ui-demo
```

The bridge starts one `pig --mode rpc` per page, so every page has a session of its own and can prompt it. `--pig <path>` names the pig to start (default `$PIG_BIN`, then `pig` on the PATH), `--port <n>` the port (default: any free one); everything after `--` goes to pig.

When the page reads slower than pig writes, the bridge stops reading pig's output, so pig's own queue bound and `ui_view_gap` apply; the bridge buffers nothing without bound.

## A page beside the terminal of a running session: `--connect`

```sh
pig --piglet ./my-piglet.yaml                                # an interactive session, in a terminal
node bridge.mjs --connect ~/.pig/agent/view-endpoint         # in another; open the printed address
```

With `--connect` the bridge starts no pig. Every page is one more client of a session that already runs, next to its terminal: both draw the same mounts, a click in the page is redrawn in the terminal, and a key in the terminal is redrawn in the page. The session must serve a view endpoint, which a Piglet selects with two keys:

```yaml
viewClient: terminal    # the terminal draws the mounts
viewEndpoint: socket    # other programs of this user may attach: <agent directory>/view-endpoint/<pid>.sock
```

- `--connect` takes the socket, or the directory it is in. In a directory the bridge tries every `*.sock`: exactly one that takes a connection is the session. A socket that takes none belongs to a pig that ended without removing it; the bridge ignores it and leaves it there. With two or more running sessions the bridge does not choose: the page is told their sockets, and you start the bridge with one of them.
- Prompting is not available. A view endpoint serves the seven `ui_view_*` commands and nothing else, so the bridge passes on only those and the page shows one line in place of its prompt. Type in the terminal.
- `/reload` keeps the connection. The session sends the mounts of the reloaded extensions as `broken`: they go from the page, each with a line in the page's log, and what the extensions mount afterwards is drawn.
- A new, resumed, forked or switched session and the exit close the connection and remove the socket. The page then shows no mount. The bridge tries again every 100 ms, tells the page why it waits (`no running session: ...`), and when the next session serves its socket the page attaches to it and draws what that session holds. After `--wait <seconds>` (default 60) without a session the page's stream ends with the reason; reload the page to try again.
- A page that stops reading is owed at most 8 MiB and is then cut, which ends its attachment; the browser connects again by itself and the page attaches afresh. The bridge never stops reading the session for a page, and it refuses a page's line while the session is owed 8 MiB it has not read.
- Security is the same in both modes: 127.0.0.1 only, and the token and the origin are checked before the bridge opens any connection to the session.

## Files

| File | What it is |
|---|---|
| `bridge.mjs` | the relay: `GET /events` streams a session's lines to a page, `POST /send` writes one line of the page to it, `GET /state` counts what the bridge holds; each needs the token of the printed address and this origin |
| `index.html`, `client.js` | the page: attach, the client's rule for numbers, gaps and resync, drawing, a11y and focus, intents and their cancel, reports |
| `demo-extension.mjs` | `/ui-demo`: one panel with every view node kind that carries an id |
| `bridge_test.go` | a pig for every page: `TestBridgeRelaysTheCarrier` drives the bridge with an HTTP client and always runs; `TestPageDrawsFocusesAndRaisesIntents` drives the page in headless Chromium, needs `CHROMIUM_BIN` and is skipped without it |
| `connect_test.go` | `--connect` against a view endpoint the test serves: the `TestConnect*` tests drive the bridge with an HTTP client, `TestPageFollowsARunningSession` drives the page in headless Chromium. `TestUIViewWebPageAndTerminalShareOneSession` in `test/integration` does it with a real interactive session |
