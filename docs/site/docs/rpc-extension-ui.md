# RPC extension UI

RPC mode binds a functional extension UI transport. A dialog emits an
`extension_ui_request` record and waits for the matching
`extension_ui_response`.

Example selector request:

```json
{"type":"extension_ui_request","id":"...","method":"select","title":"Choose","options":["A","B"]}
```

Return a value, confirmation, or cancellation:

```json
{"type":"extension_ui_response","id":"...","value":"A"}
```

```json
{"type":"extension_ui_response","id":"...","confirmed":true}
```

```json
{"type":"extension_ui_response","id":"...","cancelled":true}
```

PiG supports `select`, `confirm`, `input`, and `editor` as blocking dialogs.
It emits fire-and-forget requests for `notify`, `setStatus`, `setWidget`,
`setTitle`, and `set_editor_text`. TUI-only component factories remain no-ops
in RPC mode.

