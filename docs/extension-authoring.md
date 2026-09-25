# Pig extension authoring

Pig extensions target the upstream pi extension API through the subprocess
bridge. The language-specific SDKs are bridges into the same protocol, not
separate APIs.

Read these files before adding or changing extension behavior:

1. `docs/extension-api-parity.md`: parity boundary and SDK coverage matrix.
2. `DIVERGENCES.md`: numbered intentional differences from upstream pi.
3. `coding/extension/host/subprocess/protocol.go`: canonical wire protocol.
4. `extensions/sdk/`, `extensions/sdk-rs/`, `extensions/sdk-py/`: SDK bridges.
5. `extensions/sdk-ts/`: declarations for Pi-compatible TypeScript extensions and PiG-only additions.
6. `tests/extension-conformance/conformance_test.go`: cross-SDK behavior gate.

## Hard rules

- Do not add a WASM runtime.
- Do not add an embedded JavaScript runtime.
- Do not add dynamic Go plugins or `plugin.Open`.
- Do not add a general dynamic linked/in-process extension loader. Piglet builds
  may fuse compatible reviewed Go SDK factories through the governed D31 path;
  the subprocess host remains the semantic reference.
- Do not introduce multi-register (`RegisterPayload.Extensions`,
  `RequestPayload.TargetExtension`) unless a new approved spec explicitly
  chooses it. Packed cells currently use the current subprocess wire with one socket per
  contained extension.
- If a behavior differs from upstream pi, either fix it or add a numbered
  `D<N>` entry to `DIVERGENCES.md` with call-site markers and tests.

## Translating TypeScript behavior to Go

Read [TypeScript-to-Go porting](typescript-to-go-porting.md) before choosing a
Go representation. In particular, an upstream TypeScript interface is a
consumer-visible shape, not an instruction to add a Go interface. Extension
protocol interfaces are justified public boundaries; extension data and helper
implementations should remain concrete unless a consumer needs substitution.
Do not reshape the public extension API to satisfy a generic interface linter or
an unmeasured performance claim.

### Async behavior

A Promise is a contract, not an instruction to start a goroutine. For every
upstream `async`, `Promise`, `.then`, `Promise.all`, `Promise.race`, or deliberately
unawaited call, inspect its callers and preserve:

1. whether the caller waits for completion;
2. result and error propagation;
3. completion ordering;
4. cancellation and lifetime ownership;
5. serial versus concurrent execution;
6. which event/UI loop owns callbacks and state mutation.

### Awaited handler: return only after the work finishes

TypeScript:

```ts
pi.registerCommand("refresh", {
  async handler(_args, ctx) {
    const models = await refreshModels(ctx.signal);
    ctx.ui.notify(`Loaded ${models.length} models`, "info");
  },
});
```

Go SDK:

```go
ext.Command("refresh", "Refresh models.", func(ctx sdk.Context, _ string) error {
    models, err := refreshModels(ctx.Done())
    if err != nil {
        return err
    }
    ctx.Notify(fmt.Sprintf("Loaded %d models", len(models)), "info")
    return nil
})
```

The Go subprocess SDK already runs each inbound handler on its own goroutine.
Blocking inside the handler preserves upstream `await`: the host receives the
response only when the callback completes. Wrapping the body in another
`go func()` and returning `nil` early changes ordering, drops the returned error,
and cancels the handler-scoped `sdk.Context` as soon as the handler returns.

### Parallel work: start together, then join

TypeScript `await Promise.all([loadA(), loadB()])` requires both operations to be
owned and joined. In Go, start both operations, collect both results/errors, and
return only after both complete. Use `errgroup.WithContext` when the project
already depends on `x/sync`, or an explicit `sync.WaitGroup` plus result/error
channels. Do not detach either goroutine, and do not let two goroutines mutate the
same map, slice, SDK state, or UI object without synchronization.

### Fire-and-forget is exceptional

Only detach work when the upstream caller intentionally does not await it.
Detached Go work must have an owner that:

- supplies cancellation independent of the completed request context;
- reports errors instead of discarding them;
- prevents writes after shutdown/reload;
- joins or drains the task during extension shutdown when upstream does.

