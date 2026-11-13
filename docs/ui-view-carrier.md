# Plugin views for a client outside the process: the `ui_view` carrier

An extension mounts views and answers intents through `ui.view.*` (D614 in [additive-features.md](additive-features.md)). A client that draws those mounts attaches to the session's registry. A Go program does that with `uiview.Registry.Attach`. A client in another process does it over RPC mode's JSON lines with the nine messages on this page. The carrier adds no record: every message is one call of the Go API, and its members are that call's records (`ClientAbilities`, `Snapshot`, `Notice`, `Result`), described in D614.

Nothing here has a version member and nothing is negotiated by version. A client states what it draws in `abilities`; the host resolves every mount for that client.

## Transport

`pig --mode rpc` reads commands on stdin and writes responses and events on stdout, one JSON object per line. `pig --mode rpc --listen <path>` serves the same lines on a Unix socket, one session per connection. The carrier's commands are RPC commands: the optional `id` of a command comes back in its response.

One connection holds one attachment at a time. Several clients use several connections. With `--listen` every connection has its own session, so its own registry.

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

Assets ride inline, in the view's `images`. A mount that declares an asset with `delivery: managed` is carried degraded and the plugin reads an `unsupported` notice.

## A client that never attaches

The seven command names above are commands for every client. Any other `type`, including `ui_view`, `ui_view_gap` and a near miss of a command name, gets RPC mode's unknown-command response unchanged. A client that sends none of the seven is written no carrier line, no response gains a member, and an extension's mount fails `unsupported` as it does without a client.

## Session replacement and the end of the connection

An attachment belongs to one session's registry. When the session is replaced (`new_session`, a switch, a fork), the attachment ends: the next command under its number is `stale`, and the same connection may send `ui_view_attach` again, which attaches to the new session. Closing the connection is a detach. Either way intents in flight settle `stale`.

## Relaying the carrier over another protocol

A program that speaks another protocol to its client and these lines to pig is a relay. Every message is self-contained and names its attachment (except `ui_view_attach`, which has none yet, and `ui_view_intent_cancel`, which names a request), so a relay keeps no state beyond the map of its own request ids. It must preserve three things:

- Order per attachment. Deliver the events of one attachment in the order pig wrote them, each attach, abilities or resync reply before the events that follow it, and each intent reply after the events that precede it.
- The gap rule. Do not drop, merge or renumber events. When the relay's client reads slower than pig writes, stop reading pig's output rather than buffering without bound: pig then applies its own queue bound and writes `ui_view_gap`. Pass the gap and the resync through unchanged.
- Request ids for intents. The id of the request that carries `ui_view_intent` is the id `ui_view_intent_cancel` names. Map the other protocol's request cancel to that command, and report `cancelled` from the intent's own response.

Carry `code` wherever the other protocol puts an error's data, and members as they are: a relay that renames or drops a member changes digests or breaks the strict shape.

## Reference client

[`examples/ui-view-web`](../examples/ui-view-web) is a static page and a bridge program (Node, standard library only) that starts `pig --mode rpc` for each page, relays its lines as Server-Sent Events and the page's lines by POST on 127.0.0.1. The page keeps the client's rule above, draws the view nodes that carry an id as focusable elements, applies `a11y` and `focus`, and raises intents. Pig itself has no web server.

Its tests are in `examples/ui-view-web/bridge_test.go`. `TestBridgeRelaysTheCarrier` drives the bridge with an HTTP client and always runs (it needs Node). `TestPageDrawsFocusesAndRaisesIntents` opens the page in headless Chromium: it needs `CHROMIUM_BIN` to name a Chromium binary and is skipped without it.
