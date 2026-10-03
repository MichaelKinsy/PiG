# PROGRESS: c1-codemode (Pi 1.0.0 product port, codemode area)

READY. Every test the sibling lane `p1-codemode` ported for this area is green. No divergence is needed, and no QUESTION is open.

Upstream: v0.99.2..v1.0.0 (tag `v1.0.0`, `a13d35a74`). Based on the 0.99.1 porter branch with the 1.0.0 pin and merged with `p1-codemode` (`bda966dbe`).

## Upstream changes ported

| upstream source | PiG |
|---|---|
| `packages/codemode/src/runtime/prelude-source.ts` (guard Proxy over `tools` and namespace globals; `store()` size explanations) | `codemode/assets/prelude.js` regenerated from the 1.0.0 `PRELUDE_SOURCE` byte for byte, hash re-pinned in `PROVENANCE.md` and `TestEmbeddedSourcesMatchTheirRecordedHashes` |
| `packages/codemode/src/declarations.ts`, `index.ts` (`renderToolOutputType` exported) | `codemode.RenderToolOutputType` |
| `packages/coding-agent/src/extensions/codemode/tool.ts` (`DESCRIPTION_INTRO`, `describeGlobals`, `describeOutput`, `describeScriptCall`, `CODEMODE_DOCS_PATH`, shorter snippet, guideline and `code` description; `MODEL_TYPES` and `MODEL_GLOBAL_DECLARATIONS` removed) | `coding/extension/builtin/codemode/{description_text,description,tool}.go`; `DocsPath()` is the codemode page of PiG's docs bundle under its config root |
| `packages/coding-agent/src/extensions/codemode/execute.ts` (`models.generateImages`, `runModelCall`, `checkClassifierContext`, `checkImagesContext`, `describeValue`, `getModelOfType` shape error, unshown-image note) | `coding/extension/builtin/codemode/{models,models_args,execute,run}.go` |
| `packages/coding-agent/src/core/model-registry.ts` (`generateImages`) | `coding.ModelRegistry.GenerateImages`; `ctx.modelRegistry.generateImages` in the Node runtime and the Go, Rust and Python SDKs through the new `generateImages` host call |
| `packages/coding-agent/src/extensions/mcp/index.ts` (`serversSectionIntro`) | `coding/mcpext/extension.go`. The rest of this file's delta (OAuth per-server credentials, sign-in link) belongs to `c1-mcp-oauth`, so its upstream-sync entry stays pending |
| `packages/coding-agent/src/core/system-prompt.ts:158` (docs line names `docs/codemode.md`) | `internal/codingagent/prompts/coding.go` |
| `packages/coding-agent/docs/codemode.md`, `docs/models.md` "Use image models" | `internal/pigdocs/content/{codemode,models}.md`, `docs/site/docs/{codemode,models}.md` |

Differential evidence against Pi 1.0.0 (npm `@earendil-works/pi-coding-agent@1.0.0`):

- `createCodemodeToolDefinition().prepareLoadout` over bash, read, MCP `CallToolResult`, integer-key and union output schemas, in modes `on` and `only`, with budgets unset, 3000 and 60, with and without `models`: every description matches PiG's except the docs path.
- `executeCodemode` over 38 malformed `models.*` calls and guarded member reads: every error name and message matches. `TestModelsGlobalArgumentErrorsMatchPi` pins the Pi output (`coding/extension/builtin/codemode/testdata/models-argument-errors.pi-1.0.0.json`).

## Red to green (sibling tests)

| test | before | after |
|---|---|---|
| `codemode/sandbox_test.go` `TestNamesCloseMatchesWhenAScriptReadsAToolThatDoesNotExist` | red | green |
| `codemode/sandbox_test.go` `TestExplainsOversizedWrites` | red | green |
| `codemode/sandbox_test.go` `TestNamesTheMembersOfANamespaceWhenAScriptReadsOneThatDoesNotExist` | red | green |
| `coding` `TestUpstreamAgentSessionCodemodeTool/presents_callable_tools_per_codemode.mode` | red | green |
| `coding` `TestUpstreamCodemodeModels/declares_models_only_for_the_session's_own_codemode_tool` | red | green |
| `coding` `TestUpstreamCodemodeModels/generates_images_with_catalog_auth_and_attaches_them_through_image()` | red | green |
| `coding` `TestUpstreamCodemodeModels/notes_generated_images_that_the_script_did_not_show` | red | green |
| `coding` `TestUpstreamCodemodeModels/reports_provider_errors_as_results_and_invalid_arguments_as_exceptions` | red | green |
| `builtin/codemode` `TestCodemodeDescriptionListsEverythingWithoutABudget` | red | green |
| `builtin/codemode` `TestCodemodeDescriptionFillsTheBudgetRoundRobinCheapestFirstAndSaysWhatIsMissing` | red | green |
| `builtin/codemode` `TestCodemodeDescriptionDocumentsDescribeNamespaceAndAlwaysCarriesTheSearchGuidance` | red | green |
| `builtin/codemode` `TestCodemodeDescriptionNamesTheModelsGlobalInOneLine` | red | green |
| `builtin/codemode` `TestCodemodePromptContributionAndSchemaAreTheLeanerOnes` | red | green |
| `builtin/codemode` `TestExtensionServesModelsUnlessDisabled` | red | green |
| `builtin/codemode` `TestDeclaresModelsOnlyForAToolCreatedWithModelAccess` | red | green |

