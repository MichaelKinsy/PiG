# Divergences

Pig preserves upstream pi-coding-agent's observable behavior unless an entry below records an intentional exception. Numbers are stable identifiers; do not reuse a retired one. The authoritative records, with call sites and evidence, are `DIVERGENCES.md` (behavior differences) and `docs/additive-features.md` (PiG-only additions) in the pig source tree. This page lists every active divergence and the additive features an agent is most likely to meet.

## Behavior differences

| ID | Title | Summary |
|---|---|---|
| D2 | Pig branding | Banner says "PiG", binary is `pig`, config root is `~/.pig`. A binary named `pi` refuses to run. Avoids upstream collisions. |
| D14 | Archive extraction | The tools manager extracts archives with the Go standard library instead of spawning `tar`, `unzip`, or PowerShell. |
| D26 | Provider attribution headers | Attribution headers carry PiG branding, respect the telemetry setting, and are sent only to OpenRouter. |
| D27 | Word navigation | Word-wise cursor movement classifies per grapheme instead of per `Intl.Segmenter` word segment. |
| D30 | Extension runner scope | The extension runner belongs to the host and is not rebuilt on an in-process session switch. |
| D35 | Easter-egg commands | Pi's two hidden animated easter-egg slash commands (arminsayshi and dementedelves) are not ported. |
| D37 | Stale thinking signatures | When a provider rejects stale thinking-block signatures, Pig retries once with the signatures stripped. |
| D39 | Self-update | A standalone binary updates itself from a release instead of through a package manager. |
| D44 | Herdr image capability | With `HERDR_ENV=1`, Pig ignores inherited outer-terminal image hints. |
| D48 | Orphaned tool results | Orphaned and duplicate tool results are removed from normalized history. |
| D50 | Unrenderable Mermaid | A Mermaid diagram Pig cannot draw gets one line saying why. |
| D51 | Signal termination | A signal-terminated interactive session restores the terminal before exit. |
| D53 | Wrapped-row clearing | The renderer fully clears when a differential rewrite would leave wrapped rows behind. |
| D54 | Code block wrapping | Fenced code block lines wrap instead of losing their tail. |
| D55 | Debug hotkey | The `ctrl+shift+d` debug hotkey fires once per press, not again on key release. |
| D56 | Subprocess liveness | Heartbeats, request-state reporting, and renderer isolation for subprocess extensions. See [Extension API](extension-api.md). |
| D57 | Unloadable install source refused | `pig install` fails when a complete conventional extension source is selected as a Package root and nothing would load, and records nothing. Pi reports success and the resources never load. Empty packages still install. See [Extensions](extensions.md). |
| D58 | Over-wide Mermaid | A Mermaid diagram wider than the message area is laid out again, narrower, until it fits. |
| D59 | Recoverable generic tool details | An extension tool card without renderers uses a width-aware argument preview. Press Ctrl+O to show the complete retained arguments and available result. Pi hides arguments when a registered extension tool provides no renderers. |
| D61 | Session replacement | Replacing the session keeps the startup project's Services and Resources. |
| D62 | `/bug` | Writes a local report archive and prints a prefilled PiG issue link. Nothing is uploaded. |
| D63 | `pig --version` | Prints the composite version, the PiG release then the Pi release it ports (`coding.Version`). `pig version` keeps separate fields. |
| D64 | Hosted endpoints | The share viewer, install report, version check, and installer API use `https://pi-in-go.dev` instead of pi.dev, with the same paths and overrides. |
| D65 | PiG User-Agent | HTTP providers and local bug-report metadata use `pig/<coding.Version> (<platform> <release>; <arch>)` instead of Pi's package-specific `pi...` identities. Configured provider headers retain upstream precedence: they override ordinary defaults, while Codex forces the product identity; Anthropic OAuth keeps `claude-cli/2.1.280`. |
| D66 | Narrow TUI rows | Truncated text reduces fixed padding and the user-message selector clips list rows when an unusually narrow pane cannot fit upstream's full row. PiG stays within the requested width; Pi terminates only if an over-wide row reaches its differential-render overflow check. |
| D67 | Windows container build user | A Windows host has no numeric uid or gid, so Piglet container builds pass no `--user` there and run as the builder image's default user; Linux and macOS pass the invoking user's `uid:gid`. |
| D68 | Windows owner-only files | Windows has no POSIX mode bits, so files PiG keeps owner-only (Piglet secrets and signing keys, the staged secret environment, the update transport CA, and the standalone update receipt) are checked and written by DACL there: only the owner, SYSTEM, and Administrators may have access. Elsewhere they use mode `0600`. |
| D69 | Windows Piglet scripts | `pig piglet build --format script` writes a cmd.exe batch file on Windows (`@setlocal DisableDelayedExpansion` then `@pig --piglet "<source>" %*`, CRLF); Linux and macOS get a POSIX shell script. Name the Windows output `.cmd`. |
| D70 | Module state across reload | `/reload` restarts every extension in a fresh process, so module-level state resets. Pi re-runs every factory too, but keeps an `.mjs` module's module-level state in Node's module cache. Keep per-reload state inside the factory. |
| D73 | Host-bound Pi exports in extensions | Extensions can import every value Pi's packages export. Pi's own code backs the pure ones, including pi-tui's `Input`, `Editor`, `SelectList`, `Markdown` and `KeybindingsManager` and pi-ai's utilities. Values that belong to Pi's own process (its UI components, session and runtime construction, package loading, the terminal and its capabilities, images, pi-ai's session-resource cleanups) throw a named error if an extension calls them. Keybinding overrides stay in the host. |
| D74 | Pi-ai provider calls in extensions | An extension's `stream`, `complete`, `streamSimple` and `completeSimple` use Pi's own dispatch and credential rules, and the request runs on PiG's port of the same provider. Provider-specific `stream()` options beyond the common ones are not forwarded, results lack `responseId` and `rawStopReason`, and OpenRouter image generation returns an error. |

## Additive features

These add PiG-only capability without changing a Pi behavior. The additive ledger lists the rest.

| ID | Title | Summary |
|---|---|---|
| D18 | Piglet management and build dispatch | Generic Piglet source, runtime, and artifact commands. Stock PiG selects no product composition. |
| D19 | Multi-language subprocess SDK bridges | First-party Go, Rust, Python SDKs target the same wire and capability surface. |
| D20 | Packed runtime cells | Host-side factory-extension packing; observationally identical to isolated mode. See [Runtime cells](runtime-cells.md). |
| D21 | Reload report telemetry | Pull-only `LastReloadReport()` + `Metrics()`. No background samplers or external push. |
| D22 | Stock documentation | `pig docs` materializes the reference bundle that matches the running binary. |

## Non-divergences

Items below are sometimes mistaken for divergences but are intentional **parity**:

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

Add an entry to `DIVERGENCES.md` when:

- Pig behavior differs from upstream pi in a way a user can observe (output, exit code, file layout, command surface).
- A capability is unsupported on the subprocess bridge and returns a typed `unsupported` error rather than silently no-op-ing.
- Pig adds product-neutral behavior that has no upstream equivalent and records it in the additive-feature ledger.

Do not write a new divergence when:

- The change is a pure refactor with no observable effect.
- The change restores parity that had drifted (parity is the goal, drift is a bug).
- The change is internal to runtime cells, since D20 already covers them generically.
