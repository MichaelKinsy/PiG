# Pig extension API parity matrix

Pig's subprocess extension API must account for every upstream Pi extension interface. A row is complete only when Pig has a protocol/API mapping, SDK coverage, and a conformance test. If exact parity is impossible or intentionally rejected, record a numbered divergence in `DIVERGENCES.md`.

Source for upstream inventory: upstream Pi `docs/extensions.md` and `examples/extensions/`.

Pi 0.87.1 added extension events and registration methods. The remaining unported declarations are named by `parity/known-gaps.toml` (scope `upstream-parity`), and `tests/upstream-parity` fails when one is fixed or a new one appears.

## Parity boundary (read this first)

Pig is a Go-native port of upstream pi. Upstream pi has exactly **one**
SDK (TypeScript) and loads extensions **in-process**. Pig deliberately
rejects in-process production extensions (no embedded JS runtime, no WASM,
no dynamic Go plugins). Two downstream-only constructs exist so that we
can still expose the upstream extension API faithfully:

- **Multi-language SDKs** (`extensions/sdk`, `extensions/sdk-rs`,
  `extensions/sdk-py`): see `docs/additive-features.md` D19. These are
  language bridges into the **same current wire contract** in
  `coding/extension/host/subprocess/protocol.go`. The wire has no independent
  version or compatibility negotiation. They are not separate APIs. Every SDK
  row in this matrix must keep its
  semantics aligned with the in-process Go reference exercised by
  `tests/extension-conformance/conformance_test.go`.
- **TypeScript declarations** (`extensions/sdk-ts`): re-export the exact pinned
  Pi extension types and add declarations for PiG-only capabilities. Node
  extensions still run through PiG's compatibility modules. This package does
  not define another runtime API.
- **Runtime cells / packed runners**: see `DIVERGENCES.md` D20. These
  are an internal optimization on the host side. They do **not** change
  the extension API: each contained extension still gets its own socket
  and runs the same wire register handshake. Treat packed cells
  as transparent for parity purposes; a row is only "complete" if it
  preserves identical semantics in both isolated and packed modes.

The wire handler declarations require a positive `handler_id` so multiple
registrations for one event preserve exact identity and order. It identifies a
handler within one extension registration; it is not a multi-register handshake. The host rejects missing, zero, or duplicate identities.

When a row is impossible or intentionally non-parity, record a numbered
entry in `DIVERGENCES.md` and link it from the Status column.

### Async contract requirement

Every upstream Promise-returning extension method, callback, handler, factory,
provider refresh/login function, and UI operation must preserve whether the host
awaits it, completion ordering, rejection/error propagation, cancellation,
concurrency, and event-loop ownership. Go `error` and result returns represent an
awaited Promise only when the host waits for the callback to return. A detached
goroutine is valid only for an intentionally unawaited upstream operation and
must have explicit lifetime ownership, cancellation, error reporting, and
shutdown draining. Conformance for an async row must include premature-return,
cancellation, and error-path assertions; TUI-facing rows must also prove that UI
mutation re-enters the main loop. See `docs/extension-authoring.md` for
TypeScript-to-Go examples.

### Subprocess liveness contract

The current subprocess wire carries `ping`, `pong`, and `request_state` without
version negotiation. Each SDK dispatcher answers `ping` directly. A request
reports `started`, then `progress` or `blocked` when applicable, and
`completed` before its response. Host calls carry `parent_request_id`.
Interactive UI calls automatically report `blocked:user`; other awaited host
calls report `blocked:host_call` and then `progress`.
The blocking-reason vocabulary is `user`, `host_call`, and `external_io`.

The host sends heartbeat only while the connection owns outstanding work or
live provider state. A frame the host has queued or is writing is outstanding
work, and every byte the extension reads renews the pong deadline, so a slow
reader stays connected and a peer that stops reading fails visibly. Missing
`pong` closes the logical connection and returns `extension_unresponsive` with
extension, request, and operation context. The host never drops an outbound
frame and sets no socket write deadline. Loading has no deadline either: like
upstream's awaited factory, a load ends when the extension registers, its
process exits, its connection closes, or the caller cancels. A packed runner
reports a member whose factory fails by closing that member's socket.
Tools, commands, events, and shortcuts have no host completion or inactivity
lease. Their caller-owned context controls cancellation. Renderers use a
generation-scoped inactivity lease off the TUI loop and retain the last
completed frame. A failed logical packed member does not quarantine siblings;
shared process death quarantines the packed cell. Parent cancellation cancels
its blocked host/UI calls and removes response correlation before a late
response can arrive.

Ordering and cancellation coverage lives in
`coding/extension/host/subprocess/liveness_test.go`. `TestLivenessConformanceSDKsMatch`
compares normalized Go, Node/TypeScript, Rust, and Python production recordings for
heartbeat while blocked, awaited and interactive calls, no-result calls,
parent cancellation, completed ordering, and late-response disposal. This is the D56
subprocess-host behavior; upstream has no subprocess transport.

Status values:

```text
planned       required implementation or acceptance evidence is incomplete
partial       some host/protocol support exists, parity incomplete
complete      protocol, SDK, and conformance test exist
n/a           intentionally not portable, with divergence or rationale
```

## Loading and resources

