# PiG Common Lisp SDK (SBCL)

Write PiG extensions in Common Lisp. The SDK speaks the same wire as the Go, Rust and Python SDKs and exposes the same extension API with Lisp
names: package `pig`, kebab-case functions, a `ctx-` prefix for the per-request context.

```lisp
(defpackage "hello" (:use "COMMON-LISP"))
(in-package "hello")

(pig:define-extension "hello"
  (:tool "hello_ping" "Return a short pong." (pig:obj "type" "object")
         (lambda (ctx params) (declare (ignore ctx params)) "pong"))
  (:command "hello" "Say hello."
            (lambda (ctx args) (declare (ignore args)) (pig:ctx-notify ctx "hello" "info"))))
```

Create one with `pig extension init ./hello --lang lisp`. Pig builds the system into a cached SBCL image (SBCL 2.6.9; `pig setup` checks it) and
starts that image; see `docs/extension-authoring.md`, "Lisp".

## Data

| JSON | Lisp |
|---|---|
| object | `equal` hash-table, string keys, member order kept (`(pig:obj "k" v ...)` builds one) |
| array | simple-vector (a list also writes as an array; write an empty array as `#()`) |
| number | integer when the text is an integer within 2^53, else double-float; written as ECMAScript number text |
| `true` `false` `null` | `t` `:false` `:null` (`nil` writes null) |

Strings carry lone UTF-16 surrogates as characters in U+D800..U+DFFF and write them as `\udXXX` escapes, as `JSON.stringify` does.

## What it serves

The Lisp SDK exposes the same extension API as the Go, Rust and Python SDKs, under Lisp names (`pig:ctx-*` for the per-request context, a short prefix per
class: `session-` / `sm-` SessionManager, `settings-` SettingsManager, `registry-` ModelRegistry, `footer-`, `kit-` component kit, `editor-` editor base,
`oauth-` / `provider-` providers, `tui-` pi-tui helpers, `type-` the Type engine). Every export is in `src/package.lisp`, one block per feature file:

| Area | Files |
|---|---|
| registration, events, tool results, tool options (exposure, loadout, prepare arguments, constrained sampling, renderers), commands, flags | `extension`, `context`, `context-extra`, `dispatch`, `tool-schema`, `tool-order` |
| the context: UI dialogs, status, widgets, footer/header, theme, working indicator, exec (with a signal and a `:max-buffer` that sets `exec-result-truncated`), compact, session and model controls, nested tool calls | `context`, `context-extra`, `widgets`, `theme` |
| component kit, `ui.custom`, overlays, mouse (`make-custom-component` takes `:captures-viewport-input`, a boolean or a function of no arguments read at each frame) | `kit`, `ui-custom`, `overlay` |
| editor components (`make-editor-component` takes `:captures-viewport-input`), autocomplete providers, terminal input | `editor-component`, `autocomplete`, `terminal-input` |
| the alternate-screen TUI: `TuiAltScreen` (`make-tui-alt-screen`, `tui-alt-screen-*`), the `tui-terminal` and `tui-component` it draws on and shows (`:captures-viewport-input`, answered live to the host's `capturesViewportInput` callback), `tui-alt-overlay-*` handles | `tui-alt-screen` |
| providers (config, native `Provider` objects, remote proxies), OAuth, login, sprites, message/entry/tool renderers, markdown transformer, thinking-run ownership (`own-thinking-runs`, `ctx-own-thinking-runs`), virtual models, MCP servers | `provider`, `oauth`, `renderer`, `virtual-model`, `mcp` |
| model calls: `modelStream`, `complete`, `classify`, `generateImages`, `ModelRegistry`, event streams, fetch transport | `model-stream`, `model-types`, `event-stream` |
| session replacement (`new-session`, `fork`, `navigate-tree`, `switch-session`), child agent sessions, `createAgentSessionRuntime`, services | `session-ops`, `child-session`, `agent-runtime` |
| session graph: SessionManager, SettingsManager, resource loader, package manager, project trust, host info, ModelRuntime, ExtensionRunner, extension factories, MCP options, the event bus, builtin tool delegation | `session-manager`, `settings-manager`, `resource-loader`, `package-manager`, `project-trust`, `host-info`, `model-runtime`, `create-provider`, `extension-runner`, `extension-factories`, `mcp-options`, `event-bus`, `builtin-tool` |
| pure helpers: Type engine (`value-check` and `value-errors` are pi-ai's `Value.Check` and `Value.Errors` and run `type-refine` refinements), pi-ai, pi-tui (width, keys, fuzzy, colors, keybindings incl. `tui-get-select-page-index`, theme), compaction, truncation, conversation, frontmatter/diff/image/clipboard host calls | `type-engine`, `type-schema`, `pi-ai`, `tui-*`, `compaction`, `truncate`, `conversation`, `coding-helpers`, `host-helper` |

`ctx-call-host` stays the escape hatch for any wire method. A member the SDK does not serve signals `pig:unsupported-capability` naming the lane that owns
it (the few that remain are the `gap:` cells shared with the other SDKs in `test/parity/sdk-exports.toml`).

The feature files plug into the connection loop through `src/hooks.lisp` (`def-register-contributor`, `def-ready-hook`, `def-state-hook`,
`def-stop-hook`, `def-notify`, `def-request`, `extension-extra`), so `dispatch.lisp` knows no feature.

## Stack

Every thread of the image has SBCL's default 2 MB control stack (a larger one is committed in full by every thread on Windows). Values the host sends nest up to 10000 levels and can be wide, so the walks over them do not recurse on that stack past a fixed depth: the JSON reader and writer (`json.lisp`, bound `*max-json-depth*` = 10016) recurse to 256 levels and keep deeper containers on a stack of their own, and the event-stream copy (`clone-stream-value`), the key ordering of tool arguments (`core-js-ordered`), `cx-json-stringify`, the bigint check (`ts-holds-big-int`) and the palette comparison (`ts-json-equal`) are iterative. `cx-json-stringify` writes to one stream, never an `&rest` list, so width costs no stack either. `tests/deep-tests.lisp` runs each at 10000 levels and 200000 members on the 2 MB stack of the test runner. The walks that stay recursive are bounded by something other than the host's data: schemas and values an extension hands to the type engine (`te-encode`, `te-decode`; Pi's TypeBox recurses the same way), `cx-js-value-string` and `ai-js-string` (arrays only, measured to 12600 levels), and the length of a delta's path (`stream-update`, to 28000 steps).

## Tests

`make test-sdk-lisp` runs `sbcl --script extensions/sdk-lisp/tests/run.lisp`: the JSON vectors (copied from the Rust and Python SDK tests), the vector tests generated from Pi (`test/parity/sdk-vectors`, `sdk-bulk`) and the SDK
against an in-process fake host. The conformance rows run through the real host in `test/extension-conformance`.