Test changes in this lane, none of which loosens a sibling assertion:

- `codemode/sandbox_test.go`: the prelude hash re-pin that the asset refresh procedure requires.
- `builtin/codemode/models_global_test.go`: the fake registry gains `GenerateImages` (CodemodeModelRuntime now includes it), and `TestModelsGlobalServesTheRegistryToScripts` uses a valid question and the 1.0.0 messages, because 1.0.0 rejects `questions: {}` and rewords the unknown-model and non-model errors.
- `prompts/system_prompt_second_half_upstream_test.go`: the exact docs line includes `docs/codemode.md`.

## New regression tests (red before this lane)

- `builtin/codemode` `TestModelsGlobalArgumentErrorsMatchPi`
- `mcpext` `TestMCPServersSectionIntroExplainsOnlyTheReachesTheListedServersUse`
- `extension-conformance` `TestModelTypesGenerateImagesAcrossSDKs` (Node, Go, Rust and Python; every placement)
- `coding` `TestExtensionGenerateImagesAction` (Session wiring: auth, explicit key, non-image model, cancellation)
- `subprocess` `TestGenerateImagesCallWithoutAnActionAnswersAnErrorResult`, `TestGenerateImagesCallForwardsModelContextAndOptions`, `TestTypedModelCallsStartAsync` (`generateImages` row), `TestNodeModelRegistryTypedOperations` (generateImages part)

## Ledgers

- `test/parity/upstream-sync/v1.0.0.toml`: ported with evidence for `codemode/src/{declarations,index,runtime/prelude-source}.ts`, `coding-agent/src/extensions/codemode/{execute,tool}.ts`, `core/model-registry.ts`, and `core/system-prompt.ts`.
- `test/parity/interfaces/test-mapping-v1.0.0.json`: `packages/codemode/test/sandbox.test.ts`, `coding-agent/test/suite/agent-session-codemode.test.ts` and `coding-agent/test/tool-search.test.ts` are `ported` with the tests above as evidence (`17d457793`). The two sandbox worker-path cases keep the owner's 0.99.2 designed-out approval in `test-porting-policy-v1.0.0.json`, because both cases are unchanged in 1.0.0. The owner should confirm this carry-forward.
- Generated files `pig-go.json` and `recommendations-v1.0.0.json` were regenerated locally to pass `interface-go-drift` and `interface-recommendations-drift`, then restored for the integrator. `docs/extension-sdk-surface.md` and `runtime-node.zip` are committed, because `sdk-surface-drift` and the Node runtime depend on them.

## Gates

- `go vet` (also `GOOS=windows`) and `golangci-lint` on every touched package: clean.
- `go test` for `codemode`, `coding/...`, `internal/codingagent/...`, `cmd/pig`, `extensions/sdk`, the Rust SDK (`cargo test`), the Python SDK (`pytest`), and `test/extension-conformance -run ModelTypes|NodeModelRegistry`: green, except failures that also occur without this lane. These are the 0.99.2 RPC/faux oracle pins, `TestOAuthPageMatchesThePinnedPackage` (the OAuth logo belongs to the login lane), and `TestNodeVendoredTuiUpstreamTests` (no Xvfb on this host).
- `sdk-surface-drift`, `port-map-drift`, `coverage-drift`, `source-hygiene`, `lint-scenarios`, `divergence-consistency`, `divergence-quality`, `behavior-contracts`, `interface-inventory`, `interface-delta`, `test-inventory`, `format-version-inventory` and `custom-factory-ledger-drift`: clean.
- The following gates still fail for reasons outside this lane: `correspondence-check` (`quietStartup`/`tuiMode` table gaps), `test-porting-release` and `known-gaps-drift` (pending TUI hot paths), `divergence-guard` (removed `agent/harness` allow markers), `docs-drift` (an earlier-release mention in an sdk-surface exception), and `test-sdk-ts` (the sdk-ts README pin). The sdk-ts `tsc` typecheck passes with the new `generateImages` row.
