# Plugin views for a client outside the process: the `ui_view` carrier

An extension mounts views and answers intents through `ui.view.*` (D614 in [additive-features.md](additive-features.md)). A client that draws those mounts attaches to the session's registry. A Go program does that with `uiview.Registry.Attach`. A client in another process does it over RPC mode's JSON lines with the nine messages on this page. The carrier adds no record: every message is one call of the Go API, and its members are that call's records (`ClientAbilities`, `Snapshot`, `Notice`, `Result`), described in D614.

Nothing here has a version member and nothing is negotiated by version. A client states what it draws in `abilities`; the host resolves every mount for that client.

## Transport

`pig --mode rpc` reads commands on stdin and writes responses and events on stdout, one JSON object per line. `pig --mode rpc --listen <path>` serves the same lines on a Unix socket, one session per connection. The carrier's commands are RPC commands: the optional `id` of a command comes back in its response.

One connection holds one attachment at a time. Several clients use several connections. With `--listen` every connection has its own session, so its own registry.

An interactive session serves the carrier too when its Piglet sets `viewEndpoint: socket`: on the Unix socket `<agent directory>/view-endpoint/<pid>.sock`, where every connection reaches that one session. Its clients and the terminal then share one registry. See [The socket of an interactive session](#the-socket-of-an-interactive-session).

## Messages

| Direction | `type` | Members | Reply `data`, or meaning | Go API |
|---|---|---|---|---|
| client to host | `ui_view_attach` | `abilities` | `{attachment, seq, snapshots}` | `Registry.Attach` |
| client to host | `ui_view_abilities` | `attachment`, `abilities` | `{}`; the mounts that now resolve differently follow as `ui_view` | `Attachment.UpdateAbilities` |
| client to host | `ui_view_resync` | `attachment` | `{attachment, seq, snapshots}` | `Attachment.Resync` |
| client to host | `ui_view_detach` | `attachment` | `{}` | `Attachment.Detach` |
| client to host | `ui_view_intent` | `attachment`, `plugin`, `mount {key, epoch, rev}`, `nodeKey?`, `intent`, `arg` | `{ticket, outcome, answer?, error?}`, written once the intent settled | `Attachment.Invoke` |
| client to host | `ui_view_intent_cancel` | `id`: the id of the intent request | `{}`; the intent then replies `cancelled` | ends the context of that `Invoke` |
| client to host | `ui_view_report` | `attachment`, `plugin`, `mount {key, epoch}`, `notice {code, severity, message?, path?, claim?, measured?, limit?}` | `{}` | `Attachment.Report` |
| host to client | `ui_view` | `attachment`, `seq`, `snapshot` | one delivery: the whole of one mount as this client draws it | `Delivery` |
| host to client | `ui_view_gap` | `attachment`, `seq` | the host refused delivery number `seq` because the client's queue was full | the sink returned false |

A snapshot is `{plugin, slot, target?, mount {key, epoch, rev}, state, reason?, result?, title?, view, intents?, assets?, drawing?, a11y?, focus?, bindings?, alternative?, degraded?, digest?}`. `state` is live, hidden, finished, removed or broken. A mount is gone when its state is finished (with the plugin's `result`), removed or broken (with a `reason`). `alternative` says which content the client's claims selected (absent for the view itself), `degraded` that the content did not fit the client and the fallback, or nothing, stands in, and `digest` is the SHA-256 of the RFC 8785 form of the content.

Mounting, updating, hiding, showing, finishing and removing are the extension's calls. A client sees only their effect, as snapshots.

Exact bytes of every message, its reply and each of its errors: one transcript per message in [`internal/viewcarrier/testdata/transcripts`](../internal/viewcarrier/testdata/transcripts), pinned by `TestCarrierTranscripts`.

## Order, gap and resync

- The host numbers the `ui_view` events of an attachment 1, 2, 3 in the order the registry accepted the operations. Replies are not numbered.
- An `ui_view_attach`, `ui_view_abilities` or `ui_view_resync` reply is written before any event numbered after it.
- An `ui_view_intent` reply is written after every event the host queued for the attachment before the intent settled. A plugin whose handler redraws its mount and then answers gives the client the new view first and the reply second.
- The host keeps at most 256 events and 4194304 bytes per attachment that the client has not been written. When a delivery does not fit, the host writes the queued events, then one `ui_view_gap` whose `seq` is the number of the refused delivery, and then nothing more for that attachment until the client sends `ui_view_resync`.
- The client's rule: an event whose `seq` is not the last one plus 1, or a `ui_view_gap`, means resync. After a resync reply, drop every event whose `seq` is at or below the reply's `seq`, forget the mounts the reply lacks, and redraw only the snapshots whose `digest` differs from the one held. A snapshot without a digest is always redrawn.
- A resync that arrives while events are still queued supersedes them: they are not written.

A client that stops reading costs the host a bounded queue and nothing else. No other part of the session waits for it.

## Intents

`ui_view_intent` raises an intent a snapshot declares in `intents`, on the mount ref the client drew (`key`, `epoch`, `rev`) and, for an intent bound to a node, with that node's `nodeKey`. The response comes when the intent settled, so other commands and events pass in between. `outcome` is done, refused, failed, cancelled, timeout or stale; `answer` is the plugin's answer and `error` its or the host's text.

- An update the plugin's handler makes to the mount while it answers belongs to the answer: the outcome is the handler's, and the `ui_view` event of that update precedes the response. A client that has read the response already holds the view the handler drew and raises its next intent on that `rev`.
- A mount that another hand changed while the handler answered settles the intent `stale` (the plugin's background work, the handler of another intent on the same mount). Of two intents in flight on one mount, the one whose handler updates first is answered and the other is `stale`.
- A ref that is not the mount's current one is `stale`. So is an intent under an attachment the connection does not hold, and one in flight when the attachment ends. These are results, not error responses.
- To cancel, send `ui_view_intent_cancel` with the same `id` as the intent request. Its own response is written first, then the intent's response with `outcome: cancelled`. A cancellable intent's handler is cancelled in the extension.
- An intent sent without an `id` settles like any other, its response has no `id`, and it cannot be cancelled. Two intents in flight cannot share an `id` (`invalid`).

## Reports

`ui_view_report` tells the plugin that the client could not draw part of a mount, or did not give it the focus its `focus` record asked for: `code` is undrawn, unsupported, limit or unfocused and `severity` info, warning or error. The plugin reads it with `ui.view.diagnostics`.

## Dismissal

One entry of a snapshot's `intents` may carry `dismiss: true`. It is the intent the plugin wants when the user dismisses a mount of the `dialog` or `overlay` slot (Escape, a close control, a click outside: the gesture is the client's). The host has checked it: at most one intent of a mount has the member, it names no node and its `arg` accepts `{}`.

- When the user dismisses such a mount, send `ui_view_intent` for that intent on the ref you drew, with `arg: {}` and no `nodeKey`. Keep drawing the mount: the plugin's handler closes it, and the snapshot that follows (removed, finished or hidden) is what takes it away. An outcome other than done leaves it open.
- A mount without such an intent keeps the earlier rule: Escape leaves the mount, and it stays open until a key or control of the plugin closes it. A client that does not read the member does the same for every mount.
- In any other slot the member has no effect: do not raise the intent for the user's leaving a panel.

## Bindings

A snapshot's `bindings [{key | gesture, intent, arg?, name?}]` tie keys and gestures to intents of the mount that name no node. The host has checked them (the record is in [additive-features.md](additive-features.md), D614, "Bindings") and sends every client all of them; firing them is the client's part:

- A key binding fires only while its mount holds the keyboard focus, and a mount with a key binding can hold the focus as a whole. The focused node of the mount gets the key first; a key it does not take fires the binding of that key; a key no binding names is the client's own. No binding is global.
- A gesture binding fires when the gesture (`swipe-left`, `swipe-right`, `swipe-up`, `swipe-down`) is made on the mount and the node under it did not take it.
- To fire a binding, send `ui_view_intent` with the binding's `intent`, no `nodeKey`, and its `arg`, or `{}` when it has none.
- `key` is a key id: `shift`, `ctrl` and `alt` in any order, each followed by `+`, then the base key. Compare keys parsed, not as text.
- The keys of `uiview.ReservedKeys` never arrive in a binding (`escape`, `tab` and `f6` with any modifiers; `c`, `d` and `z` with `ctrl`): keep them for leaving the mount, moving the focus, and the terminal's interrupt, end of input and suspend. Those are the default keys. If your user can give one of those actions another key, give no binding that key either, and decide it when the key is pressed, as the terminal client does.
- A client that takes keyboard input for a focused mount honours key bindings, and one whose pointing surface reports swipes honours gesture bindings. Report binding `i` that you cannot honour with `ui_view_report`: `{code: "unsupported", severity: "info", path: "bindings[i]"}`.
- You may state the claim `input.bindings` (a list of `key` and `gesture`) in your abilities, so that an extension's `alternatives` can test it. The host does not read it. It says which kinds of trigger you honour, not which keys.
- A binding's `name`, a `title` and a mount's `key` are labels: the host has refused a control character, a line separator and the controls that reorder text (U+202A to U+202E, U+2066 to U+2069), so you can write them on one line as they are.

## Gestures on a region of a drawing

Approved (the owner, 2026-11-13T19:27:20Z, "G-arg"; recorded in D614, "Gesture argument"): D614 says a client raises a region's intent "with the argument its declared shape asks for", and this section says which properties that is, so that every client fills the argument the same way. The reference web client implements it. It is the only client in this repository that raises the gesture of a region of a drawing: the terminal view client draws no drawings and raises none.

- A property of the argument is filled only when the intent's declared argument shape names it. An intent whose shape names none of the properties below is sent `{}`.
- `x` and `y`: the pointer's position in the drawing's coordinates (the viewport's units). For `activate` made with the keyboard, the centre of the region.
- `dx` and `dy`: for `drag`, the pointer's movement since the last event of this drag, in the drawing's units; 0 for every other gesture.
- `region`: the region's `key`. `gesture`: `activate`, `press`, `release` or `drag` (the reference web client sends it; the approved line of the owner's names the other five).
- A property of type `integer` takes the nearest whole number.
- `nodeKey` is the declared intent's own, when it has one.
- A drag is a sequence: `press` (if the region lists it), any number of `drag`, then `release` (if listed). A client raises one of them at a time, each on the mount ref it holds after the reply to the one before, sums the movement made meanwhile into the next `drag`, sends at most 30 `drag` a second, and always sends the `release`.

## Errors

A failed carrier command is RPC mode's error response with one more member, `code`:

```json
{"id":"7","type":"response","command":"ui_view_resync","success":false,"error":"stale: the connection does not hold attachment 3","code":"stale"}
```

`error` is `<code>: <text>`. The codes are closed:

| `code` | Meaning |
|---|---|
| `invalid` | malformed command, missing member, unknown member of the command or of a record inside it |
| `unsupported` | the session runs no extension host |
| `limit` | a bound below would be exceeded |
| `stale` | the connection does not hold that attachment, or the mount's epoch is not current |
| `not_found` | no mount under that key; no intent in flight under that id |
| `terminal` | the mount already ended |

Only replies to the seven carrier commands carry `code`.

## Bounds

| Bound | Value | Past it |
|---|---|---|
| attachments per connection | 1 at a time | `ui_view_attach` fails `limit` |
| attachments per registry, clients of every kind together (Go API, in-process and wire) | 8 | attach fails `limit` |
| events queued for a client that does not read | 256 | `ui_view_gap` |
| bytes of those events | 4194304 | `ui_view_gap` |
| bytes of one `ui_view` line | 4194304 | the snapshot is carried degraded, without content; the plugin reads a `limit` notice |
| bytes of one snapshot inside an attach or resync reply | the same rule as a `ui_view` line | the same |
| bytes of one carrier command line | 4194304 | the command fails `limit` |

A reply carries every current snapshot and is therefore bounded by the client's own `limits.mounts` times 4194304 bytes, not by one message bound. Nodes, depth, text, snapshot bytes, mounts and intent argument bytes are the client's `limits`, enforced by the registry as for every client.

Assets ride inline, in the view's `images`. The bytes of an image count in the client's `limits.snapshotBytes`, as the base64 they are carried in, and not in `limits.textBytes`: the `data` member of the records of that list is left out of the longest-text figure, and every other string is in it. A mount that declares an asset with `delivery: managed` is carried degraded and the plugin reads an `unsupported` notice.

## A client that never attaches

The seven command names above are commands for every client. Any other `type`, including `ui_view`, `ui_view_gap` and a near miss of a command name, gets RPC mode's unknown-command response unchanged. A client that sends none of the seven is written no carrier line, no response gains a member, and an extension's mount fails `unsupported` as it does without a client.

## Session replacement and the end of the connection

An attachment belongs to one session's registry. When the session is replaced (`new_session`, a switch, a fork), the attachment ends: the next command under its number is `stale`, and the same connection may send `ui_view_attach` again, which attaches to the new session. Closing the connection is a detach. Either way intents in flight settle `stale`.

## The socket of an interactive session

Stock PiG serves no socket. A Piglet selects it:

```yaml
viewClient: terminal    # the TUI draws the mounts
viewEndpoint: socket    # other programs of this user may attach to the same session
```

- Path: `<agent directory>/view-endpoint/<pid>.sock`, in a directory of mode 0700, the socket 0600. Only this user can connect: pig also reads each peer's user from the kernel and closes a peer of another user before reading a byte. The path must fit a socket address: at most 103 bytes on macOS and 107 on Linux. With a longer agent directory nothing is bound, no socket is served, and one warning names the path, its length and the limit.
- Lines: the seven commands and two events of this page, with the same replies, errors and bounds. Nothing else of RPC mode is served: any other `type` gets `{"type":"response","command":"<type>","success":false,"error":"Unknown command: <type>"}`. A client of the socket draws mounts and raises their intents; it cannot prompt or read the session.
- One registry: the terminal and every connection attach to the same registry, so the bound of 8 attachments counts them together, each client is sent every mount resolved for its own abilities, and the redraw a handler makes for one client's intent is delivered to all of them.
- What a client sees when the session changes or ends:

  | What happens | What the client sees | What the client does |
  |---|---|---|
  | `/reload` | The connection and the attachment stay: the registry is the same one. A reload replaces the instance of every extension it loads again (a fused Go member, a Go, Rust, Python or Lisp extension in a process, a Node extension in its runtime's process, whether or not that process is kept), and a mount belongs to the instance that made it. Before the extensions load again the client is sent each of their mounts once more, in state `broken` with reason `owner_gone`, as the terminal is, and nothing else. A mount the new instance makes follows as a new mount: the same key under a higher epoch. An intent raised on the mount of the instance before is refused (`mount is broken`) and reaches no handler. | Draws what it is sent: the broken mount goes, the new one is drawn. |
  | An extension's process ends (it crashes or is killed) | The connection and the attachment stay. Each mount of that extension arrives in state `broken` with reason `owner_gone`. An intent raised on it is refused. The extension mounts again when a reload has started it again. | Draws what it is sent. |
  | The session is replaced (new, resume, fork, switch) | The connection ends. The socket is removed and appears again under the same path when the next extension host is bound. An intent that was in flight has no reply. | Connects again, retrying while the path is absent, and sends `ui_view_attach`: the reply carries what the new registry holds. |
  | pig exits (`/quit`, a signal that ends it, the terminal is lost) | The connection ends and the socket is removed. | Stops, or waits for another session: its socket has another name, because the name is the pid. |
  | pig is killed or the machine stops | The connection ends when the kernel closes it. The socket file stays and refuses every connect. The next pig that starts in that agent directory removes it, after checking that it is this user's own socket and that a connect is refused. | Treats a socket that refuses the connect as absent. It never deletes the file. |
- Bounds beside the carrier's: 16 connections at once; a line longer than 4194304 bytes closes the connection; a connection that is owed 512 lines or 8388608 bytes it has not read is closed. A client that only stops reading is owed the carrier's queue and then `ui_view_gap`, as on any transport.
- Print, JSON and RPC mode ignore the key. On Windows the session starts, no socket is served and one warning says so.

## Relaying the carrier over another protocol

A program that speaks another protocol to its client and these lines to pig is a relay. Every message is self-contained and names its attachment (except `ui_view_attach`, which has none yet, and `ui_view_intent_cancel`, which names a request), so a relay keeps no state beyond the map of its own request ids. It must preserve three things:

- Order per attachment. Deliver the events of one attachment in the order pig wrote them, each attach, abilities or resync reply before the events that follow it, and each intent reply after the events that precede it.
- The gap rule. Do not drop, merge or renumber events. When the relay's client reads slower than pig writes, stop reading pig's output rather than buffering without bound: pig then applies its own queue bound and writes `ui_view_gap`. Pass the gap and the resync through unchanged.
- Request ids for intents. The id of the request that carries `ui_view_intent` is the id `ui_view_intent_cancel` names. Map the other protocol's request cancel to that command, and report `cancelled` from the intent's own response.

Carry `code` wherever the other protocol puts an error's data, and members as they are: a relay that renames or drops a member changes digests or breaks the strict shape.

## Reference client

[`examples/ui-view-web`](../examples/ui-view-web) is a static page and a bridge program (Node, standard library only) that relays pig's lines as Server-Sent Events and the page's lines by POST on 127.0.0.1. The bridge starts `pig --mode rpc` for each page or, with `--connect`, makes every page a client of the socket of a running interactive session, beside its terminal. The page keeps the client's rule above and draws a view as controls: a node is the control its kind and the role and states of its `a11y` record make it (a `mouse-region` with the role `checkbox` is a native checkbox), a `grid` is a grid, a mount of the `dialog` slot is a modal dialog, and a `drawing` is one SVG element whose regions take the pointer and the keyboard. It labels a mount with its `title`, fires key bindings and shows the named ones as buttons with their key, applies `a11y` and `focus`, raises intents, and updates in place, keeping every element whose node or keyed shape is still in the view. Its look is a default stylesheet of custom properties that a theme directory overrides. Pig itself has no web server.

What the page draws (the page's [README](../examples/ui-view-web/README.md) has the full table with the keys of each control, and what the page leaves out):

| Node | Element | Role and states |
|---|---|---|
| `container`, `vstack`, `hstack`, `box` | a block; a stack is a flex row or column; a `box` with a `bg` is a card | the a11y role of the node, when it has one |
| `text`, `truncated-text` | text; with the a11y role `heading`, an `h2` to `h6` | heading with its level; `status`, `alert` |
| `markdown` | headings, paragraphs, lists, quotes, code and links, built element by element | the elements' own |
| `lines` | a `pre`; with `progress` a `progress` element; with `list` rows in columns; with `image` an `img` | none; progressbar; list and listitem, the selected row `aria-current`; image |
| `select-list`, `settings-list` | a list of rows | listbox and option with `aria-selected`; with the a11y role `tablist`, tablist and tab |
| `grid` | a grid of equal columns | grid, row, columnheader, gridcell; `aria-selected`, `aria-current` |
| `input`, `editor` | an `input`, a `textarea` | textbox; `required`, `readOnly`, `disabled` |
| `mouse-region` | a `button`; with the a11y role `checkbox`, `switch` or `radio` a native checkbox or radio | the control's own; `checked`, `disabled` |
| `scroll-view`, `loader`, `image`, `spacer`, `dynamic-border` | a block that scrolls; a spinner with its message; an `img`; space; a rule | the a11y role; status; image; hidden; separator |
| an intent of the mount without a `nodeKey`; a named key binding | a `button` in the mount's header; a `button` that shows its key | button |
| a mount of the `dialog` or `overlay` slot | a `dialog` element, modal for `dialog` | dialog, named by the title |
| a hidden mount with a `whenHidden` intent | a `button` with the mount's title in a list above the prompt | button |
| a `drawing` | one `svg` element; each region an element over its shape | the region's `role` (default button) and `name` |

- Hidden mounts. The page offers a hidden mount only when it declares a `whenHidden` intent, and choosing it raises that intent with `{}`; the plugin's handler shows the mount.
- Dismiss. In a mount of the `dialog` or `overlay` slot that declares a `dismiss` intent, Escape and the dialog element's `cancel` event raise that intent with `{}`; the page closes no mount itself, and a mount that declares none stays as it is.
- Ends. A mount its plugin finished or removed goes from the page without a line. A mount that breaks while it is on the page leaves one line by its title (`<title> closed: its extension stopped.`), and the line goes when the same key is mounted again, as after a reload. The contract says when a mount is gone, not what a client tells the user: this is the page's reading.
- Gestures. What the page sends for a gesture on a region is the reading of the section above: `x`, `y`, `dx`, `dy`, `gesture` and `region`, each only when the intent's declared argument shape names it.
- Tokens. The look is custom properties on `:root`, neutral, light or dark by `prefers-color-scheme`: `--view-font`, `--view-font-mono`; `--view-text-sm`, `--view-text`, `--view-text-lg`, `--view-text-xl`, `--view-leading`; `--view-space`, `--view-col`, `--view-row`; `--view-radius`, `--view-radius-sm`, `--view-control`; `--view-motion`; `--view-bg`, `--view-surface`, `--view-surface-2`, `--view-surface-raised`, `--view-border`; `--view-fg`, `--view-fg-muted`; `--view-accent`, `--view-on-accent`, `--view-selected`, `--view-focus`; `--view-success`, `--view-warning`, `--view-error`, `--view-success-bg`, `--view-error-bg`; `--view-shadow`, `--view-shadow-raised`, `--view-backdrop`; and one `--pig-<token>` for every colour token of Pi's theme schema, set from those, which is what a view's `bg` and `color` and a drawing's `fill` and `stroke` name. A `--theme` directory overrides any of them.

Its tests are in `examples/ui-view-web`: `bridge_test.go` for the bridge that starts pig, `connect_test.go` for `--connect`, against a view endpoint the test serves, and `controls_test.go` for the controls, the drawing, the look, a hidden view the user opens and the dismiss intent. The tests that drive the bridge with an HTTP client always run (they need Node). The `TestPage*` tests open the page in headless Chromium: they need `CHROMIUM_BIN` to name a Chromium binary and are skipped without it. `TestUIViewWebPageAndTerminalShareOneSession` in `test/integration` opens the page on a real interactive session.
