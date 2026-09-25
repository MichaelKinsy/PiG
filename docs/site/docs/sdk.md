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

## Runtime

`coding.Runtime` owns extension execution and creates Sessions.

Close the Runtime after all Sessions finish. A Runtime can own extension processes and cancellation state, so abandoning it can leak resources.

## Sessions

Create a fresh Session with `Runtime.New`. Open an existing Session with the Runtime method appropriate for the saved path.

`Session.Send` blocks until the agent tool loop finishes or the context is cancelled. Do not wrap it in an unowned goroutine only to make it appear asynchronous.

Subscribe to Session events before sending when your application needs streaming updates.

## Models and authentication

`coding.BuildModel` resolves a provider and model through the Services container. Authenticate with the standalone `pig` command or provide the application-specific credential mechanism before creating the model.

Verify a model from the command line:

```bash
pig --print --model github-copilot/gpt-4o-mini "hello"
```

Do not copy credentials into source code.

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
