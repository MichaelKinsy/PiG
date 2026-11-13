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

The page is plain on purpose and names no product. `--theme <directory>` works with both modes and changes how it looks without changing the page: the bridge links the directory's `theme.css` at the end of the page's head and serves the directory's `.css`, `.svg`, `.png`, `.woff2` and `.json` files by name under `/theme/` (no subdirectory, no name with a leading dot, nothing outside the directory). A theme sets the page's tokens (the `--view-*` custom properties: [The look and its tokens](#the-look-and-its-tokens)), the colours of the theme tokens a view or a drawing names (the `--pig-<token>` custom properties) and whatever else a stylesheet can: a face, the frame of a mount, a mark and a name in the header. Without `--theme` the bridge serves `index.html` byte for byte.

## Driving a session in a test

A script that drives the terminal of the session (the integration tests do it with tmux) must leave the panel before it types a slash command. While a panel's list holds the focus, typed keys go to the list and the command never runs: send Escape, wait until the screen has settled, then type `/reload`, `/new` or `/quit`.

## Files

| File | What it is |
|---|---|
| `bridge.mjs` | the relay: `GET /events` streams a session's lines to a page, `POST /send` writes one line of the page to it, `GET /state` counts what the bridge holds; each needs the token of the printed address and this origin |
| `index.html`, `client.js` | the page: attach, the client's rule for numbers, gaps and resync, view nodes as controls, drawings as SVG, a11y and focus, intents and their cancel, reports; the default stylesheet and its tokens are in `index.html` |
| `testdata/fixture*.mjs` | two extensions of the page tests: `/fx-a mount <content>`, `update`, `hide`, `show` |
| `testdata/scene.mjs` | an extension of the page tests: `/scene mount <points>` mounts a drawing of every shape kind with regions whose intents it answers by redrawing |
| `testdata/intent-frames.txt` | the `ui_view_intent` commands the page posted for a fixed run of keys and clicks on the demo panel before it drew controls (commit 1a43bce90c) |
| `controls_test.go` | the page's controls in headless Chromium: the element, role, name and state of every node kind, also as the browser computes them for assistive technology; the intents of a click and a key on each; the same intent bytes as before; the Tab order; elements kept across an update; the contrast of the default tokens; a drawing, its regions and gestures; a dialog mount |
| `demo-extension.mjs` | `/ui-demo`: one panel with every view node kind that carries an id |
| `bridge_test.go` | a pig for every page: `TestBridgeRelaysTheCarrier` drives the bridge with an HTTP client and always runs; the `TestPage*` tests drive the page in headless Chromium (focus and intents, layout geometry, colours and markdown, typed text across updates, panels as tabs); they need `CHROMIUM_BIN` and are skipped without it, and fail without it where `CHORD_REQUIRE_CHROMIUM` is set, as in `make ci-chord-web` |
| `connect_test.go` | `--connect` against a view endpoint the test serves: the `TestConnect*` tests drive the bridge with an HTTP client, `TestPageFollowsARunningSession` drives the page in headless Chromium. `TestUIViewWebPageAndTerminalShareOneSession` in `test/integration` does it with a real interactive session |
| `theme_test.go` | `--theme`: without it the page is `index.html` byte for byte and `/theme/` holds nothing; with it the link is added and only the directory's own files of the served types are found |

## What the page draws

A view node is drawn as the control it is, never as characters. What a node is comes from its kind and, for a node with an `id`, from the role and the states its `a11y` record gives it. The view kit has no button, checkbox or progress kind of its own: a `mouse-region` whose a11y role is `checkbox` is the checkbox.

| Node | Element | Role and state | Keyboard and pointer |
|---|---|---|---|
| `container`, `vstack`, `hstack` | a block; a stack is a flex row or column with the kit's `gap`, `align` and `stack` entries | the a11y role when the node has one (`list`, `listitem`, `group`, `region`, `toolbar`, ...) | none |
| `box` | a block with its padding; with a `bg` a card: radius, hairline, soft elevation | the a11y role | none |
| `text`, `truncated-text` | text in the page's proportional face; with the a11y role `heading` an `h2` to `h6` (2 plus the boxes around it, as the a11y record has no level) | heading with its level; `status` and `alert` as given | none |
| `markdown` | headings, paragraphs, lists, quotes, code, emphasis and links, built element by element | the elements' own | links |
| `lines` | a `pre` in the monospace face: rows a component drew, coordinates, code | none | none |
| `lines` with `progress` | a `progress` element with `value` and `max` | progressbar | none |
| `lines` with `list` | one row per item: label, detail and columns as the columns of one grid | list and listitem; the selected row is `aria-current` | none |
| `lines` with `image`, `image` | an `img` at its natural size, without gap or padding | image, named by the a11y name or the file name | none |
| `select-list`, `settings-list` | a list of rows; a setting shows its value at the row's end | listbox and option with `aria-selected`; with the a11y role `tablist`, tablist and tab | arrows move, Enter or a click activates |
| `input` | an `input` | textbox, named by the a11y name or its `labelledBy` relation; `required`, `readOnly`, `disabled` are the control's own attributes | Enter submits |
| `editor` | a `textarea` | textbox | Ctrl+Enter submits |
| `mouse-region` | with the a11y role `button` or none, a `button`; with `checkbox`, `switch` or `radio`, a native checkbox or radio whose label is the region's child; with `link`, `tab`, `menuitem` or `treeitem`, a button with that role; with another role, a focusable block | the control's own; `checked` and `disabled` are the control's own state | click, Enter, Space |
| `scroll-view` | a focusable block that scrolls | the a11y role | scrolls |
| `loader` | a spinner and the message; still under reduced motion | status | none |
| `spacer`, `dynamic-border` | space; a rule | hidden; separator | none |
| an intent of the mount (no `nodeKey`) | a `button` in the mount's header | button, named by the intent's verb in words (`map.zoomIn`: Zoom in) with the full name as its title, because an intent has no label | click, Enter, Space |
| a mount of the `dialog` slot | a modal `dialog` element, named by the mount; `overlay` is a dialog that is not modal | dialog | Escape closes nothing (the mount is the extension's) and cancels the mount's cancellable intent in flight; hiding the mount closes the dialog and gives the focus back |
| a `drawing` | one `svg` element (below) | each region: its `role` (default button) and `name` | below |

The conversation and summary kinds (`user-message`, `assistant-message`, `tool-execution`, `bash-execution`, `diff`, `cancellable-loader`, `bordered-loader`, `branch-summary`, `compaction-summary`, `custom-message`, `skill-invocation`) are not drawn: the page does not state them, so a mount that uses one arrives as its fallback.

- Focus. The order of the `focus` records is the Tab order; a new mount gives the focus to the record with `request: mount`; when an update changes the view's `focus` member while the focus is inside the mount, the focus moves to that node.
- Layout. Sizes in a view are terminal cells; here a cell is the tokens `--view-col` wide (8 px) and `--view-row` high (16 px). An `hstack` or `vstack` puts `gap` cells between its children and reads each child's `stack` entry as Pi's stacks do (`basis`, `grow` 0, `shrink` 1, `minSize` 0, `maxSize`); an `hstack` shares out its width and places a shorter child by `align`. `box` and `text` have their `paddingX` and `paddingY` (1 when the view names none), `truncated-text` stays on one line. Inside a button or a checkbox's label a node has no padding of its own.
- Colour. A `bg`, a `color` and a markdown `defaultTextStyle` name theme tokens. The page has a colour for each of Pi's tokens (the `--pig-<token>` custom properties in `index.html`) and a view's `theme` overrides them for that view only. A value `#rrggbb` or `#rrggbbaa` is drawn as the colour itself, the rule drawings use.
- Markdown is never parsed as HTML: a tag, a script or an `on*` attribute in it stays text, and only `http`, `https` and `mailto` links are followed.
- Updates. Every element whose node (same kind, same `id`, or the same place among nodes without one) is still in the view is kept, with its attributes set to the new ones. So an `input` or `editor` keeps what the user typed, the selection and the focus while the rest of the view changes (a `value` the extension changes replaces the text), a checkbox, a selected row or a progress bar moves to its new state by its transition, and nothing flashes. Nothing typed reaches the extension before submit.
- Panels stand beside the chat, side by side or as tabs (the `Side by side` button). A tab is labelled with the mount's key, as a view has no title. A panel behind another is still updated; a panel newly mounted or shown again by its extension is brought forward.

### Drawings

The page states `drawing {profiles: [scene, pixels], shapes: 4096, pixels: 1048576}`, the host's own bounds, and `snapshotBytes` 4194304, the carrier's bound of one line. A drawing is one `svg` element inside the node its `node` member names (it replaces what that node would show), or after the view without one. The `viewport` is the element's `viewBox` and its `fit` how the box is filled (`contain`, `cover`, `stretch`); the element is as wide as its place and as high as the viewport's proportions make it, unless its place has a height of its own.

- Shapes: `group` (`g`), `rect`, `ellipse`, `line`, `polyline` (`polygon` when `closed`), `path` (`move`, `line`, `quad`, `cubic`, `close`), `text` (`size`, `align`; drawn in the text colour when it has no `fill`) and `image` (an asset's bytes: the entry of the view's `images` whose `ref` is the asset's `sha256`, or its `id`). `fill` and `stroke` are a colour itself or a theme token, drawn from `--pig-<token>`; a token the page has no colour for draws nothing, so a theme gives a drawing its own tokens (`--pig-water`, `--pig-road`). A shape without `fill` is not filled.
- Updates. A shape with a `key` keeps its element: a scene that is sent again changes the attributes that differ, so a shape that moved (its `transform`, `x`, `y`, `cx`, `cy`) moves by its transition and nothing is drawn anew.
- `pixels` is the image asset drawn `width` by `height` units, `nearest` or `smooth`.
- Regions. A region is an element over its shape that takes the pointer and the keyboard, with its `role` (default `button`), its `name` as accessible name and tooltip, and a visible focus ring; regions follow the view's nodes in the Tab order. Gestures: `activate` is a click, or Enter or Space on the focused region; `press` and `release` are the pointer going down and up; `drag` is the pointer moving while it is down, and an arrow key on a focused region that takes `drag` is a drag of an eighth of the viewport in that direction, followed by the release.
- What a gesture sends. D614 says a client raises a region's intent "with the argument its declared shape asks for" and names no property. This client's reading, proposed for every client in [docs/ui-view-carrier.md](../../docs/ui-view-carrier.md): a property is filled only when the intent's declared argument shape names it: `x` and `y` (the pointer in drawing coordinates), `dx` and `dy` (a drag's movement since the last event of this drag, in drawing units), `gesture` and `region` (the region's key). An intent whose shape names none of them is sent none.
- Drag rate. One drag intent is in flight at a time, because each is raised on the view the last one drew; the movement made meanwhile is summed into the next one, which is sent on an animation frame and at most 30 times a second. The release is always sent, after the last drag.

The drawing type has no dash pattern, line cap or join, font family or weight, text along a path, gradient or clip, and no wheel or pinch gesture; the page adds none.

### The look and its tokens

The default look names no product: neutral, light or dark by `prefers-color-scheme`, motion off under `prefers-reduced-motion`, every text and surface pair at a contrast of at least 4.5 to 1 (`TestPageDefaultTokensHaveContrast`). It is made of custom properties on `:root`; a theme's `theme.css` overrides any of them.

| Token | Default (light, dark) | Used for |
|---|---|---|
| `--view-font`, `--view-font-mono` | the system's UI face; the system's monospace face | all text; `lines`, code |
| `--view-text-sm`, `--view-text`, `--view-text-lg`, `--view-text-xl`, `--view-leading` | 13, 14, 16, 20 px; 20 px | the type scale and the line height |
| `--view-space` | 8 px | the spacing unit of the page |
| `--view-col`, `--view-row` | 8 px, 16 px | one cell of a view |
| `--view-radius`, `--view-radius-sm`, `--view-control` | 8 px, 6 px, 32 px | cards; controls; the height of a control |
| `--view-motion` | 150 ms (0 ms with reduced motion) | every transition |
| `--view-bg`, `--view-surface`, `--view-surface-2` | `#f6f7f9`, `#ffffff`, `#eef0f3`; `#111315`, `#1b1e22`, `#262a30` | the page, a card or control, a hovered or inset surface |
| `--view-border` | `#d9dde3`; `#343a43` | hairlines |
| `--view-fg`, `--view-fg-muted` | `#1a1d21`, `#5a6270`; `#e8eaed`, `#a3abb8` | text, secondary text |
| `--view-accent`, `--view-on-accent`, `--view-selected`, `--view-focus` | `#2456d6`, `#ffffff`, `#e3ebfd`, `#2456d6`; `#8ab0ff`, `#0c1a3a`, `#253454`, `#8ab0ff` | links, checked controls and the progress bar; text on the accent; a selected row; the focus ring |
| `--view-success`, `--view-warning`, `--view-error`, `--view-success-bg`, `--view-error-bg` | `#1a7f37`, `#8a5a00`, `#c62b3b`, `#e6f4ea`, `#fbe9eb`; `#5fd07f`, `#e6b450`, `#ff8090`, `#1d3324`, `#3a1f24` | states |
| `--view-shadow`, `--view-shadow-raised` | soft; deep | a card; a dialog |
| `--pig-<token>` | Pi's theme tokens, each set from the tokens above | the colours a view or a drawing names |
