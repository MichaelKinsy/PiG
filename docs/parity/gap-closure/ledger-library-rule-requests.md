# Library packages: open ledger rows that need engine rules, not Go changes

Detector run on the p1-impl engine with this branch's tests (packages durable, chord, mcp, telemetry, env, client, server, protocol). Every row below already has a Go form; the engine lacks a rule that maps it. Each entry names the family, the Go form and the rule the engine needs. No row is closed by a test or a Go change.

## R1. Context as Go's `context.Context` (chord, 11 no-go rows)

| Pi (packages/chord/src) | Go form |
|---|---|
| `Context` (types.ts) | `context.Context` |
| `ContextKey<T>` (types.ts:8) | `chordctx.Key[T]` (internal/chord/chordctx) |
| `createContextKey` (context/index.ts:58) | `chordctx.NewKey` |
| `withContextValue` (context/index.ts:63) | `chordctx.WithValue` (and `Value` reads it) |
| `awaitWithContext` (context/index.ts:98) | `chordctx.Await` |
| `BACKGROUND_CONTEXT`, `TODO_CONTEXT` (context/index.ts:55-56) | `context.Background()`, `context.TODO()` |

Rule: a Pi context value or helper maps to the Go standard-library context or to the named chordctx function. The Go context is the first parameter (autobind S12).

## R2. JSON-RPC shapes (mcp, 4 no-go and 3 type rows)

| Pi (packages/mcp/src/protocol/jsonrpc.ts) | Go form (mcp/jsonrpc.go) |
|---|---|
| `JSON_RPC_ERROR_CODES` (37) | the constants `JSONRPCParseError`, `JSONRPCInvalidRequest`, `JSONRPCMethodNotFound`, `JSONRPCInvalidParams`, `JSONRPCInternalError` (253-257) |
| `JsonRpcResponse` (34), `JsonRpcMessage` | `JSONRPCMessage`, one struct whose set members select request, notification or response (A8: a union of named types is one struct with optional members) |
| `isJsonRpcRequest` (93), `isJsonRpcResponse` (103) | `JSONRPCMessage.IsRequest`, `IsResponse` (138, 148) |
| `JsonRpcId` | `JSONRPCID` with `StringID`, `NumberID` |
| `OAuthClientInformationMixed` | `oauth.OAuthClientInformation` |

Rule: an `as const` record of numbers maps to Go constants named by the record's keys; a type-guard function maps to the Go method of the same meaning.

## R3. Type-level TypeScript helpers with no runtime form (telemetry 13 no-go rows; durable HooksOf, HookResult; chord Draft, JsonRepresentation)

`ExactTelemetryAttributes`, `Infer*Attributes`, `SchemaTelemetrySpan`, `TelemetrySchemaSpan*`, `HooksOf`, `HookResult`, `Draft`, `JsonRepresentation` exist only to compute TypeScript types. Go checks the same contracts through generics (`durable/harness/define.go`) or a runtime value. Rule: a conditional or mapped helper type with no value is designed out; record it with its Go mechanism.

## R4. Test-runner adapters (durable 3 no-go rows)

`StorageConformanceRunner` (testing/runner.ts:6), `ExpectLike` and `createExpectAssertions` (testing/assertions.ts) adapt a Vitest/Jest runner. Go's runner is `*testing.T`; `durablecontract` and `durabletest` call it directly. Rule: designed out, naming `testing.T`.

## R5. Narrowed union members (durable `LatestConversationSemantics`, `RewindableConversationSemantics`)

Pi narrows `DocumentSemantics` into three object types (types.ts:42-60). Go has one struct `DocumentSemantics{Scope, History, Fork}` (durable/types.go:90) with typed constants. Rule: members of a discriminated union that differ only in literal field values map to the one struct; the engine accepts it when each literal has a Go constant.

## R6. Value rows that stay open without a rule

- `RemoteError.Message` (env): duplicates `durable/core/driver` `RemoteError.Message` (L3).
- `ClientDisposedError.Cause`, `McpAbortError.Cause`, `McpConnectionClosedError.Cause`, `McpError.Cause`, `McpTimeoutError.Cause`, `OAuthRegistrationError.Cause`, `JsonlStoragePoisonedError.Error` return constants (L4); Pi's `cause` is the constructor argument, absent for these errors.
- `ContentAnnotations.*`, `ListToolsResult.*`, `ToolExecution.TaskSupport` (mcp) and the telemetry schema definition fields are wire data that no production code reads (L5). Pi's own consumers are outside this repository. Rule: a member of a Pi library type that the package marshals as JSON counts as read by the marshaller.
- `EventStream.Events` (ai): the row's needed name `[Symbol.asyncIterator` carries a bracket, so the citation at event-stream.ts:72 never matches.

## R7. Optional parameters and overloads spelled as a second Go function (durable signature rows)

