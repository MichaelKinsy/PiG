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
| `testdata/fixture*.mjs` | two extensions of the page tests: `/fx-a mount <content>`, `update`, `hide`, `show` |
| `demo-extension.mjs` | `/ui-demo`: one panel with every view node kind that carries an id |
| `bridge_test.go` | `TestBridgeRelaysTheCarrier` drives the bridge with an HTTP client and always runs; the `TestPage*` tests drive the page in headless Chromium (focus and intents, layout geometry, colours and markdown, typed text across updates, panels as tabs); they need `CHROMIUM_BIN` and are skipped without it, and fail without it where `CHORD_REQUIRE_CHROMIUM` is set, as in `make ci-chord-web` |

What the page draws of a view:

- Layout. Sizes in a view are terminal cells; here a cell is `1ch` wide and one line high. An `hstack` or `vstack` puts `gap` cells between its children and reads each child's `stack` entry as Pi's stacks do (`basis`, `grow` 0, `shrink` 1, `minSize` 0, `maxSize`); an `hstack` shares out its width and places a shorter child by `align`. `box` and `text` have their `paddingX` and `paddingY` (1 when the view names none), `truncated-text` stays on one line.
- Colour. A `bg`, a `color` and a markdown `defaultTextStyle` name theme tokens. The page has a colour for each token (the `--pig-<token>` custom properties in `index.html`) and a view's `theme` overrides them for that view only. A value `#rrggbb` or `#rrggbbaa` is drawn as the colour itself, the rule drawings use.
- Markdown is drawn as headings, paragraphs, lists, quotes, code, emphasis and links, built element by element. The text is never parsed as HTML: a tag, a script or an `on*` attribute in it stays text, and only `http`, `https` and `mailto` links are followed.
- Updates. A node of the same kind under the same `id` keeps its element, so an `input` or `editor` keeps what the user typed, the selection and the focus while the rest of the view changes; a `value` the extension changes replaces the text. Nothing typed reaches the extension before submit.
- Panels stand beside the chat, side by side or as tabs (the `Side by side` button). A tab is labelled with the mount's key, as a view has no title. A panel behind another is still updated; a panel newly mounted or shown again by its extension is brought forward.

When the page reads slower than pig writes, the bridge stops reading pig's output, so pig's own queue bound and `ui_view_gap` apply; the bridge buffers nothing without bound.