A naked `go func()` with ignored error is not a valid translation of a Promise.

### UI and cancellation

Upstream Promise-based dialogs (`select`, `confirm`, `input`, `editor`) map to
blocking Go SDK calls because the handler is already off the host UI loop. For
Pig core/in-process code, slow work leaves the TUI loop and every UI mutation
returns through the approved run-on-main mechanism. Map `AbortSignal` to
`context.Context`, `sdk.Context.Done()`, or the owning lifetime's cancellation
channel and test cancellation before and during the operation.

### Subprocess request liveness

The SDK answers host heartbeat on its socket dispatcher. Extension handlers do
not implement heartbeat. Each request reports lifecycle state automatically.
Calls that wait for `select`, `confirm`, `input`, `editor`, or `custom` report
`blocked:user`. Other awaited host calls report `blocked:host_call`.
The wire also reserves `blocked:external_io` for an operation whose runtime can
identify that boundary.

Tools, commands, events, and shortcuts may run until they return, the caller
cancels them, the transport closes, or heartbeat fails. Renderer inactivity
cancels only the current generation and preserves its last completed frame.
Cancellation also closes a blocked host/UI call that carries the request as its
parent.

### Session history

Session history stays unloaded until an extension calls `getEntries` or
`getBranch`. For a persisted session, each subprocess reads the local JSONL file
once and then subscribes at that cursor. The host sends missing history and later
appends in ordered pages of at most 4 MB. A missing or unreadable file uses the
same paged host path from cursor zero. Returned containers are shallow copies;
treat their entries as read-only.

### Focused subprocess components

`ctx.ui.custom()` remains extension-owned in subprocess realizations. The SDK
constructs and renders the component locally; Pig opens a native focus-owning
overlay and transports only ordered input, replaceable line snapshots, and the
terminal result/error. Snapshots carry their render width and a monotone
per-overlay sequence; the host rejects stale-width and out-of-order frames.
Core editor keybindings do not receive input while the overlay owns focus.
Identical snapshots are suppressed before transport, and host rendering reads
only the latest cached frame.

Go components implement `sdk.RemoteComponent` and may implement
`sdk.RemoteComponentDisposer`:

```go
type picker struct { selected int }

func (p *picker) Render(width int) []string {
    return []string{fmt.Sprintf("selected=%d width=%d", p.selected, width)}
}

func (p *picker) HandleInput(data string) (sdk.RemoteComponentResult, error) {
    switch data {
    case "\x1b[B":
        p.selected++
    case "\r":
        return sdk.RemoteComponentResult{Done: true, Value: p.selected}, nil
    case "\x1b":
        return sdk.RemoteComponentResult{Done: true}, nil
    }
    return sdk.RemoteComponentResult{}, nil
}
```

Call `ctx.Custom(component, sdk.RemoteOverlayOptions{Title: "Picker"})` from a
tool or command handler. The call waits until completion or cancellation.
Component input is serial per overlay. Resize and timer invalidation rerender with
the current width. Implement `RemoteComponentInvalidator` when component state
changes without input:

```go
type timerView struct {
    mu         sync.Mutex
    invalidate func()
}

func (v *timerView) SetInvalidate(invalidate func()) {
    v.mu.Lock()
    v.invalidate = invalidate
    v.mu.Unlock()
}
```

Store the callback under the same lock as timer-owned state. Call it after the
state changes. Treat the callback as a render request, not as permission to
render or mutate TUI state directly. PiG coalesces repeated requests, limits
timer-driven frame production to the TUI 16 ms render interval, and transports
only a changed frame. Input-driven frames remain immediate.

Rust implements `set_invalidate` on `RemoteComponent` and stores the optional
`RemoteComponentInvalidate` callback. Python can implement
`set_invalidate(callback)` and must accept `None` to detach. Node extensions use
the `tui.requestRender()` callback passed to the upstream component factory.

One terminal gives focus to one extension dialog or overlay at a time. A second
interactive call waits off the TUI loop. Cancellation removes a waiting call before it can
take focus. Input events stay ordered and never coalesce. Render snapshots are
replaceable and use a bounded queue.

