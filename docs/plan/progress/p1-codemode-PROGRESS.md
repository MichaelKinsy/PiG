# PROGRESS: p1-codemode (Pi 1.0.0 test port, codemode area)

Phase 1 of the move to upstream Pi 1.0.0 (tag `v1.0.0`, `a13d35a74`): port the new and changed upstream tests of the codemode area. No product fixes. Red is expected until the fix lane lands.

Area: leaner prompt, recovery errors, `models.generateImages`, `"name" in tools`.

Base: `porter/pi-0.99.1` at `1e16ba920` (pin still 0.99.2). Rebase onto the 1.0.0 pin commit when it lands.

## Upstream diff (v0.99.2..v1.0.0), codemode area

Changed test files (no new test files in this area):

| upstream test file | new / changed cases |
|---|---|
| `packages/codemode/test/sandbox.test.ts` | new: "names close matches when a script reads a tool that does not exist", "explains oversized writes", "names the members of a namespace when a script reads one that does not exist"; changed: "supports register and unregister between executions", "groups namespaced globals and spreads arguments on request", "keeps tools and console frozen" (`typeof x` probes became `"x" in ...`) |
| `packages/coding-agent/test/suite/agent-session-codemode.test.ts` | new: "generates images with catalog auth and attaches them through image()", "notes generated images that the script did not show"; changed: "presents callable tools per codemode.mode", "declares models only for the session's own codemode tool", "reports provider errors as results and invalid arguments as exceptions"; setup registers the image model `painter` and the `test-images` API |
| `packages/coding-agent/test/tool-search.test.ts` | changed: "codemode description catalog" / "lists everything without a budget" and "fills the budget round-robin, cheapest first, and says what is missing" (search guidance text) |

Changed source files behind these tests: `packages/codemode/src/runtime/prelude-source.ts`, `packages/codemode/src/declarations.ts` (`renderToolOutputType` exported), `packages/codemode/src/index.ts`, `packages/coding-agent/src/extensions/codemode/tool.ts`, `packages/coding-agent/src/extensions/codemode/execute.ts`, `packages/coding-agent/src/core/system-prompt.ts` (docs line names `docs/codemode.md`), `packages/coding-agent/src/core/model-registry.ts` (`generateImages` for extensions), new `packages/coding-agent/docs/codemode.md`.

Out of area (seen in the diff, left to their lanes): `ai/test/constrained-sampling.test.ts` "drops foreign item ids when replaying grammar calls as custom Responses items" (OpenAI Responses replay of grammar tool calls such as `codemode`); `coding-agent/test/suite/agent-session-mcp.test.ts` resume/reload cases and `mcp-extension.test.ts` `authServerMetadataUrl` (MCP).

## Per-test status

Status: `ported-red` fails on the current product for the 1.0.0 behavior; `ported-green` passes unchanged.

### `codemode/sandbox_test.go` (sandbox.test.ts)

| upstream case | Go test | status |
|---|---|---|
| names close matches when a script reads a tool that does not exist | `TestNamesCloseMatchesWhenAScriptReadsAToolThatDoesNotExist` | ported-red |
| supports register and unregister between executions | `TestSupportsRegisterAndUnregisterBetweenExecutions` | ported-green |
| explains oversized writes | `TestExplainsOversizedWrites` | ported-red |
| groups namespaced globals and spreads arguments on request | `TestGroupsNamespacedGlobalsAndSpreadsArgumentsOnRequest` | ported-green |
| names the members of a namespace when a script reads one that does not exist | `TestNamesTheMembersOfANamespaceWhenAScriptReadsOneThatDoesNotExist` | ported-red |
| keeps tools and console frozen | `TestKeepsToolsAndConsoleFrozen` | ported-green |

### `coding/codemode_session_upstream_test.go` (agent-session-codemode.test.ts)