The 55 durable signature rows are Pi overloads and optional trailing parameters. Go spells each with a second function or a variadic tail: `MemoryStorage.PrepareCommit` plus `PrepareCommitAt` (Pi `prepareCommit(writes, seq?)`), `memo(name, context)` and `memo(name, candidate, context)`, `snapshot*`, `entry(id, context)`, `output(...)`. Rule: an optional parameter or an overload pair maps to a Go function pair or a variadic tail when every Pi call shape has one Go call; the engine reports S3/S4 only when a required Pi parameter is missing or a call shape has no Go form. The two lists of Pi call shapes versus Go forms are in `ledger-durable-families.md`.

## R8. `CopyJsonOptions.omitUndefinedProperties` (chord, 1 no-go row)

Pi's `copyJson(value, { omitUndefinedProperties })` drops object properties whose value is `undefined` (json.ts:10-16; used by durable harness and transaction code, chord/test/json.test.ts:31-32). Go has no `undefined`: an absent map key is absent and `nil` is JSON `null`, which `chordjson.Copy` keeps as null as `copyJson` does. The Go path that drops "undefined" fields is `chordjson.Stored` (omitempty and nil pointers vanish). Rule: designed out; Go has no undefined value, and `Stored` is the omitting form.

## R9. Pi's `TUI` class is mapped to the Go `TUI` interface, which is the driver-facing subset (tui, 14 member rows)

The engine pairs Pi `TUI` (packages/tui/src/tui.ts) with `tui/renderer.go#TUI`, an interface of the render, overlay and focus calls the interactive driver makes. It reports no Go member on that interface for `addChild`, `removeChild`, `clear`, `children`, `handleMouse`, `hideOverlay`, `onDebug`, `getClearOnShrink` and `getShowHardwareCursor`. Every one of them exists in Go on the types that carry Pi's `TUI` members: `Container` (`AddChild`, `RemoveChild`, `Clear`, `Children`, tui/tui.go:116-205), the unexported `tuiBase` that embeds `Container` (`HideOverlay`, `OnDebug`, `GetClearOnShrink`, `GetShowHardwareCursor`, `RenderNow`, tui/tui.go:414-1127), `HandleMouse` (tui/mouse.go) and `Mode`/`FullRedraws` (tui/tui_alt_screen.go). `TuiMainScreen` and `TuiAltScreen` both embed `tuiBase`, so each has them. Rule: map Pi's `TUI` class to `tuiBase` (the promoted members of `TuiMainScreen` and `TuiAltScreen`) and judge the class's members there; keep the `TUI` interface as the driver contract. No Go change is needed.

## R10. Node `EventEmitter` members of `StdinBuffer` (tui, 18 member rows)

`StdinBuffer extends EventEmitter` (stdin-buffer.ts) reports `on`, `off`, `once`, `emit`, `addListener`, `removeListener`, `listenerCount`, `listeners`, `eventNames`, `setMaxListeners` and the rest. Go's `StdinBuffer` delivers through callbacks (tui/stdin_buffer.go). Rule: members inherited from Node's `EventEmitter` are designed out; the Go callbacks are the observable events.

## R11. Pi's `Agent` object properties and Go's `Agent` methods (agent, about 25 member rows)

Pi's `Agent` (packages/agent/src/agent.ts) exposes mutable properties and a `state` object. Go's `Agent` (agent/agent.go) spells the same members as getter and setter methods and as `Send*` calls. Rule: a Pi property `x` maps to the Go method pair `X()` / `SetX()` when both exist; `prompt(...)` maps to the `Send`, `SendContent` and `SendMessages` family (and `BeginSend*` for the non-blocking form); the members of `state` map to the getters `Messages`, `SystemPrompt`, `Tools`, `Model`, `ThinkingLevel`, `IsStreaming`.

| Pi member | Go form |
|---|---|
| `prompt` (2 overloads) | `Send`, `SendContent`, `SendMessages` |
| `continue`, `clearSteeringQueue`, `clearFollowUpQueue`, `clearAllQueues` | same names; Go returns the drained or produced messages where Pi returns nothing (S5 is a superset, not a gap) |
| `state` | `Messages`, `SystemPrompt`, `Tools`, `Model`, `ThinkingLevel`, `IsStreaming` |
| `getApiKey` | `GetAPIKeyFunc` / `SetGetAPIKey` |
| `onPayload` | `SetBeforeProviderHook` |
| `onResponse`, `onProviderStreamEvent`, `streamFunction`, `convertToLlm`, `toolExecution` | `OnResponse`/`SetOnResponse`, `OnProviderStreamEvent`/`SetOnProviderStreamEvent`, `StreamFunction`/`SetStreamFunction`, `ConvertToLlm`/`SetConvertToLlm`, `SetToolExecution` (S4 rows: Pi's parameters are the callback's own) |
| `beforeToolCall`, `afterToolCall` (M3) | `[]BeforeToolCallHook`, `[]AfterToolCallHook` on `AgentOptions`; Go runs a hook list in order |