| Upstream Pi interface | Required Pig equivalent | Test requirement | Status |
|---|---|---|---|
| Extension discovery from global/project locations | resolve conventional factories and exact standalones from canonical startup inputs | global/project extension load scenario | planned |
| Temporary extension paths (`-e`) | temporary extension install/launch source | temporary extension validation/load scenario | planned |
| Package-installed extensions | `pig install` source resolution and Package settings | install package then reload scenario | planned |
| `resources_discover` | subprocess event returning skill/prompt/theme paths | resource paths appear after reload | planned |
| `project_trust` | typed event/result and first-decisive dispatch; multiple registrations carry `handler_id` identity so errors/undecided results continue and yes/no stops in extension/load order; Go/Rust/Python typed helpers plus Node upstream bridge share cancellation/error semantics in isolated, packed, and D31 fused modes | `TestEmitProjectTrust*`, `TestConformance_TransportsMatch`, `TestNodeProjectTrustMultipleHandlers`, packed Go/Rust/Python assertions, and fused cancellation/dispatch tests; production pre-trust emitter remains P1 | partial: host/protocol/all-SDK conformance complete; production trust resolution pending |
| `session_shutdown` during reload | shutdown event to old cells before teardown | reload lifecycle scenario | planned |
| `session_start { reason: "reload" }` | start event to new cells after reload | reload lifecycle scenario | planned |
| `/reload` | runtime-cell reload in first-seen extension order with fresh factories and per-extension failure isolation | unchanged module state resets; configured order survives startup and 20 reloads; failing extension is reported while others load (`TestHostReloadRestartsUnchangedNodeExtension`, `TestHostLoadAndReloadPreserveConfiguredGuardOrder`, `TestReloadIsolatesExtensionLoadFailures`, `TestPackedCellFailureIsolatesFailingMember`) | ported |

## Session lifecycle events

| Upstream Pi interface | Required Pig equivalent | Test requirement | Status |
|---|---|---|---|
| `session_start` | event with reason and previous session file | startup/new/resume/fork/reload reasons | planned |
| `session_before_switch` | cancellable pre-switch event | extension cancels `/new` or `/resume` | planned |
| `session_before_fork` | cancellable pre-fork event | extension cancels fork/clone | planned |
| `session_before_compact` | cancellable/custom compaction event | `TestCompact_ExtensionOverrideAndLifecycle`, `TestCompact_ExtensionCancellationIsAborted`, cross-SDK conformance, and RPC scenario 24 | wired; cancellation and extension-provided results covered |
| `session_compact` | post-compaction event | `TestCompact_ExtensionOverrideAndLifecycle`, `TestAutoCompaction_ExtensionLifecycleMetadata`, cross-SDK conformance, and RPC scenario 24 | wired; persisted entry and manual/automatic metadata covered |
| `session_compact_failed` | awaited post-failure event after `compaction_end` listeners return, including extension cancellation, ordinary failure, and exhausted overflow recovery; manual failure releases compaction ownership before listener dispatch | `TestCompactionFailureWaitsForEndListeners` proves listener/handler order, early abort, manual replacement ownership, and `willRetry=false`; manual cancellation/nothing-to-compact and overflow regressions; `TestConformance_TransportsMatch`; packed Go/Rust/Python and fused Go event dispatch | complete |
| `session_before_tree` | cancellable/custom tree navigation event | `TestNavigateTreeEmitsSessionBeforeTreeWithPreparation`, `TestNavigateTreeSessionBeforeTreeCancel`, `TestNavigateTreeExtensionSummary`, `TestNavigateTreeSessionBeforeTreeOverrides` | wired in `Session.NavigateTree`: handlers run and are awaited in load order before summarization, `Signal` is the navigation abort context, and cancel, summary, customInstructions, replaceInstructions and label results apply; cross-SDK conformance row pending |
| `session_tree` | post-tree navigation event | `TestNavigateTreeEmitsSessionTree` | wired in `Session.NavigateTree`: emitted and awaited after every completed navigation (not after a no-op or cancel) with the new and old leaf, and with the summary entry and `fromExtension` when a summary was created; cross-SDK conformance row pending |
| `session_shutdown` | shutdown event with reason and target session | quit/reload/new/resume/fork reasons | planned |

## Agent and turn events

