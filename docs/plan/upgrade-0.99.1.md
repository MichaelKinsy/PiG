# Upgrade plan: Pi 0.87.1 → 0.99.1

Status: read-only plan, awaiting owner approval. No source, generated evidence, pin, or Git state has been changed. Mode: `upgrade v0.99.1` of the `pig-porter` skill. Constraints applied: `porter-0.99-LESSONS.md` (cited below as L1–L15) and the skill's upgrade workflow.

## 1. Target and provenance

| item | value |
|---|---|
| current pin | `0.87.1`, commit `f07218c4d4bbc12bef056a7058c3dd49dfe41abe` |
| target tag | `v0.99.1`, `git ls-remote` → `d86654abb8862e201933517d6f1fce9f88dd117f` |
| PiG release line | `0.3.0` (independent of the pin; the owner decides whether this upgrade ships as a new line, see D-F) |
| release range | tags between the two: `v0.99.0` (2026-09-29 17:21Z) and `v0.99.1` (2026-09-29 18:23Z). The changelogs list no 0.88–0.98 entries: this is one leap. 0.99.1 itself is small (GPT-6.1 Sol, default Codex model, a bundled-release `/login openai` fix). 0.99.0 carries almost everything. |
| files changed | 711 across the monorepo (242 added, 55 removed, 414 modified, `node_modules` excluded). In the four tracked packages the source-file delta that `upstreamdelta` audits is **216 files** (168 modified, 44 added, 4 removed). |

Only two tags exist in the range, so no intermediate shape needs to be preserved: the target is the 0.99.1 tree. The 0.99.0 → 0.99.1 step adds only model data, the `defaultModelPerProvider` change, and one bundling fix.

### Read-only steps performed (side effects listed)

- `./automation/gen/mirror-upstream.sh --version v0.99.1`: extracted the release into `.upstream/v0.99.1/` (gitignored; the `current` symlink still points at `v0.87.1`) and cached the tarball under `~/.cache/checkouts/github.com/earendil-works/pi/tarballs/`. The script stopped at its last check (`published pi-ai data version 0.87.1 does not match 0.99.1`, expected before the pin moves), so `.upstream/v0.99.1/.pig-upstream-source.json` is not written yet. It is written by `make upstream-mirror` after the pin changes.
- Ran the repo's TypeScript extractors against the mirror, writing to `/tmp` only: `extract-test-inventory.mjs`, `extract-cli.mjs`, `extract.mjs --origin source` (both versions, same method), `upstreamdelta -generate -from 0.87.1 -to 0.99.1`.
- Ran `make upstream-delta`, `make test-porting-release`, `make known-gaps-drift`, `make port-map-drift`, `go build ./...` on the unchanged tree. `git status --short` is empty after every step.
- Did not read `auth.json`, did not write to `~/.pig`, made no commit, branch, or stage.

## 2. Baseline

`git status` is clean at `63c6ba456`. Deterministic gates that ran and passed: `go build ./...`, `upstream-delta` (400 files for 0.84.0 → 0.87.1), `test-porting-release` (598 reviewed paths, 535 ported, 1 approved known gap `2860-replaced-session-context`), `known-gaps-drift`, `port-map-drift` (622 rows).

Not run, so the skill's "clean, passing source baseline" is **not yet proven**: `make check`, `make verify`, `make test`, `make parity`, `make test-stress`, `make parity-stress`. They need the exact 0.87.1 comparator and a long run. I propose to run `make check` and `make test-race` once, first, on the unchanged tree after approval, and record the result as the baseline before touching the pin.

The baseline carries debt that the leap inherits, and the plan must not let it look like closure:

- `upstream-sync/v0.87.1.toml`: 331 of 400 rows `deferred`, 23 `ported`, 46 `designed-out`.
- `async-contracts.toml`: 263 `deferred`, 7 `translated`, 21 `designed-out`.
- `mapping-v0.87.1.json` (interface ledger): 5,609 `pending`, 8,156 `deferred`, 31 `ported`, 7 `partial`. `interface-mapping-strict` cannot pass today.
- Behavior-input mapping: 53 of 56 handler rows and 109 of 111 render rows `deferred`.
- Test mapping: ported 535, reviewed paths 598, 1 partial hot-path with an approved 0.3.x exception.

Consequence: `make upstream-delta` for 0.99.1 will accept `deferred` rows the same way it did for 0.87.1. L1 says the tests are the port, so this plan ports the changed and new tests instead of deferring them, and does not use `deferred` for any file whose test can be ported.

## 3. Decisions the owner must make before source changes

Nothing below is assumed. Each item blocks the workstreams named.

**D-A. Comparator and published package inside the npm cooldown.** `~/.npmrc` sets `min-release-age=3`. the `@earendil-works/pi-coding-agent` 0.99.1 package was published 2026-09-29T18:23Z, so `npm install` refuses it until 2026-10-02 (`ETARGET … before 9/26/2026`). Everything that needs the published package is blocked until then: the exact `pi` comparator (`make parity-deps` checks `pi --version`), the published-origin interface inventory, `model-catalogs`, and every paired parity scenario. Options: (1) start after 2026-10-02; (2) approve a one-time install of exactly 0.99.1 (and its locked transitive set) with the cooldown lifted for that command, integrity checked against the lockfile; (3) start only the workstreams that need source alone (tests, docs, Go-only design) and hold the pin change. I recommend (3) until (1), because a supply-chain cooldown is the owner's policy to waive, not mine. Note that `undici`/`openai` are dependency-proof inputs (see D-I).

**D-B. Built-in MCP, codemode and tool-search.** 0.99.0's headline feature is new built-in extensions (`packages/coding-agent/src/extensions/{mcp,codemode,tool-search}`) over two new packages (`packages/mcp`, `packages/codemode`). `codemode` runs model-written JavaScript in a QuickJS WASM sandbox in a worker (`quickjs-wasi 3.6.2`). `AGENTS.md` forbids adding a WASM or embedded-JS runtime without an approved spec, and requires classifying each additive capability (required substrate / inert capability / Standard or Piglet / delete / divergence). PORT_MAP currently scopes the denominator to four packages and treats `mcp` only through the additive `pig-mcp-adapter` (`docs/additive-features.md`), which overlaps with upstream's new native `mcp.json`, `/mcp`, `pi mcp …` and `mcp__<server>__<tool>` naming. I need a decision on: (a) port native MCP into Stock PiG (client, stdio and streamable-HTTP transports, OAuth, `mcp.json`, `/mcp`, `pi mcp`, the new `registerMcpServer` API); (b) port codemode with which sandbox (needs a written spec, Go engine choice, and its resource bounds), or record a numbered divergence that omits codemode; (c) how `pig-mcp-adapter` and `builtin:mcp` coexist. Until decided, the `mcp`, `codemode` and `tool-search` files and their new test files (Appendix B: `mcp`, `codemode`, `tool-search`, `agent-session-mcp*`, `agent-session-codemode`, `codemode-renderer`) cannot be mapped honestly, and the shared surfaces they touch (`ToolDefinition.exposure`, `ctx.executeTool`, nested tool calls, `--no-extensions` semantics, `defaultTools` `+name/-name`) cannot be closed.

**D-C. Extension API growth across four SDKs.** 0.99.0 adds, to the Pi extension API: `registerMcpServer/unregisterMcpServer/getMcpServers`, `registerVirtualModel/unregisterVirtualModel`, `getSettings`, the `provider_stream_event` and `mcp_servers_change` events, `ToolDefinition.{exposure,namespace,annotations,outputSchema,prepareLoadout,defaultActive}`, `ctx.executeTool()`/`getCallableTools()`, `parentToolCallId` on tool events, `structuredContent`/`isError` results, `Extension.replaceable`, and `ExtensionToolContext`. `AGENTS.md` requires each capability to land in Go, Rust, Python and TypeScript with a conformance row bound to a value that differs from each SDK's fallback, plus `docs/extension-api-parity.md` rows and async contracts. Confirm this is in scope for this leap, or approve deferring named APIs with numbered, owner-approved gaps.

**D-D. System theme becomes the default.** The `system` theme derives colors from the terminal's reported foreground, background and ANSI palette (OSC 10/11/4), and light/dark now come from the reported background first. The startup header shows the pi logo plus version, the `[Themes]` banner section is gone, `dark.json`/`light.json` are rewritten in OKHSL, and `TUI.queryTerminalColorScheme/queryTerminalBackgroundColor` are replaced by `queryTerminalColors()`. This changes pixels in most interactive scenarios (`interactive-rendering`, `footer`, `startup`, `fullscreen`, `tui-components`, `selectors`, `slash-commands`, `tree`). Decide the deterministic terminal-color stimulus the parity harness answers with for both sides, and confirm scenario byte-comparators are re-derived from upstream 0.99.1 rather than normalized (AGENTS "Loop smells").

**D-E. Model data schema 6 and the image-model API break.** `ImagesModels`, `createImagesModels`, `createImagesProvider`, `openrouterImagesProvider`, `builtinImagesProviders/Models` and the plural image type names are removed; image and classifier models join `Models`/`Provider` with a `type` field; `image-models.generated.{ts,js}` and `generate-image-models.ts` are deleted; the model JSON is hydrated (`npm run hydrate-model-data`) rather than generated into a tracked `.ts` file. PiG's `make model-catalogs` (`automation/gen/generate-model-catalogs.sh`) hard-requires `image-models.generated.js`, and `cmd/gen-image-models`, `ai/images_models.go`, `ai/image_models_generated.go` are built on it. This is a rebuild of the catalog pipeline plus a breaking change to PiG's Go `ai` API (no compatibility shim, L14). Approve the rebuild and the resulting Go API change, or name a narrower target.

**D-F. Release line and test-porting policy.** The approved `deferred-0.3.x` known-gap exception (`docs/parity/KNOWN-GAPS-0.3.x.md`, owner approval 2026-09-28) is bound to 0.3.x, to `test-porting-policy-v0.87.1.json`, and to the exact 0.87.1 hashes. 112 of the 116 changed upstream test files carry the `hot-path` tag, and 0.99.1 hashes invalidate their reviewed mappings. If this ships as 0.4.x, the gate (`make test-porting-release`) rejects every pending or partial hot-path port, and the exception does not carry over. Decide the release line and whether any new gap approval will be requested (this plan requests none).

**D-G. Sign in with ChatGPT and `deviceId`.** New `openai` OAuth (`auth/oauth/openai-chatgpt.ts`) hits `auth.openai.com` and needs a localhost callback (`PI_OAUTH_CALLBACK_HOST` is a new environment variable), and Pi now writes a stable random `deviceId` into global settings. Hermetic proof needs a local fake authorization server and a fixed device ID source. Confirm no live account is used.

**D-H. Package scope for tests and mapping.** The test inventory covers all packages, including `chord` (6 new, 10 changed test files), `durable` (23 new, 6 changed) and the new `mcp` (5) and `codemode` (3). Existing `chord` and `durable` tests are `designed-out`. I propose: designed-out with the same rationale for `chord` and `durable` (a rationale per file, not a blanket), and `mcp`/`codemode` follow D-B. Also decide whether `packages/mcp` and `packages/codemode` join the interface denominator (`TRACKED_PACKAGES` in `test/parity/interface-extractor/src/inventory.mjs` is `agent, ai, coding-agent, tui`).

