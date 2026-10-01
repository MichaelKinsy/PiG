# Go SDK

PiG exposes its coding agent as Go packages. Use the SDK when a Go application needs to create and control PiG sessions without starting the command-line interface as a child process.

Use RPC mode when the caller is not a Go program or when a process boundary is required.

## Module

```text
github.com/MichaelKinsy/PiG
```

PiG is under active development. Pin an exact release or commit when you embed it. Do not assume API stability before the first public release.

After the repository and version tags are public, use the generated API reference
on pkg.go.dev:

- [`github.com/MichaelKinsy/PiG`](https://pkg.go.dev/github.com/MichaelKinsy/PiG)
- [`github.com/MichaelKinsy/PiG/agent`](https://pkg.go.dev/github.com/MichaelKinsy/PiG/agent)
- [`github.com/MichaelKinsy/PiG/ai`](https://pkg.go.dev/github.com/MichaelKinsy/PiG/ai)
- [`github.com/MichaelKinsy/PiG/coding`](https://pkg.go.dev/github.com/MichaelKinsy/PiG/coding)
- [`github.com/MichaelKinsy/PiG/tui`](https://pkg.go.dev/github.com/MichaelKinsy/PiG/tui)
- [`github.com/MichaelKinsy/PiG/extensions/sdk`](https://pkg.go.dev/github.com/MichaelKinsy/PiG/extensions/sdk)

The extension SDK is a nested Go module. Its module version uses the tag prefix
`extensions/sdk/`, while the command and other public packages use the root
module tag.

## Minimal flow

A program normally:

1. creates a `coding.Services` container;
2. creates a `coding.Runtime`;
3. resolves a model;
4. creates or opens a `coding.Session`;
5. sends a prompt;
6. consumes returned messages or session events;
7. closes the Session and Runtime.

```go
package main

import (
    "context"
    "log"

    "github.com/MichaelKinsy/PiG/coding"
)

func main() {
    services, err := coding.NewServices(coding.ServicesOptions{
        CWD:      ".",
        AgentDir: coding.DefaultAgentDir(),
    })
    if err != nil {
        log.Fatal(err)
    }

    runtime, err := coding.NewRuntime(coding.RuntimeOptions{
        Services: services,
    })
    if err != nil {
        log.Fatal(err)
    }
    defer runtime.Close()

    model, err := coding.BuildModel("github-copilot/gpt-4o-mini", services)
    if err != nil {
        log.Fatal(err)
    }

    session, err := runtime.New(coding.SessionStartOptions{
        Model:        model,
        SystemPrompt: "You are a concise coding assistant.",
    })
    if err != nil {
        log.Fatal(err)
    }
    defer session.Close()

    _, err = session.Send(context.Background(), "Summarize this repository.")
    if err != nil {
        log.Fatal(err)
    }
}
```

The repository contains a complete example at `examples/sdk/main.go`.

## Services

`coding.Services` owns shared collaborators such as settings, authentication, model registry state, and paths. Pass it to the Runtime instead of constructing hidden global dependencies.

Set the working directory and agent directory explicitly when your application does not use command-line defaults.

The normal agent directory is:

```text
~/.pig/agent
```

Use `PIG_HOME` or explicit options for isolated tests and applications.

Use `coding.NewInMemorySettingsManager(initialSettings)` to keep settings without file I/O. Pass that manager through `ServicesOptions.SettingsManager`. The Services container retains it, and Session setting changes survive `Reload` in memory. An explicit `ServicesOptions.ProjectTrusted` value applies to the supplied manager; otherwise its trust state remains unchanged. File-backed settings remain the default. Reload discards transient overrides, and explicit empty tool and Resource lists remain empty.

`SettingsManager.GetThemeSetting()` returns a `*string`: nil means omitted, while a pointer to an empty string preserves an explicitly empty name. JSON loading, cloning, project merging, and `SetTheme("")` retain that distinction. `GetTheme()` returns a fixed theme name, or an empty string for an omitted or slash-separated automatic setting.

## Runtime

`coding.Runtime` owns extension execution and creates Sessions.

Close the Runtime after all Sessions finish. A Runtime can own extension processes and cancellation state, so abandoning it can leak resources.

### Factory-owned replacement

Use `coding.CreateAgentSessionRuntime` when an embedded Go application must replace the current Session. Supply a `CreateAgentSessionRuntimeFactory` that constructs the destination CWD's Services, resources, tools, and a fresh extension runner, then creates a Session over the supplied `SessionManager`. Return that Session and its owning Services in `CreateAgentSessionRuntimeResult`.

Read the current Session through `Runtime.Session()`. `Runtime.NewSession`, `Runtime.SwitchSession`, `Runtime.Fork`, and `Runtime.ImportFromJsonl` use the retained factory. Replacement settles the outgoing response before shutdown and invalidates its runner before installing the next Session. A cancelled before-switch or before-fork handler leaves the current Session in place. Import selects a suffixed filename when the original filename already exists.

Install `Runtime.SetRebindSession` to subscribe to the new Session's events and bind its extensions. The Runtime awaits that callback before returning. `Session.BindExtensions` dispatches the configured `session_start` reason after binding. Close the Runtime to release its current Session.

In a UI host, render the replacement Session and attach its event consumer before binding extensions. A `session_start` handler can notify or send messages during that binding. Capture the Session before waiting. If another replacement wins while the binding is pending, do not let the stale completion attach a consumer or update the terminal title.

Native Go replacement options accept `WithSession func(*extension.ReplacedSessionContext) error`. The Runtime invokes it after rebinding and returns its error to the caller. Use the supplied context, not a retained old command context. Its `SendMessage` and `SendUserMessage` calls await the Session operation. User-message command/template expansion defaults to false. `NewSessionOptions.Setup` runs before rebinding and refreshes the Session from the initialized log.

The `pig` CLI creates and replaces its Session through this path in print, JSON, RPC and interactive mode. Its factory rebuilds the destination working directory's Services, resources and extension host for every replacement. The `Session` methods `NewSession`, `SwitchSession`, `CloneInPlace`, `ForkToNewSession`, `ForkToNewSessionWithText`, the headless `Session.DispatchSlash` `/fork` path and `ImportFromJsonl`, and the default `Session.ExtensionCommandActions` new, fork and switch actions, do not use a factory. They keep the host-scoped runner and the startup project's Services, so D30 (Part 2) and D61 still apply to code that calls them. D30 (Part 1) also applies to subprocess extensions in the CLI modes: after a replacement their host rejects later calls with Pi's stale message, but SDK-local getters return the outgoing Session's values and host calls do not throw at the call site (they return or raise an error, or write to stderr, by SDK). The subprocess extension SDKs do not yet expose the `withSession` callback. See [divergences](https://github.com/MichaelKinsy/PiG/blob/main/docs/parity/DIVERGENCES.md).

## Sessions

Create a fresh Session with `Runtime.New`. Open an existing Session with the Runtime method appropriate for the saved path.

`Runtime.Open` validates and owns one file snapshot. Session construction uses that snapshot instead of opening the file again. Restoring the model and thinking level reads branch settings without projecting message content; the transcript projection runs separately.

`Session.Send` blocks until the agent tool loop finishes or the context is cancelled. Do not wrap it in an unowned goroutine only to make it appear asynchronous.

`Session.Prompt(ctx, text, options)` returns the turn messages and an error. `PromptOptions` controls invocation images, input source, streaming delivery and command/template expansion. It does not replace system-prompt construction options. `Session.SendUserMessage(ctx, content, options)` awaits an extension-originated user turn and defaults expansion to false. `Session.SendCustomMessage(ctx, message, options)` awaits custom delivery and any triggered turn. The bound extension actions use `SendExtensionUserMessage` and `SendMessage`; the Session owns their background continuations. Preserve a nil `TriggerTurn` separately from explicit false.

When an `agent_settled` extension handler appends custom messages, their message events precede the final settlement event on both Session listeners and mode output.

Subscribe to Session events before sending when your application needs streaming updates.

### Supply a SessionManager

`coding.SessionManager` is the actual append-only log, not a snapshot of its messages. Create an in-memory manager with `coding.NewInMemorySessionManager(cwd)`. Pass it to `ServicesOptions.SessionManager` when it supplies the project CWD default. Pass the same pointer to `SessionStartOptions.SessionManager` or `SessionOptions.SessionManager` to select the backing log.

```go
manager, err := coding.NewInMemorySessionManager(".")
if err != nil {
    return err
}
services, err := coding.NewServices(coding.ServicesOptions{
    AgentDir:       agentDir,
    SessionManager: manager,
})
if err != nil {
    return err
}
runtime, err := coding.NewRuntime(coding.RuntimeOptions{Services: services})
if err != nil {
    return err
}
defer runtime.Close()
session, err := runtime.New(coding.SessionStartOptions{
    Model:          model,
    SessionManager: manager,
    ThinkingLevel:  ai.ThinkingHigh,
})
if err != nil {
    return err
}
defer session.Close()
// session.SessionManager() == manager
```

An explicit Services CWD wins over the manager's CWD. Without a manager override, new persisted Sessions use the selected Services agent directory. `GetSessionFile` returns nil in memory. A persisted manager selects a file path before the first assistant message flushes it, so `IsPersisted` does not imply that the file already exists. Forks and clones keep the source persistence mode. A retained branch without an assistant defers its file write. A factory-owned Runtime fork replaces the Session but retains the in-memory SessionManager object.

Direct calls to a Session's tools receive its current identity, model and thinking state. A tool call with an explicit ToolEnvironment keeps that context instead. Clones keep separate context bindings.

## Stream observations

A stream event delivers a partial assistant message whose exported fields equal the stream state at the moment the consumer receives it. Read `event.Partial` directly; no special call is needed. The fields do not change afterward, so a partial you keep reads as it did at delivery. To read the producer's current state of a partial you retained, call `partial.Observe()` or `ai.RefreshEvent(event)`; both return independently owned data. A JavaScript listener that keeps an event and reads it after an `await` sees the producer's later changes; a Go listener that keeps one must ask for them. Agent listeners and Session listeners receive messages refreshed at their own dispatch, so their direct reads match what Pi's listeners read at that tick (`agent.RefreshEvent`). Extension processes receive JSON snapshots taken at dispatch (D82).

Agent `message_start` and `message_update` messages retain Pi's shallow-copy meaning. Copied top-level fields stay fixed, while referenced nested content can advance. For example, a held start message can acquire final content while its copied stop reason remains `pending`. Final messages contain no temporary provider parser fields.

`AssistantMessageEventStream.Result` completes independently of event iteration and preserves the terminal message's identity. Use `ObserveEvents` when a consumer needs to perform another asynchronous wait. It yields a `StreamObservation` scope with each event. Prepare the values that the callback reads synchronously, then use `scope.Await` for the blocking wait. Use `scope.Yield` for an already completed operation whose continuation must still be queued. Do not block an observed callback on an unrelated channel without releasing its scope.

For a constant-size projection, `partial.ObserveUsage()` copies only the current usage values, and `partial.ObserveToolCallIdentity(index)` reads only the selected block's ID, name, and index/type validity. These methods preserve the same full or shallow reference semantics without traversing content arguments or metadata. Agent assistant messages also provide `ObserveUsage()`, preserving absent usage.

The Agent owns stream creation and iterator adoption on one continuation. Direct stream consumers can use `ai.WithStreamContinuations(ctx)` before publishing their request context, then wrap stream creation and iteration in `ai.RunStreamContinuation`. Pass that shared context to the producer. Pass `scope.Context(ctx)` to `ObserveEvents` so the iterator reuses the caller's continuation. This prevents producer reactions from interrupting synchronous setup before the caller first awaits. `ai.StreamObservationFromContext` retrieves the active scope without replacing the Agent's shared abort-signal object.

## Models and authentication

`coding.BuildModel` resolves a provider and model through the Services container. Authenticate with the standalone `pig` command or provide the application-specific credential mechanism before creating the model.

Verify a model from the command line:

```bash
pig --print --model github-copilot/gpt-4o-mini "hello"
```

Do not copy credentials into source code.

Use `coding.CreateModelRuntime(ctx, options)` for model lookup and catalog refresh without creating a Session. `CreateModelRuntimeOptions` selects `AuthPath`, `ModelsPath`, `ModelsStore`, and `ModelsStorePath`. Initial refresh restores cached catalogs without network access unless `AllowModelNetwork` is true. Set `RefreshOnCreate` to a pointer to false to skip that refresh. `ModelRefreshTimeoutMs` bounds an enabled initial network refresh. The caller context also cancels the initial refresh.

`ModelsPath` preserves three states: nil selects the default `models.json`; a pointer to nil disables the file; a pointer to a string pointer selects that path. The default catalog store sits beside the selected model file. Disabling the file selects an in-memory catalog store unless `ModelsStore` is supplied. Initial provider errors or cancellation do not discard the runtime; inspect the result of an explicit `Refresh` call when the caller needs refresh status.

`ModelRuntime.Login` and `ModelRuntime.Logout` serialize operations for the same provider through credential persistence and local catalog/auth synchronization. Different providers can authenticate concurrently. A queued operation can be cancelled without running. After a credential change commits, `*coding.CredentialSynchronizationError` reports a failed local synchronization without implying that the credential change was rolled back. Inspect `ProviderID`, `Operation`, `Credential`, and `errors.Unwrap(err)` before deciding how to recover. Local synchronization does not fetch models over the network.

`ModelRegistry.GetAPIKeyAndHeaders` and extension model-auth lookups share request-auth resolution for built-in and configured models. Preserve nil values in returned `Headers`; they remove default provider headers. Pass the returned `Env` with compatibility requests so credential-scoped endpoint settings remain available. Model Runtime resolves request credentials once and combines them with current model metadata before streaming.

`ai.RuntimeCredentials` retains a caller-supplied cancellation cause when its own `Read`, `List`, or `Delete` abort check rejects an operation. A failed or cancelled deletion leaves the runtime key in place.

File-backed credential `Modify` and `Delete` calls can cancel while waiting behind another operation on the same store. Cancelling a queued call does not release an active callback's file lock.

For deterministic provider tests, `ai.NewFauxProvider` streams each queued response through the normal event lifecycle. An empty response still emits `start` before its terminal event. Queued `error` and `aborted` responses emit an error event and retain their supplied reason, error message, and timestamp.
Use JSON-serializable model metadata. Circular map or slice references in `SamplingParams` fail JSON serialization instead of causing an unbounded model-projection copy. Keep the returned model metadata read-only.

## Model scopes

Set `SessionOptions.ScopedModels` or the corresponding Runtime session-start option to supply an ordered cycling scope. `coding.ScopedModel` is the shared native type: it contains an `*ai.Model` and an optional thinking-level preference. An empty scope permits all available models.

`Session.ScopedModels()` and native extension Context getters retain the scope's list and model identities. Treat returned lists and models as read-only. Use `SetScopedModels` to publish a replacement. This changes the session-only scope, not the configured enabled-model list. Subprocess SDKs receive model metadata values rather than Go pointers.

## Tools

Pass caller-owned tools through `coding.SessionStartOptions`. Use the public `agent.AgentTool` contract.

A programmatic Session does not automatically need every interactive CLI tool. Select the tools that belong to the application.

Return final tool output in `agent.AgentToolResult.Content`, an ordered `[]ai.ToolResultMessageContent` slice. Use `ai.TextContent` and `ai.ImageContent` values in the order the model should receive them:

```go
result := agent.AgentToolResult{
    Content: []ai.ToolResultMessageContent{
        ai.TextContent{Text: "before"},
        ai.ImageContent{Data: imageBase64, MimeType: "image/png"},
        ai.TextContent{Text: "after"},
    },
}
```

Use `[]ai.ToolResultMessageContent{}` for no content and `[]ai.ToolResultMessageContent{ai.TextContent{Text: ""}}` for an explicit empty text block. `result.Text()` joins text blocks with newline separators, and `result.Images()` selects image blocks. These display projections do not change the ordered content. A non-nil `agent.AfterToolCallResult.Content` replaces the complete content array; an empty slice clears it, while nil leaves it unchanged.

## Extensions

Pass extension definitions through `coding.RuntimeOptions` when your application owns their configuration. The same public extension behavior applies to command-line and embedded use.

Use a Piglet when the composition should also be usable from the `pig` executable or built as a Piglet Binary.

## Cancellation and shutdown

Pass a `context.Context` to session operations. Cancel it when the calling operation ends.

The low-level `harness.WithAbortSignal(parent, signal)` keeps the parent's values. If the parent cannot cancel, the derived context retains the supplied signal's `Done` channel. If both inputs can cancel, the first cancellation determines the derived context's cause.

Close resources in this order:

1. finish or cancel active Session operations;
2. close each Session;
3. close the Runtime;
4. release application-owned Services dependencies.

Do not replay a failed extension operation automatically unless the application knows that the operation is idempotent.

## Harness events

Low-level harness restore reads cached control values in source order. It reads an active operation's metadata before its state and performs no writes during restore.

A failed drive clears its finished pass before observers receive the harness fault. Closing a harness still rejects observation of an unfinished effect immediately; that effect retains drive ownership until it returns.

Low-level harness event batches clone every payload before binding recipients. Function values raise `agentharness.DataCloneError`. A rejected batch delivers no events and does not prevent later valid batches. A lane command that already committed retains its durable and in-memory state when event cloning fails.

## RPC alternative

Start PiG in RPC mode when the caller needs a language-neutral process boundary:

```bash
pig --mode rpc
```

See [RPC mode](/docs/latest/rpc).

## TypeScript extension declarations

Node extensions use the TypeScript API from
`@earendil-works/pi-coding-agent`. PiG maps those imports to compatible runtime
modules when it starts the extension.

The source tree includes `extensions/sdk-ts`, a declaration-only package that
pins the exact Pi version and adds types for PiG-only APIs such as
`ui.setLogin`. It does not contain a second TypeScript runtime. Install it from
the source tree during development until PiG publishes a versioned package.

## Upstream distinction

The npm package `@earendil-works/pi-coding-agent` remains the canonical
TypeScript SDK. PiG's Go SDK and non-TypeScript bridges do not replace it.

See the [upstream Pi SDK documentation](https://pi.dev/docs/latest/sdk) when you are integrating Pi itself.

## Related documentation

- [Extensions](/docs/latest/extensions)
- [Piglets](/docs/latest/piglets)
- [Derivative harnesses](/docs/latest/derivative-harnesses)
- [RPC mode](/docs/latest/rpc)