| Upstream Pi interface | Required Pig equivalent | Test requirement | Status |
|---|---|---|---|
| `input` | raw input event before skill/template expansion | continue/transform/handled actions chain correctly | planned |
| `before_agent_start` | event can inject message and modify chained system prompt | prompt mutation order scenario (`TestBeforeAgentStartMessagesReachProvider`, `TestForcedSystemPromptIsPerRun`) | wired; injected messages join the prompt after the user message, a forced prompt lasts one run |
| `agent_start` | event once per user prompt | event order scenario | planned |
| `agent_end` | event with messages for prompt | event order scenario | planned |
| `agent_before_settle` | awaited after retry, recovery, compaction, and queued continuations drain; handlers chain durable entry drafts and one valid continuation against a recomputed context preview; draft mutations survive a missing result and handler failure, while an explicit successful result replaces the proposal | runner chaining/repair regressions, canonical Session continuation, settled-action ordering, `TestReviewBoundaryMutationThroughFusedSDK`, isolated Go/Node/Rust/Python and packed Go/Python/Rust mutation/error/result conformance | complete |
| `agent_settled` | event once after the complete run settles; run-starting handler actions wait until every settled handler returns | Session and interactive ordering regressions | complete |
| `turn_start` | event per LLM/tool turn | turn index/timestamp delivered | planned |
| `turn_end` | event with message, tool results, and their persisted entry IDs | production Session regression, isolated Go/Node/Rust/Python conformance, packed Go/Python/Rust, fused Go, and bridge payload regression | complete |
| `context` | conversation-only transforms restore prompt sections and tool declarations; an unchanged list (same message identities, including in-place field edits) retains system boundaries; an in-place reorder or an equal-valued fresh message is a replacement that collapses them | `TestContextHandlerResultShrinksProviderRequest`, `TestContextPhaseInPlaceEditKeepsSystemBoundaries`, `TestContextPhaseInPlaceReorderCollapsesSystemBoundaries`, `TestContextPhaseEqualValueReplacementCollapsesSystemBoundaries`, `TestContextPhaseIdentityThroughSDKTransports` and `TestContextIdentityCrossesSDKTransports` (fused Go, Node), plus packed Go/Python/Rust and fused Go realization tests | wired in the Session for every mode; handlers see the conversation without system messages, as upstream's `context` phase. The Rust SDK reports no list identity, so its returned lists are compared by value (a known limit) |
| `context_with_system` | full-transcript transforms run after context handlers; returned system/tool state reaches the provider | `TestContextWithSystemHandlerSeesAndReplacesTranscript`, packed Go/Python/Rust, and fused Go RunWithConn; head removal reports an error but honors output | covered |
| `message_start` | message lifecycle event | user/assistant/toolResult start events | planned |
| `message_update` | assistant streaming update event | streaming update delivered | planned |
| `message_end` | event can replace finalized message with same role | message replacement scenario (`TestMessageEndReplacementPersisted`) | wired; dispatched on the agent goroutine before persistence, replacement applied to state, events and the session file |
| `tool_execution_start` | preflight event in assistant source order | parallel tool ordering scenario | planned |
| `tool_execution_update` | progress event for tool execution | progress delivery scenario | planned |
| `tool_execution_end` | final tool lifecycle event | completion ordering scenario | planned |

## Provider and model events

| Upstream Pi interface | Required Pig equivalent | Test requirement | Status |
|---|---|---|---|
| `before_provider_request` | event receives provider payload and may replace it | payload replacement scenario (`TestBeforeProviderRequestRewritesPayloadInSDKSession`) | wired in the Session for every mode |
| `after_provider_response` | event receives status and headers before stream consume | headers/status scenario | planned |
| `model_select` | event with current/previous model and source | `/model`, cycle, restore scenario | planned |
| `thinking_level_select` | notification event for thinking level changes | set/cycle/model clamp scenario | planned |

## UI prompt events

Upstream's runner wraps the bound UI surface (interactive and RPC modes, not
print) so every `ctx.ui.select`, `confirm`, `input`, `editor`, and `custom`
call reports the outermost blocking prompt (runner.ts `withUIPrompt`). A
nested or overlapping prompt raises the depth without an event, and the end
event repeats the outermost prompt's kind and title. `custom` carries no
title, and an empty title is omitted from the payload.

Async contract: the prompt does not wait for remote asynchronous handler completion. Upstream queues each event with `queueMicrotask(() => void this.emit(event))`; handler errors go to the extension error listeners. Pig admits queued emissions in FIFO handler-body order. A Go in-process handler returns at its only observable suspension boundary; a handler that starts asynchronous work returns before that work finishes. A subprocess handler acknowledges when the SDK reports `blocked` or `completed`, or returns its response; a socket write or `started` state is not handler admission. Go, Rust, and Python report awaited host calls as suspension boundaries. Node reports the return from invoking the handler, before awaiting its Promise. A suspended start handler therefore does not block the matching end emission. Each runtime retains registration order within one emission. Invalidation (reload, session replacement) drops pending events and cancels active handler contexts.
In-process extensions reach the scope through the runner's wrapped UI surface.
`TestPromptHandlerBodyFIFOAcrossSDKs`, `TestPackedPromptHandlerBodyFIFO`, and `TestReviewPromptHandlerBodyFIFOThroughFusedSDK` record handler entry before any host call and check the unsorted sequence association. `TestPromptEndRunsWhileSDKStartWaitsForHost` and `TestNodePromptEndRunsWhileStartPromisePending` prove that admission does not await suspended completion.
Subprocess, packed, and fused extensions reach the terminal through the
subprocess UI bridge, which opens the scope when the dialog call arrives and
before it waits for terminal focus, because the extension already waits on the
user from that point. Interactive mode rebinds the bridge to the new runner on
reload.

| Upstream Pi interface | Required Pig equivalent | Test requirement | Status |
|---|---|---|---|
| `ui_prompt_start` | unawaited event `{reason: "ui_prompt", kind, title?}` when the outermost blocking prompt opens, from the runner's wrapped UI surface and the subprocess UI bridge | `TestUIPrompt_*` (depth, overlap, not-awaited, cancellation, handler error, no UI, stale runner), `TestUIBridgePromptScope*` (scope before focus wait, cancellation, custom, no UI), `TestReplaceExtensionRunnerRebindsSubprocessPromptScope`, `TestConformance_TransportsMatch` (`ui_prompt_events`: in-process, Go, Node, Rust, Python isolated), `TestPackedSDKFocusedComponentAndSessionActionsMatch` (Go/Python/Rust packed), `TestHost_MixedFusedPackedAndIsolatedExtensions` (fused, packed, isolated Node) | complete |
| `ui_prompt_end` | unawaited event with the outermost prompt's kind and title when that prompt settles, including cancellation and failure | same rows | complete |

