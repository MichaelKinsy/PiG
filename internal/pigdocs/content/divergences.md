# Divergences

Pig preserves upstream pi-coding-agent's observable behavior unless an entry below records an intentional exception. Numbers are stable identifiers; do not reuse a retired one. The authoritative records, with call sites and evidence, are `docs/parity/DIVERGENCES.md` (behavior differences) and `docs/additive-features.md` (PiG-only additions) in the pig source tree. This page lists every active divergence and the additive features an agent is most likely to meet.

## Behavior differences

| ID | Title | Summary |
|---|---|---|
| D2 | Pig branding | Banner says "PiG", binary is `pig`, config root defaults to `~/.pig`. `PIG_USE_PI_DIRS=1` explicitly selects Pi's agent directory and project `.pi` resources. See [Configuration](config.md#using-pis-directories). A binary named `pi` refuses to run. |
| D14 | Archive extraction | The tools manager extracts archives with the Go standard library instead of spawning `tar`, `unzip`, or PowerShell. |
| D26 | PiG never identifies as Pi | Every value Pi sends to identify itself carries PiG's value instead, in the Go host and in the Pi JavaScript that Node extensions use: OpenRouter, NVIDIA and Cloudflare attribution headers (gated by the telemetry setting, as in Pi), the OpenCode client header, the OpenAI Codex `originator`, the xAI `referrer`, the user agent and `AI_AGENT=pig`. Pi-owned endpoints route to `pi-in-go.dev` or are refused, as D62 and D64 describe. GitHub Copilot's editor headers and Anthropic OAuth's `claude-cli` user agent stay as Pi sends them, because those providers require them. |
| D27 | Word segmentation engine cache | Word segmentation always matches ICU with a warm dictionary-engine cache; a fresh Pi process can place one boundary differently at a U+30FC or U+FF70 span start. |
| D30 | Extension runner scope | The extension runner belongs to the host and is not rebuilt on an in-process session switch. |
| D37 | Stale thinking signatures | When a provider rejects stale thinking-block signatures, Pig retries once with the signatures stripped. |
| D39 | Self-update | A standalone binary updates itself from a release instead of through a package manager. |
| D44 | Herdr image capability | With `HERDR_ENV=1`, Pig ignores inherited outer-terminal image hints. |
| D48 | Orphaned tool results | Orphaned and duplicate tool results are removed from normalized history. |
| D51 | Interactive SIGINT | SIGINT restores the interactive terminal before numeric exit 130. Print/JSON leave SIGINT to the default signal action. Both products restore a live terminal on SIGTERM/SIGHUP; a disconnected terminal exits 129. |
| D53 | Wrapped-row clearing | The renderer fully clears when a differential rewrite would leave wrapped rows behind. |
| D55 | Debug hotkey | The `ctrl+shift+d` debug hotkey fires once per press, not again on key release. |
| D56 | Subprocess liveness | Heartbeats, request-state reporting, and renderer isolation for subprocess extensions. See [Extension API](extension-api.md). |
| D57 | Unloadable install source refused | `pig install` fails when a complete conventional extension source is selected as a Package root and nothing would load, and records nothing. Pi reports success and the resources never load. Empty packages still install. See [Extensions](extensions.md). |
| D61 | Session replacement | Replacing the session keeps the startup project's Services and Resources. |
| D62 | `/bug` | Writes a local report archive and prints a prefilled PiG issue link. Nothing is uploaded. |
| D63 | `pig --version` | Prints the composite version, the PiG release then the Pi release it ports (`coding.Version`). `pig version` keeps separate fields. |
| D64 | Hosted endpoints | The share viewer, install report, version check, and installer API use `https://pi-in-go.dev` instead of pi.dev, with the same paths and overrides. |
| D65 | PiG User-Agent | HTTP providers and local bug-report metadata use `pig/<coding.Version> (<platform> <release>; <arch>)` instead of Pi's package-specific `pi...` identities. Configured provider headers retain upstream precedence: they override ordinary defaults, while Codex forces the product identity; Anthropic OAuth keeps `claude-cli/2.1.280`. |
| D66 | Narrow TUI rows | Truncated text reduces fixed padding and the user-message selector clips list rows when an unusually narrow pane cannot fit upstream's full row. PiG stays within the requested width; Pi terminates only if an over-wide row reaches its differential-render overflow check. |
| D68 | Windows owner-only files | Windows has no POSIX mode bits, so files PiG keeps owner-only (Piglet secrets and signing keys, the staged secret environment, the update transport CA, and the standalone update receipt) are checked and written by DACL there: only the owner, SYSTEM, and Administrators may have access. Elsewhere they use mode `0600`. |
| D70 | Module state across session replacement | Every session replacement (`/new`, `/resume`, `/fork`, `/clone`, `ctx.newSession()`, `ctx.fork()`, `ctx.switchSession()` and their RPC equivalents) starts extensions in fresh runtime processes, so module-level state and `process.env`/`globalThis` changes reset. Pi keeps an `.mjs` module's state in Node's module cache. `/reload` is not affected: it re-invokes each factory in the process that holds its module, as Pi does. Keep per-session state inside the factory. |
| D73 | Live main-process object identity | Extensions use Pi's pinned coding-agent and TUI implementations for imported objects, including independent Sessions, ModelRuntime, themes, ScrollView, terminal/image helpers, and caller-created screens. These objects do not expose the live Go Main Screen or arbitrary Go component references. An overlay's `unfocus({target})` accepts `null`, the extension's installed editor component or another of its mounted overlays and throws for any other component. Editor input and rendered frames cross the subprocess connection. |
| D74 | Pi-ai provider calls in extensions | An extension's `stream`, `complete`, `streamSimple` and `completeSimple` use Pi's own dispatch and credential rules, and the request runs on PiG's port of the same provider. Provider-specific `stream()` options beyond the common ones are not forwarded, results lack `responseId` and `rawStopReason`, and OpenRouter image generation returns an error. A direct pi-ai call also carries PiG's OpenRouter and NVIDIA attribution headers, which Pi's pi-ai does not send there. |
| D78 | SDK Provider object carriers (known gap) | Registered-native methods cross SDK processes. Builtin/composed native SDK retrieval remains incomplete, and foreign registered configuration is snapshot data rather than an author's live object. Owner-approved for 0.3.x on 2026-09-28, not completed parity. |
| D79 | User-package metadata registry isolation | User-scoped package version lookups run from managed storage, not the invoking project's cwd, so its `.npmrc` cannot redirect them. The selected command and user configuration remain active. Trusted project-scoped packages keep Pi's behavior. |
| D80 | Secret-input privacy | `maskSecretInput` defaults to true and shows dots, a count and a last-four suffix for secret login input. False restores Pi's plain-text behavior. Change **Mask secret input** in `/settings`. |
| D82 | Partial-message observation (RPC33) | Foreign partial messages are snapshots, not live references. Initial content/usage/stop state and named parser scratch fields can differ. The strict RPC33 comparison retains its raw failure. |
| D83 | Cross-process event-bus boundaries (known gap) | Every Node extension shares one `pi.events` bus and listeners in other processes receive the original payload by reference. Independent tasks in different processes are ordered by timing, only a listener's first post-await continuation is ordered with the emitter's microtasks, cross-process reference cycles are released when a process exits, and brand checks such as `util.types.isMap` fail on foreign objects, where `util.types.isProxy` also returns true. Because V8 offers no hook, `util.inspect` with `customInspect: false` (`console.dir`) prints an empty placeholder object (inside `Proxy(...)` on Node 26.0.0 and later), `util.inspect` with `showProxy: true` shows the Proxy wrapper, and on Node 26.0.0 and later `util.inspect` of a non-extensible foreign payload prints it inside `Proxy(...)`; Node 25 and earlier print that payload as Pi does. Every other inspect, clone and write case must match Pi; the Promise nesting level and cross-owner shared clones are open defects targeted at 0.3.1. |
| D84 | Direct Go provider registration | A Go caller that registers a Provider directly on the Model Registry or Model Runtime, without an extension host, sees the synchronous projection until its next awaited model-runtime call runs Pi's local refresh. Extension-host registrations match Pi. |
| D85 | RPC stdin-end residuals | After RPC stdin ends, a suspended extension command's continuation (timer, I/O, nested `setImmediate`, child process) can still run its side effects before PiG stops that runtime process; Pi never runs it, and stdout matches Pi. A suspended `session_shutdown` handler in one runtime process does not keep another runtime process's command alive, so PiG does not print a response that Pi prints. |
| D86 | RPC settle-tail stdin hold | In RPC mode PiG holds stdin from `agent_end` until `agent_settled`, as Pi does in effect. A settle-tail handler in the Go, Rust or Python SDK that waits on a timer or I/O keeps the hold closed, so a command sent during its wait is answered after `agent_settled`, where Pi answers it during the wait. Node handlers report their wait and match Pi. Temporary: removed when those SDK runtimes report a tail handler's suspension. |

