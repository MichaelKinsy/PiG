# ui-view-web: a reference web client for plugin views

A static page that draws the views extensions mount (`ui.view.*`) and raises their intents, over the `ui_view` carrier ([docs/ui-view-carrier.md](../../docs/ui-view-carrier.md)). A browser cannot speak lines on a pipe or a Unix socket, so `bridge.mjs` (Node, standard library only) relays pig's lines as Server-Sent Events and the page's lines by POST, and serves the page on 127.0.0.1. Pig has no web server; nothing here is part of it.

The bridge has two modes.

## A pig for every page

```sh
cd examples/ui-view-web
node bridge.mjs -- -e ./demo-extension.mjs      # prints http://127.0.0.1:<port>/ and the path of open.html, which sends a browser to the page, once
# open the open.html its first line names (below, "Opening the page") and type /ui-demo
```

The bridge starts one `pig --mode rpc` per page, so every page has a session of its own and can prompt it. `--pig <path>` names the pig to start (default `$PIG_BIN`, then `pig` on the PATH), `--port <n>` the port (default: any free one); everything after `--` goes to pig.

When the page reads slower than pig writes, the bridge stops reading pig's output, so pig's own queue bound and `ui_view_gap` apply; the bridge buffers nothing without bound.

## A page beside the terminal of a running session: `--connect`

```sh
pig --piglet ./my-piglet.yaml                                # an interactive session, in a terminal
node bridge.mjs --connect ~/.pig/agent/view-endpoint         # in another; open the open.html its first line names
```

With `--connect` the bridge starts no pig. Every page is one more client of a session that already runs, next to its terminal: both draw the same mounts, a click in the page is redrawn in the terminal, and a key in the terminal is redrawn in the page. The session must serve a view endpoint, which a Piglet selects with two keys:

```yaml
viewClient: terminal    # the terminal draws the mounts
viewEndpoint: socket    # other programs of this user may attach: <agent directory>/view-endpoint/<pid>.sock
```

- `--connect` takes the socket, or the directory it is in. In a directory the bridge tries every `*.sock`: exactly one that takes a connection is the session. A socket that takes none belongs to a pig that ended without removing it; the bridge ignores it and leaves it there. With two or more running sessions the bridge does not choose: the page is told their sockets, and you start the bridge with one of them.
- Prompting is not available. A view endpoint serves the seven `ui_view_*` commands and nothing else, so the bridge passes on only those and the page shows one line in place of its prompt. Type in the terminal.
- `/reload` keeps the session's registry, so it keeps the connection and the page's attachment. It replaces the instance of every extension, and a mount belongs to the instance that made it: each mount arrives once more as `broken` (reason `owner_gone`) and goes from the page with one line (`<title> closed: its extension stopped.`), and what the reloaded extensions mount afterwards is drawn as new mounts: the line of a key that is mounted again goes with it.
- A new, resumed, forked or switched session and the exit close the connection and remove the socket. The page then shows no mount. The bridge tries again every 100 ms, tells the page why it waits (`no running session: ...`), and when the next session serves its socket the page attaches to it and draws what that session holds. After `--wait <seconds>` (default 60) without a session the page's stream ends with the reason; reload the page to try again.
- A page that stops reading is owed at most 8 MiB and is then cut, which ends its attachment; the page connects again by itself after a second, with the same secret, and attaches afresh. The bridge never stops reading the session for a page, and it refuses a page's line while the session is owed 8 MiB it has not read.
- Security is the same in both modes: 127.0.0.1 only, and the secret and the origin are checked before the bridge opens any connection to the session. The address works once and holds no secret after the page has loaded; see "Opening the page".

## Opening the page

The first line the bridge prints names the file to open:

```text
ui-view-web: http://127.0.0.1:<port>/ (open: <dir>/open.html)
```

`open.html` sends your browser to the page's address. Open it with the path the line printed:

```sh
open <path>             # macOS
xdg-open <path>         # Linux
Start-Process <path>    # Windows, PowerShell
```

