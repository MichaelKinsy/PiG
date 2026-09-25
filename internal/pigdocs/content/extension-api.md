# Extension API

This document is the canonical reference for the wire protocol and host API surface that pig exposes to extensions. The same API is bridged by all official subprocess SDKs (Go in `extensions/sdk`, Rust in `extensions/sdk-rs`, Python in `extensions/sdk-py`). Node extensions use Pi's canonical TypeScript API through Pig's compatibility runtime. `extensions/sdk-ts` adds declarations for Pig-only calls. Method names below are normalized; SDK bindings use language-idiomatic spellings.

## Wire protocol (subprocess, current unversioned shape)

The host and extension exchange length-prefixed JSON envelopes over a Unix domain socket. Each frame is a 4-byte big-endian unsigned length followed by exactly that many bytes of one JSON object; a single frame is capped at `MaxFrameSize` (128 MB), a constant the host and every SDK share. The host binds the listening socket and passes the path via `PIG_EXT_SOCKET` (or `PIG_EXT_SOCKET_<EXT_NAME>` for each contained extension in a packed cell). Every envelope carries a `type`, an optional `id` for request/response correlation, and a body field named after the type:

```json
{ "type": "request", "id": "r-7", "request": { ... } }
```

So a `register` envelope nests its body under `register`, a `ready` under `ready`, a `call` under `call`, and so on. You do not frame or parse this yourself when using an SDK; the SDK owns the length prefix and the envelope shape.

### Message types

| Direction | Type | Purpose |
|---|---|---|
| ext → host | `register` | Initial handshake declaring capabilities. |
| host → ext | `ready` | Acks register, ships session state and host context. |
| host → ext | `request` | Tool call, command invocation, event, or render. Expects `response`. |
| host → ext | `notify` | Fire-and-forget (e.g. lifecycle hints). |
| host → ext | `cancel` | Cancel an in-flight `request` by ID. |
| ext → host | `response` | Reply to a `request`. |
| ext → host | `call` | Extension invokes a host method. Host replies with `call_result`. |
| host → ext | `call_result` | Reply to an extension `call`. |
| ext → host | `widget_push` | Push pre-rendered widget lines to the host's widget cache. |
| host → ext | `shutdown` | Graceful shutdown. Extension flushes pending IO and exits. |

Cancellation is cooperative. The host emits a `cancel` envelope when the user aborts, when a new turn starts, or on shutdown; the extension is expected to short-circuit the corresponding handler and reply with a typed cancelled response.