**D-I. `openai` SDK major bump and dependency proof.** `packages/ai` moves `openai` 6.40.0 → 7.19.0 (and `@earendil-works/pi-ai` now depends on `@earendil-works/pi-telemetry ^0.99.1`). `test/parity/dependency-sources.json` proves divergence-guard `// upstream:` citations with SHA-256-verified dependency snapshots keyed to exact versions, so each citation that points at a dependency snapshot needs re-verification, and any behavior we mirror from the `openai` SDK (retry, header, URL handling) needs a re-probe. Confirm re-snapshotting is in scope.

## 4. What changed upstream, by package

Counts are files in the release-to-release diff (`node_modules` excluded); "denominator" is the `upstreamdelta` source-file set. Appendix A lists every tracked source file with its PORT_MAP status, scenario families and Go target.

| package | added | removed | modified | in denominator | notes |
|---|---:|---:|---:|---|---|
| `agent` | 4 (1 src, 1 test) | 0 | 14 | 6 src | small |
| `ai` | 25 (14 src, 10 test) | 47 (46 src, mostly generated JSON) | 140 | 105 src | large; regenerated data |
| `coding-agent` | 54 (26 src, 23 test) | 0 | 150 | 90 src | feature center |
| `tui` | 6 (3 src, 3 test) | 0 | 32 | 15 src | colors, wheel scroll, native clipboard |
| `mcp` (new) | 32 | 0 | 0 | outside denominator | D-B |
| `codemode` (new) | 18 | 0 | 0 | outside denominator | D-B |
| `chord`, `durable`, `client`, `protocol`, `server`, `session-backends`, `telemetry`, `evals` | chord 18, durable 81 | chord 5, durable 2 | 59 (chord 26, durable 22, six small packages 11), mostly tests, docs and package metadata | outside denominator | D-H |
| root | 4 | 1 | 19 | n/a | build moved to TypeScript 7 `tsc`; `tsx` and `tsgo` removed; root `package-lock.json` added |

### 4.1 `packages/ai`