## Known gaps for 0.3.x

D78, D82 and D83 are owner-approved known gaps (2026-09-28), not waivers or evidence of parity. Same-process objects must keep Pi's identity and mutation behavior. Foreign configuration readers (D78) and partial-message observers (D82) receive snapshots. D83 was narrowed on 2026-09-29: foreign bus listeners receive the original payload by reference, and only the named cross-process boundaries remain. The earlier no-sharing rule for isolated extensions is retired.

RPC33 retains complete JSON and stderr comparison and three declared runs. Its raw partial-observation failure stays visible. Approval does not permit missing events, changed order, wrong deltas, broken cancellation, bad final results, persistence errors or lifecycle leaks. The authoritative integration boundary and outstanding same-process defects are in `docs/findings/0.3.0-known-gaps.md` in the source tree.

## Additive features

These add PiG-only capability without changing a Pi behavior. The additive ledger lists the rest.

| ID | Title | Summary |
|---|---|---|
| D18 | Piglet management and build dispatch | Generic Piglet source, runtime, and artifact commands. Stock PiG selects no product composition. |
| D19 | Multi-language subprocess SDK bridges | First-party Go, Rust, Python SDKs target the same wire and capability surface. |
| D20 | Packed runtime cells | Host-side factory-extension packing; observationally identical to isolated mode. See [Runtime cells](runtime-cells.md). |
| D21 | Reload report telemetry | Pull-only `LastReloadReport()` + `Metrics()`. No background samplers or external push. |
| D22 | Stock documentation | `pig docs` materializes the reference bundle that matches the running binary. |
| D67 | Windows container build user | Piglet container builds use the image default user on Windows and the invoking uid/gid on Unix. This is additive builder policy, not a Pi divergence. |
| D69 | Windows Piglet scripts | Piglet script builds emit a quoted CRLF cmd.exe launcher on Windows and a POSIX launcher on Unix. Name Windows output `.cmd`. |

