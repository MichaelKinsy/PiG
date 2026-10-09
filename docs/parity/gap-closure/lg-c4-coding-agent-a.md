# lg-c4-coding-agent-a: coding-agent rows of files a-m at Pi 1.1.0

Base: integrate-042 (ca47733fe8 and later). Detector: `make interface-gaps`, 998 gaps before this lane's change and 982 after it, with no new gap. Of the 16 rows that left the list, 10 are this lane's (listed below). `ModelSelectorComponent.dispose`, `FooterComponent.dispose` and `SessionManager._persist` were closed by lg-imla-8 and lg-imla-1b on integrate-042 while this lane worked.

## Closed by this lane (10 rows)

| Pi row | Go | Evidence |
|---|---|---|
| `AgentSessionEventListener`, `::call:0` | `coding.AgentSessionEventListener`, the type `Session.Subscribe` takes (agent-session.ts:240) | `TestSessionSubscribeTakesAnAgentSessionEventListener` |
| `getDocsPath`, `getReadmePath`, `getExamplesPath` and their `::call:0` | `prompts.defaultPigDocsPath`, `defaultPigReadmePath`, `defaultPigExamplesPath`; `docsSection` reads them (system-prompt.ts:163-165, D22 bundle) | `TestDefaultDocsPathsFeedTheSystemPrompt` (two mutations red) |
| `keyText`, `::call:0` | `tui.ActionKeyText` (renames.json); caller `tui/extension_selector.go` | `tui/keybinding_hints_test.go` |