## Tool and shell events

| Upstream Pi interface | Required Pig equivalent | Test requirement | Status |
|---|---|---|---|
| `tool_call` | cancellable event before tool execution | block tool scenario (`TestToolCallHandlerErrorBlocksExecution`, `TestToolCallTerminateAndInputMutation`) | wired in the Session for every mode; a handler error blocks the call; blocked-call `terminate` honored |
| `tool_call` mutable input | event can patch arguments before execution | input mutation chains in load order | wired for in-process handlers; subprocess handlers receive a copy |
| `tool_result` | event can patch content/details/isError | result patch chains in load order (`TestEmitToolResult_ContentOnlyKeepsIsError`) | wired; an omitted `isError` keeps the flag |
| Typed tool event variants (`PowerShellToolCallEvent`, `PowerShellToolResultEvent`, and the bash, read, edit, write, grep, find and ls variants) | each built-in tool's `tool_call` and `tool_result` carry the upstream `toolName`, input and details; `powershell` uses the bash shapes (`PowerShellToolInput`, `PowerShellToolDetails`) | cross-SDK row `TestToolEventVariantsMatchAcrossSDKs`: in-process Go, subprocess Go, Node, Rust, Python and fused Go record the input and details, block a call, and replace a result | wired for every SDK over the wire; Go has the typed variants with marshalling and in-process result chaining, but the Session emits every tool event as the generic `CustomToolCallEvent`/`CustomToolResultEvent`, so in-process Go handlers see the generic variant |
| `user_bash` | user shell command interception/replacement; a handler returns a `{ result }` (shown and recorded without running) or `{ operations }` (an `extension.BashOperations` the command runs through) | operations/result replacement scenario | partial: a Go handler can return either form; subprocess SDK handlers can return a result only, because operations need an exec callback the wire does not carry; interactive mode does not yet apply returned results or operations |
| Parallel tool ordering | event ordering matches upstream preflight/completion rules | parallel tool scenario | planned |

## Extension API methods

| Upstream Pi interface | Required Pig equivalent | Test requirement | Status |
|---|---|---|---|
| `registerTool` | registration contribution in protocol | tool visible and invokable | partial |
| `registerCommand` | registration contribution in protocol | slash command invokes handler | partial |
| `registerShortcut` | registration contribution in protocol | shortcut dispatch scenario | planned |
| `registerFlag` | early runtime registration inspection before CLI parse | flag visible before session start | planned |
| `registerMessageRenderer` | cached line-renderer bridge with default component fallback | production custom message, width/expanded generations, stale response, disconnect | complete; production + Node parity + Go/Rust/Python conformance |
| `sendMessage` | host request for custom session message | steer/followUp/nextTurn scenario | wired; conformance covered |
| `sendUserMessage` | string or text/image blocks; extension-source input interception runs before idle prompt or active steer/follow-up delivery and can handle or transform text/images | `TestUserMessageContentAcrossSDKs`, `TestPackedUserMessageContentAcrossSDKs`, structured interactive handled/source/transform regressions, and transport bridge payload test | covered in isolated Go/Node/Rust/Python, packed Go/Rust/Python, and fused Go |
| `appendEntry` | host request for custom persistent entry | entry persists through reload | wired; conformance covered |
| `setSessionName` / `getSessionName` | host request/state query | session selector name scenario | wired; conformance covered |
| `setLabel` | host request for entry labels | tree label scenario | planned |
| `getCommands` | host query; the Node runtime answers from replicated state | `SlashCommandInfo` list (extension commands, the inline llama.cpp command, prompt templates, skills) with source and sourceInfo: `extensions-runtime/21-extension-tool-and-command-info`, `TestSubprocessGetCommands_ListsExtensionCommandsTemplatesAndSkills` | covered in print, JSON, RPC and interactive modes |
| `exec` | host-mediated process execution | cancellation/timeout scenario | planned |
| `getActiveTools` / `getAllTools` | host query; the Node runtime answers from replicated state | `getAllTools` returns upstream `ToolInfo` for the whole tool registry, inactive built-ins included: `extensions-runtime/21-extension-tool-and-command-info`, `TestExtensionToolInfosAppliesRegistryAllowlistAndDenylist`, `TestHost_Integration_TSFileShim`; `getActiveTools` planned | `getAllTools` covered; `getActiveTools` planned |
| `setActiveTools` | host request | active tool set changes prompt/tools | planned |
| `setModel` | host request | model changes or reports missing key | planned |
| `getThinkingLevel` / `setThinkingLevel` | host query/request | thinking level event emitted | planned |
| `registerProvider` | provider registration protocol | provider appears in model registry | planned |
| `unregisterProvider` | provider removal protocol | built-ins restored when overridden | planned |
| `config.oauth` (provider OAuth) | OAuth extension bridge: `oauth_*` requests (host→ext) + `oauth.cb.*` login callbacks (ext→host) over the current subprocess wire; host builds an `ai.OAuthProviderInterface` proxy on the provider register/unregister lifecycle. Registration-only inspection exposes the provider to pre-session `pig login --list|<id>`; login starts only the owning installed or embedded extension. The node runtime bridges upstream `config.oauth` (compat/extension-oauth-types.ts) so one `.ts`/`.mjs` extension runs on pi (in-process) and pig (bridged). | `TestConformance_OAuthTransportsMatch` (Go/Rust/Python subprocess recordings identical) + `TestNodeOAuthBridge` (node `.mjs` config.oauth over the real host) + host-proxy relay/cache/store unit tests + registration-inspection installed/embedded/fused tests. Interactive `/login` pi-vs-pig scenario pending a pi binary. | wired; conformance + node + runtime registration inspection covered (subprocess; the bridge has no in-process path) |
| `config.oauth.isSubscription` | `ProviderOAuth.IsSubscription`, registration metadata `isSubscription`, Go `OAuthProvider.IsSubscription`, Rust/Python `is_subscription`, and Node's upstream field; false/omitted/null do not mark a subscription. The proxy answers synchronously from registration metadata with no IPC, Promise, new task, or change to login/refresh ordering. Replacement and shutdown discard the old metadata. The stock footer also requires an OAuth credential, except for Pi's Kimi Coding rule. | `TestOAuthSubscriptionRegistration`, `TestOAuthSubscriptionSDKPlacements` (isolated/packed Go/Rust/Python), `TestConformance_OAuthTransportsMatch` (including fused Go), `TestNodeOAuthBridge`, `TestExtensionOAuthSubscriptionFooter`, and `07-footer-extension-subscription` | complete |
| `pi.events` | inter-extension event bus over host | event emitted between extensions/cells | planned |