## Non-divergences

Items below are sometimes mistaken for divergences but are intentional **parity**:

- **Mermaid fallback.** Unsupported or oversized diagrams retain the original code block. Pig does not add an unsupported-diagram hint or narrow labels to force a diagram to fit.
- **Fenced-code wrapping.** Pi 0.99.2 wraps code rows in the final Markdown content-width pass. PiG uses the same pass and breakpoints.
- **TUI scrollback.** Pig now emits `\x1b[2J\x1b[H\x1b[3J` on full redraw, matching upstream `pi-tui`. Earlier pig builds omitted the `3J` (scrollback clear); that was a parity drift, fixed.
- **Model cycling.** `Ctrl+P` / `Shift+Ctrl+P` cycle through provider-qualified specs. Bare model IDs are legacy-accepted but always rewritten on save.
- **Provider auth precedence.** Environment variables override stored credentials at request time, identical to upstream.
- **Skill loader.** Skills require `SKILL.md`. Directories without one are silently inert, matching upstream.

## Forbidden runtime patterns

These are explicit non-options. Calling them out so the coding agent does not propose them:

- WASM extension runtime - not supported.
- Embedded JS / PigScript - not supported.
- Dynamic Go plugin runtime - not supported.
- Fused Go factories in a Piglet Binary use the same extension registration contract as subprocess factories.
- Protocol v2 multi-register payload - deliberately deferred; do not add `RegisterPayload.Extensions` or `RequestPayload.TargetExtension` without an explicit divergence + design.

## When to write a new divergence

Add an entry to `docs/parity/DIVERGENCES.md` when:

- Pig behavior differs from upstream pi in a way a user can observe (output, exit code, file layout, command surface).
- A capability is unsupported on the subprocess bridge and returns a typed `unsupported` error rather than silently no-op-ing.
- Pig adds product-neutral behavior that has no upstream equivalent and records it in the additive-feature ledger.

Do not write a new divergence when:

- The change is a pure refactor with no observable effect.
- The change restores parity that had drifted (parity is the goal, drift is a bug).
- The change is internal to runtime cells, since D20 already covers them generically.