- Models and providers: one `Models`/`Provider` surface with `type: "chat" | "image" | "classifier"` (`BaseModel`, `ImageModel`, `ClassifierModel`, `AnyModel`, `ModelTypeMap`, `isModelType`, `getModelType`, `getModelsOfType/getModelOfType/getAvailableOfType/getAllModels/getAllAvailable`, `Models.generateImages`, `Models.classify`, `createProvider({ models, images, classifiers })`). `utils/model-operations.ts` and `utils/models-error.ts` (`ModelsError`, code union) are new; stream entry points now fail non-chat models with a `ModelsError`. `ModelsStoreEntry.models` accepts every type and drops unknown ones on refresh.
- Generated data: schema version 6; `providers/*.json` moves out of tracked source (hydrated); `all.json` array variants; 50 `providers/*.models.ts` and `providers/all.ts` change; new `typesafe` provider. Model data for GPT-6.1 Sol (OpenAI, Azure OpenAI Responses, OpenAI Codex), Claude Sonnet 5.5, Kimi K3 defaults, Jev classifiers on OpenRouter, Cloudflare Workers AI, Vercel AI Gateway, OpenCode Zen.
- New classifier APIs: `typesafe-system-one`, `cloudflare-workers-ai-system-one`, `llama-cpp-classify` (next-token probabilities via llama-server), shared `system-one-shared.ts`; `ClassifierResult.usage` is priced from the catalog.
- Auth: shared browser callback server (`auth/oauth/callback-server.ts`) used by Anthropic, OpenAI Codex, OpenRouter, Radius; new `openai-chatgpt.ts`; Anthropic falls back to pasting the redirect URL when the callback port is busy; provider-error sign-in no longer waits forever; Radius exchanges the code before rendering the page; `LoginOptions.getDeviceId()`. `oauth-page` moves to `utils/oauth-page` (published subpath).
- Provider fixes with wire effect (each needs a serialized-request assertion): model-level `samplingParams` on direct `stream()`/`complete()` for OpenAI-compatible APIs; OpenAI Responses unfinished tool calls end in error when `output_index` is missing; OpenAI `service_tier: "fast"` pricing; Vercel AI Gateway 1-hour Anthropic cache-write pricing; Mistral GLM empty deltas and reasoning-effort mapping; OpenCode `qwen3.8-flash` empty thinking signature replay; Copilot Opus 5.5 thinking levels; ChatGPT usage-limit errors not retried.
- New hook on every provider option type: `onProviderStreamEvent` (parsed provider events before normalization) and `AssistantMessage.thinkingLevel`.
- Removed: image-models generated files, `images-models.ts`, `openrouter-images` provider (folded into `openrouter.json`'s `openrouter-images` api group).

### 4.2 `packages/agent`

`onProviderStreamEvent` agent option; the loop records the requested `thinkingLevel` on each assistant message; `AgentTool.outputSchema`, `AgentToolResult.{structuredContent,isError}`; new `runToolCall`/`RunToolCallOptions`/`ToolCallHooks` (agent-loop refactor, needed by nested tool calls); pico3 `legacy-tracker.ts` and `view`/`session` edits (experimental Pico3, already mapped in `agent/harness/pico3`).

### 4.3 `packages/tui`

`TUI.queryTerminalColors()` replaces two query methods (breaking); new `colors.ts`/`oklab.ts` (`Color`, `parseColor` for `#rgb`/`#rrggbb`/`oklch()`/`okhsl()`, `mixColors`, `styleText`, `getTerminalColorMode`, …); `TERM=*-direct` truecolor; `wheel-scroll.ts` and `wheelScrollLines: "auto"` with `setWheelScrollLines`; `NativeClipboard.getFilePaths()` (macOS native module, binary prebuilds change); autocomplete fixes for `/skill` and wrapper characters (`(`, `[`, `{`, `<`, backtick); Kitty image stretching; mouse-forwarding input loss; overlay-closed-at-shutdown cursor; `visibleWidth` ASCII fast path, `Box` render cache, `Markdown` token reuse (performance only, but byte output must be unchanged, so the existing comparator suite is the guard).

### 4.4 `packages/coding-agent`

- Tool orchestration: `nested-tool-calls.ts`, `ExecuteToolOptions`, `parentToolCallId`, bounded `nestedCalls` on results, cost of nested calls rolled into the caller, compaction over nested calls (`compaction-nested-calls.test.ts`).
- Virtual models (`core/virtual-models.ts`, `registerVirtualModel`, footer shows the routed model, `/session` cost per physical model, `AgentSession.routedModel`, `VIRTUAL_MODEL_STATE_ENTRY` session entry) — session persistence format grows a new custom entry.
- Model runtime/registry: `generateImages`, `classify`, typed accessors, remote catalog request `types=chat,image,classifier`, extension model lists with discriminated chat/image/classifier entries, `models.json` providers keep built-in image generation.
- Settings: `fullscreenWheelScrollLines` (setting, `/settings` row, callback), `getOrCreateDeviceId`, `getSettings`, `defaultTools` `+name`/`-name` merge (project applies on top of user), `-builtin:<name>` in `extensions`; `pi config` gets a Built-in section.
- Resource loading and CLI: `builtin:<name>` naming in errors/diagnostics/RPC source info (replaces `<inline:name>`/`<builtin:name>`), `--extension builtin:<name>`, `--no-extensions` also disables built-ins (including the llama.cpp provider), `pi mcp add|remove|list|login|logout` subcommand and help text, warning when an extension replaces a built-in tool/command/flag, managed git packages no longer auto-install peer dependencies plus a warning for host-provided modules in `dependencies`, pinned git `-e` refresh after the ref changes.
- Session: the session file is created on the first user message, not on start (`#10000`), which changes persistence for a crash before the first reply.
- RPC: successful `prompt`/`steer`/`follow_up` responses carry a per-input disposition; `RpcClient.prompt()` accepts `streamingBehavior`; `RpcClient` listener-unsubscribe fix.
- Interactive: system theme, `theme.style()`/`theme.colors`/`theme.appearance`, `#rgb`/`oklch`/`okhsl` theme values and `appearance`, `ThemedText`, pi-logo header, generic tool-call rendering (`key=value` titles, expanded `key: value` lines, MCP titles `server/tool`, 5-line collapse), footer usage-total cache, theme change repaints header/resources/notices, first-time setup no longer shows detected appearance, codemode renderer.
- Tools: `bash`/`powershell` structured results hold up to 1 MiB (first and last 512 KiB kept when longer; empty output is `""` not `(no output)`; new `truncated` and `full_output_path`), `sanitizeBinaryOutput()` rewritten, `read` `null` offset/limit fix, `read` renderer.
- Clipboard: Finder-copied files paste as quoted file paths (bash mode) via `getFilePaths()`; X11 image-target misdetection fix.
- Export HTML: `H` toggle for `display:false` custom messages.
- Defaults (shared data, L9): `openai-codex` → `gpt-6.1-sol`, `fireworks`/`together`/`opencode-go` → Kimi K3, and `defaultModelPerProvider` becomes `Partial<Record<…>>` because the `typesafe` provider has chat models only for classification (no default chat model): this is the "provider with no default model" shape and needs explicit tests.
- Experimental: `client-tui.ts`, `process.ts`, `micro/runtime.ts` (READMEs) edits; PORT_MAP rows are mostly pending here already.
- Llama.cpp: classifier model per chat model, autoload preset context-window fix (`#10077`), `--no-extensions` interaction.
- Bundling: `/login openai` bundled-release fix (0.99.1) is a TypeScript packaging defect; expected N/A for Go, to be confirmed (record as designed-out with the upstream reason, not silently).
- Docs mirrors: `docs/mcp.md`, `docs/virtual-models.md` new; 19 modified docs (`cli`, `configuration`, `custom-provider`, `extensions`, `keybindings`, `llama-cpp`, `models`, `providers`, `settings`, `themes`, `tui`, …); `docs.json`; examples (`debug-provider.ts`, `jev-router.ts`, `14-codemode-mcp.ts`). `internal/pigdocs` and the docs-drift gate consume these.

## 5. Affected families

Family attribution comes from the `covers` lists of the existing parity scenarios (count = changed source files each family already claims). New behavior with no owning family is listed after the table. Family names are proposals for owner ratification.

| family (`test/parity/scenarios/…`) | changed files claimed | what re-opens |
|---|---:|---|
| `ai-sdk` | 57 | Models/Provider unification, image/classifier types, provider option hooks, OAuth shared server, fast-tier and cache pricing |
| `extensions-runtime` | 29 | extension types/runner/loader, tool exposure, nested calls, virtual models, `provider_stream_event`, built-in naming |
| `providers-faux-streaming` | 28 | `onProviderStreamEvent`, `thinkingLevel`, `samplingParams`, Responses unfinished-tool-call error, Mistral, Codex |
| `providers-registry` | 18 | catalog schema 6, default models, typesafe provider, GPT-6.1 Sol, Sonnet 5.5 |
| `rpc` | 17 | prompt/steer/follow_up disposition, `RpcClient` |
| `tools` | 13 | bash/powershell structured results, truncation, read null args, renderers |
| `session`, `model-runtime-store-catalog`, `project-trust`, `settings`, `interactive-rendering` | 11 each | routed-model entry, first-message session file, `types=` catalog requests, `mcp.json` trust, settings additions, theme/render |
| `compaction`, `oauth`, `cli-utils`, `model-resolver-selector`, `tui-components` | 8 each | nested-call compaction, ChatGPT/callback OAuth, `pi mcp`/`builtin:` CLI, default models, colors/`Box`/`Markdown` |
| `slash-commands` | 7 | `/mcp`, `/settings` row, no `[t]` tag |
| `selectors`, `startup`, `footer`, `clipboard-images` | 6, 5, 5, 5 | config Built-in section, header/logo/Themes removal, routed model, file-path paste |
| `print`, `json`, `autocomplete`, `extension-host`, `tree`, `fullscreen` | 4, 3, 3, 2, 2, 1 | wheel scroll `auto`, wrapper autocomplete |
| `export-html` | (template.js/css, not in the covers set) | `H` toggle |
| `experimental-pico3` | (1 mapped row) | `legacy-tracker.ts`, `view`, `session` |

Behavior with no current family, proposed for ratification: `builtin-extensions` (mcp, codemode, tool-search, `builtin:` naming), `virtual-models`, `model-types` (image and classifier operations; could fold into `model-runtime-store-catalog`), and a `tui-colors` slice inside `tui-components`. 75 of the 216 changed files have no scenario family at all; they need one before any `covers` claim.

## 6. Upstream tests to port (L1)

Denominator (`extract-test-inventory.mjs`): 598 → 670 files, 6,093 → 6,909 cases. Delta: **73 added files, 1 removed, 116 changed** (SHA changed).

| set | files | cases | note |
|---|---:|---:|---|
| new, tracked packages (`agent` 1, `ai` 10, `coding-agent` 22, `tui` 3) | 36 | 233 (1 + 61 + 157 + 14) | port with original inputs and expectations |
| new, `mcp` + `codemode` | 8 | 93 | D-B |
| new, `chord` + `durable` | 29 | 465 | D-H, designed-out unless the owner says otherwise |
| changed, tracked packages (`agent` 3, `ai` 48, `coding-agent` 40, `tui` 9) | 100 | 158 case ids added, 71 removed; 37 files changed an assertion or helper only | 112 of all 116 changed files are hot-path |
| changed, `chord` + `durable` | 16 | n/a | designed-out today |
| removed | 1 | `chord/test/delta-retention.test.ts` | drop the mapping row |

Rules for this leap:

1. Each new or changed file is ported in the same change as its production file (`AGENTS.md`), with the original case names, inputs and expected values. Ledger rows for changed files are re-hashed only after every changed case and body is ported. No `partial` closes a hot path.
2. Cases the Node harness cannot host (for example `vitest` fake timers, `tsx` loaders) are ported at the nearest Go caller boundary and the substitution is recorded per case in the mapping `rationale`, never as a blanket designed-out (L1).
3. Failing under load means a product race until proven otherwise (L2, L4). No test is loosened, skipped or retimed. A harness change must cite the Pi line where Pi's own caller behaves the same way.
4. Every key fix is mutation-checked: revert, see red, restore (L3).
5. Shared-path provider tests use at least four provider shapes: an OAuth provider (Anthropic or the new ChatGPT-on-OpenAI), an API-key provider, an OpenAI-compatible provider on a custom base URL, and a provider with no default chat model (the new `typesafe` case); both `openai-completions` and `openai-responses` where reached. `test-faux` and Copilot alone do not count (`AGENTS.md`).
6. Regression cases for poisoned history (errored/aborted assistant turns, empty text blocks, orphaned tool calls, model/provider switches) are added where the change touches transcript conversion (`thinkingLevel`, nested calls, virtual-model entries, structured results).

Appendices B and C list every file.

## 7. Public API changes

Authoritative numbers come from `make interface-proposals` with the published 0.99.1 package (blocked by D-A). The counts below come from a source-origin extraction of both trees with the same method and the 0.87.1 package as the type dependency root, so they are a close estimate: 919 interface IDs added, 190 removed, 309 changed in the four packages (0.87.1 inventory: 13,739 IDs; extraction of the unchanged 0.87.1 tree with this method matches the committed ID set exactly and differs in 90 shape hashes, all because of dependency resolution, not source).

**Breaking upstream changes** that force Go API changes (no shims):

- `ai`: removal of `createImagesModels`, `createImagesProvider`, `ImagesProvider`, `ImagesModels`, `MutableImagesModels`, `ImagesModel`, `ImagesApi`, `KnownImagesApi`, `KnownImagesProvider`, `ImagesProviderId`, `CreateImagesProviderOptions`; `Model`, `Provider`, `Models`, `MutableModels`, `ModelsStoreEntry`, `hasApi`, `calculateCost`, `modelsAreEqual`, `OAuthAuth`, every `*Options` type change; new `ModelsError`, `LoginOptions`, `Classifier*` types, `NestedToolCalls`.
- `tui`: `queryTerminalColorScheme`, `queryTerminalBackgroundColor`, `parseOsc11BackgroundColor` removed; `queryTerminalColors` and `TerminalColors` added; the `Color`/`TextStyle`/`styleText` family (88 new IDs).
- `coding-agent`: `ProviderModelConfig` field properties removed (moved into a discriminated model-config union), `ProviderConfig.{images,classifiers}`, `ModelRuntime`/`ModelRegistry` typed accessors and `classify`/`generateImages`, `SettingsManager`/`SettingsConfig` additions, `Theme` gains `style/colors/appearance`, `PromptOptions.preflightResult`, `AgentSession.steer/followUp` return dispositions, `RpcClient.prompt/steer/followUp`.
- `agent`: additive (`runToolCall`, `onProviderStreamEvent`, `outputSchema`, `structuredContent`, `isError`, `thinkingLevel`).

**Not seen by the CLI inventory.** `cli-v0.99.1` (64 IDs) equals `cli-v0.87.1` exactly, yet `--help` text changed (`mcp <command>`, `--extension … builtin:<name>`, `--no-extensions` wording) and the `pi mcp` subcommand family is new. The extractor does not cover subcommand trees for `mcp`, so these need a hand-written behavior contract (or an extractor extension approved by the owner).

**Tooling that breaks on 0.99.1 and must be fixed before regeneration:**

1. `make behavior-input-inventory` fails: `behavior-input-extractor: duplicate renderer id render:packages/coding-agent/src/extensions/mcp/ui.ts#McpManagerView.render`. A local `render` arrow function inside a method collides with the class's `render` method. Fix in `test/parity/interface-extractor/src/behavior-input-inventory.mjs` (owner-approval item because it changes an evidence generator).
2. `make model-catalogs` requires `image-models.generated.js`, which 0.99.1 does not ship (D-E).
3. `make upstream-mirror` needs the published package data at the new version (D-A).
4. `test-porting-policy`, `test-mapping`, `mapping`, `behavior-input-mapping`, `cli`, `recommendations`, `upstream-tests`, `upstream-v…` and `delta-v0.87.1-v0.99.1` inventories are all versioned by file name; the 0.87.1 files are the review baseline and stay until the new ones are complete.

**Extension SDKs.** `extensions/sdk-ts` pins `@earendil-works/pi-coding-agent` `0.87.1` (peer and dev); it moves to `0.99.1` together with the pin, and `make test-sdk-ts` verifies the exact declarations. Go, Rust and Python SDK surfaces change per D-C (`make sdk-surface-drift`).

**Docs.** Shipped docs bundle (`internal/pigdocs`) and the docs-drift gate follow the 19 modified and 2 new coding-agent docs.

## 8. Likely divergences (candidates only; none is created without approval)

Each candidate is a hypothesis to probe against the 0.99.1 executable first. The skill forbids adding a divergence without stopping for approval, and `AGENTS.md` requires user-visible or interop-relevant cause, `SCRUTINIZED:approved`, a remove-when condition, call-site markers and coverage.

| candidate | cause | if approved |
|---|---|---|
| codemode sandbox engine | QuickJS WASM in a worker has no drop-in Go equivalent; embedded-JS runtime is forbidden without an approved spec (D-B) | numbered divergence or approved spec |
| MCP client stack | native Go client for stdio and streamable HTTP with OAuth, versus `pig-mcp-adapter`; naming and `builtin:mcp` coexistence (D-B) | possible additive-features entry, not a divergence, if `builtin:mcp` is exact |
| `Sign in with ChatGPT` | live-account behavior cannot be a hermetic oracle; device-ID storage in settings (D-G) | test limitation, not a divergence, unless the flow differs |
| system theme terminal queries | Windows console and non-tty paths; timing of OSC replies (D-D) | verify per platform, then decide |
| session file created at first user message | Go session persistence and the Pico3/JSONL storage layers must match the new crash-before-reply state | expected to be exact; probe first |
| nested-call `nestedCalls` bounds and cost roll-up | concurrency ownership across tool workers | expected to be exact; race-test |
| `getFilePaths()` native clipboard | Pi uses a macOS N-API module; PiG's native clipboard path differs by platform (`docs/parity/native-clipboard-linux.md`) | Linux/Windows paths likely n/a for Pi too; record per platform |
| Node/Bun-only build changes | TypeScript 7, `tsx` removal, bundling (`/login openai` `openai-chatgpt.js` missing) | designed-out with upstream reason |
| existing D-entries | re-probe every active divergence against 0.99.1 (`AGENTS.md`: a prior approval is evidence, not permission); D82/D83 touch stream observation and share code with `onProviderStreamEvent` and `runToolCall` | keep, reclassify, or delete |

## 9. Hermeticity and time-dependent behavior (L10)

New in 0.99.x and requiring oracle isolation before any scenario is derived from Pi:

- `PI_SKIP_VERSION_CHECK` still governs the version check (`utils/version-check.ts`); confirm no additional startup network call. The catalog request now carries `types=chat,image,classifier` (`model-catalog-protocol.test.ts` shows the shape).
- Terminal colors: the default theme now depends on OSC 10/11/4 replies and the light/dark report; the oracle terminal (tmux) must answer identically for Pi and PiG (D-D).
- `PI_OAUTH_CALLBACK_HOST` (new): local callback servers for Anthropic, Codex, OpenRouter, Radius, ChatGPT and MCP OAuth. Tests bind loopback ports; use ephemeral ports and fake authorization servers.
- `deviceId`: random per install, written to global settings and omitted from bug reports; fix the source for tests.
- MCP: stdio servers spawn processes, `mcp.log` rotation at 5 MB, retry backoff on 408/429/5xx (two retries), 10 s startup wait. Each is a bounded process/time contract needing cancellation and cleanup evidence (L4, L5: a killed but unreaped stdio server must be marked dead on the first unexpected close).
- Kernel and runtime facts are asked, not assumed (L11): run on Node 22, 24 and 26 for the TypeScript-side tools; Windows/macOS/Linux for the platform paths (`getFilePaths()`, taskkill, console color queries).

## 10. Verification order

Ordered by dependency: contracts before code, foundations before callers, and joint runs early (L15). Every gate below uses maintained Make targets.

**Phase 0: baseline (before approval-dependent changes).** Run `make check` and `make test-race` on the unchanged tree; record the result. Resolve D-A.

**Phase 1: pin and evidence generation** (after approval; branch-neutral, no commits).
1. Change the upstream pin in `internal/coding/pigversion/pigversion.go` (`UpstreamVersion`, `UpstreamCommit`) and the exact Pi dependency in `extensions/sdk-ts` (peer and dev dependency plus lock). `make set-version` changes the PiG line, not the pin, and waits for D-F.
2. `make upstream-mirror`, `make model-catalogs` (after D-E work), `make interface-proposals`, `make test-inventory-generate`, `make behavior-input-inventory` (after fixing the extractor), `go run ./test/parity/cmd/upstreamdelta -generate -from 0.87.1 -to 0.99.1`, `make interface-delta` (L12: generated files are regenerated centrally, never hand-edited).
3. Add PORT_MAP rows for the 44 added source files and remove the rows of the 4 removed ones, then run `make port-map-drift` and `make upstream-delta` (expected red until every row has a non-pending disposition and evidence).

**Phase 2: foundations (families in dependency order).** Each family uses the skill's family workflow (probe the exact 0.99.1 executable first, red-proof, smallest source fix, re-probe callers, `make parity-family FAMILY=…`, `go test -count=3` for concurrency, process or Session ownership).
1. `ai` data and types: catalog pipeline (D-E), `type` on models, `Models`/`Provider` unification, `ModelsError`, defaults incl. no-default-model shape → families `providers-registry`, `model-runtime-store-catalog`, `ai-sdk`.
2. `ai` wire behavior: `onProviderStreamEvent`, `thinkingLevel`, `samplingParams`, Responses unfinished-tool-call error, fast-tier and Vercel cache pricing, Mistral, OpenCode, Copilot → `providers-faux-streaming`, `ai-sdk`.
3. `ai` classifiers and OAuth: system-one APIs, llama-cpp-classify, shared callback server, ChatGPT sign-in → `oauth`, `ai-sdk`.
4. `agent`: `runToolCall`, `thinkingLevel`, structured results → agent loop tests, `experimental-pico3`.
5. `tui`: colors/OKLab, `queryTerminalColors`, wheel scroll, autocomplete wrappers, Kitty stretching, mouse forwarding, native clipboard `getFilePaths` → `tui-components`, `autocomplete`, `fullscreen`, `clipboard-images`.
6. `coding-agent` core: settings, model runtime/registry, resource loader (`builtin:` naming), extension types/runner/loader, nested tool calls, virtual models, session persistence → `settings`, `session`, `compaction`, `project-trust`, `extensions-runtime`, `extension-host` (+ the four SDKs per D-C).
7. `coding-agent` surfaces: RPC dispositions, print/json, CLI (`pi mcp`, `builtin:` flags) → `rpc`, `print`, `json`, `cli-utils`.
8. Built-in extensions per D-B: MCP, tool-search, codemode → proposed `builtin-extensions`.
9. Interactive: theme system, header/logo, generic tool-call rendering, footer, settings/config selectors, export-html toggle → `interactive-rendering`, `startup`, `footer`, `selectors`, `slash-commands`, `tree`, `export-html`.

**Phase 3: joint runs.** After families 1–3, and again after 6, run the aggregate tree through the full Go suite and `make parity-fast`; do not let branches meet only at the end (L15).

**Phase 4: hygiene before any completion claim (L13).** `make lint`, `make go-fix-clean`, `GOOS=windows go vet ./...` (and `GOOS=linux`) for touched packages, drift checks (`make ci-drift ci-contracts`, `make known-gaps-drift`, `make test-porting-release`, `make interface-inventory-drift`, `make async-contracts`, `make upstream-delta`), package tests with `-race`, `make divergence-guard`.

**Phase 5: stress and release gates (L4).** `make test-stress`, `make parity-stress`, `make verify`, then load tests for every goroutine/process/stream/session change: `-race -count=24` at least, pinned to few cores (`GOMAXPROCS=4`, `taskset`-style on Linux, CPU burners on the same cores). A 1-in-20 or 1-in-200 failure on a loaded runner is a real race. Targets: MCP stdio process reuse, codemode worker lifetime, nested-call cancellation, OAuth callback server shutdown, session-first-message file creation, `onProviderStreamEvent` ordering against the stream (hold the stream at the observation point as D82 does, L6).

**Completion report.** Shared-path Pi citations (`.upstream/v0.99.1/…:line`), provider-shape results, red/green evidence per fix (L3), the loop self-report from `AGENTS.md`, and every remaining blocker. The working tree is left for review: no stage, commit, branch, push, PR, tag or release.

## 11. Risks and unknowns

- The interface delta above is a source-origin estimate; published-package parity (`compareInventories`) is unverified until D-A.
- `upstreamdelta` reports 216 tracked source files, but the reviewed interface ledger is 13,803 mappings large with 13,765 not ported; the leap adds about 1,100 IDs of churn. The leap adds about 1,100 added or removed IDs and 309 changed IDs to that ledger. This plan targets test and behavior contracts for them, not row counts, and I will not close them by adding `deferred` rows.
- MCP, codemode and tool-search alone are about 9,600 lines of TypeScript (the two new packages plus `coding-agent/src/extensions`) with 93 test cases in the new packages and many more in `coding-agent`. They are the schedule and design risk of this leap.
- The async contract ledger needs rows for every new async source (36 new files with `async`/`Promise`; MCP `index.ts`, `oauth/flow.ts`, `client.ts`, `streamable-http.ts` and `llama-cpp-classify.ts` are the heaviest). Each needs caller-waits, ordering, cancellation and error propagation recorded before the Go port (`AGENTS.md`, async parity).
- Baseline debt (section 2) is not reduced by this plan except for files the leap touches.

## 12. Approval requested

Approve, amend or reject: (1) decisions D-A through D-I, (2) the family order in Phase 2, (3) the extractor fix for the duplicate renderer id. On approval I will start at Phase 0. I will not change the pin, generated evidence, or any source until then.

## Appendix A. Changed tracked source files (216, from `upstreamdelta -generate`)

Columns: change, current PORT_MAP status, scenario families whose `covers` name the file, current Go target (first 90 characters). `(none)` = no PORT_MAP row yet.

### packages/agent (6 files)

| file | change | status | families | Go target |
|---|---|---|---|---|
| `src/agent-loop.ts` | modified | 🟡 | compaction, providers-faux-streaming, rpc, session, tools | `partial: agent/agent_loop.go + agent/agent.go + agent/message_json.go + agent/tool_execut |
| `src/agent.ts` | modified | ✅ | compaction, print, rpc, tools | `agent/agent.go + agent/lifecycle.go + agent/queue.go (StreamFunction exposes the caller o |
| `src/harness/pico3/legacy-tracker.ts` | added | (none) | - |  |
| `src/harness/pico3/session.ts` | modified | ✅ | - | `agent/harness/pico3/session.go, session_docs.go, tx.go, tx_writes.go; transactions_test.g |
| `src/harness/pico3/view.ts` | modified | ✅ | - | `agent/harness/pico3/view.go; watch_test.go` |
| `src/types.ts` | modified | ✅ | providers-faux-streaming | `agent/types.go + agent/agent.go + agent/message_json.go` |

### packages/ai (105 files)

| file | change | status | families | Go target |
|---|---|---|---|---|
| `src/api/anthropic-messages.ts` | modified | 🟡 | providers-faux-streaming, providers-registry, rpc | `ai/direct_simple.go + ai/anthropic.go + ai/anthropic_stream.go + ai/anthropic_client.go + |
| `src/api/azure-openai-responses.ts` | modified | ✅ | providers-faux-streaming, providers-registry | `ai/direct_simple.go + ai/azure_openai_responses.go (logical model versus deployment ident |
| `src/api/bedrock-converse-stream.ts` | modified | ✅ | providers-faux-streaming, providers-registry | `ai/bedrock.go + ai/direct_simple.go (simple API dispatch preserves the selected model and |
| `src/api/cloudflare-workers-ai-system-one.lazy.ts` | added | (none) | - |  |
| `src/api/cloudflare-workers-ai-system-one.ts` | added | (none) | - |  |
| `src/api/cloudflare.ts` | modified | ✅ | ai-sdk | `ai/cloudflare.go` |
| `src/api/google-generative-ai.ts` | modified | ✅ | providers-faux-streaming, providers-registry, rpc | `ai/google.go + ai/direct_simple.go (SDK system-instruction user role and optional/strict/ |
| `src/api/google-vertex.ts` | modified | ✅ | providers-faux-streaming, providers-registry | `ai/google_vertex.go` |
| `src/api/llama-cpp-classify.lazy.ts` | added | (none) | - |  |
| `src/api/llama-cpp-classify.ts` | added | (none) | - |  |
| `src/api/mistral-conversations.ts` | modified | ✅ | providers-faux-streaming, providers-registry, rpc | `ai/mistral.go + ai/mistral_reader.go + ai/mistral_response_body.go + ai/direct_simple.go  |
| `src/api/openai-codex-responses.ts` | modified | 🟡 | providers-faux-streaming, providers-registry | `ai/direct_simple.go + ai/openai_codex_responses.go + ai/openai_codex_http.go + ai/openai_ |
| `src/api/openai-completions.ts` | modified | ✅ | extensions-runtime, json, providers-faux-streaming, providers-registry, rpc | `ai/openai_error.go + ai/direct_simple.go + ai/openai.go + ai/provider_request_options.go  |
| `src/api/openai-responses-shared.ts` | modified | ✅ | providers-faux-streaming, providers-registry | `ai/openai_responses.go + ai/openai_stream_decoder.go + ai/native_body_iterator.go (SDK it |
| `src/api/openai-responses.ts` | modified | ✅ | providers-faux-streaming, providers-registry | `ai/openai_error.go + ai/openai_responses.go + ai/direct_simple.go + ai/provider_request_o |
| `src/api/openrouter-images.lazy.ts` | modified | n/a | - | `(lazy dynamic-import wrapper; Go providers are statically linked)` |
| `src/api/openrouter-images.ts` | modified | ✅ | ai-sdk, model-runtime-store-catalog, providers-faux-streaming | `ai/openrouter_images.go` |
| `src/api/pi-messages.ts` | modified | ✅ | providers-faux-streaming | `ai/pi_messages.go + ai/pi_messages_events.go + ai/pi_messages_reader.go (ai/pi_messages_t |
| `src/api/simple-options.ts` | modified | ✅ | providers-faux-streaming | `ai/simple_options.go` |
| `src/api/system-one-shared.ts` | added | (none) | - |  |
| `src/api/typesafe-system-one.lazy.ts` | added | (none) | - |  |
| `src/api/typesafe-system-one.ts` | added | (none) | - |  |
| `src/auth/helpers.ts` | modified | ✅ | ai-sdk, providers-faux-streaming, providers-registry | `ai/auth_login.go + ai/auth.go + ai/auth_store.go + ai/auth_resolve.go + ai/auth_providers |
| `src/auth/oauth/anthropic.ts` | modified | ✅ | oauth | `ai/oauth_anthropic.go` |
| `src/auth/oauth/callback-server.ts` | added | (none) | - |  |
| `src/auth/oauth/load.ts` | modified | ✅ | ai-sdk | `ai/oauth_registry.go` |
| `src/auth/oauth/oauth-page.ts` | removed | ✅ | oauth | `ai/oauth_page.go` |
| `src/auth/oauth/openai-chatgpt.ts` | added | (none) | - |  |
| `src/auth/oauth/openai-codex.ts` | modified | ✅ | oauth | `ai/oauth_openai_codex.go` |
| `src/auth/oauth/openrouter.ts` | modified | ✅ | oauth | `ai/oauth_openrouter.go (OpenRouter OAuth login/exchange; registered in oauth_registry.go; |
| `src/auth/oauth/radius.ts` | modified | ✅ | - | `ai/oauth_radius.go + internal/codingagent/interactive_auth.go (ai/oauth_radius_test.go, i |
| `src/auth/resolve.ts` | modified | ✅ | providers-registry, startup | `ai/auth.go + ai/auth_store.go + ai/auth_resolve.go + ai/auth_providers.go + ai/oauth_*.go |
| `src/auth/types.ts` | modified | ✅ | ai-sdk, oauth, providers-faux-streaming, providers-registry | `ai/auth.go + ai/auth_store.go + ai/auth_resolve.go + ai/auth_interaction.go + ai/auth_pro |
| `src/bun-oauth.ts` | modified | n/a | - | `(Bun-only OAuth flow registration; Go links OAuth implementations directly)` |
| `src/cli.ts` | modified | ✅ | cli-utils | `cmd/pig/auth_commands.go (login/logout)` |
| `src/env-api-keys.ts` | modified | ✅ | oauth, providers-registry | `ai/auth_env_keys.go` |
| `src/image-models.generated.ts` | removed | ✅ | ai-sdk | `ai/image_models_generated.go` |
| `src/image-models.ts` | modified | ✅ | ai-sdk | `ai/images_registry.go` |
| `src/images-api-registry.ts` | modified | ✅ | ai-sdk | `ai/images.go + ai/images_registry.go` |
| `src/images-models.ts` | removed | ✅ | ai-sdk, model-runtime-store-catalog | `ai/images_models.go (provider lifecycle, coalesced refresh, auth/options merging and gene |
| `src/images.ts` | modified | ✅ | ai-sdk | `ai/images.go` |
| `src/index.ts` | modified | n/a | - | `ai/doc.go` |
| `src/model-catalog.ts` | modified | n/a | - | `designed out: pig flattens the API-grouped catalog at cmd/gen-models codegen into models_ |
| `src/models-store.ts` | modified | ✅ | providers-faux-streaming | `ai/models_store.go (ModelsStoreEntry, ModelsStore, InMemoryModelsStore; ai/models_store_t |
| `src/models.generated.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go` |
| `src/models.ts` | modified | ✅ | ai-sdk, model-resolver-selector, model-runtime-store-catalog, providers-faux-streaming | `ai/models_runtime*.go + ai/models_provider.go + ai/models_catalog_codec.go + ai/models_op |
| `src/providers/all.ts` | modified | ✅ | ai-sdk | `ai/registry.go + ai/register_builtins.go + ai/images_registry.go` |
| `src/providers/amazon-bedrock.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/ant-ling.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/anthropic.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/azure-openai-responses.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/baseten.models.ts` | modified | n/a | - | `(not ported: Baseten provider declined)` |
| `src/providers/cerebras.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/cloudflare-ai-gateway.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/cloudflare-stream.ts` | modified | ✅ | ai-sdk, providers-faux-streaming | `ai/cloudflare.go` |
| `src/providers/cloudflare-workers-ai.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/cloudflare-workers-ai.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalo |
| `src/providers/deepseek.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/fireworks.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/github-copilot.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/google-vertex.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/google.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/groq.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/huggingface.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/images/register-builtins.ts` | modified | ✅ | ai-sdk | `ai/images_registry.go` |
| `src/providers/kimi-coding.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/meta.models.ts` | modified | ✅ | - | `ai/models_generated.go (0.87.1 catalog shard; ai/meta_provider_test.go)` |
| `src/providers/minimax-cn.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/minimax.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/mistral.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/moonshotai-cn.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/moonshotai.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/nvidia.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/openai-codex.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/openai-codex.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go + internal/codingagent/model_registry.go (OpenAI Codex provider me |
| `src/providers/openai.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/openai.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go + internal/codingagent/model_registry.go (OpenAI provider metadata |
| `src/providers/opencode-go.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/opencode.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/opencode.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalo |
| `src/providers/openrouter-images.ts` | removed | ✅ | ai-sdk, model-runtime-store-catalog | `ai/images_models.go + ai/images_registry.go + ai/openrouter_images.go (native image provi |
| `src/providers/openrouter.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/openrouter.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalo |
| `src/providers/qwen-token-plan-cn.models.ts` | modified | ✅ | - | `ai/models_generated.go (0.81 catalog shard input)` |
| `src/providers/qwen-token-plan-individual.models.ts` | modified | ✅ | - | `ai/models_generated.go (0.87.1 catalog shard; qwen-token-plan-models.test.ts cases in ai/ |
| `src/providers/qwen-token-plan.models.ts` | modified | ✅ | - | `ai/models_generated.go (0.81 catalog shard input)` |
| `src/providers/radius.models.ts` | modified | ✅ | - | `ai/models_generated.go (0.87.1 radius shard, served for the default gateway by ai/radius. |
| `src/providers/together.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/typesafe.models.ts` | added | (none) | - |  |
| `src/providers/typesafe.ts` | added | (none) | - |  |
| `src/providers/vercel-ai-gateway.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/vercel-ai-gateway.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go + internal/codingagent/model_registry.go (provider metadata/catalo |
| `src/providers/xai.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/xiaomi-token-plan-ams.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/xiaomi-token-plan-cn.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/xiaomi-token-plan-sgp.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/xiaomi.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/zai-coding-cn.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/providers/zai.models.ts` | modified | ✅ | ai-sdk | `ai/models_generated.go (0.80 catalog shard input)` |
| `src/types.ts` | modified | ✅ | providers-faux-streaming | `ai/types.go + ai/model_compat_json.go + ai/provider_streams.go + ai/deferred_option.go +  |
| `src/utils/headers.ts` | modified | ✅ | providers-registry | `ai/githubcopilot.go + ai/provider_response.go` |
| `src/utils/model-operations.ts` | added | (none) | - |  |
| `src/utils/models-error.ts` | added | (none) | - |  |
| `src/utils/oauth-page.ts` | added | (none) | - |  |
| `src/utils/retry.ts` | modified | ✅ | providers-faux-streaming | `ai/assistant_retry.go (RetryAssistantCall, RetryDelayMs, IsRetryableAssistantError; assis |

### packages/coding-agent (90 files)

| file | change | status | families | Go target |
|---|---|---|---|---|
| `src/bun/quickjs-wasm.d.ts` | added | (none) | - |  |
| `src/bun/runtime-setup.ts` | modified | n/a | - | `(Bun-only runtime setup)` |
| `src/cli/args.ts` | modified | ✅ | cli-utils, model-resolver-selector, project-trust | `cmd/pig/args.go` |
| `src/cli/startup-ui.ts` | modified | ✅ | cli-utils, project-trust, session | `internal/codingagent/startup_ui.go (ShowStartupSelector/ShowStartupInput/SelectStartupSes |
| `src/config.ts` | modified | ✅ | cli-utils, selectors, settings | `internal/codingagent/paths.go + internal/codingagent/selfupdate_package_command.go + inte |
| `src/core/agent-session-runtime.ts` | modified | 🟡 | extensions-runtime, project-trust, rpc, session | `partial: coding/runtime.go, coding/runtime_replacement.go, coding/session_bind.go, coding |
| `src/core/agent-session-services.ts` | modified | ✅ | extensions-runtime | `coding/services.go` |
| `src/core/agent-session.ts` | modified | 🟡 | compaction, extensions-runtime, interactive-rendering, json, model-resolver-selector, model-runtime-store-catalog, print, providers-faux-streaming, rpc, session, slash-commands, tools | `coding/session.go + coding/session_boundaries.go + coding/session_bind.go + coding/sessio |
| `src/core/bug-report.ts` | modified | ✅ | - | `internal/codingagent/bug_report.go (export subset: metadata, redaction, diagnostics, arch |
| `src/core/compaction/compaction.ts` | modified | ✅ | compaction, rpc | `internal/codingagent/compaction/compaction.go (internal/codingagent/compaction/compaction |
| `src/core/compaction/utils.ts` | modified | ✅ | compaction | `internal/codingagent/compaction/utils.go + agent/harness/compaction/utils.go (shared file |
| `src/core/crash-log.ts` | modified | 🟡 | - | `internal/codingagent/crash_log.go + interactive_crash.go (crash persistence, startup noti |
| `src/core/extensions/index.ts` | modified | n/a | - | `(barrel)` |
| `src/core/extensions/loader.ts` | modified | 🟡 | extension-host, extensions-runtime | `cmd/pig/extensions.go + coding/extension/provider_runtime.go (shared ordered provider que |
| `src/core/extensions/runner.ts` | modified | 🟡 | extensions-runtime, json, print, project-trust, providers-faux-streaming, rpc | `coding/extension/host/inproc/runner.go + coding/extension/provider_runtime.go + coding/ex |
| `src/core/extensions/types.ts` | modified | 🟡 | extension-host, extensions-runtime, rpc | `partial: coding/extension/*.go (Layer 0, including synchronized late tool registrations i |
| `src/core/extensions/wrapper.ts` | modified | ✅ | extensions-runtime, tools | `coding/extension_bridge.go + coding/session_tool_registry.go` |
| `src/core/keybindings.ts` | modified | ✅ | extensions-runtime, tree | `internal/codingagent/keybindings.go` |
| `src/core/mcp-servers.ts` | added | (none) | - |  |
| `src/core/model-registry.ts` | modified | ✅ | extensions-runtime, model-runtime-store-catalog, providers-registry | `internal/codingagent/model_registry.go + internal/codingagent/extension_model_registry.go |
| `src/core/model-resolver.ts` | modified | ✅ | model-resolver-selector, model-runtime-store-catalog, oauth, startup | `cmd/pig/model.go + cmd/pig/startup_model.go + cmd/pig/resolve_cli_model.go + coding/exten |
| `src/core/model-runtime.ts` | modified | ✅ | extensions-runtime, model-resolver-selector, model-runtime-store-catalog, providers-faux-streaming, providers-registry, selectors | `internal/codingagent/model_registry.go + internal/codingagent/provider_registration_valid |
| `src/core/nested-tool-calls.ts` | added | (none) | - |  |
| `src/core/package-manager.ts` | modified | ✅ | cli-utils, extensions-runtime, project-trust, startup | `coding/packagecontent/packagecontent.go, coding/packagecontent/glob.go, coding/packagecon |
| `src/core/provider-composer.ts` | modified | ✅ | footer, model-runtime-store-catalog, providers-faux-streaming | `coding/extension/provider.go + coding/registered_stream_provider.go + coding/model_stream |
| `src/core/remote-catalog-provider.ts` | modified | ⬜ | - | `(not implemented: owner-approved remote catalog refresh through PiG's own endpoint; runti |
| `src/core/resource-loader.ts` | modified | ✅ | extensions-runtime, project-trust, rpc, settings, slash-commands | `coding/resource_loader.go (native DefaultResourceLoader for skills, prompt templates, con |
| `src/core/sdk.ts` | modified | 🟡 | extensions-runtime, footer, model-runtime-store-catalog, providers-faux-streaming, rpc, session, tools | `coding/session.go + coding/resource_loader.go (resourceLoader option; DefaultResourceLoad |
| `src/core/session-manager.ts` | modified | 🟡 | compaction, extensions-runtime, print, rpc, session, slash-commands | `internal/codingagent/session_manager.go + internal/codingagent/session_manager_accessors. |
| `src/core/settings-manager.ts` | modified | ✅ | compaction, extensions-runtime, project-trust, settings | `coding/settings.go + internal/codingagent/settings.go + internal/codingagent/settings_com |
| `src/core/source-info.ts` | modified | ✅ | extensions-runtime, selectors | `internal/codingagent/resource_source_info.go + cmd/pig/resource_source_info.go + cmd/pig/ |
| `src/core/system-prompt.ts` | modified | ✅ | extensions-runtime, session, slash-commands, tools | `internal/codingagent/prompts/coding.go (default/explicit-empty tools and opaque forced pr |
| `src/core/tools/bash.ts` | modified | ✅ | interactive-rendering, rpc, session, tools | `internal/codingagent/tools/bash.go + internal/codingagent/tools/bash_operations.go + inte |
| `src/core/tools/output-accumulator.ts` | modified | ✅ | tools | `internal/codingagent/tools/output_accumulator.go + truncate.go` |
| `src/core/tools/render-utils.ts` | modified | ✅ | tools | `internal/codingagent/tool_render.go` |
| `src/core/tools/renderers/bash.ts` | modified | ✅ | - | `tui/shell_renderers.go (formatShellCall, formatDuration) + tui/tool_execution.go (live el |
| `src/core/tools/renderers/read.ts` | modified | 🟡 | interactive-rendering | `partial: tui/tool_execution.go (FormatReadHeader) + internal/codingagent/tool_render.go ( |
| `src/core/tools/tool-definition-wrapper.ts` | modified | ✅ | extensions-runtime, session, tools | `coding/extension_bridge.go + coding/session_tool_registry.go` |
| `src/core/tools/truncate.ts` | modified | ✅ | tools | `internal/codingagent/tools/truncate.go` |
| `src/core/trust-manager.ts` | modified | ✅ | project-trust, settings | `internal/codingagent/trust_manager.go + internal/pilock/lock.go (unit-tested + project-tr |
| `src/core/usage-totals.ts` | modified | ✅ | footer, slash-commands | `internal/codingagent/session_stats.go + internal/codingagent/status_line.go + internal/co |
| `src/core/virtual-models.ts` | added | (none) | - |  |
| `src/experimental/client-tui.ts` | modified | 🟡 | - | `internal/experimental/client_tui.go + client_tui_sources.go (service-only fullscreen clie |
| `src/experimental/micro/runtime.ts` | modified | ⬜ | - | `(pending: owns ModelRuntime, Pico3 harness, JSONL storage; default model openai-codex/gpt |
| `src/experimental/process.ts` | modified | ⬜ | - | `(pending: spawns detached coordinator/server/session-worker roles via __PI_INTERNAL_SPAWN |
| `src/extensions/codemode/execute.lazy.ts` | added | (none) | - |  |
| `src/extensions/codemode/execute.ts` | added | (none) | - |  |
| `src/extensions/codemode/index.ts` | added | (none) | - |  |
| `src/extensions/codemode/renderer.ts` | added | (none) | - |  |
| `src/extensions/codemode/tool.ts` | added | (none) | - |  |
| `src/extensions/codemode/worker.ts` | added | (none) | - |  |
| `src/extensions/index.ts` | modified | ✅ | - | `cmd/pig/llama.go + internal/codingagent/llama/host.go (built-in llama.cpp provider and /l |
| `src/extensions/llama/provider.ts` | modified | ✅ | - | `internal/codingagent/llama/provider.go + internal/codingagent/llama/host.go` |
| `src/extensions/mcp/cli.lazy.ts` | added | (none) | - |  |
| `src/extensions/mcp/cli.ts` | added | (none) | - |  |
| `src/extensions/mcp/config.ts` | added | (none) | - |  |
| `src/extensions/mcp/index.ts` | added | (none) | - |  |
| `src/extensions/mcp/log.ts` | added | (none) | - |  |
| `src/extensions/mcp/oauth.ts` | added | (none) | - |  |
| `src/extensions/mcp/resources.ts` | added | (none) | - |  |
| `src/extensions/mcp/runtime.lazy.ts` | added | (none) | - |  |
| `src/extensions/mcp/runtime.ts` | added | (none) | - |  |
| `src/extensions/mcp/tools.ts` | added | (none) | - |  |
| `src/extensions/mcp/ui.ts` | added | (none) | - |  |
| `src/extensions/tool-search/index.ts` | added | (none) | - |  |
| `src/extensions/tool-search/tool.ts` | added | (none) | - |  |
| `src/index.ts` | modified | 🟡 | - | `coding/extension/host/subprocess/runtime-node/shims/pi-coding-agent.mjs (pinned Node barr |
| `src/main.ts` | modified | ✅ | cli-utils, clipboard-images, extensions-runtime, model-resolver-selector, project-trust, providers-registry, session, startup | `cmd/pig/main.go + cmd/pig/startup_session.go + cmd/pig/extensions.go (mode/metadata routi |
| `src/modes/interactive/components/config-selector.ts` | modified | ✅ | selectors | `tui/config_selector.go` |
| `src/modes/interactive/components/first-time-setup.ts` | modified | n/a | - | `(designed out: upstream gates this component to the upstream package identity; Stock P |
| `src/modes/interactive/components/footer.ts` | modified | ✅ | extensions-runtime, footer | `internal/codingagent/status_line.go + internal/codingagent/interactive_auth.go` |
| `src/modes/interactive/components/pi-logo.ts` | added | (none) | - |  |
| `src/modes/interactive/components/settings-selector.ts` | modified | ✅ | clipboard-images, settings | `tui/settings_list.go + internal/codingagent/slash_session_handlers.go` |
| `src/modes/interactive/components/themed-text.ts` | added | (none) | - |  |
| `src/modes/interactive/components/tool-execution.ts` | modified | ✅ | extensions-runtime, interactive-rendering | `tui/tool_execution.go (shell elapsed Text wrapping covered for bash/powershell output, no |
| `src/modes/interactive/interactive-mode.ts` | modified | 🟡 | autocomplete, compaction, extensions-runtime, footer, interactive-rendering, model-resolver-selector, model-runtime-store-catalog, oauth, project-trust, selectors, session, settings, slash-commands, startup, tools, tree | `partial: internal/codingagent/interactive.go (formatResumeCommand preserves TTY/persisten |
| `src/modes/interactive/theme/system-theme.ts` | added | (none) | - |  |
| `src/modes/interactive/theme/theme-controller.ts` | modified | ✅ | interactive-rendering | `internal/codingagent/interactive_theme.go + interactive_theme_modal.go (startup detection |
| `src/modes/interactive/theme/theme-json.ts` | modified | ✅ | - | `tui/theme_json.go (ValidateThemeJSON, wired in tui/theme_loader.go LoadThemeFile; upstrea |
| `src/modes/interactive/theme/theme.ts` | modified | ✅ | extensions-runtime, interactive-rendering, selectors, settings | `tui/theme.go + tui/theme_loader.go + tui/theme_watcher.go (indexed-color load/var/ANSI/CS |
| `src/modes/interactive/tui-renderer.ts` | modified | ✅ | - | `internal/codingagent/interactive_tui.go (createInteractiveTui, fullscreenTuiOptions), int |
| `src/modes/rpc/rpc-client.ts` | modified | ✅ | - | `coding/rpcclient/rpc_client.go + commands.go + types.go + process_unix.go/process_windows |
| `src/modes/rpc/rpc-mode.ts` | modified | ✅ | extensions-runtime, rpc | `cmd/pig/rpc_mode.go + cmd/pig/rpc_dispatch.go + cmd/pig/rpc_admission.go + cmd/pig/rpc_js |
| `src/modes/rpc/rpc-types.ts` | modified | ✅ | rpc | `cmd/pig/rpc_types.go` |
| `src/package-manager-cli.ts` | modified | ✅ | cli-utils, project-trust | `cmd/pig/package_commands.go + cmd/pig/cli_error.go + cmd/pig/package_command_trust.go + c |
| `src/utils/clipboard-image.ts` | modified | 🟡 | clipboard-images | `internal/codingagent/clipboard.go + internal/codingagent/clipboard_paste.go (backend read |
| `src/utils/clipboard.ts` | modified | ✅ | clipboard-images | `internal/codingagent/clipboard.go + internal/codingagent/clipboard_copy.go + internal/cod |
| `src/utils/paths.ts` | modified | ✅ | cli-utils, extensions-runtime | `internal/resolvepath/resolvepath.go; internal/codingagent/paths.go, canonical_path_other. |
| `src/utils/shell.ts` | modified | 🟡 | providers-faux-streaming, tools | `internal/codingagent/tools/shell_config.go + shell_config_unix.go + shell_config_windows. |
| `src/utils/syntax-highlight.ts` | modified | ✅ | tui-components | `tui/highlight.go` |

### packages/tui (15 files)

| file | change | status | families | Go target |
|---|---|---|---|---|
| `src/autocomplete.ts` | modified | ✅ | autocomplete, extensions-runtime | `tui/autocomplete.go + tui/file_autocomplete.go` |
| `src/colors.ts` | added | (none) | - |  |
| `src/components/box.ts` | modified | ✅ | tui-components | `tui/box.go` |
| `src/components/editor.ts` | modified | 🟡 | autocomplete, extensions-runtime, interactive-rendering, settings, tui-components | `tui/editor.go + tui/editor_segments.go + tui/editor_state.go + tui/editor_autocomplete_un |
| `src/components/markdown.ts` | modified | ✅ | interactive-rendering, slash-commands, tui-components | `tui/markdown.go + tui/markdown_options.go + tui/markdown_style.go + tui/markdown_list.go` |
| `src/index.ts` | modified | 🟡 | - | `partial: coding/extension/host/subprocess/runtime-node/shims/pi-tui.mjs re-exports pinned |
| `src/native-platform.ts` | modified | 🟡 | - | `partial: tui/native_platform.go + internal/nativeplatform/clipboard*.go + coding/extensio |
| `src/oklab.ts` | added | (none) | - |  |
| `src/terminal-colors.ts` | modified | ✅ | interactive-rendering, tui-components | `tui/terminal_colors.go; tui/theme.go (ParseOsc11BackgroundColor, parseOscHexChannel)` |
| `src/terminal-image.ts` | modified | ✅ | clipboard-images, interactive-rendering, settings | `tui/terminal_image.go` |
| `src/terminal.ts` | modified | 🟡 | settings, tui-components | `tui/terminal.go + tui/terminal_input.go + tui/terminal_reader.go + tui/input_timer.go + t |
| `src/tui-alt-screen.ts` | modified | ✅ | fullscreen | `tui/tui_alt_screen.go, tui/tui_alt_screen_input.go, tui/tui_alt_screen_mouse.go, tui/tui_ |
| `src/tui.ts` | modified | 🟡 | extensions-runtime, settings, tui-components | `tui/tui.go, tui/terminal_color_query.go (FIFO background-query lifetime), tui/overlay_com |
| `src/utils.ts` | modified | ✅ | model-resolver-selector, tui-components | `tui/widthx/*.go + internal/wordsegmenter/segments.go + internal/wordsegmenter/cjk.go + in |
| `src/wheel-scroll.ts` | added | (none) | - |  |

## Appendix B. New upstream test files (73)

| file | cases | disposition needed |
|---|---:|---|
| `packages/agent/test/harness/pico3/legacy-tracker.test.ts` | 1 | port (agent/harness/pico3 is mapped; legacy-tracker has no Go home) |
| `packages/ai/test/classifier-models.test.ts` | 5 | port |
| `packages/ai/test/cloudflare-workers-ai-system-one.test.ts` | 4 | port |
| `packages/ai/test/llama-cpp-classify.test.ts` | 15 | port |
| `packages/ai/test/model-types.test.ts` | 4 | port |
| `packages/ai/test/oauth-callback-server.test.ts` | 12 | port |
| `packages/ai/test/openai-chatgpt-oauth.test.ts` | 6 | port |
| `packages/ai/test/openai-completions-provider-stream-event.test.ts` | 1 | port |
| `packages/ai/test/openai-responses-chatgpt-sign-in.test.ts` | 3 | port |
| `packages/ai/test/openai-responses-usage-limit.test.ts` | 2 | port |
| `packages/ai/test/typesafe-system-one.test.ts` | 9 | port |
| `packages/chord/test/delta-apply-immutable.test.ts` | 7 | owner decision D-H (chord package outside the four-package denominator) |
| `packages/chord/test/delta-diff.test.ts` | 23 | owner decision D-H (chord package outside the four-package denominator) |
| `packages/chord/test/delta-tracker/retention.test.ts` | 1 | owner decision D-H (chord package outside the four-package denominator) |
| `packages/chord/test/delta-tracker/tracker.test.ts` | 66 | owner decision D-H (chord package outside the four-package denominator) |
| `packages/chord/test/service-delivery.test.ts` | 10 | owner decision D-H (chord package outside the four-package denominator) |
| `packages/chord/test/state-delivery.test.ts` | 12 | owner decision D-H (chord package outside the four-package denominator) |
| `packages/codemode/test/declarations.test.ts` | 12 | owner decision D-B |
| `packages/codemode/test/sandbox.test.ts` | 41 | owner decision D-B |
| `packages/codemode/test/source.test.ts` | 4 | owner decision D-B |
| `packages/coding-agent/test/clipboard-paste-file-paths.test.ts` | 5 | port |
| `packages/coding-agent/test/codemode-renderer.test.ts` | 3 | port |
| `packages/coding-agent/test/compaction-nested-calls.test.ts` | 1 | port |
| `packages/coding-agent/test/jev-router-example.test.ts` | 2 | port |
| `packages/coding-agent/test/mcp-command.test.ts` | 8 | port |
| `packages/coding-agent/test/mcp-extension.test.ts` | 18 | port |
| `packages/coding-agent/test/mcp-oauth-refresh.test.ts` | 2 | port |
| `packages/coding-agent/test/model-catalog-protocol.test.ts` | 1 | port |
| `packages/coding-agent/test/model-runtime-classifiers.test.ts` | 1 | port |
| `packages/coding-agent/test/model-runtime-images.test.ts` | 7 | port |
| `packages/coding-agent/test/nested-tool-calls.test.ts` | 6 | port |
| `packages/coding-agent/test/resource-loader-theme.test.ts` | 2 | port |
| `packages/coding-agent/test/suite/agent-session-codemode.test.ts` | 19 | port |
| `packages/coding-agent/test/suite/agent-session-mcp-oauth.test.ts` | 5 | port |
| `packages/coding-agent/test/suite/agent-session-mcp.test.ts` | 24 | port |
| `packages/coding-agent/test/suite/agent-session-tool-orchestration.test.ts` | 3 | port |
| `packages/coding-agent/test/suite/virtual-models.test.ts` | 13 | port |
| `packages/coding-agent/test/system-theme.test.ts` | 7 | port |
| `packages/coding-agent/test/theme-style.test.ts` | 6 | port |
| `packages/coding-agent/test/themed-text.test.ts` | 1 | port |
| `packages/coding-agent/test/tool-search.test.ts` | 9 | port |
| `packages/coding-agent/test/virtual-models.test.ts` | 14 | port |
| `packages/durable/test/env-adaptive-publisher.test.ts` | 4 | owner decision D-H (durable package outside the denominator) |
| `packages/durable/test/env-node-spill.test.ts` | 1 | owner decision D-H (durable package outside the denominator) |
| `packages/durable/test/env-node.test.ts` | 45 | owner decision D-H (durable package outside the denominator) |
| `packages/durable/test/env-output-capture.test.ts` | 20 | owner decision D-H (durable package outside the denominator) |
| `packages/durable/test/env-truncate.test.ts` | 13 | owner decision D-H (durable package outside the denominator) |
| `packages/durable/test/harness-context.test.ts` | 6 | owner decision D-H (durable package outside the denominator) |
| `packages/durable/test/harness-conversations.test.ts` | 14 | owner decision D-H (durable package outside the denominator) |
| `packages/durable/test/harness-generation-recovery.test.ts` | 7 | owner decision D-H (durable package outside the denominator) |
| `packages/durable/test/harness-generation.test.ts` | 20 | owner decision D-H (durable package outside the denominator) |
| `packages/durable/test/harness-inspect.test.ts` | 2 | owner decision D-H (durable package outside the denominator) |
| `packages/durable/test/harness-prompt.test.ts` | 7 | owner decision D-H (durable package outside the denominator) |
| `packages/durable/test/harness-registry.test.ts` | 12 | owner decision D-H (durable package outside the denominator) |
| `packages/durable/test/harness-submissions.test.ts` | 10 | owner decision D-H (durable package outside the denominator) |
| `packages/durable/test/harness-tasks-recovery.test.ts` | 22 | owner decision D-H (durable package outside the denominator) |
| `packages/durable/test/harness-tasks.test.ts` | 34 | owner decision D-H (durable package outside the denominator) |
| `packages/durable/test/jsonl-storage.test.ts` | 23 | owner decision D-H (durable package outside the denominator) |
| `packages/durable/test/session-checkpoints-migrations.test.ts` | 20 | owner decision D-H (durable package outside the denominator) |
| `packages/durable/test/session-definitions.test.ts` | 2 | owner decision D-H (durable package outside the denominator) |
| `packages/durable/test/session-documents.test.ts` | 27 | owner decision D-H (durable package outside the denominator) |
| `packages/durable/test/session-forks.test.ts` | 14 | owner decision D-H (durable package outside the denominator) |
| `packages/durable/test/session-states.test.ts` | 10 | owner decision D-H (durable package outside the denominator) |
| `packages/durable/test/session-tables.test.ts` | 12 | owner decision D-H (durable package outside the denominator) |
| `packages/durable/test/session-watches.test.ts` | 21 | owner decision D-H (durable package outside the denominator) |
| `packages/mcp/test/client.test.ts` | 12 | owner decision D-B |
| `packages/mcp/test/content.test.ts` | 2 | owner decision D-B |
| `packages/mcp/test/oauth.test.ts` | 7 | owner decision D-B |
| `packages/mcp/test/stdio.test.ts` | 2 | owner decision D-B |
| `packages/mcp/test/streamable-http.test.ts` | 13 | owner decision D-B |
| `packages/tui/test/colors.test.ts` | 4 | port |
| `packages/tui/test/visible-width.test.ts` | 4 | port |
| `packages/tui/test/wheel-scroll.test.ts` | 6 | port |

Removed: `packages/chord/test/delta-retention.test.ts` (currently designed-out; drop the mapping row).

## Appendix C. Changed upstream test files in the four tracked packages (100)

Every file below has a new upstream SHA-256, so its reviewed `ported` mapping is stale until the changed cases are ported and the hash is re-recorded. `HP` = tagged hot-path in the 0.87.1 release policy. Case counts are before → after; `+`/`-` count case ids added or removed (a renamed case counts as one of each).

| file | disposition | HP | cases | + | - |
|---|---|---|---|---:|---:|
| `packages/agent/test/agent-loop.test.ts` | ported | yes | 33 → 35 | 2 | 0 |
| `packages/agent/test/agent.test.ts` | ported | yes | 34 → 35 | 1 | 0 |
| `packages/agent/test/harness/pico3/spec-view-events.test.ts` | ported | no | 10 → 10 | 1 | 1 |
| `packages/ai/test/abort.test.ts` | ported | yes | 41 → 41 | 0 | 0 |
| `packages/ai/test/anthropic-adaptive-thinking-models.test.ts` | ported | yes | 1 → 1 | 0 | 0 |
| `packages/ai/test/anthropic-cache-write-1h-cost.test.ts` | ported | yes | 2 → 3 | 1 | 0 |
| `packages/ai/test/anthropic-empty-thinking-signature-compat.test.ts` | ported | yes | 7 → 7 | 0 | 0 |
| `packages/ai/test/anthropic-oauth.test.ts` | ported | yes | 3 → 4 | 1 | 0 |
| `packages/ai/test/anthropic-sse-parsing.test.ts` | ported | yes | 13 → 14 | 1 | 0 |
| `packages/ai/test/azure-openai-base-url.test.ts` | ported | yes | 16 → 17 | 1 | 0 |
| `packages/ai/test/bedrock-raw-stop-reason.test.ts` | ported | yes | 2 → 4 | 2 | 0 |
| `packages/ai/test/cache-retention.test.ts` | ported | yes | 19 → 19 | 0 | 0 |
| `packages/ai/test/context-overflow.test.ts` | ported | yes | 35 → 35 | 1 | 1 |
| `packages/ai/test/cross-provider-handoff.test.ts` | ported | yes | 2 → 2 | 0 | 0 |
| `packages/ai/test/empty.test.ts` | ported | yes | 120 → 120 | 0 | 0 |
| `packages/ai/test/fetch-option.test.ts` | ported | yes | 6 → 6 | 0 | 0 |
| `packages/ai/test/fireworks-model-generation.test.ts` | ported | no | 4 → 4 | 0 | 0 |
| `packages/ai/test/fireworks-models.test.ts` | ported | yes | 19 → 19 | 2 | 2 |
| `packages/ai/test/google-raw-stop-reason.test.ts` | ported | yes | 6 → 7 | 1 | 0 |
| `packages/ai/test/image-model-data.test.ts` | ported | yes | 3 → 4 | 4 | 3 |
| `packages/ai/test/image-tool-result.test.ts` | ported | yes | 46 → 46 | 2 | 2 |
| `packages/ai/test/images-models.test.ts` | ported | yes | 6 → 13 | 13 | 6 |
| `packages/ai/test/images.test.ts` | ported | yes | 3 → 2 | 0 | 1 |
| `packages/ai/test/max-thinking.test.ts` | ported | yes | 4 → 4 | 0 | 0 |
| `packages/ai/test/mistral-http-transport.test.ts` | ported | yes | 8 → 10 | 2 | 0 |
| `packages/ai/test/mistral-reasoning-mode.test.ts` | ported | yes | 10 → 11 | 8 | 7 |
| `packages/ai/test/model-data-validation.test.ts` | ported | yes | 11 → 15 | 4 | 0 |
| `packages/ai/test/models-runtime.test.ts` | ported | yes | 39 → 40 | 1 | 0 |
| `packages/ai/test/oauth-auth.test.ts` | ported | yes | 12 → 13 | 1 | 0 |
| `packages/ai/test/openai-codex-oauth.test.ts` | ported | yes | 8 → 9 | 1 | 0 |
| `packages/ai/test/openai-codex-stream.test.ts` | ported | yes | 30 → 30 | 2 | 2 |
| `packages/ai/test/openai-completions-prompt-cache.test.ts` | ported | yes | 15 → 15 | 0 | 0 |
| `packages/ai/test/openai-completions-tool-choice.test.ts` | ported | yes | 49 → 49 | 2 | 2 |
| `packages/ai/test/openai-responses-compat.test.ts` | ported | yes | 18 → 18 | 1 | 1 |
| `packages/ai/test/openai-responses-terminal-event.test.ts` | ported | yes | 9 → 12 | 3 | 0 |
| `packages/ai/test/openrouter-images.test.ts` | ported | yes | 3 → 3 | 0 | 0 |
| `packages/ai/test/openrouter-oauth.test.ts` | ported | yes | 13 → 13 | 2 | 2 |
| `packages/ai/test/pi-messages.test.ts` | ported | yes | 8 → 9 | 1 | 0 |
| `packages/ai/test/provider-error-body-passthrough.test.ts` | ported | yes | 1 → 1 | 0 | 0 |
| `packages/ai/test/providers.test.ts` | ported | yes | 29 → 30 | 1 | 0 |
| `packages/ai/test/radius-oauth.test.ts` | ported | yes | 3 → 4 | 1 | 0 |
| `packages/ai/test/retry.test.ts` | ported | yes | 19 → 21 | 2 | 0 |
| `packages/ai/test/sampling-options.test.ts` | ported | yes | 6 → 6 | 3 | 3 |
| `packages/ai/test/stream.test.ts` | ported | yes | 233 → 233 | 6 | 6 |
| `packages/ai/test/supports-xhigh.test.ts` | ported | yes | 27 → 28 | 2 | 1 |
| `packages/ai/test/telemetry-options.test.ts` | designed-out | yes | 3 → 3 | 1 | 1 |
| `packages/ai/test/together-models.test.ts` | ported | yes | 3 → 3 | 1 | 1 |
| `packages/ai/test/tokens.test.ts` | ported | yes | 31 → 31 | 0 | 0 |
| `packages/ai/test/tool-call-without-result.test.ts` | ported | yes | 30 → 30 | 0 | 0 |
| `packages/ai/test/total-tokens.test.ts` | ported | yes | 34 → 34 | 0 | 0 |
| `packages/ai/test/unicode-surrogate.test.ts` | ported | yes | 90 → 90 | 0 | 0 |
| `packages/coding-agent/test/agent-session-concurrent.test.ts` | ported | yes | 7 → 7 | 0 | 0 |
| `packages/coding-agent/test/agent-session-dynamic-tools.test.ts` | ported | yes | 4 → 4 | 0 | 0 |
| `packages/coding-agent/test/clipboard-image-native-errors.test.ts` | ported | yes | 1 → 1 | 0 | 0 |
| `packages/coding-agent/test/clipboard-image.test.ts` | ported | yes | 8 → 10 | 2 | 0 |
| `packages/coding-agent/test/default-tools-setting.test.ts` | ported | yes | 5 → 6 | 1 | 0 |
| `packages/coding-agent/test/edit-tool-legacy-input.test.ts` | ported | yes | 8 → 8 | 0 | 0 |
| `packages/coding-agent/test/experimental-cli-entry.test.ts` | ported | no | 3 → 3 | 0 | 0 |
| `packages/coding-agent/test/extensions-discovery.test.ts` | ported | yes | 30 → 31 | 1 | 0 |
| `packages/coding-agent/test/extensions-runner.test.ts` | ported | yes | 50 → 50 | 0 | 0 |
| `packages/coding-agent/test/footer-width.test.ts` | ported | yes | 9 → 11 | 2 | 0 |
| `packages/coding-agent/test/image-resize-callers.test.ts` | ported | yes | 4 → 4 | 0 | 0 |
| `packages/coding-agent/test/interactive-tui.test.ts` | ported | yes | 11 → 11 | 0 | 0 |
| `packages/coding-agent/test/llama-extension.test.ts` | ported | yes | 11 → 13 | 2 | 0 |
| `packages/coding-agent/test/model-resolver.test.ts` | ported | yes | 51 → 51 | 1 | 1 |
| `packages/coding-agent/test/model-runtime-cloudflare-compat.test.ts` | ported | yes | 2 → 2 | 0 | 0 |
| `packages/coding-agent/test/package-command-paths.test.ts` | ported | no | 31 → 33 | 2 | 0 |
| `packages/coding-agent/test/package-manager.test.ts` | ported | yes | 123 → 127 | 8 | 4 |
| `packages/coding-agent/test/remote-catalog-provider.test.ts` | designed-out | yes | 7 → 8 | 1 | 0 |
| `packages/coding-agent/test/resource-loader.test.ts` | ported | yes | 42 → 50 | 8 | 0 |
| `packages/coding-agent/test/rpc-prompt-response-semantics.test.ts` | ported | yes | 4 → 7 | 4 | 1 |
| `packages/coding-agent/test/sdk-stream-options.test.ts` | ported | yes | 9 → 10 | 1 | 0 |
| `packages/coding-agent/test/session-file-invalid.test.ts` | ported | yes | 1 → 1 | 0 | 0 |
| `packages/coding-agent/test/session-id-readonly.test.ts` | ported | yes | 6 → 6 | 0 | 0 |
| `packages/coding-agent/test/session-manager/file-operations.test.ts` | ported | yes | 28 → 31 | 3 | 0 |
| `packages/coding-agent/test/session-manager/tree-traversal.test.ts` | ported | yes | 31 → 31 | 2 | 2 |
| `packages/coding-agent/test/settings-manager.test.ts` | ported | yes | 46 → 51 | 5 | 0 |
| `packages/coding-agent/test/settings-selector.test.ts` | ported | yes | 4 → 4 | 0 | 0 |
| `packages/coding-agent/test/startup-session-name.test.ts` | ported | yes | 1 → 1 | 0 | 0 |
| `packages/coding-agent/test/stdout-cleanliness.test.ts` | ported | yes | 2 → 2 | 0 | 0 |
| `packages/coding-agent/test/suite/agent-session-compaction.test.ts` | ported | yes | 28 → 28 | 0 | 0 |
| `packages/coding-agent/test/suite/agent-session-runtime.test.ts` | ported | yes | 12 → 12 | 0 | 0 |
| `packages/coding-agent/test/suite/regressions/5943-session-start-notify.test.ts` | ported | yes | 7 → 7 | 0 | 0 |
| `packages/coding-agent/test/suite/regressions/7150-rpc-prompt-during-compaction.test.ts` | ported | yes | 1 → 1 | 0 | 0 |
| `packages/coding-agent/test/syntax-highlight.test.ts` | ported | yes | 8 → 8 | 0 | 0 |
| `packages/coding-agent/test/system-prompt.test.ts` | ported | yes | 14 → 14 | 0 | 0 |
| `packages/coding-agent/test/theme-controller.test.ts` | ported | yes | 6 → 9 | 5 | 2 |
| `packages/coding-agent/test/theme-detection.test.ts` | ported | yes | 11 → 5 | 3 | 9 |
| `packages/coding-agent/test/theme-export.test.ts` | ported | yes | 2 → 3 | 1 | 0 |
| `packages/coding-agent/test/tool-execution-component.test.ts` | ported | yes | 23 → 25 | 2 | 0 |
| `packages/coding-agent/test/tools.test.ts` | ported | yes | 84 → 85 | 3 | 2 |
| `packages/tui/test/autocomplete-skill-slash.test.ts` | ported | yes | 3 → 5 | 2 | 0 |
| `packages/tui/test/autocomplete.test.ts` | ported | yes | 36 → 40 | 4 | 0 |
| `packages/tui/test/editor.test.ts` | ported | yes | 192 → 192 | 0 | 0 |
| `packages/tui/test/mouse-components.test.ts` | ported | yes | 8 → 9 | 1 | 0 |
| `packages/tui/test/overlay-options.test.ts` | ported | yes | 24 → 26 | 2 | 0 |
| `packages/tui/test/terminal-colors.test.ts` | ported | yes | 9 → 5 | 4 | 8 |
| `packages/tui/test/terminal-image.test.ts` | ported | yes | 65 → 73 | 8 | 0 |
| `packages/tui/test/terminal.test.ts` | ported | yes | 21 → 22 | 1 | 0 |
| `packages/tui/test/tui-alt-screen.test.ts` | ported | yes | 62 → 63 | 1 | 0 |

Files whose SHA changed with an identical case list (`+0 -0`) changed an assertion, fixture or helper: port the changed body, not only new case ids. Also changed, outside the four packages: 10 chord and 6 durable test files (all currently designed-out).

## 13. Owner approval (2026-09-29, recorded by the lead)

Approved as recommended, plus red-green-refactor:
- **D-A:** start the source-only workstreams now (tests, Go design, docs). The npm cooldown is **not** waived. Move the pin, the exact `pi` comparator and published-origin evidence on or after 2026-10-02.
- **D-B:** port native MCP into core PiG faithfully (client, stdio and streamable-HTTP transports, OAuth, `mcp.json`, `/mcp`, `pi mcp`, `registerMcpServer`). Retire `pig-mcp-adapter` in favor of `builtin:mcp`, with a migration note. Codemode and tool-search: run Pi's own extension code in PiG's existing Node runtime (the 0.3.0 "Pi's own code" approach), with no Go sandbox; write the short spec AGENTS.md requires (resource bounds and worker lifetime) before code. Design their boundaries so a Piglet can later disable or strip them (issue #92).
- **D-C:** in scope, in all four SDKs, with conformance rows and parity docs.
- **D-D:** the harness answers OSC 10/11/4 with one fixed, deterministic palette for both sides. Re-derive scenario expectations from upstream 0.99.1, with no normalizing.
- **D-E:** approve the catalog-pipeline rebuild and the Go `ai` API change (pre-1.0, with migration notes, no shim).
- **D-F:** ship as **0.4.0**. No new known-gap exceptions: every hot-path test is ported.
- **D-G:** hermetic only: a local fake authorization server, a fixed device-ID source, no live account.
- **D-H:** `chord` and `durable` are designed-out **per file**, with a rationale each. `packages/mcp` and `packages/codemode` join `TRACKED_PACKAGES`.
- **D-I:** re-snapshot dependency proofs (`openai` 7, `pi-telemetry`) and re-probe mirrored behavior.

**Red-green-refactor is required for every Phase 2 family:**
1. **Red:** port the family's new and changed upstream tests FIRST (Appendix B/C), with Pi's original inputs and expectations. Add only the minimal API signatures (returning a not-implemented error) so they compile and fail for the right reason. Commit the ported tests plus the signatures on their own, and record the red run.
2. **Green:** make the smallest faithful implementation, in a later commit, without editing the ported tests. Any test change needs an upstream citation proving it was mis-ported.
3. **Refactor:** clean up with the tests green, then load-test the concurrency-sensitive paths (L4).
4. **Review gate:** each family's Opus review checks that the tests commit precedes the implementation, that the red run is recorded, and mutation-checks the key fixes.

Commits: the owner approves commits on the local branch `porter/pi-0.99.1` (signed off with `-s`), so the red and green order is visible in history. Still no push, PR, tag or release without the owner.