The path holds no code, and the file is readable by you only. Beside it is `address`, which holds the page's address, `http://127.0.0.1:<port>/#<code>`, and nothing else, for a script that reads it. Never pass the content of `address` as an argument to a browser or to `open`, `xdg-open` or `Start-Process`: that puts the code in the argument lists of the opener and of the browser, and on Linux every user of the machine can read an argument list (`/proc/<pid>/cmdline`, `ps`). Copying it into a browser's address bar is fine. If your browser does not follow the script in `open.html`, the file also shows a link: click it.

### The address works once

The code after the `#` is good for one load of the page and for ten minutes. The page takes the code out of the address bar at once, hands it to the bridge and is given a session secret in exchange. After that the address in the address bar is plain `http://127.0.0.1:<port>/`, and the code is dead: the same address opened again, from a bookmark, the browser's history, a copy of the file or another browser, is refused. The browser's history may keep the address you opened, and that used address opens nothing. This is how long it lives and what to do next:

- When the code is used, and when its ten minutes are over unused, the bridge writes the next code into `open.html` and `address` (a new file is renamed over the old one, so a reader never sees half a file, and the new code is there before the first page is answered). Open `open.html` again for another tab or after the ten minutes: it always holds a code that works.
- With `--print-token` there is no file and no next code: the printed address is good for one load and ten minutes, and then you start the bridge again.
- The page tells you what is wrong. If you open an address that was used or has expired, it says: `This address was used or has expired. Open the file the bridge named again; if there is none, restart the bridge.` If the bridge was restarted while the tab was open, it says: `The bridge does not know this tab any more: it was restarted. Open the file the new bridge named.` If you open `http://127.0.0.1:<port>/` in a tab that was never given an address (a new tab, a bookmark of the plain page), it says: `This tab is not signed in. Open the file the bridge named.` In none of these cases does the page ask the bridge for anything.
- A reload of the tab keeps the page signed in: the secret is in `sessionStorage`, which is that tab's and no other's. A second tab needs `open.html` again. Closing the tab ends it. The bridge keeps the newest 64 secrets, and a tab whose secret was pushed out gets the second message above.

Why the bridge works this way: the address is a live secret, and a log, a terminal recording, a CI log, a shared screen or a browser's history must not hold one that still works. The code is in the fragment (`#`), which a browser never sends to a server, so it is in no request, no `Referer` and no log. The page posts it to `/exchange` in a body. The secret the bridge answers with goes in an `Authorization` header with every request, the stream of events included (the page reads it with `fetch`, because an `EventSource` cannot send a header), so it is in no address either. The bridge sets no cookie: a cookie of `127.0.0.1:<port>` is sent by the browser to every other port of `127.0.0.1`, and a server another user runs there would receive it. `sessionStorage` belongs to one origin, and an origin includes the port; the browser sends it nowhere.

With `-- <pig arguments>` the secret lets any process of this machine that has it prompt the pig the bridge starts, as you, and so do what the agent can do; with `--connect` it passes the seven `ui_view_*` commands only. Other users of the machine can reach the port, and the secret is all that stops them: the code is 16 random bytes, the secret 32, both compared in constant time (the secret by its digest), and neither is in a file or an argument list another user can read. A request that is not the page's own host and origin is refused before its secret or its code is looked at, and a refused request, or a wrong code, uses nothing up.

