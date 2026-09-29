# Node shim stand-in audit

Baseline: `fdcf667708`. Reference: Pi 0.87.1. Regenerate with `python3 test/parity/unit-evidence/audit-node-shims.py --baseline fdcf667708 --corpus <locked-corpus-root> --output test/parity/unit-evidence/node-shim-audit.md`.

The table lists every throwing or empty stand-in in the three package shims, plus the partial helpers identified by inspection. Corpus names are static production imports from the locked corpus, not feature-pass claims. Tests and nested dependency installations are excluded. Real package execution is recorded separately in `fix-node-createagentsession.md`.

| Module / export | Pi source | Disposition | Published corpus imports |
|---|---|---|---|
| `pi-coding-agent.AgentSession` | `packages/coding-agent/src/core/agent-session.ts:328` | exact Pi module |  |
| `pi-coding-agent.AgentSessionEvent` | `packages/coding-agent/src/core/agent-session.ts:164` | removed type-only stand-in; Pi has no runtime export |  |
| `pi-coding-agent.AgentSessionRuntime` | `packages/coding-agent/src/core/agent-session-runtime.ts:74` | exact Pi module |  |
| `pi-coding-agent.ArminComponent` | `packages/coding-agent/src/modes/interactive/components/armin.ts:60` | exact Pi module |  |
| `pi-coding-agent.AssistantMessageComponent` | `packages/coding-agent/src/modes/interactive/components/assistant-message.ts:14` | exact Pi module | `@narumitw/pi-btw:dist/index.ts`<br>`@narumitw/pi-btw:src/transcript-pager.ts`<br>`pi-cc-extensions:extensions/feature/compact-thinking.ts`<br>`pi-cc-extensions:extensions/renderer/compact-mode.ts`<br>`pi-cc-extensions:extensions/renderer/tool/grouping.ts`<br>`pi-cc-extensions:extensions/renderer/transcript-refresh.ts` |
| `pi-coding-agent.BashExecutionComponent` | `packages/coding-agent/src/modes/interactive/components/bash-execution.ts:21` | exact Pi module |  |
| `pi-coding-agent.BorderedLoader` | `packages/coding-agent/src/modes/interactive/components/bordered-loader.ts:7` | exact Pi module | `@narumitw/pi-btw:dist/index.ts`<br>`@narumitw/pi-btw:src/menu.ts`<br>`pi-docparser:extensions/docparser/doctor.ts` |
| `pi-coding-agent.BranchSummaryMessageComponent` | `packages/coding-agent/src/modes/interactive/components/branch-summary-message.ts:10` | exact Pi module | `pi-cc-extensions:extensions/renderer/tool/message-display.ts` |
| `pi-coding-agent.CompactionSummaryMessageComponent` | `packages/coding-agent/src/modes/interactive/components/compaction-summary-message.ts:10` | exact Pi module | `pi-cc-extensions:extensions/renderer/tool/message-display.ts` |
| `pi-coding-agent.CustomMessageComponent` | `packages/coding-agent/src/modes/interactive/components/custom-message.ts:12` | exact Pi module |  |
| `pi-coding-agent.DefaultPackageManager` | `packages/coding-agent/src/core/package-manager.ts:806` | exact Pi module | `@henryqw/pi-subagent:dist/index.js` |
| `pi-coding-agent.DefaultResourceLoader` | `packages/coding-agent/src/core/resource-loader.ts:196` | exact Pi module | `@akagilnc/pi-workflow-roles:dist/pi/in-process-session.js`<br>`@akagilnc/pi-workflow-roles:src/pi/in-process-session.ts`<br>`@langchain/langsmith-pi-extension:dist/sdk.js` |
| `pi-coding-agent.DynamicBorder` | `packages/coding-agent/src/modes/interactive/components/dynamic-border.ts:11` | exact Pi module | `@juicesharp/rpiv-ask-user-question:view/dialog-builder.ts`<br>`gentle-pi:extensions/ask-user-choice.ts`<br>`gentle-pi:extensions/ask-user-question.ts`<br>`pi-mcp-adapter:mcp-panel-theme.ts`<br>`pi-subagents:src/slash/selector.js` |
| `pi-coding-agent.ExtensionAPI` | `packages/coding-agent/src/core/extensions/types.ts:1349` | removed type-only stand-in; Pi has no runtime export | `@raindrop-ai/pi-agent:dist/extension.d.ts` |
| `pi-coding-agent.ExtensionCommandContext` | `packages/coding-agent/src/core/extensions/types.ts:359` | removed type-only stand-in; Pi has no runtime export |  |
| `pi-coding-agent.ExtensionContext` | `packages/coding-agent/src/core/extensions/types.ts:313` | removed type-only stand-in; Pi has no runtime export |  |
| `pi-coding-agent.ExtensionEditorComponent` | `packages/coding-agent/src/modes/interactive/components/extension-editor.ts:26` | exact Pi module |  |
| `pi-coding-agent.ExtensionInputComponent` | `packages/coding-agent/src/modes/interactive/components/extension-input.ts:18` | exact Pi module |  |
| `pi-coding-agent.ExtensionRunner` | `packages/coding-agent/src/core/extensions/runner.ts:353` | exact Pi module |  |
| `pi-coding-agent.ExtensionSelectorComponent` | `packages/coding-agent/src/modes/interactive/components/extension-selector.ts:19` | exact Pi module |  |
| `pi-coding-agent.FooterComponent` | `packages/coding-agent/src/modes/interactive/components/footer.ts:50` | exact Pi module |  |
| `pi-coding-agent.InteractiveMode` | `packages/coding-agent/src/modes/interactive/interactive-mode.ts:415` | exact Pi module | `pi-cc-extensions:extensions/feature/shell/flush-docked-bash.ts` |
| `pi-coding-agent.KeybindingsManager` | `packages/coding-agent/src/core/keybindings.ts:371` | removed type-only stand-in; Pi has no runtime export | `@narumitw/pi-btw:dist/index.ts`<br>`@narumitw/pi-btw:src/keybindings.ts`<br>`gentle-pi:lib/native-choice-list.ts` |
| `pi-coding-agent.LoginDialogComponent` | `packages/coding-agent/src/modes/interactive/components/login-dialog.ts:11` | exact Pi module |  |
| `pi-coding-agent.MessageRenderer` | `packages/coding-agent/src/core/extensions/types.ts:1310` | removed type-only stand-in; Pi has no runtime export |  |
| `pi-coding-agent.ModelRuntime` | `packages/coding-agent/src/core/model-runtime.ts:131` | exact Pi module | `@akagilnc/pi-workflow-roles:dist/pi/in-process-session.js`<br>`@akagilnc/pi-workflow-roles:src/pi/in-process-session.ts`<br>`@companion-ai/feynman:dist/model/registry.js`<br>`pi-btw:extensions/btw.ts` |
| `pi-coding-agent.ModelSelectEvent` | `packages/coding-agent/src/core/extensions/types.ts:914` | removed type-only stand-in; Pi has no runtime export |  |
| `pi-coding-agent.ModelSelectorComponent` | `packages/coding-agent/src/modes/interactive/components/model-selector.ts:40` | exact Pi module |  |
| `pi-coding-agent.OAuthSelectorComponent` | `packages/coding-agent/src/modes/interactive/components/oauth-selector.ts:29` | exact Pi module |  |
| `pi-coding-agent.ProjectTrustStore` | `packages/coding-agent/src/core/trust-manager.ts:209` | exact Pi module |  |
| `pi-coding-agent.ResourceLoader` | `packages/coding-agent/src/core/resource-loader.ts:40` | removed type-only stand-in; Pi has no runtime export |  |
| `pi-coding-agent.RpcClient` | `packages/coding-agent/src/modes/rpc/rpc-client.ts:56` | exact Pi module |  |
| `pi-coding-agent.SessionEntry` | `packages/coding-agent/src/core/session-manager.ts:183` | removed type-only stand-in; Pi has no runtime export |  |
| `pi-coding-agent.SessionSelectorComponent` | `packages/coding-agent/src/modes/interactive/components/session-selector.ts:694` | exact Pi module |  |
| `pi-coding-agent.SettingsSelectorComponent` | `packages/coding-agent/src/modes/interactive/components/settings-selector.ts:447` | exact Pi module |  |
| `pi-coding-agent.ShowImagesSelectorComponent` | `packages/coding-agent/src/modes/interactive/components/show-images-selector.ts:13` | exact Pi module |  |
| `pi-coding-agent.SkillInvocationMessageComponent` | `packages/coding-agent/src/modes/interactive/components/skill-invocation-message.ts:11` | exact Pi module | `pi-cc-extensions:extensions/renderer/tool/message-display.ts` |
| `pi-coding-agent.Theme` | `packages/coding-agent/src/modes/interactive/theme/theme.ts:282` | exact Pi module |  |
| `pi-coding-agent.ThemeSelectorComponent` | `packages/coding-agent/src/modes/interactive/components/theme-selector.ts:13` | exact Pi module |  |
| `pi-coding-agent.ThinkingSelectorComponent` | `packages/coding-agent/src/modes/interactive/components/thinking-selector.ts:36` | exact Pi module |  |
| `pi-coding-agent.ToolExecutionComponent` | `packages/coding-agent/src/modes/interactive/components/tool-execution.ts:47` | exact Pi module | `pi-cc-extensions:extensions/renderer/compact-mode.ts`<br>`pi-cc-extensions:extensions/renderer/default-mode.ts`<br>`pi-cc-extensions:extensions/renderer/index.ts`<br>`pi-cc-extensions:extensions/renderer/mouse/interaction.ts`<br>`pi-cc-extensions:extensions/renderer/tool/grouping.ts`<br>`pi-cc-extensions:extensions/renderer/transcript-refresh.ts` |
| `pi-coding-agent.ToolResultEvent` | `packages/coding-agent/src/core/extensions/types.ts:1096` | removed type-only stand-in; Pi has no runtime export |  |
| `pi-coding-agent.TreeSelectorComponent` | `packages/coding-agent/src/modes/interactive/components/tree-selector.ts:1336` | exact Pi module | `@narumitw/pi-btw:dist/index.ts`<br>`@narumitw/pi-btw:src/main-tree-picker.ts` |
| `pi-coding-agent.TurnEndEvent` | `packages/coding-agent/src/core/extensions/types.ts:853` | removed type-only stand-in; Pi has no runtime export |  |
| `pi-coding-agent.UserMessageComponent` | `packages/coding-agent/src/modes/interactive/components/user-message.ts:13` | exact Pi module | `@narumitw/pi-btw:dist/index.ts`<br>`@narumitw/pi-btw:src/transcript-pager.ts` |
| `pi-coding-agent.UserMessageSelectorComponent` | `packages/coding-agent/src/modes/interactive/components/user-message-selector.ts:110` | exact Pi module |  |
| `pi-coding-agent.collectEntriesForBranchSummary` | `packages/coding-agent/src/core/compaction/branch-summarization.ts:108` | exact Pi module |  |
| `pi-coding-agent.compact` | `packages/coding-agent/src/core/compaction/compaction.ts:987` | exact Pi module | `pi-claude-bridge:src/index.ts` |
| `pi-coding-agent.convertToPng` | `packages/coding-agent/src/utils/image-convert.ts:30` | exact Pi module |  |
| `pi-coding-agent.copyToClipboard` | `packages/coding-agent/src/utils/clipboard.ts:74` | exact Pi module | `@narumitw/pi-btw:dist/index.ts`<br>`@narumitw/pi-btw:src/fullscreen-ui.ts`<br>`@narumitw/pi-btw:src/main-tree-picker.ts`<br>`pi-mcp-adapter:mcp-panel.ts`<br>`pi-powerline-footer:index.ts` |
| `pi-coding-agent.createAgentSession` | `packages/coding-agent/src/core/sdk.ts:175` | exact Pi module | `@akagilnc/pi-workflow-roles:dist/pi/in-process-session.js`<br>`@akagilnc/pi-workflow-roles:src/pi/in-process-session.ts`<br>`@langchain/langsmith-pi-extension:dist/sdk.js`<br>`pi-btw:extensions/btw.ts`<br>`pi-goal-x:extensions/goal-auditor.ts` |
| `pi-coding-agent.createAgentSessionFromServices` | `packages/coding-agent/src/core/agent-session-services.ts:202` | exact Pi module |  |
| `pi-coding-agent.createAgentSessionRuntime` | `packages/coding-agent/src/core/agent-session-runtime.ts:422` | exact Pi module |  |
| `pi-coding-agent.createAgentSessionServices` | `packages/coding-agent/src/core/agent-session-services.ts:135` | exact Pi module |  |
| `pi-coding-agent.createLocalPowerShellOperations` | `packages/coding-agent/src/core/tools/powershell.ts:32` | exact Pi module |  |
| `pi-coding-agent.createPowerShellTool` | `packages/coding-agent/src/core/tools/powershell.ts:59` | exact Pi module |  |
| `pi-coding-agent.createPowerShellToolDefinition` | `packages/coding-agent/src/core/tools/powershell.ts:49` | exact Pi module |  |
| `pi-coding-agent.detectSupportedImageMimeTypeFromFile` | `packages/coding-agent/src/utils/mime.ts:25` | exact Pi module |  |
| `pi-coding-agent.discoverAndLoadExtensions` | `packages/coding-agent/src/core/extensions/loader.ts:749` | exact Pi module |  |
| `pi-coding-agent.findCutPoint` | `packages/coding-agent/src/core/compaction/compaction.ts:468` | exact Pi module |  |
| `pi-coding-agent.findTurnStartIndex` | `packages/coding-agent/src/core/compaction/compaction.ts:434` | exact Pi module |  |
| `pi-coding-agent.formatDimensionNote` | `packages/coding-agent/src/utils/image-resize.ts:116` | exact Pi module |  |
| `pi-coding-agent.generateBranchSummary` | `packages/coding-agent/src/core/compaction/branch-summarization.ts:293` | exact Pi module | `pi-claude-bridge:src/index.ts` |
| `pi-coding-agent.generateSummary` | `packages/coding-agent/src/core/compaction/compaction.ts:667` | exact Pi module |  |
| `pi-coding-agent.generateSummaryWithUsage` | `packages/coding-agent/src/core/compaction/compaction.ts:718` | exact Pi module |  |
| `pi-coding-agent.getDocsPath` | `packages/coding-agent/src/config.ts:445` | exact Pi module |  |
| `pi-coding-agent.getExamplesPath` | `packages/coding-agent/src/config.ts:450` | exact Pi module |  |
| `pi-coding-agent.getPackageDir` | `packages/coding-agent/src/config.ts:389` | exact Pi module | `@gotgenes/pi-permission-system:src/index.ts` |
| `pi-coding-agent.getPowerShellConfig` | `packages/coding-agent/src/utils/shell.ts:125` | exact Pi module |  |
| `pi-coding-agent.getReadmePath` | `packages/coding-agent/src/config.ts:440` | exact Pi module | `@heyhuynhgiabuu/pi-pretty:dist/tools/read.js`<br>`@heyhuynhgiabuu/pi-pretty:src/tools/read.ts` |
| `pi-coding-agent.getShellConfig` | `packages/coding-agent/src/utils/shell.ts:67` | exact Pi module |  |
| `pi-coding-agent.hasTrustRequiringProjectResources` | `packages/coding-agent/src/core/trust-manager.ts:185` | exact Pi module |  |
| `pi-coding-agent.initTheme` | `packages/coding-agent/src/modes/interactive/theme/theme.ts:774` | exact Pi module |  |
| `pi-coding-agent.loadProjectContextFiles` | `packages/coding-agent/src/core/resource-loader.ts:119` | exact Pi module |  |
| `pi-coding-agent.loadSkills` | `packages/coding-agent/src/core/skills.ts:409` | exact Pi module |  |
| `pi-coding-agent.loadSkillsFromDir` | `packages/coding-agent/src/core/skills.ts:168` | exact Pi module |  |
| `pi-coding-agent.main` | `packages/coding-agent/src/main.ts:566` | exact Pi module |  |
| `pi-coding-agent.parseArgs` | `packages/coding-agent/src/cli/args.ts:71` | exact Pi module |  |
| `pi-coding-agent.prepareBranchEntries` | `packages/coding-agent/src/core/compaction/branch-summarization.ts:195` | exact Pi module |  |
| `pi-coding-agent.readStoredCredential` | `packages/coding-agent/src/core/auth-storage.ts:496` | exact Pi module | `@narumitw/pi-usage:dist/index.ts`<br>`@narumitw/pi-usage:src/codex-resets.ts`<br>`@narumitw/pi-usage:src/oauth-credential-source.ts`<br>`@narumitw/pi-usage:src/query.ts` |
| `pi-coding-agent.renderDiff` | `packages/coding-agent/src/modes/interactive/components/diff.ts:79` | exact Pi module |  |
| `pi-coding-agent.resizeImage` | `packages/coding-agent/src/utils/image-resize.ts:85` | exact Pi module |  |
| `pi-coding-agent.resolveCliModel` | `packages/coding-agent/src/core/model-resolver.ts:406` | exact Pi module |  |
| `pi-coding-agent.resolveModelScopeWithDiagnostics` | `packages/coding-agent/src/core/model-resolver.ts:364` | exact Pi module |  |
| `pi-coding-agent.runPrintMode` | `packages/coding-agent/src/modes/print-mode.ts:33` | exact Pi module |  |
| `pi-coding-agent.runRpcMode` | `packages/coding-agent/src/modes/rpc/rpc-mode.ts:54` | exact Pi module |  |
| `pi-coding-agent.wrapRegisteredTool` | `packages/coding-agent/src/core/extensions/wrapper.ts:17` | exact Pi module |  |
| `pi-coding-agent.wrapRegisteredTools` | `packages/coding-agent/src/core/extensions/wrapper.ts:25` | exact Pi module |  |
| `pi-tui.Component` | `packages/tui/src/tui.ts:111` | owned by fix-node-scrollview; unchanged in this slice |  |
| `pi-tui.EditorTheme` | `packages/tui/src/components/editor.ts:237` | owned by fix-node-scrollview; unchanged in this slice |  |
| `pi-tui.Focusable` | `packages/tui/src/tui.ts:152` | owned by fix-node-scrollview; unchanged in this slice |  |
| `pi-tui.Image` | `packages/tui/src/components/image.ts:25` | owned by fix-node-scrollview; unchanged in this slice |  |
| `pi-tui.OverlayHandle` | `packages/tui/src/tui.ts:270` | owned by fix-node-scrollview; unchanged in this slice |  |
| `pi-tui.ProcessTerminal` | `packages/tui/src/terminal.ts:137` | owned by fix-node-scrollview; unchanged in this slice | `@kontextmind/kxm:packages/core/tui/dist/index.js`<br>`@kontextmind/kxm:packages/core/tui/src/adapters/terminal.ts`<br>`@kontextmind/kxm:plugins/kxm/src/tui.ts`<br>`@kontextmind/kxm:scripts/demo-overlay.mjs` |
| `pi-tui.ScrollView` | `packages/tui/src/components/scroll-view.ts:22` | owned by fix-node-scrollview; unchanged in this slice | `@kontextmind/kxm:packages/core/tui/dist/index.js`<br>`@kontextmind/kxm:packages/core/tui/src/tui/panelComponent.ts`<br>`@kontextmind/kxm:plugins/kxm/src/tui.ts`<br>`@narumitw/pi-btw:dist/index.ts`<br>`@narumitw/pi-btw:src/transcript-pager.ts`<br>`@narumitw/pi-btw:src/workspace-layout.ts`<br>`gentle-pi:lib/shell-sidebar-layout.ts` |
| `pi-tui.SelectItem` | `packages/tui/src/components/select-list.ts:12` | owned by fix-node-scrollview; unchanged in this slice |  |
| `pi-tui.TUI` | `packages/tui/src/tui.ts:425` | owned by fix-node-scrollview; unchanged in this slice |  |
| `pi-tui.TuiAltScreen` | `packages/tui/src/tui-alt-screen.ts:197` | owned by fix-node-scrollview; unchanged in this slice | `@kontextmind/kxm:packages/core/tui/dist/index.js`<br>`@kontextmind/kxm:packages/core/tui/src/adapters/terminal.ts`<br>`@kontextmind/kxm:plugins/kxm/src/tui.ts`<br>`@kontextmind/kxm:scripts/demo-overlay.mjs`<br>`@narumitw/pi-btw:dist/index.ts`<br>`@narumitw/pi-btw:src/fullscreen-ui.ts` |
| `pi-tui.TuiMainScreen` | `packages/tui/src/tui-main-screen.ts:124` | owned by fix-node-scrollview; unchanged in this slice |  |
| `pi-tui.detectCapabilities` | `packages/tui/src/terminal-image.ts:139` | owned by fix-node-scrollview; unchanged in this slice |  |
| `pi-tui.getCapabilities` | `packages/tui/src/terminal-image.ts:160` | exact Pi capability cache (required by Theme initialization) |  |
| `pi-tui.getCellDimensions` | `packages/tui/src/terminal-image.ts:40` | owned by fix-node-scrollview; unchanged in this slice |  |
| `pi-tui.getNativeClipboard` | `packages/tui/src/native-platform.ts:59` | owned by fix-node-scrollview; unchanged in this slice |  |
| `pi-tui.renderImage` | `packages/tui/src/terminal-image.ts:610` | owned by fix-node-scrollview; unchanged in this slice |  |
| `pi-tui.resetCapabilitiesCache` | `packages/tui/src/terminal-image.ts:171` | owned by fix-node-scrollview; unchanged in this slice |  |
| `pi-tui.setCapabilities` | `packages/tui/src/terminal-image.ts:189` | owned by fix-node-scrollview; unchanged in this slice |  |
| `pi-tui.setCapabilityOverrides` | `packages/tui/src/terminal-image.ts:176` | owned by fix-node-scrollview; unchanged in this slice |  |
| `pi-tui.setCellDimensions` | `packages/tui/src/terminal-image.ts:44` | owned by fix-node-scrollview; unchanged in this slice |  |
| `pi-ai.cleanupSessionResources` | `packages/ai/src/session-resources.ts:12` | exact Pi module |  |
| `pi-ai.registerSessionResourceCleanup` | `packages/ai/src/session-resources.ts:5` | exact Pi module |  |