`Agent.finishTurn`, `prepareRequest`, `prepareNextTurn` and `prepareNextTurnWithContext` (agent.ts:125-130, 209-210) have Go forms too: `SetFinishTurn`/`FinishTurnHook`, `SetPrepareRequest`/`PrepareRequestHook`, `SetPrepareNextTurn`/`PrepareNextTurnHook` and `SetPrepareNextTurnWithContext`/`PrepareNextTurnWithContextHook` (agent/agent.go:1605-1640); the next run reads them (agent/agent_loop.go:51-53). They fall under the same property-to-method-pair rule. `AgentTool.constrainedSampling`, `outputSchema`, `prepareArguments` and `replay` are `AgentTool` fields Pi declares and Go reaches through its tool registration; the agent owner confirms the mapping.

## R12. The `pi` package's own index exports (coding-agent, 218 not-exercised rows) fail P1(b) L1 by design

`autobind/library.go` L1 excludes the upstream package that ships the `pi` command, so every coding-agent member that no path from `cmd/pig` reaches stays `not-exercised`: 188 rows read "no production code reads, sets or calls X (a test alone does not close a member)" and 30 read "production code uses X only in Y, which no path from cmd/pig reaches". By file: `coding/rpcclient/commands.go` 72, `coding/extension/api.go` 60, `coding/extension/events.go` 19, `coding/rpcclient/types.go` 10, `coding/extension/eventbus.go` 10, `coding/rpcclient/rpc_client.go` 8, `internal/packagemanager/package_manager.go` 4, `coding/resource_loader.go` 4, `coding/extension/host_actions.go` 4, then a handful.

Pi publishes these members through `packages/coding-agent/src/index.ts` (`RpcClient`, `RpcClientOptions` at lines 421-422; the extension API and event types) with `main`, `types` and `exports` in its package.json, for embedders and extension authors. Pig has no embedder: `cmd/pig/rpc_mode.go` serves RPC and never drives it with `RpcClient`, and extensions reach the API over the subprocess wire. So the rows are caller-free in Pig, but not duplicates or stand-ins: each is a Pi export with a Go counterpart that the existing tests drive (for example `TestRpcClient*` against a spawned `pig --mode rpc`).

Decision needed from the lead, one of:
1. Extend L1 so a member counts as library API when the upstream file that declares it is reached by the `export ... from` closure of the package index, even in the package that ships the `pi` command. The 218 rows then close the way the other library packages do: a Pi-cited, mutation-checked asserting test per member, which this lane would add file by file starting with `coding/rpcclient/commands.go`.
2. Keep L1 as is. Then each row needs a production caller (a Pig command that embeds `RpcClient`, which nobody has asked for) or an approved removal of the caller-free member from the Go port, recorded as a numbered divergence.

This lane did not change coding-agent production code.

## R13. `extension.API` (coding/extension/api.go) has no production implementer; ExtensionActions.GetCommands has no reader

After the L1 change, the coding-agent rows that remain `not-exercised` are one family. `extension.API` declares Pi's `ExtensionAPI` for Go extension authors. The only Go type that implements it is the test double `extensiontest.Fake`; extensions that run in Piglets reach the same capabilities through the `extensions/sdk*` bridges over the wire. A Go test of an `API` member therefore asserts the double, not a Pi behavior. Four members have asserting tests through real wrappers (the typed `On*` handlers on the runner and `Fake` unsubscribe behavior) and close with Pi citations. The rest (about 90 rows: `Register*`, `Get*`, `Set*`, `Exec`, `SendMessage`, most `On*`) have none.

Decision needed from the lead, one of:
1. Treat `extensions/sdk` (the Go SDK) as the implementer of `ExtensionAPI` in the ledger, so its conformance rows in `test/extension-conformance` close the `API` rows.
2. Keep the rows open until an in-process implementer of `extension.API` exists.

`ExtensionActions.GetCommands` (host_actions.go) is bound by no production code and read by none; the subprocess bridge reads its own `HostCallbacks.GetCommands`. Pi binds `getCommands` in `agent-session.ts:3347` and reads it in `loader.ts:436`. Either bind and read it for the in-process runner or delete the Go field with an approved record.

R13 outcome: the engine (autobind P1(b) L7) now closes a member of an interface whose only implementations are test doubles only on a cross-SDK conformance test (`test/extension-conformance` or `TestConformance*`). The Fake-bridged `API.On*` tests in `coding/extension/host/inproc/api_on_dispatch_test.go` therefore no longer count, although they still pin the runner's dispatch of kept handlers. No conformance test references an `extension.API` member: the conformance harnesses drive extensions through `extension.Extension` handlers (in-process) or the SDK wire, and a Go adapter from `extension.API` to `Extension.Handlers` would be a new in-process extension runtime, which AGENTS.md forbids without an approved spec. The `extension.API` rows (about 100 coding-agent rows) stay open until the lead approves an `extension.API` implementer or maps them to the SDK conformance rows.