Completion, host cancellation, transport failure, reload, and shutdown stop the
component worker. PiG rejects late frames from a completed generation. The SDK
detaches the invalidation callback before it calls `dispose` exactly once. A
component callback must return promptly. If it does not stop by the cleanup
deadline, the SDK reports the failure and does not race disposal against the
stuck callback.

Rust uses `RemoteComponent` with
`Context::custom_component`; Python uses `Context.custom(component, options)`.
The Node bridge accepts the upstream component factory directly.

### Review evidence

An async port is not complete until a test can fail for premature return,
serializing intended parallel work, lost cancellation, lost rejection/error, or
an off-loop UI mutation. Record the chosen mapping in
`docs/extension-api-parity.md` for extension API surfaces and run the same
conformance behavior in isolated and packed modes.

### Project trust handlers

Use the typed `OnProjectTrust` / `on_project_trust` helper. Handlers run in
extension and registration order. Return `undecided` to continue; the first
`yes` or `no` wins. The host awaits each handler, collects an error and
continues, and propagates cancellation through the request context. Multiple
registrations retain distinct wire handler identities; do not replace
them with one local dispatcher or start detached work.

Go:

```go
ext.OnProjectTrust(func(ctx sdk.Context, data map[string]any) (sdk.ProjectTrustResult, error) {
    return sdk.ProjectTrustResult{Trusted: sdk.ProjectTrustUndecided}, nil
})
```

Rust returns `ProjectTrustResult` with `ProjectTrustDecision`; Python returns a
`ProjectTrustResult` typed dictionary. Production trust resolution loads only
global/explicit pre-trust extensions; project-local extensions cannot decide
whether they themselves are trusted.

## Set a native login

For a custom PiG mascot, scaffold the standard PiG artwork and login
handler first:

```bash
pig extension init ./my-pig --name my-pig --login
```

Keep the generated `Brand` and `Hero` fields unchanged. Edit only the mascot,
its palette colors, and identity text.

Use `SetLogin` to give an interactive Pig session an extension-defined identity.
Pig owns the layout and operational status. The definition has one strict
current unversioned shape.

The grids have exact dimensions: `brand` is 41 by 5, `hero` is 32 by 14, and
`mascot` is 16 by 14. Use ASCII symbols in each grid. `.` means transparent.
Map every other symbol to one `#RRGGBB` color. Use at most 32 colors. A fully transparent `brand` omits the brand band, and the hero and mascot start at the top of the header.

Pig rejects unknown or duplicate fields, bad dimensions, non-ASCII grid data,
missing or unused palette symbols, invalid colors, and invalid text. `name`,
`description`, and `tagline` must be non-empty. Their display-width limits are
24, 48, and 76 columns. The combined name and description line is limited to 80
columns.

`SetLogin` and `SetHeader` use the same slot. The last successful call wins.
Clear the header to restore Stock Pig's text header. A rejected login keeps the
current header. Quiet startup keeps the slot hidden. Widths below 66 columns
show compact text without art. The host renders from local validated data and
does not call the extension during each render.

Go:

```go
err := ctx.SetLogin(sdk.LoginDefinition{
    Brand: brand, Hero: hero, Mascot: mascot, Palette: palette,
    Name: "Example Bot", Description: "Custom coding agent", Tagline: "Build with care.",
})
```

Rust:

```rust
ctx.set_login(&LoginDefinition {
    brand, hero, mascot, palette,
    name: "Example Bot".into(), description: "Custom coding agent".into(),
    tagline: "Build with care.".into(),
})?;
```

Python:

```python
ctx.set_login(LoginDefinition(
    brand=brand, hero=hero, mascot=mascot, palette=palette,
    name="Example Bot", description="Custom coding agent", tagline="Build with care.",
))
```

Node:

```js
await ctx.ui.setLogin({
  brand, hero, mascot, palette,
  name: "Example Bot", description: "Custom coding agent", tagline: "Build with care.",
});
```