## Other shim modules

- `builtin-tools.mjs`: all tool factories/definitions, truncation helpers and the file-mutation queue now re-export `packages/coding-agent/src/core/tools/index.ts` and `truncate.ts`. The baseline substituted text-only `renderCall`/`renderResult` components and partially reimplemented file/image/tool behavior. The actual tool modules replace that implementation, including PowerShell and the image worker.
- `proper-lockfile.mjs`: the baseline supplied only a partial `lockSync`, removed regular files, and omitted async `lock`. It now uses the exact locked `proper-lockfile@4.1.2`, including its transitive dependency versions. Pi callers are `packages/coding-agent/src/core/auth-storage.ts:52-189` and `core/settings-manager.ts:214-299`. Go auth, model, settings and trust stores use the same directory-lock protocol.
- `pi-ai-bridge.mjs`: missing-key and pre-abort error streams implement normal API failures. Built-in API execution remains D74's Go-host bridge. `bridgeImages` remains D74's explicit unsupported image-generation result; the corpus audit found no production import of that capability. Provider-specific option/result gaps in D74 are not retired here.
- `pig-config.mjs`: D2's separate configuration root is intentional, not a stand-in. Independent stores use this root.
- `pi-agent-core.mjs`: the complete Pi module graph and default stream function are real implementations. There is no throwing Agent stand-in.
- `pi-ai-oauth.mjs`: Pi's OAuth entry is type-only; its empty runtime namespace is correct.
- `typebox*.mjs` and `jiti/*.mjs`: these are locked dependency modules. Their validation/resolution exceptions are ordinary library errors, not fabricated exports.

An imported independent UI class is not the live Go Main Screen. Prototype patches or arbitrary main-process component references remain D73's identity boundary. Providing real imported constructors does not claim that every private main-screen patch in `pi-cc-extensions` affects PiG's Go objects.
