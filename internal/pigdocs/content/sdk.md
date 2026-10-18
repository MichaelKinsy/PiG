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

1. creates a `coding.AgentSessionServices` container;
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
    services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{
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

`coding.AgentSessionServices` owns shared collaborators such as settings, authentication, model registry state, and paths. Pass it to the Runtime instead of constructing hidden global dependencies.

Set the working directory and agent directory explicitly when your application does not use command-line defaults.

The normal agent directory is:

```text
~/.pig/agent
```

Use `PIG_HOME` or explicit options for isolated tests and applications.

## Runtime

`coding.Runtime` owns extension execution and creates Sessions.

Close the Runtime after all Sessions finish. A Runtime can own extension processes and cancellation state, so abandoning it can leak resources.

## Sessions

Create a fresh Session with `Runtime.New`. Open an existing Session with the Runtime method appropriate for the saved path.

`Session.Send` blocks until the agent tool loop finishes or the context is cancelled. Do not wrap it in an unowned goroutine only to make it appear asynchronous.

Subscribe to Session events before sending when your application needs streaming updates.

### Supply an Agent or a cache warmer

`SessionOptions.Agent` is an `*agent.Agent` you built. The Session drives it instead of building its own, as Pi's `AgentSessionConfig.agent` does (`agent-session.ts:253,486`). The Session installs its hooks into it: it chains a `FinishTurn`, `PrepareRequest`, `PrepareNextTurn` and `TransformLLMMessages` hook the Agent already had, replaces `BeforeToolCall`, `AfterToolCall`, `PreparePrompt`, `PrepareToolResult` and `SessionFile`, and sets the tools and session ID. You own the rest of the Agent's configuration: the stream function, `TransformContext`, `OnPayload` and header hooks (so no `before_provider_request` or `before_provider_headers` event fires unless your hooks emit it), steering and follow-up modes, thinking budgets, transport and image conversion. A nil `Model` and `ThinkingLevel` take the Agent's, so the Session does not restore them from history. An Agent serves one Session: `Clone` and replacements build their own. Without it the Session builds its Agent with the cache-warming stream function. Pi's `extensionRunnerRef` cell does not exist in PiG (D113): read `Session.ExtensionRunner()` after construction.

`SessionOptions.CacheWarmer` is a `coding.CacheWarmer` you built, as Pi's `AgentSessionConfig.cacheWarmer` is (`agent-session.ts:266,494-497`). The Session wires its `OnWarmed` to the `entry_appended` event, reports its `Status`, forwards `OnAgentSettled` and `OnModeChanged`, and cancels it when the Session closes. Your stream function starts it. A Session replacement builds its own warmer for the new Session.

## Models and authentication

`coding.BuildModel` resolves a provider and model through the Services container. Authenticate with the standalone `pig` command or provide the application-specific credential mechanism before creating the model.

Verify a model from the command line:

```bash
pig --print --model github-copilot/gpt-4o-mini "hello"
```

Do not copy credentials into source code.

### Send Anthropic requests through your own client

`ai.StreamOptions.AnthropicClient` replaces the Anthropic Messages provider's client construction, as Pi's `AnthropicOptions.client` does (`anthropic-messages.ts:286-290`, `607-609`). The provider sends its request through it and skips federation, OAuth and API-key header assembly. Other providers ignore it. Use it for an alternative client that shares the Messages API, such as Anthropic on Vertex.

An `ai.AnthropicClient` has one method, `CreateMessage(ctx, ai.AnthropicCreateParams, ai.AnthropicRequestOptions) (*http.Response, error)`. `ctx` carries the request's abort. The provider reads the streamed events from the response body and handles a non-success status itself. A retryable failure that implements `ProviderRequestError` is retried within `StreamOptions.MaxRetries`. `AnthropicCreateParams.Body` is the JSON request body without the betas list, and `Betas` is the comma-joined `anthropic-beta` header value (nil sends no header). `AnthropicRequestOptions.TimeoutMs` is `StreamOptions.TimeoutMs`.

## Tools

Pass caller-owned tools through `coding.SessionStartOptions`. Use the public `agent.AgentTool` contract.

A programmatic Session does not automatically need every interactive CLI tool. Select the tools that belong to the application.

## Extensions

Pass extension definitions through `coding.RuntimeOptions` when your application owns their configuration. The same public extension behavior applies to command-line and embedded use.

Use a Piglet when the composition should also be usable from the `pig` executable or built as a Piglet Binary.

## Cancellation and shutdown

Pass a `context.Context` to session operations. Cancel it when the calling operation ends.

Close resources in this order:

1. finish or cancel active Session operations;
2. close each Session;
3. close the Runtime;
4. release application-owned Services dependencies.

Do not replay a failed extension operation automatically unless the application knows that the operation is idempotent.

### Contexts that print and cancel like Pi's

`github.com/MichaelKinsy/PiG/chord/context` (package `chordctx`) ports Pi's `@earendil-works/chord/context` onto `context.Context`. An abort signal is cancellation, and the abort reason is the cancellation cause. `Background()` and `TODO()` are Pi's `BACKGROUND_CONTEXT` and `TODO_CONTEXT`, and contexts derived from them print as Pi's do. `NewKey` makes a typed key from a description that names it in diagnostics only. `WithValue` and `Value` store and read a value, and the parent is unchanged. `WithAbortSignal(parent, signal)` is cancelled by the parent or the signal, whichever is first, with that one's cause. `WithoutAbortSignal` keeps every value and ignores caller cancellation, for mandatory cleanup only. `WithCancel` returns a child and a `cancel(cause)` function. `Await[T](ctx, work)` returns the work's outcome, or the cancellation cause from this waiter only: it does not cancel the work. `Telemetry(ctx)` is Pi's `Context.telemetry`: the explicit telemetry parent, never nil, which is `telemetry.NoopTelemetryContext` for `Background()`, `TODO()`, and any context that carries none. `WithTelemetry(parent, telemetry)` derives a context with a telemetry parent and changes neither the parent, its cancellation, nor its values. `TraceIn[T](ctx, options, fn)` starts a child span of `Telemetry(ctx)` and calls `fn(child, span)`, where `child` is derived from `ctx` and `Telemetry(child)` is that span, so spans nest through the contexts passed down and there is no ambient current span. `fn`'s result and error pass through unchanged. Pi's callback receives one context typed with a narrowed `telemetry` member; Go cannot narrow a context's type, so the span is a second argument. These helpers do not instrument Chord hosts, methods, or network frames.

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
