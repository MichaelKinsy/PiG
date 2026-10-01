# Lane fix-99-j5-ledgers: joint run 5 ledger gates

Branch `fix-99-j5-ledgers` from `staging/porter/pi-0.99.1` (`113a10ba9`). Upstream: upstream 0.99.1 (`.upstream/v0.99.1`). Every run used a temporary `HOME`, `PIG_HOME`, `PIG_CODING_AGENT_DIR` (rule 17); `MISE_DATA_DIR`, `CARGO_HOME` and `RUSTUP_HOME` pointed at the real toolchains.

Lead answer (2026-09-30): the Node D-C API, the typed `ctx.modelRegistry` methods and provider `images`/`classifiers` are real gaps with their own lanes (port-99-f6f-node, port-99-f6h-model-types). They get no `sdk-surface-exceptions.toml` entry; their cells stay red and pending under those owners. The 9a theme cells stay red under 9a.

## Red baseline (before any edit)

| gate | failing tests | cause |
|---|---|---|
| `test/parity/cmd/sdksurface` | `TestExtensionSDKSurfaceMatrixIsCurrent` (91 problems, matrix stale), `TestSurfaceCoversPiExtensionAPI` (41 versus 39 events) | 0.99.1 members with no map row; a hard-coded event count; a stale `pi.events.*` exception |
| `test/parity/cmd/customfactoryledger` | `TestGenerateIsDeterministicAndClassifiesEverySelectedMember`, `TestCheckedInLedgerHasNoDrift` | 26 unreviewed members: `Theme.style/colors/appearance`, `ThemeStyle*`, `ThemeToken`, `ThemeBg`, `ThemeAppearance`, `TUI.queryTerminalColors`, `TerminalColors*`, `TerminalColorMode`, `Color` |
| `test/parity/correspondence`, `cmd/correspondence`, `closure`, `cmd/closure` | 14 tests | `coding/session.go#compact` has 0 calls to `compaction.Compact` (the call moved to `runDefaultCompaction`, `agent-session.ts:2633-2661`); the settings table has the new `fullscreen-wheel-scroll-lines` row (`settings-selector.ts:733-742`) that Pig's selector lacks |

Environment note: the correspondence and closure tests need `test/parity/interface-extractor/node_modules` (`npm ci`, TypeScript 5.9.3); the sdksurface probe needs `extensions/sdk-ts/node_modules`. Both are gitignored.

## Commits

1. `test(parity): port the upstream 0.99.1 ledger denominators ... (red)`: `typescript_test.go` expects `fullscreen-wheel-scroll-lines` in the settings table; both mapping-count tests (`correspondence/compare_test.go`, `cmd/correspondence/main_test.go`) subtract the listed `finding:table-item:missing:*` gaps from the denominator (a missing item yields a finding and no mapping); the caller fixture is `runDefaultCompaction`; the sdksurface event count derives from the pinned `types.ts` (a regex over the `ExtensionAPI` interface: 41) instead of the literal 39; a Python test for `get_all_tools` exposure, namespace and annotations.
2. `fix(parity): review the upstream 0.99.1 ledger rows ... (green)`: see below.
3. `docs(extension): record the upstream 0.99.1 extension API additions in the parity matrix`.
4. `chore(port-99): regenerate generated files`: `docs/extension-sdk-surface.md`, `custom-factory-call-surface-draft-v0.99.1.json`, and whatever `make parity` regenerates (coverage).

## What changed (green)