Preview one extension without starting a model session:

```bash
pig extension preview-login <path>
```

The command requires exactly one extension source. It fails if resolution finds
zero or multiple extensions, `session_start` fails, no login is set, or the
login is invalid. Use `/reload` after an edit. A login remains an extension
Resource. Activate it through a Piglet `extensions` entry, user extension
policy, or `pig -e <path>`. Verify activation by starting or reloading through
that path and checking the identity name. Keep product identities outside Stock
Pig and select them through an ordinary Piglet extension entry.

## Choosing a source form

Pig accepts exactly one conventional factory or exact standalone form.

| Language | Factory | Standalone |
|---|---|---|
| Go | importable `func Extension() *sdk.Extension` package | exact module with executable `package main` |
| Rust | `src/lib.rs` with `pub fn new_extension() -> Extension` | exact crate with `src/main.rs` only |
| Python | one module with `def new_extension() -> Extension` | executable `main.py` with a shebang |
| Node | Pi-compatible default export | exact executable script with a shebang |
| Native | n/a | exact executable file |

Go, Rust, and Python factories may share a generated language cell. Node
factories remain isolated under the approved D20 runtime boundary. Every
standalone remains isolated. Pig rejects mixed languages, several factories,
factory-plus-standalone roots, nonstandard factory symbols, missing entrypoints,
and identity mismatches. A direct directory uses its directory name as the
expected identity. A Package or Piglet supplies its declared member name.
Select a narrower exact root instead of relying on scan order.

A Piglet Binary may fuse a compatible Go factory into Pig's process. Fused code must return errors and use extension host/UI APIs for output. The build rejects `os.Exit`, `os.Chdir`, `log.Fatal*`, `fmt.Print*`, and `os.Stdout` in the factory's local package closure with `PIGLET_FUSED_PROCESS_HAZARD` because those operations would terminate Pig, change its working directory, or corrupt terminal output.

Use `pig extension init <path>` to create a factory. Add `--isolated` to create
a standalone. The scaffold writes no extension metadata file.

### Portable staged SDK resolution

`pig extension init` writes versioned SDK dependencies without machine-local
paths. During a source build, Pig resolves an un-replaced Go requirement on
`github.com/MichaelKinsy/PiG/extensions/sdk` or an un-pathed Rust `pig-sdk`
dependency to the SDK staged by the running binary under the active config
root. The build uses a temporary Go modfile or Cargo patch and never edits the
authored `go.mod` or `Cargo.toml`. An explicit valid author replacement or path
wins. If staged material is missing, run `pig sdk sync`.

Pig 0.84 Go factories import the SDK as
`github.com/mainstai/pig/extensions/sdk`. Pig still loads them. The generated
runner resolves that legacy path to a copy of the running binary's SDK and
ignores the extension's own replacement for it. Legacy factories share cells
only with other legacy factories. They are never fused into a Piglet Binary,
because their `*sdk.Extension` is a distinct Go type. Change the import path and
the `go.mod` requirement to fuse one.

## Factory contracts

### Go

```go
package myext

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

func Extension() *sdk.Extension {
    ext := sdk.New("my-go-ext")
    ext.Tool("hello", "Say hello", sdk.Schema{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
        return map[string]any{"content": "hello"}, nil
    })
    return ext
}
```

The package can use ordinary helper and `internal` packages. A module or
workspace root with several factory packages is ambiguous and reports every
candidate. Select one exact package directory. Pig keeps its containing module
or workspace as the compiler closure. `NewExtension` and other factory symbols
are not accepted.

### Rust

```rust
pub fn new_extension() -> pig_sdk::Extension {
    let mut ext = pig_sdk::Extension::new("my-rust-ext");
    ext.tool("hello", "Say hello", pig_sdk::empty_schema(), |_ctx, _args| {
        pig_sdk::ToolResult::json(serde_json::json!({"content": "hello"}))
    });
    ext
}
```

Keep the factory in `src/lib.rs`. Use only `src/main.rs` for standalone form.

### Python

