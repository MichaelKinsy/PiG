# Extensions

Extensions add executable behavior to PiG. They can register tools, commands, event handlers, providers, renderers, and interactive UI.

PiG preserves the Pi extension model while using a process-oriented host that supports Go, Rust, Python, and Node. A compatible Go extension can also be compiled into a Piglet Binary.

## Why PiG uses this design

Upstream Pi loads TypeScript extensions in process. PiG is a Go-native implementation and does not embed a JavaScript runtime or load dynamic Go plugins.

PiG instead uses one public extension contract with several execution realizations:

- isolated subprocess;
- packed subprocess cell;
- fused Go factory in a Piglet Binary.

This design has four goals:

1. Preserve observable Pi extension behavior.
2. Support several implementation languages.
3. Isolate source extension failures where practical.
4. Allow reviewed Go extensions to become part of one native agent executable.

Execution placement must not change extension behavior. An extension uses the same registration and host-call contract in source, packed, and fused modes.

## Known cross-process gaps

The owner approves three visible known gaps for 0.3.x on 2026-09-28: the remaining SDK Provider surface and foreign configuration identity (D78), partial-message observation (D82), and event-bus scheduling boundaries across processes (D83, narrowed 2026-09-29). These are not claims of parity or general lifecycle waivers. See [Providers](/docs/latest/providers) and [RPC mode](/docs/latest/rpc).

Every Node extension shares one `pi.events` bus, including explicitly isolated extensions and exact standalones. A listener in another process receives the emitter's original payload by reference: synchronous mutations, reentrant emits, retained and post-await aliases, and callbacks behave as in Pi. D83 names what differs across processes: independent tasks in different processes are ordered by timing, only a listener's first post-await continuation is ordered with the emitter's microtasks, reference cycles that cross processes are released when a process exits, and brand checks such as `util.types.isMap` fail on a foreign object, where `util.types.isProxy` also returns true. `util.inspect` with `customInspect: false` prints an empty placeholder object instead of a foreign payload, `util.inspect` with `showProxy: true` shows the Proxy wrapper, and on Node 26.0.0 and later `util.inspect` of a non-extensible foreign payload prints it inside `Proxy(...)`, because V8 offers no hook. Node 25 and earlier print that payload as Pi does. A listener that never returns blocks its emitter, as in Pi.

Same-process configuration roots, children, callbacks and receivers must also remain Pi-exact. A foreign configuration snapshot is not a live author-held object (D78). A retained remote partial message does not advance with its producer (D82). Event order, cancellation, final results, persistence and cleanup remain required. The strict RPC33 comparison retains its raw failure.

## Extension responsibilities

An extension owns one executable capability or a cohesive group of capabilities. It can contribute:

- model-callable tools;
- slash commands;
- lifecycle handlers;
- provider and OAuth declarations;
- message and entry renderers;
- status, widget, header, login, and focused UI;
- extension-owned namespaced state.

An extension does not select itself. Settings, a Package, an explicit `-e` path, or a Piglet selects it.

## Supported source forms

PiG accepts one conventional factory or one exact standalone source.

| Language | Factory | Standalone |
|---|---|---|
| Go | `func Extension() *sdk.Extension` | Executable `package main` module |
| Rust | `pub fn new_extension() -> Extension` in `src/lib.rs` | Crate with `src/main.rs` |
| Python | `def new_extension() -> Extension` in one module | Executable `main.py` with a shebang |
| Node | Pi-compatible default export | Executable script with a shebang |
| Native | Not applicable | Exact executable file |

PiG rejects mixed languages, multiple factories, factory and standalone combinations, nonstandard factory symbols, missing entry points, and identity mismatches.

## Create an extension

Prefer Go for an extension that may become part of a native Piglet Binary:

```bash
pig extension init ./review --lang go
```

Create an isolated standalone only when the capability needs its own executable boundary:

```bash
pig extension init ./review --lang go --isolated
```

The scaffold does not write an extension manifest. Runtime registration is the source of extension identity and capabilities.

## Validate without installing

```bash
pig install ./review --validate-only --json
```

Validation:

- resolves the source form;
- builds or starts the extension;
- checks runtime registration;
- verifies identity;
- detects duplicate capabilities;
- reports placement;
- changes no Package settings.

Validate a set to find conflicts before using it in a Piglet:

```bash
pig install --validate-only --set ./review,./policy --json
```

## Select an extension

Use an explicit path for a temporary source run:

```bash
pig -e ./review
```

Use a Piglet for an agent application:

```yaml
extensions:
  - name: review
    origins: [local:./extensions/review]
```

Use a Package when you need to distribute the extension to other applications. Installing the Package does not activate a Piglet.

## MCP servers, virtual models and tool exposure

An extension in any supported language can register MCP servers (`registerMcpServer`), virtual models (`registerVirtualModel`) and tools with `exposure`, `namespace`, `annotations`, `outputSchema`, `defaultActive` and `prepareLoadout`, and a tool can call other tools through `ctx.executeTool()`. See [MCP servers](mcp.md#extensions), [virtual models](virtual-models.md) and [codemode](codemode.md).

`namespace: { name, description, instructions }` groups related tools, as MCP servers do. Codemode lists a namespace under one heading with its `description`. `instructions` holds longer usage guidance, such as MCP server instructions. It is not listed, and codemode scripts read it with `describeNamespace(name)`. A server config given to `registerMcpServer` accepts the same `description` as an `mcpServers` entry in `mcp.json`.

## Markdown transformers

Register a display-only Markdown transformer with `pi.registerMarkdownTransformer` in Node, `MarkdownTransformer` in Go, or `markdown_transformer` in Rust and Python. Transformers receive the message type, streaming state, and current content width. They change the displayed user, assistant, and visible-thinking text, not the stored message or provider input. A string replaces the current Markdown, including an empty string. Node Promise results and other non-string results are ignored, as in Pi.

PiG runs the complete chain off the render loop through its multi-language bridge (D19). Normal updates preserve render-admission order across components. A replacement discards obsolete output without ending an admitted callback's wait early. Equal-text content updates rerun stateful transformers. Removing a message removes its queued work and prevents late publication.

Renderer inactivity, disconnect, and shutdown retain the subprocess isolation boundary (D56). Cancellation of a host request does not prove that the extension callback body has returned. Keep synchronous transformers short and do not depend on side effects from an abandoned callback.

## Final tool-result content

Return an ordered `content` array when a tool produces text and images. Each text block uses `{ "type": "text", "text": "..." }`. Each image block uses `{ "type": "image", "data": "...", "mimeType": "image/png" }`, where `data` contains base64-encoded image bytes.

PiG preserves the blocks and their order in final tool events, provider context, Session history, and custom-renderer input. An empty text block remains distinct from `content: []`. Text signatures remain attached to their text blocks.

After `tool_result` handlers run, PiG normalizes images before storing the result. A conversion or resize note appears immediately after the affected image. Disabling `images.autoResize` prevents resizing but still permits conversion of unsupported formats, such as BMP, to PNG. If image processing fails, PiG retains the original image block instead of dropping tool output.

## Subprocess realization

Source extensions normally run through PiG's subprocess host.

### Advantages

- Supports Go, Rust, Python, Node, and native executables.
- Separates many crashes and exits from the PiG process.
- Provides explicit heartbeat and cancellation handling.
- Supports source development and reload.
- Avoids unsafe dynamic native plugin loading.

### Costs

- Adds process startup and IPC overhead.
- Requires serialization at the process boundary.
- Can require a language runtime or compiler on first source build.
- Requires explicit lifecycle, timeout, and connection handling.
- Cannot transfer live Go or TypeScript object closures across the wire.

## Packed subprocess cells

Compatible factories in one language can share a generated runtime process. Each extension keeps its own logical connection and registration.

Packing reduces process count. It does not create a shared extension identity. Copies selected from distinct folders retain their original registration names and separate host identities. Go copies with the same module path build in separate cells so each runs its own source (D20). Tool and flag conflicts are reported after loading, as in Pi.

Cold native builds print `Building extensions...` only in interactive mode with a terminal stderr (D19). Cached builds, Node loads, print, JSON, and RPC do not print this notice.

A stalled logical member is isolated from healthy members where the process remains healthy. If the shared process dies, PiG quarantines the affected cell. The next plan can separate failed members from healthy members.

## Fused Go realization

A Piglet Binary can compile a compatible Go factory into the PiG executable.

### Advantages

- Produces one target-native executable.
- Removes target-side Go build requirements.
- Avoids extension subprocess startup.
- Keeps exact extension source in the Binary build closure.

### Costs

- Requires a Binary rebuild for every extension change.
- Shares PiG's process failure boundary.
- Requires a target-specific build.
- Supports compatible Go factories only.
- Needs stricter panic, deadlock, and resource-lifetime review.

Fusion is a delivery optimization. Fused factories use the same registration and host-call semantics as subprocess factories.

## Session compaction events

Register `session_before_compact` to inspect or replace compaction work. The
event contains the preparation, branch entries, custom instructions, reason,
retry state, and cancellation context. Return `cancel: true` to cancel the
operation. Return `compaction` to provide the result without a model call.

PiG persists a successful result before it emits `session_compact`. The event
contains the persisted compaction entry, reason, retry state, and
`fromExtension` value. Isolated, packed, and fused extensions receive the same
events.

PiG emits `session_compact_failed` after a compaction fails or is aborted, once
the `compaction_end` listeners have returned. The event contains the reason,
`errorMessage` (absent when the compaction was aborted), `aborted`, retry state,
and `fromExtension` value.

PiG Standard requires every selected extension to fuse:

```yaml
build:
  extensionRealization: fused
```

A Standard Binary build fails if any selected extension would use a subprocess.

## Connection and liveness model

PiG cannot guarantee that an extension connection never fails. An extension process can crash, be killed, deadlock, exhaust a resource, or lose its transport.

PiG instead provides a bounded failure contract:

1. Detect transport or heartbeat failure.
2. Cancel pending calls owned by the failed connection.
3. Close UI and renderer state owned by that connection.
4. Isolate or quarantine the affected member or cell.
5. Preserve unrelated healthy extensions where possible.
6. Keep the previous working extension set when a replacement fails validation.
7. Permit an explicit reload or supervised restart when safe.

A heartbeat proves that the transport dispatcher is alive. It does not prove that an extension handler is making progress.

Tools, commands, events, and shortcuts can perform long operations until they
finish or are cancelled. Renderers use generation-scoped inactivity and retain
the last valid frame when a new generation stalls.

## Custom messages

`pi.sendMessage(message, options)` uses the current Session in print, JSON, and RPC as well as interactive mode. An idle message with `triggerTurn: false` is persisted without starting a model turn. RPC emits its `message_start` and `message_end` before acknowledging the command that sent it. `deliverAs: "nextTurn"` defers delivery until a later prompt. Preserve omission of `triggerTurn` separately from explicit false when a run is active. The Session owns triggered work and shutdown; the bridge does not create another run owner.

## Session context

PiG loads session history for an extension only when the extension reads it. A
persisted session is read from its local JSONL file and reconciled with the host
cursor. If the file is unavailable, the host sends ordered pages of at most 4
MB. Later appends use the same bounded path.

## Why PiG does not replay interrupted operations

If a connection fails during a tool call or command, that operation fails. PiG does not automatically replay it.

A replay could repeat:

- a file mutation;
- a shell command;
- a network request;
- a user message;
- an external transaction.

PiG cannot infer that these effects are idempotent. The caller must decide whether retry is safe.

An extension must persist durable state before it needs that state after restart. PiG cannot recover state that existed only in the failed process memory.

## Reload behavior

`/reload` uses the same normalized extension inputs and first-seen order as startup.

PiG:

1. resolves the requested extension set;
2. computes content identity and reuses valid build artifacts;
3. plans runtime cells;
4. constructs a fresh factory and runtime for every configured, embedded, fused, and built-in extension;
5. starts and validates replacements;
6. verifies registration;
7. publishes the successful set;
8. shuts down the old set.

A failed extension is removed and reported while other extensions load.

If a Node factory throws or rejects, its captured `pi` API becomes inactive. Later registration, action, getter, and event-bus calls throw `Extension "<path>" failed to load and its API is no longer active.` synchronously, even when the method normally returns a Promise. Event-bus subscriptions made by that factory are removed. Healthy extensions keep their APIs.

Node extensions keep Jiti's compiled-source cache under `<config-root>/cache/jiti` (D20). The loader checks source contents on each load and separates compiler and runtime versions, so a changed source is compiled again. Changing the temporary directory does not discard this cache. The cache does not retain evaluated factories between loads. `JITI_FS_CACHE=false` disables it. The transform cache is disposable and is separate from the published artifacts managed by `pig extensions cache prune`.

Node also keeps native bytecode under `<config-root>/cache/node-compile` when a virtual SDK library is imported (D20). Node validates source bytes and VM compatibility before reuse. This cache contains compiled code, not evaluated factories or extension state. `NODE_DISABLE_COMPILE_CACHE=1` disables it independently of Jiti. Existing Node cache configuration remains authoritative. Like the Jiti cache, it is disposable and is not managed by `pig extensions cache prune`.

## Session replacement

`/new`, `/resume`, `/fork`, `/clone`, `ctx.newSession()`, `ctx.fork()`, `ctx.switchSession()` and the RPC commands `new_session`, `switch_session`, `fork` and `clone` build the replacement Session's extensions from scratch: a new runner, new extension processes and a new `session_start`. PiG starts a fresh process where Pi calls the extension factory again inside the same Node process, so module-level state and process-wide state such as `process.env` do not carry over to the next Session (D70). The extension that requested the replacement keeps its process until its command handler returns. A `ctx` or `pi` that an extension captured before the replacement still answers local getters with the old Session's values, and its host calls are rejected with Pi's stale message without throwing at the call site: the SDK returns or raises an error or writes it to stderr, depending on the language (D30).

## Cancellation

A handler receives request cancellation through its language SDK.

In Go, use `sdk.Context.Done()` and return the cancellation error from owned work. Do not detach a goroutine from the handler unless the upstream operation is intentionally unawaited and another lifecycle owns it.

An awaited handler must return only after its work finishes. Returning early changes completion ordering and loses errors.

Python custom-provider callbacks and the SDK's event forwarding share the request Context's cancellation signal. Check `ctx.is_cancelled()` while doing provider work. Cancellation before forwarding, during iteration, or after the iterator ends produces an aborted-operation error instead of forwarding further events.

Interactive shutdown cancels and joins its input reader and decoder before stopping the renderer or restoring the terminal. Temporary dialogs keep the shared input owner alive. External-editor handoffs pause the terminal reader without closing stdin, so the next owner can receive unread input.

## Retained callback contexts

A callback may retain its extension Context after its originating command or event handler finishes normally. Later host calls use the same live extension connection, not the completed request. This applies to retained terminal-input callbacks that read current editor text and tool-expansion state, including empty/reset values. Editor text includes the full contents of compact pastes, not their `[paste #…]` markers. Node callbacks receive the same owner-published expanded text that host-query SDKs read.

Keep cancellation and connection retirement separate from normal completion. A cancelled originating request does not become a new runtime-owned request. Calls through a closed or replaced connection fail; they do not move to the latest handler or a replacement connection. Obtain a fresh Context after reload or Session replacement. Await work that belongs to the current handler instead of detaching it and relying on a retained Context to keep it alive.

These lifetime rules belong to the multi-language bridge (D19). They do not replay interrupted operations or change the renderer inactivity policy (D56).

## Terminal strings

Terminal listeners receive JavaScript UTF-16 strings. A printable character outside the BMP can arrive as separate high- and low-surrogate chunks, with an unmatched unit visible in the editor between them. Do not join chunks before returning a verdict. The wire uses valid JSON `\u` escapes for unmatched units.

Go SDK strings use the native editor's WTF-8 convention for unmatched units. Use `github.com/MichaelKinsy/PiG/extensions/sdk/json` instead of `encoding/json` when serializing callback text yourself. Node and Python strings retain these units naturally. Rust terminal and focused-component input callbacks receive `&JsString`, verdict `data` is `Option<JsString>`, and `get_editor_text` returns `JsString`. Use `as_units()` for lossless access, `from_units(...)` to construct a string, and `to_string_lossy()` only for display. Rust editor setters accept ordinary strings or `JsString`.

## Registration inputs

Every tool must supply an object parameter schema, including a tool with no arguments. Use an empty object schema rather than omitting the schema or passing null. Invalid schemas fail before registration changes; a later valid declaration does not hide an earlier invalid one.

Flag values preserve false and empty strings. Clearing an override restores its registered default instead of keeping a stale value in an SDK snapshot.

Provider configuration is validated before it replaces an existing registry entry. Supply `api` with a `streamSimple` callback and supply an API at the provider or model level for each new model. A failed registration does not remove the previous provider. See [Provider extensions](/docs/latest/providers#provider-extensions) for binding and removal behavior.

## Focused UI

A subprocess extension can open focused custom UI. The extension renders local line snapshots. PiG owns the focus shell and sends ordered input back to the component.

One terminal gives focus to one extension dialog or overlay at a time. Other interactive calls wait outside the TUI loop. Cancellation removes a waiting call before it takes focus.

Timer-driven render requests are available in Go, Rust, Python, and Node. PiG coalesces repeated requests, limits them to the 16 ms TUI frame interval, and sends only changed snapshots. Input-driven frames remain immediate.

The host rejects stale-width, out-of-order, and late-generation frames. The SDK detaches timer invalidation before it disposes the component. Completion, cancellation, reload, disconnect, and shutdown stop component work. A callback that does not return causes a bounded cleanup error instead of concurrent disposal.

PiG Standard includes PiG Runner as an ordinary Go extension. `/runner` and `/pig-runner` use the same public custom-component contract as any other extension. A Standard Binary fuses the extension without a private Stock PiG launcher.

## Extension-owned state

Store product or extension state below a namespaced path:

```text
~/.pig/state/<extension-id>/
```

Do not add product state fields to Stock PiG settings.

PiG Standard's login extension follows this rule for sprite selection.

## Product boundary

Keep product behavior outside Stock PiG. A product extension can provide branding, onboarding, authentication, marketplace access, or workflow commands through public extension and Piglet contracts.

Do not add a product-specific launcher, startup branch, or hidden registration to `cmd/pig`.

## Security model

Extensions run with the permissions of the PiG process unless an operating-system boundary limits them. Process separation improves failure isolation but is not a security sandbox.

Before you install an extension:

- review its source and dependencies;
- verify its origin and digest;
- understand its required files, commands, network access, and secrets;
- use a container or virtual machine when you need a security boundary.

## Breaking changes / Go SDK migration to 0.3.0

Pi 1.0.0 represents unknown context usage as null, not zero. Go therefore keeps `ContextUsage.Tokens` as `*int` and `Percent` as `*float64`. `GetContextUsage()` returns nil when there is no usable model window, and a host failure as an error in every SDK (Go `error`, Rust `io::Result`, Python `HostCallError`). After compaction, a non-nil usage can contain nil counts. Rust uses `Option`; Python uses `None` for the same states.

Before:

```go
if usage != nil && usage.Tokens > 0 {
    fmt.Printf("%d tokens (%.1f%%)", usage.Tokens, usage.Percent)
}
sdk.SendMessageOptions{TriggerTurn: true}
```

After:

```go
if usage != nil && usage.Tokens != nil && usage.Percent != nil {
    fmt.Printf("%d tokens (%.1f%%)", *usage.Tokens, *usage.Percent)
}
sdk.SendMessageOptions{TriggerTurn: sdk.Bool(true)}
```

Use `usage.TokensOr(0)` or `usage.PercentOr(0)` only when the caller intentionally selects zero for unknown usage. Check `usage != nil` first. The helpers do not change the nullable fields. `sdk.Bool(false)` explicitly selects false; nil leaves an option unset. For `TriggerTurn`, unset steers during an active turn while false defers the custom message. Do not replace omitted options with false automatically. These helpers are Go ergonomics for the shared SDK contract (D19).

### Host-backed getters report failures and Pi's undefined

Pi's getters throw when the host call fails and return `undefined` for absent state. Every SDK now does the same: a failure is an error, never an empty value, an assumed default or a cached name. Pi's `undefined` is a nil pointer in Go, `Option::None` in Rust and `None` in Python.

Go `Context` methods (each returns the error last):

| Method | Before | After |
|---|---|---|
| `GetEditorText` | `string` | `(string, error)` |
| `GetSessionName` | `string` | `(*string, error)`, nil when unnamed |
| `GetSessionID` | `string` | `(string, error)` |
| `GetSessionFile` | `string` | `(*string, error)`, nil for an in-memory session |
| `GetLeafID` | `string` | `(*string, error)`, nil for an empty session |
| `GetFlag` | `any` | `(any, error)`; a failure no longer answers the registered default |
| `GetThinkingLevel`, `GetSystemPrompt`, `GetToolsExpanded` | value | `(value, error)` |
| `GetActiveTools`, `GetAllTools`, `GetCommands`, `GetAllThemes` | slice | `(slice, error)` |
| `GetSystemPromptOptions` | `SystemPromptOptions` | `(SystemPromptOptions, error)`; collections are always present |
| `GetContextUsage`, `GetModelInfo` | pointer | `(pointer, error)`, nil when absent |
| `GetModelAuth` | `json.RawMessage` | `(json.RawMessage, error)` |
| `IsIdle`, `HasPendingMessages`, `IsProjectTrusted` | `bool` | `(bool, error)`; no assumed idle or trusted state |
| `GetBranch`, `GetEntries` | slice | `(slice, error)`; a failed session-log subscription is returned on every read |

Rust `Context` methods return `io::Result<T>` (`io::Result<Option<T>>` for `get_session_name`, `get_session_file`, `get_leaf_id`, `get_flag`, `get_context_usage` and `get_model_info`); `get_branch` and `get_entries` return `io::Result<Vec<_>>`. Python methods raise `HostCallError`; `get_session_name`, `get_session_file` and `get_leaf_id` return `None` when absent, and `get_branch` and `get_entries` raise the subscription failure.

### Timeouts are JavaScript numbers

Pi's exec and dialog `timeout` is a number, so fractional and very large values are meaningful. Go `ExecOptions.Timeout` and `DialogOptions.Timeout` change from `int` to `float64` (an `int` variable no longer compiles; convert it). Rust `ExecOptions.timeout` and `DialogOptions.timeout` change from `Option<u64>` to `Option<f64>`, and both types lose `Eq`. Python accepts a float. Exec timeouts and interactive dialog countdowns start only for a positive value, as in Pi. In RPC mode a dialog arms Node's timer for any non-zero value, negative included (rpc-mode.ts), and a timed-out dialog resolves like a cancelled one: `undefined` for select and input, `false` for confirm.

### System prompt options

`getSystemPromptOptions` returns Pi's collection-complete shape: `selectedTools`, `toolSnippets`, `toolGuidelines`, `promptGuidelines`, `appendSystemPrompt`, `sections`, `contextFiles` and `skills` are always present, empty when unset. Go `SystemPromptOptions` gains `ToolGuidelines`.

Legacy `github.com/mainstai/pig/extensions/sdk` imports remain buildable in factories and exact standalones. PiG creates a private alias of the current SDK, including its subpackages and self-imports, without editing authored files (D19). Use the current `github.com/MichaelKinsy/PiG/extensions/sdk` path for new or fused extensions.

## SDKs

PiG ships subprocess SDK bridges for:

- Go: `extensions/sdk`;
- Rust: `extensions/sdk-rs`;
- Python: `extensions/sdk-py`.

Node extensions use Pi-compatible exports through the Node runtime bridge. The
`extensions/sdk-ts` package re-exports the pinned Pi types and declares PiG-only
calls. It contains no runtime implementation.

All runtime bridges target the same current extension behavior. A new capability is incomplete until each supported runtime exposes equivalent behavior and the conformance suite can detect a broken implementation.

## Related documentation

- [Piglets](/docs/latest/piglets)
- [Piglet Binaries](/docs/latest/piglet-binaries)
- [Packages](/docs/latest/packages)
- [Derivative harnesses](/docs/latest/derivative-harnesses)
- [Upstream Pi extensions](https://pi.dev/docs/latest/extensions)