- `address` and `open.html` are in a directory the bridge makes in the temporary directory (mode 0700), they have mode 0600, and the bridge removes the directory when it ends: on Ctrl-C, on SIGTERM and SIGHUP, and on an error that ends it. A bridge that is killed with SIGKILL cannot remove it; the files then hold a code no process accepts.
- On Windows the mode bits do not apply. What protects the files there is the access list (ACL) that the user's temporary directory (`%TEMP%`) gives to what is made in it.
- Every answer of the bridge carries `Referrer-Policy: no-referrer` and `Cache-Control: no-store`. The bridge prints no request.
- `--print-token`, or the environment variable `UI_VIEW_WEB_PRINT_TOKEN=1`, prints one line, `ui-view-web: http://127.0.0.1:<port>/#<code>`, and makes no file. Use it for a script that reads the line, and know that whatever records the output then holds an address that is live until it is used or ten minutes pass. The line holds one code, good for one exchange: a program that needs two tabs exchanges once and reuses the secret, or starts a second bridge. An empty value of the variable, or any value other than `1`, does not turn it on.

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
| `bridge.mjs` | the relay: `POST /exchange` trades the one-time code for a session secret, `GET /events` streams a session's lines to a page, `POST /send` writes one line of the page to it, `GET /state` counts what the bridge holds; the last three need the secret in an `Authorization: Bearer` header (never in an address) and this origin |
| `open.html`, `address` | not in this directory: the bridge makes them in a private temporary directory and removes them when it ends. Open `open.html`, the file its first line names; `address` holds the address with the live one-time code for a script, and its content is never an argument to a browser, because other users of the machine can read process arguments. Both are written again with the next code when a code is used or its ten minutes are over |
| `index.html`, `client.js` | the page: attach, the client's rule for numbers, gaps and resync, view nodes as controls, drawings as SVG, a11y and focus, intents and their cancel, reports; the default stylesheet and its tokens are in `index.html` |
| `testdata/fixture*.mjs` | four extensions of the page tests: `/fx-a mount <content>`, `update`, `hide`, `show`; `fx-c` and `fx-d` also answer two intents of their view: `fx.open` shows the mount and `fx.close` hides it |
| `testdata/scene.mjs` | an extension of the page tests: `/scene mount <points>` mounts a drawing of every shape kind with regions whose intents it answers by redrawing |
| `testdata/intent-frames.txt` | the `ui_view_intent` commands the page posted for a fixed run of keys and clicks on the demo panel before it drew controls (commit 1a43bce90c) |
| `controls_test.go` | the page's controls in headless Chromium: the element, role, name and state of every node kind, also as the browser computes them for assistive technology; the intents of a click and a key on each; the same intent bytes as before; the Tab order; elements kept across an update; the contrast of the default tokens; a drawing, its regions and gestures; a dialog mount and its focus rule; a title, as the card's heading, and bindings; a grid; a colour for every theme token; the page's columns, its prompt in sight and its status line; a hidden view the user opens; the dismiss intent of a dialog and an overlay |
| `demo-extension.mjs` | `/ui-demo`: one panel with every view node kind that carries an id |
| `bridge_test.go` | a pig for every page: `TestBridgeRelaysTheCarrier` drives the bridge with an HTTP client and always runs; the `TestPage*` tests drive the page in headless Chromium (focus and intents, layout geometry, colours and markdown, typed text across updates, panels as tabs); they need `CHROMIUM_BIN` and are skipped without it, and fail without it where `CHORD_REQUIRE_CHROMIUM` is set, as in `make ci-chord-web` |
| `connect_test.go` | `--connect` against a view endpoint the test serves: the `TestConnect*` tests drive the bridge with an HTTP client, `TestPageFollowsARunningSession` and `TestPageSaysNothingOfAMountItsExtensionEndedOrMountedAgain` (what the page says when a mount ends) drive the page in headless Chromium. `TestUIViewWebPageAndTerminalShareOneSession` in `test/integration` does it with a real interactive session |
| `theme_test.go` | `--theme`: without it the page is `index.html` byte for byte and `/theme/` holds nothing; with it the link is added and only the directory's own files of the served types are found |
| `token_print_test.go` | the first line: the default names a private file and holds no code or secret in stdout or stderr in any form it could print; `--print-token` and `UI_VIEW_WEB_PRINT_TOKEN=1` print the one-use line and make no file, any other value does not; the files are removed on SIGINT, SIGTERM, SIGHUP and an uncaught error; `open.html` is private and opens the page from headless Chromium, whose address bar then holds no code, also after a reload; every answer carries `Referrer-Policy` and `Cache-Control` and sets no cookie; no process holds a code or a secret in its argument list or environment |
| `one_time_test.go` | the one-time code: it works once and a replay, an expired code, a wrong Host or Origin, a wrong or malformed code and a code in an address are refused, and none of the refused ones uses it up; two racing requests give one secret; the next code is written before the answer, whole, mode 0600, never a used one; an unused code is replaced after its ten minutes, by a timer that does not keep the process alive; the bridge keeps 64 secrets; in headless Chromium no request URL, `Referer`, header or cookie the page makes holds the code or the secret, the stream reconnects with its secret, the three messages appear, a refused secret is dropped, and no cookie or secret reaches another port of the host. The other tests of this directory get their secret through `bridge.auth` and their page through `bridge.openIn` |

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
| `select-list`, `settings-list` | a list of rows: an item's description is a second, muted line under its label; a setting shows its value at the row's end | listbox and option with `aria-selected`; with the a11y role `tablist`, tablist and tab | arrows move, Enter or a click activates |
| `grid` | a grid of equal columns: the head, then the cells, each with its label, at most `chipRows` chips and a line `+K more` for the rest | grid, row, columnheader and gridcell; the cursor is `aria-selected`, the cell marked `current` is `aria-current` | arrows move the cursor over the cells that have a value, Enter, Space or a click chooses one and raises the grid's intent with the cell's value; an update that does not move `selected` keeps the user's cursor |
| `input` | an `input`, without the browser's spelling marks and suggestions | textbox, named by the a11y name or its `labelledBy` relation; `required`, `readOnly`, `disabled` are the control's own attributes | Enter submits |
| `editor` | a `textarea` | textbox | Ctrl+Enter submits |
| `mouse-region` | with the a11y role `button` or none, a `button`; with `checkbox`, `switch` or `radio`, a native checkbox or radio whose label is the region's child; with `link`, `tab`, `menuitem` or `treeitem`, a button with that role; with another role, a focusable block | the control's own; `checked` and `disabled` are the control's own state | click, Enter, Space |
| `scroll-view` | a focusable block that scrolls | the a11y role | scrolls |
| `loader` | a spinner and the message; still under reduced motion | status | none |
| `spacer`, `dynamic-border` | space; a rule | hidden; separator | none |
| an intent of the mount (no `nodeKey`) that no binding and no region of the drawing raises | a `button` in the mount's header | button, named by the intent's verb in words (`map.zoomIn`: Zoom in) with the full name as its title, because an intent has no label; this naming is the reference client's reading | click, Enter, Space |
| a key binding with a `name` | a `button` in a toolbar under the view that shows the key in a `kbd` | button, named by the binding's `name`, with `aria-keyshortcuts` | click, and the key |
| a mount of the `dialog` slot | a modal `dialog` element, named by the mount's title; `overlay` is a dialog that is not modal; several stack in mount order, the last on top | dialog | the page closes nothing (the mount is the extension's). Escape cancels the mount's cancellable intent in flight; with none in flight, Escape or the browser's request to close the dialog (its `cancel` event) raises the intent the mount declares `dismiss`, with `{}`, and the extension's handler hides or removes the mount; a mount that declares none stays as it is; hiding the mount closes the dialog and gives the focus back |
| a hidden mount that declares a `whenHidden` intent | a `button` that reads the mount's title, in the list `Open` above the prompt (a `nav` named Hidden views) | button | click, Enter, Space: raises that intent with `{}`; the extension's handler shows the mount |
| a `drawing` | one `svg` element (below) | each region: its `role` (default button) and `name` | below |

