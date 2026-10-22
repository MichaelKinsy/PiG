# RPC extension UI

Extensions request user interaction through `ctx.ui`. In RPC mode, PiG turns the supported calls into a request and response subprotocol beside the normal [RPC commands](rpc-commands.md) and [session events](json.md). The subprotocol is the one of Pi 1.4.0 ([Pi's RPC extension UI reference](https://github.com/earendil-works/pi/blob/v1.2.0/packages/coding-agent/docs/rpc-extension-ui.md)).

There are two kinds of methods:

- Dialog methods (`select`, `confirm`, `input`, `editor`) write an `extension_ui_request` to standard output and wait until the client writes an `extension_ui_response` with the same `id` to standard input.
- Fire-and-forget methods (`notify`, `setStatus`, `setWidget`, `setTitle`, `set_editor_text`) write an `extension_ui_request` and expect no response. The client can show the information or ignore it.

A dialog request with a `timeout` field (milliseconds) resolves by itself when the timeout ends: `select`, `input` and `editor` with no value, `confirm` with `false`. The client does not need to track the timeout.

## Limitations

Some `ctx.ui` methods need the terminal UI, so RPC mode leaves them out or reduces them:

- `custom()` returns no component.
- `onTerminalInput()` returns an unsubscribe function that does nothing.
- `setWorkingMessage()`, `setWorkingVisible()`, `setWorkingIndicator()`, `setHiddenThinkingLabel()`, `setFooter()`, `setHeader()`, `addAutocompleteProvider()`, `setEditorComponent()` and `setToolsExpanded()` do nothing.
- `getEditorText()` returns `""` and `getEditorComponent()` returns nothing.
- `getToolsExpanded()` returns `false`.
- `pasteToEditor()` calls `setEditorText()` without terminal paste handling.
- `getAllThemes()` returns `[]` and `getTheme()` returns nothing.
- `setTheme()` returns `{ success: false, error: "Theme switching not supported in RPC mode" }`.

`ctx.mode` is `"rpc"` and `ctx.hasUI` is `true` in RPC mode, because the dialog and fire-and-forget methods work through this subprotocol. Check `ctx.mode === "tui"` before you use a terminal-only method such as `custom()`.

A PiG command that opens a custom picker in the terminal, such as `/sprite`, shows a plain `select` list in RPC mode.

## Requests from PiG

Every request has `"type": "extension_ui_request"`, a unique `id` and a `method`.

### select

```json
{"type":"extension_ui_request","id":"uuid-1","method":"select","title":"Allow dangerous command?","options":["Allow","Block"],"timeout":10000}
```

Respond with `value` (the chosen option) or `cancelled: true`.

### confirm

```json
{"type":"extension_ui_request","id":"uuid-2","method":"confirm","title":"Clear session?","message":"All messages will be lost.","timeout":5000}
```

Respond with `confirmed: true` or `false`, or with `cancelled: true`.

### input

```json
{"type":"extension_ui_request","id":"uuid-3","method":"input","title":"Enter a value","placeholder":"type something..."}
```

Respond with `value` (the entered text) or `cancelled: true`.

### editor

```json
{"type":"extension_ui_request","id":"uuid-4","method":"editor","title":"Edit some text","prefill":"Line 1\nLine 2\nLine 3"}
```

Respond with `value` (the edited text) or `cancelled: true`.

### notify

```json
{"type":"extension_ui_request","id":"uuid-5","method":"notify","message":"Command blocked by user","notifyType":"warning"}
```

`notifyType` is `info`, `warning` or `error`. Without it, the notification is `info`.

### setStatus

```json
{"type":"extension_ui_request","id":"uuid-6","method":"setStatus","statusKey":"my-ext","statusText":"Turn 3 running..."}
```

A request without `statusText` clears the status entry of that key. An empty `statusText` (`""`) is written as the extension passed it. The same holds for every member: a request carries an empty string, an empty `options` list or a `timeout` of 0 as the extension passed it, and omits only what the extension left out (the `placeholder` of `input`, the `prefill` of `editor`, `notifyType`, `statusText`, `timeout`).

### setWidget

```json
{"type":"extension_ui_request","id":"uuid-7","method":"setWidget","widgetKey":"my-ext","widgetLines":["--- My Widget ---","Line 1","Line 2"],"widgetPlacement":"aboveEditor"}
```

A request without `widgetLines` clears the widget. `widgetPlacement` is `aboveEditor` (the default) or `belowEditor`. RPC mode sends only string lines; a component factory is ignored.

### setTitle

```json
{"type":"extension_ui_request","id":"uuid-8","method":"setTitle","title":"pig - my project"}
```

### set_editor_text

```json
{"type":"extension_ui_request","id":"uuid-9","method":"set_editor_text","text":"prefilled text for the user"}
```

## Responses to PiG

Send a response for dialog methods only. The `id` must match the request.

Value response (`select`, `input`, `editor`):

```json
{"type":"extension_ui_response","id":"uuid-1","value":"Allow"}
```

Confirmation response (`confirm`):

```json
{"type":"extension_ui_response","id":"uuid-2","confirmed":true}
```

Cancellation response (any dialog). The extension receives no value, or `false` for `confirm`:

```json
{"type":"extension_ui_response","id":"uuid-3","cancelled":true}
```

When standard input closes, PiG cancels every open dialog before it exits.

## Source references

- `coding/cli/rpc_ui.go` defines the requests, the responses and the reduced methods.
- [Pi RPC types](https://github.com/earendil-works/pi/blob/v1.2.0/packages/coding-agent/src/modes/rpc/rpc-types.ts) defines the request and response unions.