- **customfactoryledger** (`semantics.go`, `main.go`): reviewed rows for every new member with citations (`theme.ts:57-127,186-456,727-745`; `tui.ts:478-481,1464-1492`; `colors.ts:4-25`; `terminal-colors.ts:10-17`). `Theme.style/colors/appearance` and the `ThemeStyle`, `ThemeBg`, `ThemeToken`, `ThemeAppearance` roots are recorded "absent in Pig" (family 9a owns them), so the ledger states the gap and does not claim closure. `queryTerminalBackgroundColor` and `queryTerminalColorScheme` rows are gone (upstream `TUI` interface: `tui.ts:465-481`). The `Theme.fg/getFgAnsi` text reflects the faint-token SGR 2 (`theme.ts:367-371,391-394`). The foreground and background token lists now follow `theme.ts:57-115` (`scrollbarThumb` is a foreground token in 0.87.1 and 0.99.1, so the old list was wrong; `searchMatchBg` is new). New members are not marked "exercised by the pinned overlay examples" (`overlay-qa-tests.ts` and `overlay-test.ts` use none of them: grep).
- **sdksurface** (`sdk-surface.toml`): rows for `pi.getAllTools → exposure|namespace|annotations` (host wire `subprocess.ToolInfo`, `ui_bridge.go:396-411`), `tool execute result.structuredContent|isError` (`subprocess.ToolResult.structured_content|is_error`), and `provider model.type|output|samplingParams` (`extension.ProviderModelConfig`), each mapped to the symbol each SDK realizes; the `pi.events.*` exception removed (6F-events landed). 91 problems became 59, and the 59 are exactly the cells below.
- **correspondence** (`golang.go`, `known-gaps.toml`): the caller of `compact` is `runDefaultCompaction` (`agent-session.ts:2633-2661`; the identical edit is in fix-99-j5-ext, so the merge is clean). Two listed known gaps (`finding:table-item:missing:fullscreen-wheel-scroll-lines`, `finding:table-order:table:settings-selector`) name 9a as the owner; the closure builder still refuses while they are listed, which keeps the row pending.
- **Python SDK**: the `get_all_tools` docstring names `exposure`, `namespace` and `annotations` (it passes the host's dict through). Regression `test_get_all_tools_reports_exposure_namespace_and_annotations`, mutation-checked (dropping `exposure` in `get_all_tools` fails it).
- **docs**: `docs/extension-api-parity.md` gains the 0.99.1 additions section (Go, Rust, Python complete; Node pending); `automation/ci/version-records.toml` loses its 0.87.1 record for the regenerated matrix (the entry said to remove it).

## Still red on purpose (59 sdksurface problems), by owner

- **port-99-f6f-node** (Node runtime, `runtime-node/runtime.mjs`; Go, Rust and Python realize all of these): `pi.registerMcpServer`, `pi.unregisterMcpServer`, `pi.getMcpServers`, `pi.registerVirtualModel`, `pi.unregisterVirtualModel`, `pi.getSettings` (`loader.ts:411-494`); `tool.outputSchema|exposure|namespace|defaultActive|prepareLoadout` (`types.ts:585-610`); `tool execute result.structuredContent` (`normalizeToolResult` drops it; `agent/src/types.ts:433`). Product symptom: `examples/extensions/jev-router.ts` fails with `pi.registerVirtualModel is not a function`.
- **port-99-f6h-model-types** (all four SDKs): `ctx.modelRegistry.findOfType|getModelsOfType|getAvailableOfType|getModelOfType|classify|registerVirtualModel|unregisterVirtualModel` (`model-registry.ts:71-217`), `provider.images`, `provider.classifiers` (`types.ts:1896-1898`).
- **9a** (all four SDKs): `ctx.ui.theme.appearance|colors` (`theme.ts:313-340`); Go, Rust and Python `ctx.ui.theme.style` (`theme.ts:342-366`; Node has it through the vendored Theme).

## Not done here

- `test/parity/async-contracts.toml` rows for `executeTool`, `virtual_model_route` and the native `pi.events` realm (the f6f evidence files carry the text): the gate is clean with those files still `deferred`, and each needs its own review. Also not applied: the D86 draft for the by-value native event bus and the D83 boundary-5 wording (`docs/plan/evidence/port-99-f6f-events.md`); a divergence needs the lead's approval.
- The README sections and changelog fragments the f6f evidence lists.

## Full `make parity` (hermetic, real upstream 0.99.1 comparator, tree at the green commit)

`make parity`: 132 failing scenarios (of the hermetic set; 2 skipped by design: `05-kitty-settings-visible` headless terminal, `01-clipboard-read-deferred`), 928 s. The parity-fast rerun after f8-spec listed 136 failures with 1 new; this run has no scenario outside the same owners. Log kept in `/tmp/j5env/make-parity.txt` (not committed); failure artifacts are under the gitignored `test/parity/artifacts/`. Not fixed here: these belong to other lanes.

The reviewer's question: the Bedrock buffered and the tool/pending `message_end` cases. Every `providers-faux-streaming` Bedrock scenario, and the three `message_end` scenarios (`rpc/06-rpc-chat-faux`, `rpc/38-rpc-idle-custom-message`, `json/01-json-mode-streams-events`), pass in the full run. The only `providers-faux-streaming` failure is `19-http-proxy-connect` (below).

### By owner

- **9a (theme, system theme, header/banner, startup listing, settings and selector chrome), about 100 scenarios.** Colors differ (`38;2;138;190;...` versus Pi's 0.99.1 palette, `38;5;4` versus truecolor), `Theme` menu shows `dark` where Pi has `system` ("Theme created from your terminal's colors"), the banner is the `▀▀█` logo, the startup help lines changed (`ctrl+v to paste files on macOS, images, or text`), the skills/extensions resource listing format changed, and the export-html CSS variables (`--accent: #8abeb7` versus Pi's `#800080`).
  - interactive-rendering: 12-custom-message, 21-extension-notify-style, 21-resume-thinking-blocks, 22-exit-final-layout, 22-resume-hidden-thinking, 23-resume-assistant-terminal-state, 24-resume-file-tool-previews, 25-review-thinking-tool-boundary, 26-review-thinking-markdown, 27-compact-read-labels, 28-compact-read-labels-collapsed, 30/31/32/33/34-stock-*, 35-stock-colorfgbg-prefix, 36/37-auto-theme-*, 42-skill-invocation-expansion
  - startup 00 to 09 and 12, settings 02 to 09 and 12, selectors 01, 06, 09 to 15, footer 05/08/09/11, fullscreen 01/02, tui-components 02/16/21/22, tree/06, slash-commands 02/05/09/12/13, autocomplete 01/09/10/11, cli-utils/10, oauth (all 8 login/logout selectors), model-resolver-selector 07 and 15 to 23 (pickers), project-trust 09/10/14, compaction/11 (billing color), tools/12 (managed tool status color), export-html 01 to 06 (5 scenarios), extensions-runtime 17, 22, 24, 25, 27, 30, 33, 34-empty-custom-footer, 35, 35c, 53 (both), 56-node-lazy-imports (code highlight colors), 65.
  - `autocomplete/01-slash-popup` shows `(1/25)` for Pig and `(1/24)` for Pi: the slash command list differs by count (9b slash commands with 9a chrome); the `/mcp` command is in the Pi list and not in Pig's (next item).
- **f8-mcp / 9b (`mcp/ui.ts`, the `/mcp` manager, `McpManagerView`).** `extensions-runtime/26-extension-load-order`, `28-prompt-precedence` and `55-package-manifest-startup`: Pi's `get_commands` lists `mcp` (source `extension`, "Manage MCP servers...") after `llama`; Pig has no such command. `startup/05` and `07`: Pig lists `builtin:codemode, builtin:tool-search` in the `[Extensions]` header where Pi does not list its builtin extensions (f8-spec, with the 9a header).
- **fix-99-j5-rpc (already known):** `rpc/17-rpc-abort-retry`, `rpc/26-rpc-session-stats-context-estimate`, `rpc/41-rpc-tools-allowlist-overrides-no-tools`.
- **fix-99-j5-ext (already known):** `session/27-replaced-session-context` (the `*coding.Session` bound as `SessionManager`).
- **Pi-side probes that break against 0.99.1 (oracle scripts, not product):** `ai-sdk/01-ai-sdk-helpers` and `model-runtime-store-catalog/18-images-runtime-auth` (`providersAll.builtinImagesModels is not a function`: 0.99.1 dropped `images-models.ts`; probes `testdata/ai-sdk-pi.mjs:78`, `images-runtime.mjs:30`), `compaction/13-compaction-completion-auth-errors` (the probe asserts the 0.87.1 no-auth message; `agent-session.ts` 0.99.1 resolves summarization auth after preparation), `session/11-session-fork-label-boundaries` (`AssertionError` in the Pi test copy), `session/28-session-reload-ui` and `29-session-rebind-ui` (`RUNTIME_ORIGINAL` output empty: the upstream `5943` helper was rewritten to `createTestUiContext`), `tools/17-tool-rpc-error-and-bash-wire` (Pi emits no JSONL), `oauth/06-oauth-callback-page` (Pi probe never reaches the step: `utils/oauth-page.ts` relocation), `interactive-rendering/40-suspend-resume-session` (the probe still says `expected pinned Pi 0.87.1`). Owner: the ledgers-and-oracles lane (regenerate each oracle from real 0.99.1).
- **Real product diffs, other owners:**
  - `providers-faux-streaming/19-http-proxy-connect`: Pi makes two `CONNECT` requests per openai-completions and openai-responses call; Pig makes one (ai wire, families 2 and 3; check OpenAI SDK 7 client construction per request).
  - `extensions-runtime/29-export-tool-renderers`: the tool `parameters` object key order in `toolsAdded` (`type` first in Pi, alphabetical in Pig): tool definition schema owner (6D/6G).
  - `extensions-runtime/34-extension-process-identity`: the relaunch probe prints `0.3.0+0.99.1` where Pi prints `0.99.1` (scenario normalization of the version string; ledgers-and-oracles).
  - `selectors/10-login-subscription-providers`: Pig labels `OpenAI (ChatGPT subscription)`, Pi `OpenAI` (family 3, ChatGPT sign-in).