The conversation and summary kinds (`user-message`, `assistant-message`, `tool-execution`, `bash-execution`, `diff`, `cancellable-loader`, `bordered-loader`, `branch-summary`, `compaction-summary`, `custom-message`, `skill-invocation`) are not drawn: the page does not state them, so a mount that uses one arrives as its fallback.

- Title. A mount is labelled with its `title`: in its tab, as a dialog's accessible name and as the heading of its card, in the type of a heading. A title that the view's own first heading repeats is not drawn twice. Without a title a tab reads the mount's key and the card has a small caption, `<plugin> / <key>`.
- Focus. The order of the `focus` records is the Tab order; a new mount gives the focus to the record with `request: mount` and an update to the record with `request: update`; when an update changes the view's `focus` member while the focus is inside the mount, the focus moves to that node. A dialog mount takes the focus when it is shown. None of them takes the focus from a text field outside the mount that holds text: the page then reports `unfocused` (`ui_view_report`, severity info, path `focus`), a dialog opens without being modal, and it becomes modal when the user enters it. Unlike the terminal, the page lets a mount take the focus from a control that is not a text field.
- Bindings. A key binding fires while the focus is inside its mount and the focused control did not take the key (a text field takes what is typed, a list its arrows and Enter, a button Enter and Space), with the binding's `arg` or `{}`. Keys are compared parsed; Escape, Tab and the browser's own keys are never bound. A mount with a key binding can hold the focus as a whole. The page states the claim `input.bindings = [key]`; it reports no swipes, so it reports each gesture binding `unsupported` (severity info, path `bindings[i]`).
- Hidden mounts. A hidden mount stays in the page, undrawn. One that declares a `whenHidden` intent is offered to the user: a button with the mount's title in the list `Open` above the prompt. Choosing it raises that intent with `{}` and no node key, and the extension's handler shows the mount; the page shows nothing by itself, and says so in the chat when the intent does not settle `done`. A hidden mount without the intent is not listed, and on a live mount the intent has no button.
- Dismiss. In a mount of the `dialog` or the `overlay` slot that declares a `dismiss` intent, Escape and the dialog element's `cancel` event raise that intent with `{}` and no node key, once until it has settled; the intent is also a button in the mount's header, like any intent of the mount. The page never closes the mount: the extension's handler does. A mount that declares none keeps Escape for the cancel of its intent in flight, and in another slot the member has no effect.
- Ends. A mount its extension ended (`finished`, `removed`) goes from the page without a line: the extension closed its own view. A mount that was on the page and breaks (`broken`: its extension stopped, or a reload is loading it again) goes with one line in the chat, `<title or key> closed: its extension stopped.` (another reason is named in brackets), and the line goes when the same key is mounted again, as it is after a reload under a higher epoch. D614 says when a mount is gone and not what a client tells the user of it: this is the reference client's reading. With `?debug` each end is also written as the carrier states it (`<plugin>/<key> <state> (<reason>)`).
- Layout. Sizes in a view are terminal cells; here a cell is the tokens `--view-col` wide (8 px) and `--view-row` high (16 px). An `hstack` or `vstack` puts `gap` cells between its children and reads each child's `stack` entry as Pi's stacks do (`basis`, `grow` 0, `shrink` 1, `minSize` 0, `maxSize`); an `hstack` shares out its width and places a shorter child by `align`. `box` and `text` have their `paddingX` and `paddingY` (1 when the view names none), `truncated-text` stays on one line. Inside a button or a checkbox's label a node has no padding of its own.
- Colour. A `bg`, a `color` and a markdown `defaultTextStyle` name theme tokens. The page has a colour for every colour token of Pi's theme schema (`schemas/theme.schema.json`, 56 names: the `--pig-<token>` custom properties in `index.html`, each set from one of the page's neutral tokens, so `mdLink` is the accent and `mdHeading` the text colour; `TestPageHasAColourForEveryThemeToken`) and a view's `theme` overrides them for that view only. A value `#rrggbb` or `#rrggbbaa` is drawn as the colour itself, the rule drawings use.
- Markdown is never parsed as HTML: a tag, a script or an `on*` attribute in it stays text, and only `http`, `https` and `mailto` links are followed.
- Updates. Every element whose node (same kind, same `id`, or the same place among nodes without one) is still in the view is kept, with its attributes set to the new ones. So an `input` or `editor` keeps what the user typed, the selection and the focus while the rest of the view changes (a `value` the extension changes replaces the text), a checkbox, a selected row or a progress bar moves to its new state by its transition, and nothing flashes. Nothing typed reaches the extension before submit.
- Panels stand beside the chat, side by side or as tabs (the `Side by side` button). A panel behind another is still updated; a panel newly mounted or shown again by its extension is brought forward. While the chat holds no line, the panels and the prompt are one centred column of at most 880 px; once it holds one, the panels stand beside it. Under panels taller than the window the page scrolls and the prompt stays in sight at the window's lower edge; what the browser scrolls into view, a focused row for one, stops above it. With `--connect` the page has no chat, so the panels take the page's width and the status text follows them.
- The status line under the prompt says what the user must know (connecting, no running session, the stream ended) and is empty once the page is attached. The carrier's numbers (attachment, last event, mounts) are written there only when the address has `?debug`.