| upstream case | Go test | status |
|---|---|---|
| presents callable tools per codemode.mode | `TestUpstreamAgentSessionCodemodeTool/presents_callable_tools_per_codemode.mode` | ported-red |
| declares models only for the session's own codemode tool | `TestUpstreamCodemodeModels/declares_models_only_for_the_session's_own_codemode_tool` (now includes the `createCodemodeTool()` override half through a Go extension) | ported-red |
| lists models and classifies with catalog auth, ignoring script-supplied fields | unchanged case; setup now registers `painter` too | ported-green |
| generates images with catalog auth and attaches them through image() | `TestUpstreamCodemodeModels/generates_images_with_catalog_auth_and_attaches_them_through_image()` | ported-red |
| notes generated images that the script did not show | `TestUpstreamCodemodeModels/notes_generated_images_that_the_script_did_not_show` | ported-red |
| reports provider errors as results and invalid arguments as exceptions | `TestUpstreamCodemodeModels/reports_provider_errors_as_results_and_invalid_arguments_as_exceptions` | ported-red |

Harness: `coding/codemode_session_harness_test.go` `registerScorerProvider` registers the image model `painter` (base URL `https://images.test/v1`) and the `test-images` image API with upstream's answers (`painted <prompt>` plus one PNG and `usage(100, 0.04)`; `explode` gives an error result), and records each image request.

### `coding/extension/builtin/codemode/description_upstream_test.go` (tool-search.test.ts, codemode description catalog)

| upstream case | Go test | status |
|---|---|---|
| lists everything without a budget | `TestCodemodeDescriptionListsEverythingWithoutABudget` | ported-red |
| fills the budget round-robin, cheapest first, and says what is missing | `TestCodemodeDescriptionFillsTheBudgetRoundRobinCheapestFirstAndSaysWhatIsMissing` | ported-red |

### Pig tests that pinned removed 0.99.2 upstream text, updated to 1.0.0

| Go test | source it pins | status |
|---|---|---|
| `TestCodemodeDescriptionDocumentsDescribeNamespaceAndAlwaysCarriesTheSearchGuidance` | tool.ts `DESCRIPTION_INTRO` + `describeGlobals(false)`: exact description without tools | red |
| `TestCodemodeDescriptionNamesTheModelsGlobalInOneLine` (new) | tool.ts `describeGlobals(true)`: the one `models` line with `CODEMODE_DOCS_PATH` | red |
| `TestCodemodePromptContributionAndSchemaAreTheLeanerOnes` (new) | tool.ts `codemodeToolSystemPromptContribution` and `codemodeSchema` | red |
| `TestExtensionServesModelsUnlessDisabled` | index.ts `options.models ?? true`, now the `` `models` `` line | red |
| `TestDeclaresModelsOnlyForAToolCreatedWithModelAccess` | the override half of "declares models only for the session's own codemode tool" | red |

Signature stub: `coding/extension/builtin/codemode.DocsPath()` (upstream `CODEMODE_DOCS_PATH`) returns the empty string. The tests reject an empty path so the containment checks cannot pass vacuously.

## Failure list by suspected root cause

1. Sandbox prelude: `tools` and namespace globals are plain frozen objects (`codemode/assets/prelude.js`). 1.0.0 `prelude-source.ts` wraps them in a `guard` Proxy: reading a missing string member throws a `TypeError` that names close matches (names compared lowercased with non-alphanumerics removed, exact then substring, at most five), or the `Available:` list when there are at most 20 names, plus the `ALL_TOOLS`/`searchTools` hint for `tools` and the `"name" in` hint. `in`, `Object.keys`, `then`, `toJSON` and `Object.prototype` names such as `toString` pass through.
   - `TestNamesCloseMatchesWhenAScriptReadsAToolThatDoesNotExist`
   - `TestNamesTheMembersOfANamespaceWhenAScriptReadsOneThatDoesNotExist`
   - Note: `TestEmbeddedSourcesMatchTheirRecordedHashes` pins the prelude sha256 and must be re-pinned with the 1.0.0 prelude. The vendored Node copy `coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-codemode/runtime/prelude-source.js` comes from the re-vendored dist.
2. Sandbox prelude, `store()` size errors: the message still says `value exceeds 262144 characters of JSON`; 1.0.0 reports the actual size (`value has 307202 characters of JSON, more than the limit of ...`) and explains what the store is for (`Show images with image()`), and the full-store error says how to delete keys.
   - `TestExplainsOversizedWrites`
