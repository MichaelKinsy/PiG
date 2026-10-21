# Go extension SDK

The Go SDK bridges Pi's extension API through PiG's subprocess host (D19). Each factory returns `*sdk.Extension`. Use `pig extension init ./my-extension --lang go` to create a module. Run `pig reload --sdk-path` to stage and locate the SDK from the installed binary.

## Pi helper exports

The helpers Pi exports to extensions keep their names in Go case and behave like Pi's, which the conformance suite checks against Pi's own code: `TruncateHead`, `TruncateTail`, `TruncateLine`, `FormatSize` and `DefaultMaxLines`/`DefaultMaxBytes` (Pi's truncate.ts), `WithFileMutationQueue`, `IsToolCallEventType` (Pi's `isToolCallEventType`, matching the tool name exactly), and `AgentDir`, `ConfigDirName`, `Version`, `PackageDir`, `GetShellConfig` (Pi's `getAgentDir()`, `CONFIG_DIR_NAME`, `VERSION`, `getPackageDir()`, `getShellConfig()`). Pi's pure pi-ai helpers are here too (`StringEnum`, `ClampThinkingLevel`, `GetSupportedThinkingLevels`, `CalculateCost`, `ContentText`, `NormalizeContext`, `GetCurrentSystemPrompt`, `IsContextOverflow`, `IsRetryableAssistantError`, `EnvAPIKeyAuth` and the rest of `pi_ai_text.go` and `pi_ai_models.go`); the transcript helpers take and return raw JSON because a system message's `sections` are read in key order. Byte limits count UTF-8 bytes and `TruncateLine` counts UTF-16 units like a JavaScript string. A nil `TruncationOptions` limit takes Pi's default; 0 is used as given. The host-config functions return `ErrHostInfoUnavailable`, never a made-up path, when the process was not started by a PiG host. `Context.GetEditorComponent` returns the factory this extension installed with `SetEditorComponent`, or nil. Pi's `getEditorComponent` returns the factory installed by any extension, so one extension can wrap another's editor; PiG returns only the calling extension's own factory.

`Context.SetWorkingMessage("")` shows an empty working label, as Pi does; `Context.ClearWorkingMessage()` restores the default label (Pi's `setWorkingMessage()` with no argument).  `Context.ClearHiddenThinkingLabel()` does the same for `SetHiddenThinkingLabel`.

`Context.SetStatus(key, "")` sets an empty status text, as Pi's `setStatus(key, "")` does (RPC mode writes it); `Context.ClearStatus(key)` is Pi's `setStatus(key, undefined)`. An empty placeholder or prefill of `Input` and `Editor` is the unset member (Pi's `placeholder?`, `prefill?`; a Go string cannot be undefined), so RPC mode writes no such member for it.

## Upgrading an older extension

`pig extension upgrade` applies the rewrite rules in the `upgrade` package of this module to an extension written for an earlier SDK. Add a rule there, with an old-extension fixture under `upgrade/testdata/fixtures`, for every breaking change; `make sdk-apidiff` fails a break without a rule and a release note. See the upgrade section of the extensions guide.

## Breaking changes / migration to 0.3.0

Pi 0.99.1 distinguishes unknown context usage from zero and an omitted boolean option from false. Go uses pointers for these values. Rust uses `Option`; Python uses `None`. The Go helpers below select an explicit caller fallback without changing the nullable fields or wire values.

### Context usage

`ContextUsage.Tokens` is `*int`, and `Percent` is `*float64`. Either is nil when unknown, such as after compaction before the next model response. `GetContextUsage()` itself returns nil when no usable model context window is available.

Before:

```go
if usage := ctx.GetContextUsage(); usage != nil && usage.Tokens > 0 {
    fmt.Printf("%d tokens (%.1f%%)", usage.Tokens, usage.Percent)
}
```

`GetContextUsage`, like every host-backed `Context` getter, now also returns an `error`: a host or transport failure is returned instead of an empty value, an assumed default (`IsIdle` no longer assumes idle, `IsProjectTrusted` no longer assumes trusted, `GetFlag` no longer falls back to the registered default) or a nil result. Pi's `undefined` results are nil pointers: `GetSessionName`, `GetSessionFile` and `GetLeafID` return `*string`, `GetModelInfo` and `GetContextUsage` return nil when absent, and `GetFlag` returns a nil value. `GetBranch` and `GetEntries` also return `error`: a failed session-log subscription is reported on every read. Go `ExecOptions.Timeout` and `DialogOptions.Timeout` are `float64`. The full signature table is in `docs/site/docs/extensions.md` ("Host-backed getters report failures and Pi's undefined"). Use `if usage, err := ctx.GetContextUsage(); err != nil { return err } else if usage != nil { ... }`.

After, when unknown means “do not display a count”:

```go
usage, err := ctx.GetContextUsage()
if err != nil {
    return err
}
if usage != nil && usage.Tokens != nil && usage.Percent != nil {
    fmt.Printf("%d tokens (%.1f%%)", *usage.Tokens, *usage.Percent)
}
```

When a particular calculation intentionally treats unknown as zero, use `usage.TokensOr(0)` and `usage.PercentOr(0)`. Check `usage != nil` first. Known zero remains zero even if the selected fallback is nonzero. These helpers do not estimate usage or replace the nil fields.

### Optional booleans

`SendMessageOptions.TriggerTurn` is `*bool`. Other pointer-valued boolean options use the same construction pattern.

Before:

```go
sdk.SendMessageOptions{TriggerTurn: true, DeliverAs: "followUp"}
```

After:

```go
sdk.SendMessageOptions{TriggerTurn: sdk.Bool(true), DeliverAs: "followUp"}
sdk.SendMessageOptions{TriggerTurn: sdk.Bool(false)} // explicitly prevent a turn
sdk.SendMessageOptions{}                            // let the host apply Pi's default
```

Do not replace omitted options with `sdk.Bool(false)`. While a turn streams, an omitted `triggerTurn` steers a custom message into that turn; false defers it. These pointer types are intentional corrections to earlier lossy value mappings, not a wire compatibility layer.

### Tool constrained sampling

`ToolDefinition.ConstrainedSampling` is the `ToolConstrainedSampling` union, matching Pi's `false | ConstrainedSamplingConfig | undefined`. A `ConstrainedSampling` value or pointer requests a configuration, `sdk.DisabledConstrainedSampling{}` sends an explicit false, and nil omits the field. `ToolWithConstrainedSampling` accepts the same union. Existing `&sdk.ConstrainedSampling{...}` and `sdk.ConstrainedSampling{...}` values still compile; code that reads the field as `*ConstrainedSampling` needs a type switch.

### Legacy Go module path

Source imports of `github.com/mainstai/pig/extensions/sdk` remain supported for factories and exact standalones. PiG builds a private copy of its current SDK under that module path, including all subpackages and self-imports. It does not modify the extension's `go.mod` or source. Use `github.com/MichaelKinsy/PiG/extensions/sdk` for new extensions and for Go factories intended to fuse into a Piglet Binary.

## Upstream 0.99.1 extension API

The SDK carries the extension API additions of upstream 0.99.1. Every one is additive; no existing call changes.

| upstream | Go |
|---|---|
| `registerMcpServer`, `unregisterMcpServer` | `Extension.RegisterMcpServer(name, McpServerConfig) error`, `UnregisterMcpServer`; the same on `Context` |
| `getMcpServers` | `Context.GetMcpServers()`, read from state the host replicates |
| `registerVirtualModel`, `unregisterVirtualModel` | `Extension.RegisterVirtualModel(VirtualModel) error`, `UnregisterVirtualModel`; the same on `Context`. `VirtualModel.Route` receives the extension `Context` |
| `getSettings` | `Context.GetSettings()` |
| tool `outputSchema`, `exposure`, `namespace`, `annotations`, `defaultActive`, `prepareLoadout` | the same fields on `ToolDefinition` |
| result `structuredContent` | `ToolResult.StructuredContent` |
| `ctx.tools`, `ctx.executeTool(name, args, options)` | `Context.Tools()`, `Context.ExecuteTool(name, args, *ExecuteToolOptions)`, valid inside a tool handler |
| events `provider_stream_event`, `mcp_servers_change` | `Extension.OnProviderStreamEvent`, `Extension.OnMcpServersChange` |

A registration made before `Run` is applied when the extension loads, and the host validates it: a rejected MCP server or virtual model fails the load with the host's message, as an upstream factory that throws does. A registration made after load returns the host's refusal as its error. `ExecuteTool` never fails for a tool failure: it returns an outcome with `IsError` set. `ExecuteToolOptions.OnUpdate` runs on its own goroutine, in order, and finishes before `ExecuteTool` returns.

An extension cannot set upstream's `Extension.replaceable`: the CLI sets it on its built-in extensions.

## Child agent sessions

`ctx.CreateAgentSession(sdk.CreateAgentSessionOptions{...})` is Pi's `createAgentSession`: a child agent session over the host's model runtime that your extension prompts (`Prompt`, `Steer`, `FollowUp`), observes (`Subscribe`), aborts (`Abort`) and disposes (`Dispose`), with `CustomTools` served by this extension. Calls block as Pi's Promises resolve; the session ends with the connection. See [Child agent sessions](../../docs/extension-authoring.md#child-agent-sessions-createagentsession).
