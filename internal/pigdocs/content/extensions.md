# Pig extensions

Pig extensions are external programs that add tools, commands, events,
providers, renderers, and UI behavior. Pig hosts them through the current
subprocess wire. A Piglet Binary can fuse a compatible Go factory, but fused
behavior must match subprocess behavior.

Pig does not use PigScript, embedded JavaScript, WASM, dynamic Go plugins, or an
authored extension manifest.

## Source forms

Pig accepts exactly two forms.

### Conventional factory

Use one standard factory for the language:

- Go: `func Extension() *sdk.Extension`
- Rust: `pub fn new_extension() -> Extension` in `src/lib.rs`
- Python: `def new_extension() -> Extension` in one module
- Node: one Pi-compatible default export

Pig generates the runner. Go, Rust, and Python factories can share a
language-specific runtime cell. Node factories run in isolated Node processes.

### Exact standalone

Use an exact executable or conventional executable source:

- Go: an exact module with executable `package main`
- Rust: an exact crate with `src/main.rs` and no factory library
- Python: executable `main.py` with a shebang
- Node: an exact executable script with a shebang
- Native: an exact executable file

Every standalone runs in an isolated process.

Pig rejects mixed languages, an ambiguous root with several factories,
factory-plus-standalone roots, nonstandard factory symbols, missing entrypoints,
and identity mismatches. Select one exact factory package inside a containing Go
module or workspace. Pig uses the containing module or workspace as the build
closure.

## Native login identity

An extension can call `SetLogin` during `session_start`. Pig validates the typed
definition and renders it through the shared header slot. Widths below 66
columns use compact text. Quiet startup keeps the login hidden. Rendering uses
local cached data and performs no per-render IPC.

Preview the result before loading a session:

```bash
pig extension preview-login <path>
```

The preview requires exactly one extension. Use `/reload` after an edit. A
login remains an extension Resource. Activate it through a Piglet `extensions`
entry, user extension policy, or `pig -e <path>`. Verify activation by starting
or reloading through that path and checking the identity name. Keep product
identities outside Stock Pig and select them through an ordinary Piglet
extension entry. See `extension-api.md` for the exact grids, palette,
validation errors, and SDK calls.

## TypeScript async is a contract, not a goroutine instruction

The Go SDK already dispatches every inbound handler on its own goroutine. Keep
awaited work inside the handler and return its error. The request-scoped `sdk.Context` is cancelled when the handler returns.

Translate `await Promise.all` into owned concurrent work that joins before the
handler returns. Never use a naked fire-and-forget goroutine. A deliberately
unawaited task needs independent cancellation, error reporting, and shutdown
draining.

## Identity and capabilities

The selecting Package or Piglet name supplies the expected extension identity.
Direct directory selections use the directory name, and direct files use the
file stem. Runtime registration must match that identity. Runtime registration is the only source for tools, commands,
events, providers, renderers, descriptions, and other capabilities.

A Package contributes only exact members from its extension inventory. Source
markers at an arbitrary Package root do not make that Package an extension.

## Discovery

Pig collects extensions from these sources:

1. `~/.pig/agent/extensions`
2. trusted `<cwd>/.pig/extensions`
3. configured Package extension members
4. selected Piglet composition
5. exact paths in settings
6. explicit `-e` paths
7. embedded or fused Piglet Binary components

`--no-extensions` disables ambient discovery and settings, but it preserves
explicit `-e` paths. Project discovery requires trust. Startup and `/reload`
use the same normalized resolver.

Pig does not discover `~/.pig/extensions`.

## Scaffold

Create a factory:

```bash
pig extension init ./my-extension --lang go
```

Create a Go login factory with a neutral example definition:

```bash
pig extension init ./my-pig --lang go --login
```

The login scaffold contains a valid neutral grid, palette, and `SetLogin`
session-start handler. Replace its art and identity text for your extension.

Create a standalone:

```bash
pig extension init ./my-extension --lang go --isolated
```

Supported scaffold languages are Go, Python, and Rust. The scaffold writes no
YAML file. Pig stages the matching SDK under the active config root.

## Validation

Validate one source:

```bash
pig install ./my-extension --validate-only --json
```

Validate a set:

```bash
pig install --validate-only --json ./ext-a ./ext-b
```

Validation resolves the source form, builds or launches it, accepts runtime
registration, verifies identity, detects duplicate capabilities, and reports the
placement plan. It does not install the source.

## Authentication inspection

An extension declares OAuth through runtime provider registration. Before a
session, `pig login --list` inspects each enabled extension independently. For a
source extension, a valid projection for the exact source configuration and
immutable artifact digest avoids startup. A projection miss starts registration
inspection. Factory construction executes extension code, but Pig dispatches no
session, tool, command, event, shortcut, or renderer handler. One broken
extension produces a diagnostic and does not remove healthy OAuth targets.
Embedded packed members are inspected independently through the complete
compiled artifact.

Duplicate provider IDs fail and name both owners. Login starts only the owning
extension and checks its live registration. Embedded and fused extensions use
the same registration contract.

## Liveness

The SDK answers heartbeat directly. Tools, commands, events, and shortcuts have
no completion or inactivity timeout. Their caller-owned context controls
cancellation. Renderers use generation-scoped inactivity and retain the last
completed frame. A pong proves transport health.

See `docs/extension-authoring.md` and `docs/extension-api-parity.md`.

## Session context

PiG loads session history for an extension only when the extension reads it. A
persisted session is read from its local JSONL file and reconciled with the host
cursor. If the file is unavailable, the host sends ordered pages of at most 4
MB. Later appends use the same bounded path.

## Reload

`/reload` resolves the same extension inputs as startup and preserves their first-seen order. Pig reuses valid build artifacts, but it constructs a fresh factory and runtime for every configured, embedded, fused, and built-in extension so module and closure state reset as they do in Pi. It starts and validates replacements, checks registration, and publishes the successful set. A failed extension is removed and reported while other extensions load.

## Product boundary

`plugin.json` is cross-tool marketplace metadata. It does not contain Pig
runtime fields. A deployment resource can point at extension source or
artifacts and calls the same Pig validation path. It does not define the local
Pig runtime source contract.