## Tool definition features

| Upstream Pi feature | Required Pig equivalent | Test requirement | Status |
|---|---|---|---|
| `label` | tool metadata | prompt/UI metadata scenario | partial |
| `description` | tool metadata | prompt/UI metadata scenario | partial |
| `parameters` schema | JSON schema registration | schema validation scenario | partial |
| `promptSnippet` | prompt contribution metadata | available tools prompt snippet scenario | planned |
| `promptGuidelines` | prompt guideline contribution | guideline activation scenario | planned |
| `constrainedSampling` | provider-side constrained sampling request on a tool | producer→consumer conformance: SDK field (Go/Rust/Python) → wire → host bridge → `ai.ToolSchema.ConstrainedSampling`; openai-completions/responses grammar+strict emission, replay, streaming | complete; Go/Rust/Python conformance + provider-wire parity |
| `prepareArguments` | local pre-validation compatibility transform in every SDK runtime | cross-language `prepared_tool` recording through the production dispatch path | complete |
| `execute` | tool invocation | basic invocation scenario | partial |
| `onUpdate` streaming | `tool_update` notify delivered to the running tool in frame order, before its result; `signal` is the request's cancellation (Node `AbortSignal`, Go `ctx.Done()`, Rust/Python `is_cancelled`) | `TestToolSignalAndUpdatesSDKsMatch` across in-process Go and Go/Node/Rust/Python subprocesses | complete |
| `terminate` | `terminate` field on the wire tool result | `rich_tool` row of `TestConformance_TransportsMatch`; batch semantics owned by the agent loop | partial |
| throw-to-error behavior | thrown errors mark result `isError` | tool error scenario | planned |
| `renderCall` | declarative renderer protocol or accepted divergence | custom renderer scenario | planned |
| `renderResult` | declarative renderer protocol or accepted divergence | custom renderer scenario | planned |
| `renderShell` | renderer shell selection | self-shell/fallback scenario | planned |
| Built-in tool override | same-name registration override semantics | override `read` scenario | planned |
| Active tool management | active set host API | dynamic tools scenario | planned |
| File mutation queue | host API equivalent | concurrent edit/write scenario | planned |
| Output truncation helpers | SDK utilities in each language | truncation scenario | planned |

## Context APIs