```python
import pig_sdk


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("my-python-ext")
    ext.tool("hello", "Say hello", {"type": "object"}, lambda ctx, args: {"content": "hello"})
    return ext
```

One selected root can contain only one module with this factory.

### Node

Export one Pi-compatible default extension function. Pig runs each Node factory
in an isolated Node subprocess. Import the canonical API from
`@earendil-works/pi-coding-agent`. Pig supplies the compatible runtime module.

Use `@michaelkinsy/pig-extension-types` during development when the extension
uses a PiG-only API such as `ui.setLogin`. The declaration package pins the
exact Pi version from `coding.UpstreamVersion`; it does not provide a second
runtime implementation. See `extensions/sdk-ts/README.md`.

## Contributing an OAuth provider

Register OAuth only through the runtime provider declaration. `pig login --list`
resolves enabled extensions. For each source extension without a valid
projection for its exact source configuration and immutable artifact digest,
Pig starts it for registration inspection, collects OAuth provider declarations,
and stops it without dispatching session or capability handlers. A valid
projection avoids startup. Embedded packed members are inspected independently
through the complete compiled artifact. Duplicate provider IDs fail and name
both owners. Login then starts only the owning extension. Embedded and fused
extensions use the same registration contract.

The host builds a proxy that RPCs the provider's closures; a login closure drives
the host UI through callbacks. Only the closures you set are advertised; a
credential store is optional (when set, the provider owns credential persistence
instead of `auth.json`).

Set `IsSubscription` in Go, `is_subscription` in Rust or Python, or `config.oauth.isSubscription` in Node when the OAuth method is subscription-backed. The default is false. The host retains this metadata at registration, so reading it does not call the extension. The stock footer shows `(sub)` only when the active provider has both a stored OAuth credential and this flag. OAuth alone does not imply a subscription. Kimi Coding keeps Pi's explicit API-key subscription exception.

Go:

```go
ext.RegisterProvider("example-provider", sdk.ProviderConfig{
    "name": "Example Provider",
    "oauth": &sdk.OAuthProvider{
        Login: func(cb *sdk.OAuthLoginCallbacks) (sdk.OAuthCredentials, error) {
            cb.OnDeviceCode(sdk.OAuthDeviceCodeInfo{UserCode: code, VerificationURI: url})
            // ... poll for approval ...
            return sdk.OAuthCredentials{Access: token}, nil
        },
        GetAPIKey:       func(c sdk.OAuthCredentials) string { return c.Access },
        CredentialStore: myStore, // optional; implements sdk.OAuthCredentialStore
    },
})
```

Rust: `ext.register_oauth_provider("example-provider", json!({"name": "Example Provider"}), OAuthProvider { login: Box::new(|cb| ...), .. })`.

Python: `ext.register_oauth_provider("example-provider", {"name": "Example Provider"}, OAuthProvider(login=..., ...))`.

Value-returning callbacks (`OnPrompt`/`OnSelect`/`OnManualCodeInput`) return the
user's input or an `ErrOAuthCancelled`/`OAuthCancelled` error on dismissal.

## Validation

Validate one extension:

```bash
go run ./cmd/pig install --validate-only --json ./examples/extensions/python-factory
```

Validate a set and inspect placement decisions:

```bash
go run ./cmd/pig install --validate-only --json ./ext-a ./ext-b
```

A conventional Go, Rust, or Python factory is packable. A Node factory and
every standalone stay isolated.

## Adding a new extension API surface

1. Read upstream pi behavior in `docs/extensions.md` or upstream source.
2. Add/confirm protocol shape in `coding/extension/host/subprocess/protocol.go`.
3. Wire host behavior in `coding/extension/host/subprocess/`.
4. Add matching Go, Rust, and Python SDK behavior (`extensions/sdk`, `extensions/sdk-rs`, `extensions/sdk-py`).
5. Update the Node compatibility runtime and `extensions/sdk-ts` declarations when the surface is available to Node extensions.
6. Add or extend `tests/extension-conformance` so every runtime matches the in-process reference.
7. Update `docs/extension-api-parity.md`.
8. Add a numbered divergence only when parity is impossible or intentionally rejected.
