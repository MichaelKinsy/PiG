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
- `/reload` keeps the session's registry, so it keeps the connection and the page's attachment. It replaces the instance of every extension, and a mount belongs to the instance that made it: each mount arrives once more as `broken` (reason `owner_gone`) and goes from the page with a line in the page's log, and what the reloaded extensions mount afterwards is drawn as new mounts.
- A new, resumed, forked or switched session and the exit close the connection and remove the socket. The page then shows no mount. The bridge tries again every 100 ms, tells the page why it waits (`no running session: ...`), and when the next session serves its socket the page attaches to it and draws what that session holds. After `--wait <seconds>` (default 60) without a session the page's stream ends with the reason; reload the page to try again.
- A page that stops reading is owed at most 8 MiB and is then cut, which ends its attachment; the browser connects again by itself and the page attaches afresh. The bridge never stops reading the session for a page, and it refuses a page's line while the session is owed 8 MiB it has not read.
- Security is the same in both modes: 127.0.0.1 only, and the token and the origin are checked before the bridge opens any connection to the session.

## A presentation of your own: `--theme`

```sh
node bridge.mjs --theme ./my-theme --connect ~/.pig/agent/view-endpoint
```

The page is plain on purpose and names no product. `--theme <directory>` works with both modes and changes how it looks without changing the page: the bridge links the directory's `theme.css` at the end of the page's head and serves the directory's `.css`, `.svg`, `.png`, `.woff2` and `.json` files by name under `/theme/` (no subdirectory, no name with a leading dot, nothing outside the directory). A theme sets the colours of the theme tokens a view names (the `--pig-<token>` custom properties) and whatever else a stylesheet can: type, the frame of a mount, a mark and a name in the header. Without `--theme` the bridge serves `index.html` byte for byte.

## Driving a session in a test

A script that drives the terminal of the session (the integration tests do it with tmux) must leave the panel before it types a slash command. While a panel's list holds the focus, typed keys go to the list and the command never runs: send Escape, wait until the screen has settled, then type `/reload`, `/new` or `/quit`.

## Files

| File | What it is |
|---|---|
| `bridge.mjs` | the relay: `GET /events` streams a session's lines to a page, `POST /send` writes one line of the page to it, `GET /state` counts what the bridge holds; each needs the token of the printed address and this origin |
| `index.html`, `client.js` | the page: attach, the client's rule for numbers, gaps and resync, drawing, a11y and focus, intents and their cancel, reports |
| `testdata/fixture*.mjs` | two extensions of the page tests: `/fx-a mount <content>`, `update`, `hide`, `show` |
| `demo-extension.mjs` | `/ui-demo`: one panel with every view node kind that carries an id |
| `bridge_test.go` | a pig for every page: `TestBridgeRelaysTheCarrier` drives the bridge with an HTTP client and always runs; the `TestPage*` tests drive the page in headless Chromium (focus and intents, layout geometry, colours and markdown, typed text across updates, panels as tabs); they need `CHROMIUM_BIN` and are skipped without it, and fail without it where `CHORD_REQUIRE_CHROMIUM` is set, as in `make ci-chord-web` |
| `connect_test.go` | `--connect` against a view endpoint the test serves: the `TestConnect*` tests drive the bridge with an HTTP client, `TestPageFollowsARunningSession` drives the page in headless Chromium. `TestUIViewWebPageAndTerminalShareOneSession` in `test/integration` does it with a real interactive session |
| `theme_test.go` | `--theme`: without it the page is `index.html` byte for byte and `/theme/` holds nothing; with it the link is added and only the directory's own files of the served types are found |

What the page draws of a view:

- Layout. Sizes in a view are terminal cells; here a cell is `1ch` wide and one line high. An `hstack` or `vstack` puts `gap` cells between its children and reads each child's `stack` entry as Pi's stacks do (`basis`, `grow` 0, `shrink` 1, `minSize` 0, `maxSize`); an `hstack` shares out its width and places a shorter child by `align`. `box` and `text` have their `paddingX` and `paddingY` (1 when the view names none), `truncated-text` stays on one line.
- Colour. A `bg`, a `color` and a markdown `defaultTextStyle` name theme tokens. The page has a colour for each token (the `--pig-<token>` custom properties in `index.html`) and a view's `theme` overrides them for that view only. A value `#rrggbb` or `#rrggbbaa` is drawn as the colour itself, the rule drawings use.
- Markdown is drawn as headings, paragraphs, lists, quotes, code, emphasis and links, built element by element. The text is never parsed as HTML: a tag, a script or an `on*` attribute in it stays text, and only `http`, `https` and `mailto` links are followed.
- Updates. A node of the same kind under the same `id` keeps its element, so an `input` or `editor` keeps what the user typed, the selection and the focus while the rest of the view changes; a `value` the extension changes replaces the text. Nothing typed reaches the extension before submit.
- Panels stand beside the chat, side by side or as tabs (the `Side by side` button). A tab is labelled with the mount's key, as a view has no title. A panel behind another is still updated; a panel newly mounted or shown again by its extension is brought forward.