| Upstream Pi context field/method | Required Pig equivalent | Test requirement | Status |
|---|---|---|---|
| `ctx.cwd` | context field | current cwd scenario | partial |
| `messageRole` / `messageText` event helpers | pig-only reading aid over the upstream message payload; identical in all four SDKs | `TestMessageTextReadsTheWireShape` | ported |
| `ctx.isProjectTrusted()` | available in every SDK (Go, Rust, Python, Node) over one `isProjectTrusted` host call; unbound defaults to trusted per upstream runner.ts:280 | `TestConformance_TransportsMatch` context-probe, bound false so an SDK that never calls cannot pass by defaulting | ported |
| `tui.terminal` (ui.custom) | overlay components size from `terminal.columns`/`terminal.rows`, mirroring upstream `TUI.terminal` (tui.ts:333) and `Terminal` (terminal.ts:79-80) | `TestCustomOverlayExposesTerminalGeometry`; verified live by pi-atelier's sidebar and menu | ported |
| `footerData` provider | ui.setFooter's third argument is upstream's `ReadonlyFooterDataProvider` (getGitBranch, getExtensionStatuses, getAvailableProviderCount, onBranchChange - footer-data-provider.ts:387) | `TestFooterFactoryReceivesProvider` | ported |
| footer/header `tui` argument | factories receive a TUI with `requestRender` and `terminal.columns`/`rows`, and the built component is cached so factory side effects run once | `TestFooterFactoryReceivesProvider`; pi-atelier's footer renders live | ported |
| `ctx.mode` | run mode `tui`/`rpc`/`json`/`print`; sent in the ready payload and applied on every ready update | `TestNodeContextExposesRunMode`; verified live by pi-atelier, which gates `session_start` on `"tui"` | ported |
| `ctx.hasUI` | context field | print vs interactive scenario | planned |
| `ctx.sessionManager` | lazy local mirror; persisted JSONL supplies the first snapshot and bounded 4 MB host pages provide fallback and incremental updates | one-entry-page cross-SDK conformance plus opt-in recorded-session benchmark | complete |
| `ctx.modelRegistry` / `ctx.model` | model query API | model query scenario | planned |
| Model `inputLimits` metadata | preserve configured limits in model queries and active-model helpers in every SDK; clone nested model metadata | `TestModelInfoInputLimitsProjection`, `TestModelStreamingSDKsMatch`, packed Go/Rust/Python active-model checks, and fused Go checks | complete for metadata projection; image preprocessing is tracked separately |
| `ctx.signal` | cancellation token propagated to handler | cancellation scenario | Go/Rust SDK unit-covered; scenario pending |
| `ctx.isIdle()` | host query | idle state scenario | planned |
| `ctx.abort()` | host request | abort active turn scenario | planned |
| `ctx.hasPendingMessages()` | host query | queued message scenario | planned |
| `ctx.shutdown()` | host request | graceful shutdown scenario | planned |
| `ctx.getContextUsage()` | host query | usage estimate/current model scenario | planned |
| `ctx.compact()` | host request | manual compaction scenario | planned |
| `ctx.getSystemPrompt()` | host query | before_agent_start chained prompt scenario | planned |
| `ctx.mode` | context field (`ExtensionMode`: tui/rpc/json/print) | mode reported per run mode; unset defaults to print | conformance-covered (context-probe) |
| (no upstream equivalent) | `ctx.width` / `ctx.height` terminal geometry, refreshed by `width_change` / `height_change` notifications | `TestConformance_Geometry` asserts all four SDKs report the height delivered by the notification | complete (pig-additive) |

## Command-only context APIs

| Upstream Pi command context API | Required Pig equivalent | Test requirement | Status |
|---|---|---|---|
| `waitForIdle()` | host request | command waits for stream end | planned |
| `newSession()` | host request with replacement context | new session lifecycle scenario | planned |
| `fork()` | host request with replacement context | fork lifecycle scenario | planned |
| `navigateTree()` | host request | tree navigation scenario | planned |
| `switchSession()` | host request with replacement context | switch lifecycle scenario | planned |
| `reload()` | host request | command-triggered reload scenario | planned |
| `getSystemPromptOptions()` | host query for base system-prompt inputs | options delivered to command handler | conformance-covered (context-probe) |

## UI APIs

Focused subprocess UI classifies wire traffic by semantics: input is an
ordered non-idempotent event, a rendered line set is a replaceable snapshot,
and open/result/cancel/error/close is a lifecycle barrier. Input and barriers
are never coalesced. Timer-driven render requests are coalesced to one pending
request and limited to the upstream TUI's 16 ms render interval. Input-driven
frames remain immediate. A snapshot is suppressed only when both its text and terminal width match the previous snapshot. Each published snapshot carries its terminal width (not the resolved overlay render width) and monotone sequence. The bridge rejects stale-width or out-of-order arrivals, and the UI cache checks terminal width again at paint time. The overlay key is the realization-local
generation and reload ownership includes the connection identity, preventing an
old runner from updating or closing its replacement.

Interactive dialog calls are awaited by the extension handler. The host installs
and mutates dialog components on the owning TUI loop while the subprocess call
waits off-loop. Selection/submission returns `ok=true`, Escape returns
`ok=false`, and host/transport failures use a protocol error rather than an
ambiguous empty success. Headless contexts return `ok=false` without opening a
surface. Go and Rust SDK methods return host errors explicitly; Python and Node
raise/reject them.