3. Codemode description and prompt (`coding/extension/builtin/codemode/description_text.go`, `description.go`, `tool.go`): 0.99.2 `DESCRIPTION_INTRO`, `DEFERRED_TOOLS_GUIDANCE`, `MODEL_TYPES` and `MODEL_GLOBAL_DECLARATIONS` are still rendered. 1.0.0 uses the three-line intro, `describeGlobals` (one line per global, `models` line pointing to `CODEMODE_DOCS_PATH`), drops the Model API block, shortens the prompt snippet, guideline and `code` description, and in mode `on` appends `describeScriptCall` (`Codemode: \`tools.<id>(args)\` resolves to <describeOutput>.`) to declared tools instead of `renderToolSample`. `DocsPath()` needs `join(getDocsPath(), "codemode.md")` and Pig's docs bundle needs `docs/codemode.md`.
   - `TestUpstreamAgentSessionCodemodeTool/presents_callable_tools_per_codemode.mode`
   - `TestUpstreamCodemodeModels/declares_models_only_for_the_session's_own_codemode_tool`
   - `TestCodemodeDescriptionListsEverythingWithoutABudget`
   - `TestCodemodeDescriptionFillsTheBudgetRoundRobinCheapestFirstAndSaysWhatIsMissing`
   - `TestCodemodeDescriptionDocumentsDescribeNamespaceAndAlwaysCarriesTheSearchGuidance`
   - `TestCodemodeDescriptionNamesTheModelsGlobalInOneLine`
   - `TestCodemodePromptContributionAndSchemaAreTheLeanerOnes`
   - `TestExtensionServesModelsUnlessDisabled`
   - `TestDeclaresModelsOnlyForAToolCreatedWithModelAccess`
4. `models.generateImages` missing from the script globals (`coding/extension/builtin/codemode/models.go`, `execute.go`; the codemode model runtime has no `GenerateImages`): scripts get `TypeError: not a function`. 1.0.0 resolves the model from the catalog (ignores script-supplied `baseUrl`), uses catalog auth, records `models.generateImages` calls with `provider/id`, cost and error, adds usage to the result and session cost, rejects a non-image model with a hint, and appends a note when generated images were not passed to `image()`.
   - `TestUpstreamCodemodeModels/generates_images_with_catalog_auth_and_attaches_them_through_image()`
   - `TestUpstreamCodemodeModels/notes_generated_images_that_the_script_did_not_show`
5. Recovery errors of the `models` globals (`coding/extension/builtin/codemode/models.go`): unknown model lacks the `models.getAvailableOfType(...)` hint; a string or `undefined` model gives the generic `expects a model ...` message; `context.state`, question criteria, `generateImages` `context.input` and `getModelOfType("type", "provider/id")` are not validated (the bad question even reaches the classifier and costs usage).
   - `TestUpstreamCodemodeModels/reports_provider_errors_as_results_and_invalid_arguments_as_exceptions`

## Source changes without an upstream test (for the fix lane)

- `system-prompt.ts`: the docs line adds `codemode scripts and non-LLM models such as classifiers and image models (docs/codemode.md)`. Pig's docs section is additive D22 (`internal/codingagent/prompts/coding.go`).
- `model-registry.ts`: `ctx.modelRegistry.generateImages()` for extensions. This is extension API surface; per AGENTS.md it needs every SDK and a conformance row.
- `mcp/index.ts` `MCP_SERVERS_SECTION` first line is shorter (MCP lane; the session tests compare against the constant).
- Vendored Pi dist seam `automation/gen/vendor-pi-session-seams.mjs` (see `docs/plan/upgrade-0.99.1-progress.md`).

## Ledgers

`test/parity/interfaces/test-mapping-v0.99.2.json` is not edited: the porter creates `test-mapping-v1.0.0.json` with these files reopened as `pending`. Evidence for the 1.0.0 mapping: the Go tests listed above.

## Commands

- `go test ./codemode/` → the 3 red cases above, everything else passes.
- `go test ./coding/ -run 'Codemode|MCP'` (with Node on PATH and temp `HOME`, `PIG_CODING_AGENT_DIR`, `PI_CODING_AGENT_DIR`) → only the 5 red subtests above.
- `go test ./coding/extension/builtin/codemode/ ./coding/extension/builtin/toolsearch/` → the 7 red tests above; toolsearch passes.
- `go vet` (also `GOOS=windows`) and `golangci-lint` on touched packages: clean. `make ci-drift`: clean. `make ci-contracts`: clean after regenerating `pig-go.json` for `DocsPath` (generated file restored, integrator regenerates).