A host `request` is awaited: the Go SDK already executes its handler on a dedicated goroutine and sends `response` only after the handler returns. Keep awaited TypeScript work inside that handler and return its error. Do not start another goroutine and return early unless the upstream operation is deliberately unawaited and the extension owns an independent cancellation/error/shutdown lifecycle. The request-scoped `Context` is cancelled when the handler returns. See [Extensions: TypeScript async](extensions.md#typescript-async-is-a-contract-not-a-goroutine-instruction).

### Register payload (sketch)

```json
{
  "name": "my-extension",
  "tools":     [{ "name": "x", "description": "...", "parameters": {...JSON schema...} }],
  "commands":  [{ "name": "x", "description": "..." }],
  "shortcuts": [{ "key": "ctrl+shift+x", "description": "..." }],
  "flags":     [{ "name": "verbose", "type": "boolean", "default": false }],
  "providers": [{ "name": "my-provider", "config": { ... } }],
  "renderers": [{ "customType": "my.entry" }],
  "events":    ["session_start", "session_shutdown", "session_info_changed", "tool_result", "my.custom"]
}
```

The host rejects empty names, duplicate names within any list, schemas that do not parse as JSON, and provider configs that are not JSON-serializable.

## Host API (extension → host)

These are the call names the host accepts. SDK bindings wrap each into a method on the per-call `Context`. Cancellation is propagated automatically - when a handler's `Context` is cancelled, all in-flight host calls made by that handler are also cancelled.

### Context introspection

- `getFlag(name)` - value of a host or extension flag.
- `getActiveTools()` - names of tools currently active for the model.
- `getAllTools()` - every tool name + description known to the host.
- `getCommands()` - slash commands available to the user.
- `getThinkingLevel()` - `"none" | "low" | "medium" | "high" | "xhigh"`.
- `setThinkingLevel(level)` - switch reasoning effort if the active model supports it.
- `getContextUsage()` - `{ tokens, max, percent }`.
- `getSystemPrompt()` - current system prompt text.
- `getSessionName()` / `setSessionName(name)` - read or change the display name. Name changes emit `session_info_changed` to extensions that registered that event.
- `getSessionID()`, `getSessionFile()` - session JSONL on disk.
- `getLeafID()`, `getBranch()`, `getEntries()` - tree navigation primitives.

### Session control

- `sendMessage(customMessageRef, options)` - inject a structured message into the session.
- `sendUserMessage(content, options)` - synthesize a user turn. `deliverAs` is `"steer" | "followUp" | "nextTurn"`.
- `appendEntry(customType, data)` - append a custom JSONL entry without invoking the model.
- `setLabel(entryID, label)` - set a small display label on an existing entry.
- `setActiveTools(tools)` - pin the tool set for the next model turn.
- `refreshTools()` - re-resolve enabled tools from settings + extensions.
- `setModel(spec)` - switch the active model. `spec` is `provider/model` (preferred) or bare model ID (legacy).
- `getModelAuth(provider, model)` - read credentials/headers the host would send.
- `complete(model, prompt, options)` - request a one-shot completion through the host's provider stack (typed unsupported error if not wired in this build).
- `isIdle()`, `hasPendingMessages()`, `waitForIdle()`, `abort()`, `shutdown()`, `reload()`, `compact(options)`.
- `newSession(opts)`, `fork(entryID, opts)`, `navigateTree(targetID, opts)`, `switchSession(path, opts)` - return `{cancelled: bool}` so the extension can detect user dismissal.

### UI bridge

- `ui.notify(message, level)` - toast in the footer. `level` is `info | warn | error`.
- `ui.setStatus(key, text)` - pin a small status line keyed by `key`. Empty text clears.
- `ui.setLogin(definition)` - validate and install a native login in the shared header slot.
- `ui.setWorkingMessage(message)`, `ui.setWorkingVisible(bool)`, `ui.setWorkingIndicator(options)` - control the bordered loader.
- `ui.setHiddenThinkingLabel(label)` - label shown when reasoning is hidden.
- `ui.setTitle(title)` - terminal window title.
- `ui.select(title, options)` - modal single-choice picker.
- `ui.confirm(title, message)` - yes/no dialog.
- `ui.input(title, placeholder)` - single-line input.
- `ui.editor(title, prefill)` - multi-line editor.
- `getEditorText()` / `setEditorText(text)` / `pasteToEditor(text)`.

All UI calls return after the user dismisses the dialog; pair them with `Context.Done()` if you want responsiveness to cancellation.

### Focused custom components

`ui.custom` runs a component in the extension process while Pig owns the focus
shell. The extension sends replaceable line snapshots. Pig sends ordered input
events only while that overlay owns terminal focus.

Go components implement `RemoteComponent`. Implement
`RemoteComponentInvalidator` when a timer changes component state. Rust
components implement `set_invalidate` on `RemoteComponent`. Python components
can implement `set_invalidate(callback)`. Node component factories call the
supplied `tui.requestRender()` function.

Call the invalidation callback after state changes. Pig coalesces timer requests
to one pending frame and limits timer rendering to the 16 ms TUI interval.
Input-driven rendering remains immediate. The TUI render loop reads only the
latest host-side cache and performs no extension IPC.

One extension dialog or overlay owns terminal focus at a time. Waiting calls remain off
the TUI loop and respond to cancellation. The SDK detaches invalidation before
it calls `dispose`. A component callback must return promptly. A callback that
does not stop by the cleanup deadline produces an error and is not disposed
concurrently.

### Widgets, headers, footers

- `setWidget(key, content, options)` - show a widget above the editor. `content` may be string, lines, or a factory.
- `setHeader(lines)`, `setFooter(lines)` - install custom layout lines.
- `widget_push` (envelope, not a call) - push pre-rendered lines from the extension into the host cache, keyed by `widget` + `frameID`. Use for animated or expensive widgets. Include `width`, the terminal width the lines were rendered at; the Node runtime sets it. The host never paints a frame rendered for another width, and the extension re-renders on `width_change`. `ui.setHeader`/`ui.setFooter` lines take the same optional `width`.

> **Fit header and footer lines to `width()` yourself.**
>
> Upstream Pi takes a component factory here, so its `render(width)` is called on
> every frame and always sees the current width. A Pig extension is a subprocess
> and cannot hand over a live component, so it sends **static strings** instead.
> Nothing downstream re-measures them.
>
> As in Pi, a line wider than the terminal is not corrected by the host. When it
> reaches a differential render, Pig writes `pig-tui-crash.log`, restores the
> terminal, and exits with `Rendered line N exceeds terminal width`, exactly as
> Pi does for a component that does not truncate its output.
>
> Build the line as segments ordered most to least valuable, then keep only the
> ones that fit:
>
> ```go
> width := ctx.Width() // 0 until the host reports a size; keep everything then
> line := fitSegments([]string{
>     cwd,                    // always keep
>     "Context: " + pct,
>     "Model: " + model,
>     "Think: " + level,      // first to go in a narrow pane
> }, "  │ ", width)
> ```
>
> Measure **display columns, not bytes**: `len()` counts SGR escapes, so a styled
> segment looks far wider than it renders and the line collapses to one field.
> Strip escapes before measuring, and count East Asian Wide and Fullwidth runes as
> two columns.
>
> `width()` is current at the moment you build the line. There is no width-change
> callback, so a footer pushed before a resize stays sized for the old width until
> something else triggers a re-push; re-push on an event you already handle
> (`turn_end` is usually enough).

### Native login

`ui.setLogin` accepts one strict current unversioned definition. It contains
`brand`, `hero`, `mascot`, `palette`, `name`, `description`, and `tagline`.
The grids have exact dimensions:

| Grid | Width | Height |
|---|---:|---:|
| `brand` | 41 | 5 |
| `hero` | 32 | 14 |
| `mascot` | 16 | 14 |

Use ASCII symbols in each grid. `.` means transparent. Map each other symbol to
one `#RRGGBB` palette color. Palette keys must be one printable non-space ASCII
character. The palette can contain at most 32 colors, and each color must be
used.

Pig rejects unknown or duplicate fields, wrong grid sizes, non-ASCII grid data,
missing or unused symbols, invalid colors, and invalid text. All text must be
non-empty valid UTF-8 without control or line-separator characters. The display
width limits are 24 columns for `name`, 48 for `description`, and 76 for
`tagline`. Name plus description and spacing are limited to 80 columns.

`setLogin` and `setHeader` share one slot. The last successful call wins. Clear
the header to restore Stock Pig's text header. A rejected definition returns a host
error and keeps the current header. Quiet startup keeps the header hidden.
Terminals below 66 columns show compact text without art. Pig caches validated
render data, so rendering performs no extension IPC.

Minimal calls:

```go
err := ctx.SetLogin(sdk.LoginDefinition{Brand: brand, Hero: hero, Mascot: mascot, Palette: palette, Name: "Example Bot", Description: "Custom coding agent", Tagline: "Build with care."})
```

```rust
ctx.set_login(&LoginDefinition { brand, hero, mascot, palette, name: "Example Bot".into(), description: "Custom coding agent".into(), tagline: "Build with care.".into() })?;
```

```python
ctx.set_login(LoginDefinition(brand=brand, hero=hero, mascot=mascot, palette=palette, name="Example Bot", description="Custom coding agent", tagline="Build with care."))
```

```js
await ctx.ui.setLogin({ brand, hero, mascot, palette, name: "Example Bot", description: "Custom coding agent", tagline: "Build with care." });
```

Preview one extension with `pig extension preview-login <path>`. The command
fails for zero or multiple resolved extensions, a failed `session_start`, a
missing login, or an invalid definition. Use `/reload` after an edit. A login
remains an extension Resource. Activate it through a Piglet `extensions` entry,
user extension policy, or `pig -e <path>`. Verify activation by starting or
reloading through that path and checking the identity name. Keep product
identities outside Stock Pig and select them through an ordinary Piglet
extension entry.

### Themes

- `getAllThemes()` - list of `ThemeMeta`.
- `getTheme(name)` - full theme object.
- `setTheme(name)` - applies and returns `(applied, message)`.

### Autocomplete, terminal input, editor component

- `addAutocompleteProvider(factory)` - install a custom completion source for the editor.
- `onTerminalInput(handler)` - receive raw terminal input before normal handling; return `{ consume: true }` to swallow a chunk or `{ data }` to replace it; returns an unsubscribe. Handlers see chunks in the order they were typed. Input typed after a chunk, including Ctrl+C, waits for your verdict, so answer promptly. A listener can consume or rewrite Ctrl+C before normal handling. PiG waits off the TUI loop and cancels the request when the mode shuts down.
- `setEditorComponent(factory)` / `getEditorComponent()` - replace the editor widget.
- `setToolsExpanded(bool)` / `getToolsExpanded()`.

### Process & filesystem

- `exec(command, args, options)` - run a command with stdin/stdout/stderr piped back. Subject to permissions.
- `configHome()` - pig's config root (`~/.pig` or `PIG_HOME`).
- `cwd()` - working directory of the host session.
- `width()` - terminal columns visible to the host.
- `model()` - current model identifier.
- `toolCallID()` - only meaningful inside a tool handler.

### Providers

Extensions may register additional inference providers. The registration payload is opaque to the host except for the name. Providers registered by an extension are torn down when the extension shuts down, fails to register on reload, or its quarantined packed cell fissions. Names registered by a replacement extension at reload are preserved across the swap.

### Cancellation contract

- `Context.Done()` closes when the host sends `cancel` for that request, when the parent session shuts down, or when the extension's connection drops.
- `Context.Err()` reports `cancelled`, `deadline`, or `closed`.
- Handlers should periodically check `Done()` between expensive steps. Host calls made from a cancelled context return a typed cancellation error promptly.

## Unsupported / typed-error APIs

Any host call exposed by an SDK that is not wired in a given build returns a typed `unsupported` error rather than silently no-oping. Coverage is tracked in `docs/extension-api-parity.md` in the pig source tree; this file is a snapshot.

## SDK quickstart (Go)

```go
package myextension

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

func Extension() *sdk.Extension {
    e := sdk.New("my-extension")
    e.Tool("hello", "Say hello", sdk.Schema{
        Type: "object",
        Properties: map[string]sdk.Schema{
            "name": {Type: "string"},
        },
    }, func(ctx sdk.Context, args map[string]any) (any, error) {
        return "hello " + args["name"].(string), nil
    })
    e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) error {
        ctx.Notify("ready", "info")
        return nil
    })
    return e
}

```

The Go SDK is a nested module. `pig extension init ./my-extension` creates a
factory and a portable `go.mod`. Pig stages the SDK under
`<config-root>/state/pigsdk/sdk`. Pig adds a temporary filesystem replacement
during the build. The authored file contains no machine-specific path.

Build PiG and Go extensions with Go 1.27.1. The generated module keeps Go 1.26
as its language floor:

```go.mod
module example.com/my-extension

go 1.26

require github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0

```

Run `pig reload --sdk-path` to print that directory. See `extensions.md` for the full quickstart.

## SDK quickstart (Python)

```python
from pig_sdk import Extension, Context

def new_extension() -> Extension:
    ext = Extension("my-extension")

    @ext.tool("hello", "Say hello",
              {"type": "object", "properties": {"name": {"type": "string"}}})
    def hello(ctx: Context, args: dict):
        return f"hello {args['name']}"

    @ext.on_session_start
    def started(ctx: Context, _data):
        ctx.notify("ready", "info")

    return ext

```

## SDK quickstart (Rust)

```rust
use pig_sdk::{Extension, Context, Schema};

pub fn new_extension() -> Extension {
    let mut ext = Extension::new("my-extension");
    ext.tool("hello", "Say hello", Schema::object_with(&[("name", Schema::string())]),
        |ctx: Context, args| {
            let name = args.get("name").and_then(|v| v.as_str()).unwrap_or("");
            Ok(serde_json::json!(format!("hello {name}")))
        });
    ext
}

```