| Upstream Pi UI API | Required Pig equivalent | Test requirement | Status |
|---|---|---|---|
| `select` | host UI request | selection response/cancel/timeout scenario | partial: real subprocess tool selection + Escape cancellation + headless fallback covered; timeout pending |
| `confirm` | host UI request | confirm response/timeout scenario | planned |
| `input` | host UI request | text input response/cancel/timeout scenario | partial: real subprocess tool submission + shared explicit cancellation/error bridge covered; timeout pending |
| `editor` | host UI request | multiline editor/cancel scenario | partial: real subprocess tool submission + shared explicit cancellation/error bridge covered |
| `notify` | host UI notification | notification scenario | partial |
| `setStatus` | declarative status update | footer status scenario | partial |
| `setLogin` (no upstream equivalent) | typed `LoginDefinition` through direct `ui.setLogin`; shares the header slot with `setHeader`, validates before replacement, and renders from a local cache without per-render IPC | host validation and shared-slot semantics; Go/Rust/Python conformance; Node bridge; preview command | complete (pig-additive D60) |
| `setWidget` | cached line-widget bridge; Node runs the upstream factory locally with width/invalidation and pushes deduplicated frames, while Go/Rust/Python expose line snapshots | production widget install/update/resize/clear/reload scenario | partial: Node factory and all-SDK line pushes wired; placement/options/theme parity pending |
| `setFooter` | declarative footer update | footer replacement scenario | planned |
| `setTitle` | host title update | terminal title scenario | planned |
| `setEditorText` / `getEditorText` | editor host request/query; Go/Rust/Python query the host, Node reads the replicated snapshot because the getter is synchronous | `TestNodeRuntimeReportsRealUIStateNotStubs` | partial: all SDKs return host editor text; scenario pending |
| `pasteToEditor` | host paste request | paste/collapse behavior scenario | planned |
| `onTerminalInput` | one host listener per subscribed extension, placed at the extension's first subscription; each SDK chains its handlers' `consume`/`data` into one verdict. Upstream's listener is synchronous; the interactive host awaits a subprocess verdict on an owned background task, resumes the pass on the TUI loop, and holds input typed after the chunk, so verdicts, rewrites, and handling keep upstream order while rendering continues. A failed verdict leaves the chunk unchanged; mode shutdown cancels the request. Ctrl+C waits like every other key and may be consumed or rewritten by a listener. The pump applies read-side backpressure while a verdict is pending. | `TestConformance_TerminalInput` (Go, Node, Rust, Python, fused Go); `TestSubprocessTerminalInputVerdictLeavesOwnerLoopLive`, `TestCtrlCWaitsForTerminalInputVerdictsInOrder`, `TestTerminalInputBurstAllocationsGrowLinearly`, `TestPendingTerminalInputBackpressuresReader`, `TestInputLoopShutdownCancelsPendingTerminalInputVerdict`, `TestNodeTerminalInputListenerRewritesKeysInOrder`, `TestTerminalInputRequestEndsWithItsContext` | partial: ordered verdicts, Ctrl+C, cancellation, and bounded read-ahead covered; per-handler cross-extension ordering, shortcut precedence, and modal delivery remain gaps |
| `addAutocompleteProvider` | declarative/provider protocol | autocomplete layering scenario | planned |
| `getToolsExpanded` / `setToolsExpanded` | host query/request; Node reads the replicated snapshot | `TestNodeRuntimeReportsRealUIStateNotStubs` | partial: all SDKs return host state; scenario pending |
| `setEditorComponent` / `getEditorComponent` | declarative custom editor protocol or divergence | modal editor scenario | planned |
| `getAllThemes` / `getTheme` / `setTheme` | theme query/request; themes are structured objects on the wire, and Node resolves `getTheme` against the replicated list | `TestThemeQueriesReturnStructuredThemeNotDebugString`, `TestNodeRuntimeReportsRealUIStateNotStubs` | partial: all SDKs return host themes; `setTheme` scenario pending |
| `ctx.ui.custom` | focused subprocess remote component: ordered input, replaceable cached line snapshots, awaited result/error, resize, timer invalidation, and disposal | Go/Rust/Python/Node isolated + packed focus/scroll/cancel/reload scenarios; timer redraw, burst coalescing, callback detach, and D31 fused lifecycle | complete: all SDKs, isolated and packed cells, D31 fused components, focus serialization, timer throttling, cancellation, and cleanup are covered |
| Overlay custom UI | host-native focus shell around the remote component, with pre-bind snapshot/close buffering and input-delivery failure teardown; Node serializes layout options after the awaited factory and renders at the resolved overlay width | `TestNodeCustomOverlayResolutionAndResize`, `TestNodeCustomOverlaySizingDefaults`, `TestOverlayProxyPreservesTerminalWidth`, `TestRemoteOverlayProductionDoomGeometry`, `TestRemoteOverlayProductionRejectsCachedTerminalWidth`, and geometry goldens | partial: integer/percentage Node geometry and stale-width rejection covered; visibility callbacks, non-capturing input routing, fractional numeric options, and native SDK layout/render-width parity remain open |

## Rendering APIs

| Upstream Pi rendering API | Required Pig equivalent | Test requirement | Status |
|---|---|---|---|
| custom message renderers | cached subprocess line-renderer proxy; in-process component renderer | production message + width/expanded/stale/error scenario | complete; render loop is IPC-free |
| `MessageRenderOptions.outputPad` | required numeric `outputPad` on the message-render request; Go `OutputPad`, Rust `output_pad`, Python `options["outputPad"]`, Node `options.outputPad`; live changes replace the cached generation without changing `EntryRenderOptions` or the default custom-message box inset | `TestMessageRenderOptionsOutputPadWire`, `TestCustomMessageOutputPadProduction`, `TestRenderProxyOutputPadRefreshRejectsStaleGeneration`, `TestMessageOutputPadAcrossSDKs` (isolated and fused), `TestPackedMessageOutputPadAcrossSDKs`, and `20-message-output-padding` | complete |
| custom tool call renderers | declarative renderer protocol or accepted divergence | tool call renderer scenario | planned |
| custom tool result renderers | declarative renderer protocol or accepted divergence | tool result renderer scenario | planned |
| fallback renderer behavior | host fallback renderer | renderer error/fallback scenario | planned |
| keybinding hints | SDK utility or host formatting API | key hint scenario | planned |
| theme color helpers | declarative theme tokens | theme rendering scenario | planned |
| syntax highlight helpers | host utility or SDK utility | highlight scenario | planned |

## Performance and resource contract

Extension parity includes latency/resource behavior at the TUI and lifecycle
boundaries; green payload conformance alone is insufficient. The foundation P6
matrix covers the concrete in-process reference, D31 fused path,
packed subprocess cells, and isolated subprocesses.