### Drawings

The page states `drawing {profiles: [scene, pixels], shapes: 4096, pixels: 1048576}`, the host's own bounds, and `snapshotBytes` 4194304, the carrier's bound of one line. A drawing is one `svg` element inside the node its `node` member names (it replaces what that node would show), or after the view without one. The `viewport` is the element's `viewBox` and its `fit` how the box is filled (`contain`, `cover`, `stretch`); the element is as wide as its place and as high as the viewport's proportions make it, unless its place has a height of its own.

- Shapes: `group` (`g`), `rect`, `ellipse`, `line`, `polyline` (`polygon` when `closed`), `path` (`move`, `line`, `quad`, `cubic`, `close`), `text` (`size`, `align`; drawn in the text colour when it has no `fill`) and `image` (an asset's bytes: the entry of the view's `images` whose `ref` is the asset's `sha256`, or its `id`). `fill` and `stroke` are a colour itself or a theme token, drawn from `--pig-<token>`; a token the page has no colour for draws nothing, so a theme gives a drawing its own tokens (`--pig-water`, `--pig-road`). A shape without `fill` is not filled.
- Updates. A shape with a `key` keeps its element: a scene that is sent again changes the attributes that differ, so a shape that moved (its `transform`, `x`, `y`, `cx`, `cy`) moves by its transition and nothing is drawn anew.
- `pixels` is the image asset drawn `width` by `height` units, `nearest` or `smooth`.
- Regions. A region is an element over its shape that takes the pointer and the keyboard, with its `role` (default `button`), its `name` as accessible name and tooltip, and a ring in the focus colour when it has the focus or, for a button, the pointer; the cursor is a pointer on a region that takes `activate` and a grabbing hand on one that takes `drag`. Regions follow the view's nodes in the Tab order. A line of text directly above the node a drawing fills is drawn as its caption. Gestures: `activate` is a click, or Enter or Space on the focused region; `press` and `release` are the pointer going down and up; `drag` is the pointer moving while it is down, and an arrow key on a focused region that takes `drag` is a drag of an eighth of the viewport in that direction, followed by the release.
- What a gesture sends. D614 says a client raises a region's intent "with the argument its declared shape asks for"; the property names are the owner's approved rule (D614, "Gesture argument"; [docs/ui-view-carrier.md](../../docs/ui-view-carrier.md)): a property is filled only when the intent's declared argument shape names it: `x` and `y` (the pointer in drawing coordinates), `dx` and `dy` (a drag's movement since the last event of this drag, in drawing units), `gesture` and `region` (the region's key). An intent whose shape names none of them is sent none.
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
| `--view-surface-raised` | `#ffffff`; `#23272d` | the face of a dialog, a step above a card in the dark scheme |
| `--view-border` | `#d9dde3`; `#343a43` | hairlines |
| `--view-fg`, `--view-fg-muted` | `#1a1d21`, `#5a6270`; `#e8eaed`, `#a3abb8` | text, secondary text |
| `--view-accent`, `--view-on-accent`, `--view-selected`, `--view-focus` | `#2456d6`, `#ffffff`, `#e3ebfd`, `#2456d6`; `#8ab0ff`, `#0c1a3a`, `#253454`, `#8ab0ff` | links, checked controls and the progress bar; text on the accent; a selected row; the focus ring |
| `--view-success`, `--view-warning`, `--view-error`, `--view-success-bg`, `--view-error-bg` | `#1a7f37`, `#8a5a00`, `#c62b3b`, `#e6f4ea`, `#fbe9eb`; `#5fd07f`, `#e6b450`, `#ff8090`, `#1d3324`, `#3a1f24` | states |
| `--view-shadow`, `--view-shadow-raised` | soft; deep | a card; a dialog |
| `--view-backdrop` | black at 20 %; black at 30 % | the page under a modal dialog |
| `--pig-<token>` | every colour token of Pi's theme schema, each set from the tokens above | the colours a view or a drawing names |