`FooterComponent.Dispose` (lg-imla-8's body was empty) now does the cleanup of Pi's `footerDataProvider.dispose()` (footer-data-provider.ts:187), which `stop()` calls next to `footer.dispose()` (interactive-mode.ts:7098-7099): it stops the git branch watcher, drops the `OnBranchChange` subscribers and ignores a later branch result. Test `TestFooterDisposeStopsTheGitWatcherAndDropsSubscribers` (a stop that does not reach the watcher context, and a late publish, are both red).

## Reviewed representations of narrow consumer-owned parameter interfaces (6 rows)

A Go function that takes a minimal interface where Pi takes a class closes its `::call:0` row (and its parent) when Pi's body reads only the members the interface carries, so every Pi value is acceptable to the Go parameter. `representations.json` records the Go form; its value may list several, separated by ` | ` (`rules.go` T9c, with a table case in `rules_test.go`).

| Pi type (coding-agent) | Go form | Pi members read | Rows |
|---|---|---|---|
| `ReadonlySessionManager` | `compaction.ReadonlySession` (`GetBranch`, `GetEntry`) | `collectEntriesForBranchSummary` calls `getBranch` and `getEntry` only (branch-summarization.ts:104-148); production caller `coding/session.go:2590` | `collectEntriesForBranchSummary` + `::call:0` |
| `ModelRuntime` | `codingagent.ModelResolverRuntime` (`GetModels`, `HasConfiguredAuth`) or `codingagent.ModelScopeRuntime` (`GetAvailable`) | `resolveCliModel` reads `getModels`, `hasConfiguredAuth` (model-resolver.ts:420,479); `resolveModelScopeWithDiagnostics` reads `getAvailable` (:369) | `resolveCliModel`, `resolveModelScopeWithDiagnostics`, each with `::call:0` |

Review lg-review-c2 parked both `representations.json` entries: reviewed representations are lead-approved inputs, and a representation applies to the Pi name in every position (fields and results too), not only parameters. The parameter case is rule T9i (lg-c4-tui-b 02f298a67, restricted to a library-implemented declaration's own parameters by d1f8412e8), which closes `collectEntriesForBranchSummary` without a representation; the `ModelRuntime` rows wait for T9i or a lead-approved entry. The three `reviewed-gaps.json` entries stay until then. Result types are not treated this way: a narrower Go result loses members the caller can read (`ResolveCliModelResult.model` stays open for that reason).

## Probed and not closed (145 rows left in a-m), by what closes them

None of these closes by a Go member alone; each would add a caller-free member or break a public Go signature (LEAD-RULINGS-1520: no API break, no stubs).

- **Rule request: component constructors that take `theme`, `tui`, `keybindings`** (S4 parameter counts): `BorderedLoader`, `BashExecutionComponent`, `CompactionSummaryMessageComponent`, `ExtensionEditorComponent`, `ExtensionSelectorComponent`, `FooterComponent`, `LoginDialogComponent`, `ModelSelectorComponent` (9 Pi parameters; Go 4), `InteractiveMode`, `KeybindingsManager`, `ArminComponent`, `BranchSummaryMessageComponent`. Go components read `ActiveTheme()`/`GetTUIKeybindings()`; a rule must accept a constructor that omits the injected services (as `Box::construct:0` was closed by renames).
- **Rule request: tool results** `createBashToolDefinition`, `createEditToolDefinition`, `createFindToolDefinition`, `createGrepToolDefinition`, `createLsToolDefinition` (`::call:0`): the detector wants `ToolDefinition.annotations` on `tools.BashTool` etc. Built-in tools set no annotations in Pi 1.1.0 (only `extensions/mcp` does: tools.ts:279, resources.ts:261); Go carries `extension.ToolDefinition.Annotations` on `Definition()` (coding/extension/tool.go:199). Same placement as the `createWriteTool` request in `lg-imla-1-shard1.md`.
- **Rule request: overloads and placement** `ModelRegistry.hasConfiguredAuth(model)` is `HasConfiguredAuth(providerID)` (Pi delegates to `runtime.hasConfiguredAuth(model.provider)`, model-registry.ts:86; nine Go callers pass a provider id); `ModelRegistry.registerProvider` overloads; `ModelRuntime.getAuth::call:1` is `GetModelAuth` (renames.json cannot key a `::call:N` row); `createAgentSessionFromServices(options)` is `NewSession(services, opts)` with `services` positional.
- **Waits on port-110-cli-tools** (`--tools +name/-name`): `AgentSessionConfig.defaultToolModifiers`, `usesDefaultTools` (agent-session.ts:278, 3660-3669; sdk.ts:473) and `baseToolsOverride`. Go's `resolveToolSelection` (cmd/pig/cli_runtime_build.go:565) does not apply modifiers yet.
- **Needs a decided Go shape** `AgentSession.state` (Go has no `AgentState` type; Pi reads `session.state` in print-mode.ts:140, footer.ts:158, session-share.ts:38), `AgentSession.construct`, `AgentSessionRuntime.construct` (five Pi parameters), `CreateAgentSessionRuntimeFactory` (`projectTrustContext`), `AgentSessionRuntime.switchSession` (`cwdOverride` lives on `SessionOptions.CWDOverride`), `newSession` (`SessionManager` is `extension.SessionManager`).
- **Input-dispatch redesign** `CustomEditor` and its nine members, `CustomEditorOptions`: Pi's `handleInput` (custom-editor.ts:86) orders extension shortcuts, paste-image, interrupt, exit-on-empty, history, then registered actions. Go spreads that over `keys.go`, `remote_editor.go` and the interactive dispatcher; moving it into a `CustomEditor` type changes input ordering on the TUI path and needs the `behavior-contracts.toml` entry the detector asks for (B1).
- **Mode lifecycle** `InteractiveMode.init`/`stop` (Go's `Run` owns both; its teardown is `stopInteractiveTui`), `InteractiveModeOptions.terminal` (Go injects the writer through the unexported `rendererOut`; an exported option nothing sets would be caller-free), `MainOptions` (`extensionFactories` would be a linked extension runtime, which AGENTS.md forbids).
- **Type representation** `ResizedImage.data` (Go keeps raw bytes; Pi holds base64, and converting would encode and decode every image for no behaviour), `McpServerConfig` (A8 union), `ReadonlyFooterDataProvider`, `JsonAgentSessionEvent`, `ResolveCliModelResult.model`, `KeybindingsManager.getEffectiveConfig`.
- **Signature mismatches that are Go context or completer parameters** `compact`, `generateSummary(WithUsage)`, `convertToLlm(messages, model)`, `createLocalBashOperations(settings, binDir)`, `McpExtensionOptions.loadConfig` (Pi passes `ExtensionContext`; rule S1e expects `context.Context`, Go passes the mcpext `EventContext` struct, and all eleven test callers ignore it).
- **Different PiG mechanism** `showNewVersionNotification(release)`: PiG uses `BinaryUpdate` (self-update manifest), not Pi's npm release record.
- **Detector defect** `resizeImage::call:0`: the `ImageResizeOptions` members include the `// Default: ...` comment text (image-resize-core.ts:4-9), already listed in `lg-imla-1-shard1.md`.