- `Render` and input-loop callbacks must not perform blocking extension IPC,
  filesystem/network work, or unbounded transcript traversal.
- Session history stays unloaded until first use. A persisted session is read
  locally by the isolated runtime, then reconciled with the host cursor. Missing
  history and later appends cross the socket in ordered pages no larger than 4 MB.
- Renderer/widget/overlay caching must preserve width, expansion, invalidation,
  ordering, disconnect, timeout, fallback/error, and exact terminal bytes.
- Registration, host-call, event, tool/update, cancellation, reload, quarantine,
  fission, and shutdown paths require representative latency/allocation and
  goroutine/fd/process/memory-lifetime evidence.
- Packed/fused optimization must remain observationally identical to isolated
  the current subprocess wire. One host terminal serializes extension dialogs and
focused overlays without blocking the TUI loop. Waiting calls are
cancellation-aware. A faster fused
realization cannot create a second SDK or skip
  protocol/conformance semantics.
- The broad `coding/extension.API` interface is not accepted merely because it
  mirrors this document: it needs a production reference implementation/caller
  or deletion under P0/P2. The concrete `*sdk.Extension` author surface remains
  the current production Go factory API.

Current focused-component rendering is cache-backed on the host. The TUI render
loop performs no extension IPC or JSON work. Go/Rust/Python component workers
serialize input and render callbacks away from each SDK dispatcher. Node uses a
bounded 16 ms scheduler and socket backpressure. Isolated, packed, and fused
lifecycle tests cover the current contract.

## High-risk parity areas

These areas need design before they can be marked complete.

### Custom TUI components

Upstream Pi passes in-process TUI components. Pig subprocesses cannot pass Go component objects across process boundaries. Pig needs a declarative component protocol, a restricted built-in component registry, or a numbered divergence.

### Tool and message renderers

Message renderers are production-bound. In-process Go renderers return TUI
components. Subprocess Go/Rust/Python/Node renderers return lines through an
owned background request; the TUI component reads only a generation-checked
cache and retains a visible default fallback on startup, timeout, disconnect,
or invalid output. Width, expanded-state, and output-padding changes replace stale generations. Upstream message renderer callbacks are synchronous. The subprocess bridge awaits their result on the owned request worker; a padding change uses the existing single-flight generation loop and never waits on the TUI loop. It retains the last completed frame until a current result arrives, rejects stale results, and preserves the existing cancellation/error/shutdown ownership.
`BenchmarkRenderProxyCached` measures about 17 ns/op and one 16-byte defensive
slice-copy allocation on the recorded M4 Pro; no socket or JSON work runs in
`Render`. Tool-call renderers remain a separate surface.

### Provider registration and custom streaming

Upstream Pi allows dynamic providers and custom streaming. Pig needs provider config registration plus a bidirectional streaming protocol for custom streams.

### Extension CLI flags

Upstream Pi extensions register flags during load. Pig subprocess extensions require registration inspection before CLI parsing to expose flags in time.

## Completion rule

A row moves to `complete` only when:

```text
1. upstream behavior has been read and summarized in a test comment or doc row;
2. Pig protocol and SDK expose an equivalent;
3. at least one conformance test exercises the production path;
4. packed and isolated subprocess modes preserve the same semantics when relevant;
5. divergences are numbered in DIVERGENCES.md if parity is not exact.
```

## SDK coverage matrix

Each extension API surface must be reachable from each shipped SDK or marked
as a documented gap. Gaps are tracked here, not in DIVERGENCES.md, because
the SDK bridges themselves are covered by D19; this table only tracks
feature-by-feature parity across the bridges.

| API surface | Go SDK (`sdk`) | Node/TypeScript runtime | Rust SDK (`sdk-rs`) | Python SDK (`sdk-py`) |
|---|---|---|---|---|
| register + ready | complete | complete | complete | complete |
| tools | complete | complete | complete | complete |
| commands | complete | complete | complete | complete |
| event handlers | complete | complete | complete | complete |
| `prepareArguments` | complete | complete | complete | complete |
| `ui.notify` / `ui.setStatus` | complete | complete | complete | complete |
| `sendMessage` / `sendUserMessage` | complete | complete | complete | complete |
| `appendEntry` / `setSessionName` | complete | complete | complete | complete |
| request cancellation (cooperative) | complete | complete | complete | complete |
| heartbeat + request liveness | complete | complete | complete | complete |
| widgets (`widget_push`) | complete | complete | complete | complete |
| shortcuts | complete | complete | complete | complete |
| flags | complete | complete | complete | complete |
| providers | complete | complete | complete | complete |
| OAuth provider (`config.oauth`) | complete | complete | complete | complete |
| OAuth subscription metadata (`config.oauth.isSubscription`) | complete | complete | complete | complete |
| message renderers | complete | complete | complete | complete |
| focused custom components | complete | complete | complete | complete |
| typed native login (`setLogin`) | complete | complete | complete | complete |
| `ui_prompt_start` / `ui_prompt_end` events | complete | complete | complete | complete |
| `agent_before_settle` boundary event/result | complete | complete | complete | complete |

The wire protocol carries every surface above for every SDK. The conformance
suite compares the in-process Go reference with the Go, Node/TypeScript, Rust,
and Python subprocess runtimes. Future additions must land in all applicable
SDKs or this matrix must identify the gap in the same change.
