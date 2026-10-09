# coding-agent modes and CLI rule requests (lg-c4-coding-agent-d)

Base: integrate-042 at Pi 1.1.0, `make interface-gaps` (coding-agent rows from `build/interface-gaps/gaps.tsv`). Each family below has its Go counterpart named; none is a missing Pi behaviour.

## Functional options for Pi's defaulted constructor parameters
Closed by porting: `SkillInvocationMessageComponent`, `CompactionSummaryMessageComponent` and `BranchSummaryMessageComponent` now take Pi's positional `(message, markdownTheme, outputPad)`, with a nil theme standing for the `getMarkdownTheme()` default. Still open: `UserMessageComponent::construct:0` (`markdownTransformers` is `readonly MarkdownTransformer[]` in Pi; Go's `tui` cannot import `coding/extension`, so it takes the composed `func(string, int) string`) and `AssistantMessageComponent::construct:0` (Pi's `message?` and `updateContent(message, isStreaming)` replace Go's segment API, which the streaming path drives). Rule request: a composed `func` stands for an array of transformers whose Go composition (`createMarkdownTransform`) lives in the coding-agent layer.

## Merged Pi list classes
Pi's `TreeSelectorComponent.getTreeList()`, `SessionSelectorComponent.getSessionList()` and `ToolExecutionComponent` render pipeline return or own inner classes (`TreeList`, `SessionList`) that Go folds into the component (`tui/tree_select.go`, `internal/codingagent/session_selector.go`). A `getXList()` that returned its own receiver would be a caller-free duplicate, so these rows stay open until the list is a Go type of its own.

## Interactive lifecycle
`InteractiveMode.init()` and `stop()` are the two halves of Go's single `InteractiveMode.Run(ctx)`: its deferred teardown (`teardownCurrentTui`, `disposeArminComponents`, theme watcher, raw-mode restore) is Pi's `stop()`, and its set-up is `init()`. Splitting `Run` changes goroutine ownership and the terminal restore order; it needs an owner decision rather than a rule.

## config.ts documentation paths
`getDocsPath` is `codingagent.GetDocsPath` (the absolute `<config root>/docs`, D22). `getReadmePath` is `filepath.Join(GetDocsPath(), "README.md")` inside `prompts.docsSection`, and `getExamplesPath` is a URL (D22), so neither has a Go function with a caller; the rows stay open.

## Component constructors whose Pi parameters have no Go owner
`ExtensionEditorComponent` (Pi: `tui`, `keybindings`, `externalEditorCommand`), `BorderedLoader` (`tui`, `theme`), `SessionSelectorComponent` (`requestRender`, `onExit`, which Pi's `SessionList` never calls) and `ModelSelectorComponent` (`tui`, `modelRuntime`) take parameters that Pi uses to drive the terminal (`tui.stop()/start()` around the external editor, the loader's own interval, the model-catalog refresh). In Go the interactive owner drives those (`SetExternalEditor`, `Loader.Tick`, the refresh controller). Adding the parameters would leave fields nothing reads. Rule request: a Pi constructor parameter whose only use is a terminal or refresh lifecycle the Go owner runs is not a missing Go parameter.

## base64 strings held as bytes
`ResizedImage.data` (`string`, base64) is `imageprocessing.ResizedImage.Data []byte` (`images.go:51`); encoding it only to decode it again for the size check would add work for no observable difference. Rule request: reviewed representation of a base64 string field as `[]byte` when the field is only decoded or encoded at a Go boundary. The `resizeImage` options row reports a field named `// Default` on `ImageResizeOptions`, which looks like an inventory parse of the trailing comments in `image-resize-core.d.ts`; check the generator before treating it as a Go gap.

## /hotkeys extension shortcut order

Pi lists extension shortcuts in registration order (`runner.getShortcuts` returns a `Map`, interactive-mode.ts:6905-6916). `extension.Extension.Shortcuts` is a Go map, so `InteractiveMode.extensionShortcutsForHotkeys` orders them by extension path, then key. The two orders agree only for one shortcut per extension. Matching Pi needs the host to keep registration order (an ordered key list next to `Extension.Shortcuts` in `coding/extension/host/subprocess/host.go` and the in-process runner); that is the extension-host lane's type.

