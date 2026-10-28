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
| registration, events, tool results, tool options (exposure, loadout, prepare/refine arguments, constrained sampling, renderers), commands, flags | `extension`, `context`, `context-extra`, `dispatch`, `tool-schema`, `tool-order` |
| the context: UI dialogs, status, widgets, footer/header, theme, working indicator, exec (with a signal), compact, session and model controls, nested tool calls | `context`, `context-extra`, `widgets`, `theme` |
| component kit, `ui.custom`, overlays, mouse | `kit`, `ui-custom`, `overlay` |
| editor components, autocomplete providers, terminal input | `editor-component`, `autocomplete`, `terminal-input` |
| providers (config, native `Provider` objects, remote proxies), OAuth, login, sprites, message/entry/tool renderers, markdown transformer, virtual models, MCP servers | `provider`, `oauth`, `renderer`, `virtual-model`, `mcp` |
| model calls: `modelStream`, `complete`, `classify`, `generateImages`, `ModelRegistry`, event streams, fetch transport | `model-stream`, `model-types`, `event-stream` |
| session replacement (`new-session`, `fork`, `navigate-tree`, `switch-session`), child agent sessions, `createAgentSessionRuntime`, services | `session-ops`, `child-session`, `agent-runtime` |
| session graph: SessionManager, SettingsManager, resource loader, package manager, project trust, host info, ModelRuntime, ExtensionRunner, extension factories, MCP options, the event bus, builtin tool delegation | `session-manager`, `settings-manager`, `resource-loader`, `package-manager`, `project-trust`, `host-info`, `model-runtime`, `create-provider`, `extension-runner`, `extension-factories`, `mcp-options`, `event-bus`, `builtin-tool` |
| pure helpers: Type engine, pi-ai, pi-tui (width, keys, fuzzy, colors, keybindings, theme), compaction, truncation, conversation, frontmatter/diff/image/clipboard host calls | `type-engine`, `type-schema`, `pi-ai`, `tui-*`, `compaction`, `truncate`, `conversation`, `coding-helpers`, `host-helper` |

`ctx-call-host` stays the escape hatch for any wire method. A member the SDK does not serve signals `pig:unsupported-capability` naming the lane that owns
it (the few that remain are the `gap:` cells shared with the other SDKs in `test/parity/sdk-exports.toml`).

The feature files plug into the connection loop through `src/hooks.lisp` (`def-register-contributor`, `def-ready-hook`, `def-state-hook`,
`def-stop-hook`, `def-notify`, `def-request`, `extension-extra`), so `dispatch.lisp` knows no feature.

## Tests

`make test-sdk-lisp` runs `sbcl --script extensions/sdk-lisp/tests/run.lisp`: the JSON vectors (copied from the Rust and Python SDK tests), the vector tests generated from Pi (`test/parity/sdk-vectors`, `sdk-bulk`) and the SDK
against an in-process fake host. The conformance rows run through the real host in `test/extension-conformance`.
