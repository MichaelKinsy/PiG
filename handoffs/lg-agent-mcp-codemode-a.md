# HANDOFF lg-agent-mcp-codemode-a (ledger resplit shard 1)

Branch lg-agent-mcp-codemode-a, worktree lg-agent-mcp-codemode-a.

## Done (batches 1-3)
- Tests for the 14 NEEDS-TEST rows (chord Tracker.Revision, RemoteServiceError.Message, durable/env Ok, env RemoteError, ServerListener, telemetry fixture factory).
- ExtensionRuntime embeds ExtensionActions (stubs), FlagValues, AssertActive/Invalidate/TrackEventBusSubscription, RegisterNativeProvider (coding/extension/runtime_state.go).
- Extracted/ported: BuildCopilotDynamicHeaders, ResolveGoogleFunctionCallingMode, GetLatestCompactionEntry, IsToolCallEventType, IsPowerShellToolResult.
- tui: Editor.IsShowingAutocomplete / SetAutocompleteProvider (renames), EditorTheme/SelectListTheme + NewEditor options + FilterableList.Theme, UserMessageSelectorComponent + UserMessageList, ExtensionSelectorComponent built on Container (+Dispose, AdoptCountdown).
- ai: StreamOptions.Azure{APIVersion,BaseURL,DeploymentName,ResourceName} wired for Responses and Completions.
- shards/rule-requests-1.tsv (copy in docs/parity/gap-closure/) lists every group with status PORTED/DONE/PARTIAL or the rule/rename requested.

## Left (see rule-requests-1.tsv, family != PORTED/DONE)
- ToolExecutionComponent (Container members, 7-param ctor, updateResult/updateArgs), AgentSession (-> coding.Session placement, 131 rows), CreateAgentSessionOptions, FindToolOptions/createWriteTool/createLocalBashOperations/TruncationOptions (tool Options/Operations extension points: lead answer 4), resizeImage mimeType, TuiMode/KeybindingsConfig, HStack LAYOUT_NODE, EventStream generic, OpenAICompletionsCompat/OpenAIResponsesStreamOptions missing fields, chord defineService params, error classes name/stack/cause, durable Context-parameter rows (rule only).
- make interface-gaps was not re-run after batches 2-3 (8 min); run it and update the baseline.

## Known env failures (not from this lane)
ai/tui/codingagent tests that need the Pi oracle or the pi-ai package fail with "1.0.3, want 1.0.4" (node_modules); TestHerdrWithGraphicsSignalRendersKittyImage fails on this host.

## Rework after the rejects (batch 5)
- ExtensionRuntime: deleted the unread ExtensionActions embed/stubs and TrackEventBusSubscription. Wired instead: FlagValues (Runner defaults/SetFlagValue/GetFlagValues), Invalidate/AssertActive (Runner.Invalidate -> runtime.Invalidate, Runner.assertActive consults the runtime), the native-provider queue (subprocess Host registrations queue until BindProviderActions/BindCore drain them to the registry; ProviderActions.RegisterNativeProvider).
- GetLatestCompactionEntry now has a production caller (coding.latestCompactionTimestamp) and never skips a malformed newest entry (session-manager.ts:372).
- Deleted the duplicate tui.UserMessageSelectorComponent; the rule request maps Pi's component to tui.UserMessageSelector.
- Error classes: constructors are used by the production creation sites.
- Rule requests in docs/parity/gap-closure/rule-requests-1.tsv (REWORK rows).

## Batch 7 (32839ba64 + this)
- bindCore composes with host-bound provider actions (fixes the d81fd2cae regression); ExtensionRuntime embeds ExtensionActions, installed by Runner.BindCore and read lazily by contexts.
- Detector run on 32839ba64: 161 of 221 shard-1 rows open. Remaining are mostly rule requests (durable Context parameter x13, stack members x5, ai/compat aliases, Container members, ToolExecutionComponent ctor/updateResult, Editor ctor, TuiMode/KeybindingsConfig/HStack LAYOUT_NODE) plus ai provider rows owned by lg-ai lanes.
- lg-help-1 branch did not exist when last fetched.

## Batch 8 (eb1f9495c + merge 5807103dc)
- Merged reviewer fix 097dcbb25, lg-help-1 (fdf266a65: ResizeImage, TruncationOptions, KeybindingsConfig, AgentSession accessors) and integrate-042.
- tui: EditorTheme{BorderColor, SelectList}, DefaultEditorTheme, NewEditor(opts ...EditorOption) with WithEditorTheme/WithEditorPaddingX/WithEditorAutocompleteMaxVisible; interactive.go uses the options; tests tui/editor_theme_options_test.go.
- BACKGROUND_CONTEXT rule request: context.Background().
- Every remaining open shard-1 row now has a rule-requests-1.tsv entry (rule/rename request) or is owned by lg-help-1 / ai lanes; no remaining row needs a port in this lane.
