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

Packing reduces process count. It does not create a shared extension identity.

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

## Cancellation

A handler receives request cancellation through its language SDK.

In Go, use `sdk.Context.Done()` and return the cancellation error from owned work. Do not detach a goroutine from the handler unless the upstream operation is intentionally unawaited and another lifecycle owns it.

An awaited handler must return only after its work finishes. Returning early changes completion ordering and loses errors.

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