## Package source parsing versus parseGitUrl (hosted-git-info)

`coding/source` (D18) parses package and autocomplete sources without Pi's `hosted-git-info` dependency, so four github.com-style inputs still parse differently from `utils/git.ts` `parseGitUrl`: a `/tree/<ref>` path (Pi reads the ref), a trailing slash, a `#<ref>` fragment and a doubled `.git` suffix. `TestAutocompleteSourceTagGitPartMatchesPiParseGitUrl` pins them in `knownHostedGitInfoDifferences` and fails when one starts to match. Matching them needs the per-host rules of hosted-git-info (github, gitlab, bitbucket, gist, sourcehut); the package manager owner (`coding-agent-c`) decides whether to port them.

## models.json schema errors (model-config.ts)

`internal/codingagent/model_config_schema.go` ports `ModelConfig.load`'s typebox validation of models.json (messages, order, the 8-error cap, `formatValidationPath`), compared with the pinned Pi by `TestModelsJSONSchemaErrorsMatchPi` (445 documents).

Known difference, pinned in that test and counted in its log: Pi's `compat` schema is a union of three all-optional objects, so an object that fits one branch loads even when it holds a field of another branch with the wrong type (`compat.supportsStore: "yes"`). Pig decodes compat into one typed struct, which cannot hold such a value, so it reports `Failed to parse models.json: json: cannot unmarshal ...`. Closing it needs a tolerant decode of `providerCompat` (owner: coding-agent-c, the model registry).

Pi 1.1.0 rejects a `null` header value in models.json (`HeadersSchema` is `Record<string, string>`), so `TestModelRegistryNullableHeadersDeleteProviderDefaults` and the `X-DELETE: null` case of `TestExtensionRequestHeaderCompositionMatchesPiOrder` described behaviour the pinned Pi does not have; both were rewritten (coding-agent-c owns `coding/model_runtime_0861_test.go`).

## InteractiveMode and model-resolver rows left for an owner decision (lg-c4-coding-agent-d, after rows-split-23)

Each row below needs a public Go shape change that the lane cannot make additively. The evidence is Pi 1.1.0 source; the decision is the owner's (LEAD-DECISION-P1: a real public API break needs an owner decision).

- `InteractiveMode::construct:0` (S4, Pi `constructor(runtimeHost, options)` at interactive-mode.ts:608, Go `NewInteractiveMode(opts)`), `::property:init` and `::property:stop` (Pi `async init()` at :943 and `stop()` near :4294, Go has only `Run(ctx)`), `InteractiveModeOptions::property:terminal` (Pi `terminal?: Terminal` at :447). `Run` (interactive.go:1268) owns the whole lifetime through nested defers (crash handler, background tasks, theme watcher, raw mode, TUI teardown). Splitting it into `Init` and `Stop` moves those defers to caller-visible calls and changes who restores the terminal after a crash. A `Terminal` option needs the tui renderer to take an injected terminal, which the Go renderer (`createInteractiveTui`) does not.
- `InteractiveMode::showNewVersionNotification::call:0` (Pi `release: LatestPiRelease {version, packageName?, note?}`, version-check.ts:8; Go `*BinaryUpdate` with `LatestVersion`, `Notes`, `ChangelogURL`, `Command`). Pig's self-update (selfupdate.go) is its own design with an update command per ownership tier; a Pi-shaped parameter would drop `Command` and `ChangelogURL`, which the notice renders. Needs either a numbered divergence for the self-update notice or a `LatestPiRelease` parameter with the Pig fields read from the update service.
- `ResolveCliModelResult::property:model` and `resolveCliModel::call:0` (Pi `model: Model<Api> | undefined`, `modelRuntime: ModelRuntime`; Go `*RuntimeModel`, `ModelResolverRuntime`). `RuntimeModel` carries virtual models, which have no `ai.Model` (`NativeModel` is nil for them), and `cmd/pig` selects them through the same result. Returning `*ai.Model` loses virtual selections; keeping `RuntimeModel` stays a T9t mismatch. Needs a decision on whether virtual models get an `ai.Model` form.
