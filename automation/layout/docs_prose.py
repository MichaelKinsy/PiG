# SPDX-License-Identifier: MIT
"""Reviewed prose changes that a path-prefix substitution cannot express."""

SDK_INTRO = """# Go SDK

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
"""
SDK_REPLACEMENT = """# Go SDK

The public Go API for extension authors is the separate module `github.com/MichaelKinsy/PiG/extensions/sdk`. Use it to write an extension for PiG. Its import path and module boundary do not change with the host's internal layout.

PiG's agent, provider, Session, and terminal implementation packages live under `internal/`. Go prevents applications outside this repository's import tree from importing them. Use `pig --mode rpc` to control Sessions from an external application, including a Go application. Use a Piglet to compose and distribute an agent.

## Public extension module

```text
github.com/MichaelKinsy/PiG/extensions/sdk
```

The extension SDK is a nested Go module. Its release tags use the prefix `extensions/sdk/`. The command uses the root module's release tag. Pin an exact SDK release when building an extension.

See the [extension SDK reference](https://pkg.go.dev/github.com/MichaelKinsy/PiG/extensions/sdk) and the extension authoring guide.

## In-repository Session integration

The following example is for code inside PiG's import tree. It is not a public embedding SDK. The internal packages can change with the host implementation.
"""
SDK_EDITS = (
    (SDK_INTRO, SDK_REPLACEMENT),
    ("Use the public `agent.AgentTool` contract.", "Use the internal `agent.AgentTool` contract."),
)

AUTOMATION_TABLE_EDIT = (
    "| `automation/` | Reproducible CI image definitions.                                                 |\n| `scripts/`    | Checked development, parity, and release-support commands.                         |",
    "| `automation/` | Build, generation, CI, release, layout, and maintainer tooling. |",
)
GOVERNANCE_EDIT = (
    "Keep project governance and community files at the repository root for the\ninitial publication. They are required by the approved repository runbook and\nmust remain easy to find.",
    "Keep GitHub community-health files in `.github/`. Keep governance, maintainer, and repository quickstart guides in `docs/project/`. Link to them from the root README. Keep `LICENSE`, `LICENSES/`, `NOTICE`, `THIRD_PARTY_NOTICES.md`, `CITATION.cff`, and `REUSE.toml` at the root for licensing and discovery. Keep `AGENTS.md`, `PORT_MAP.md`, and `DIVERGENCES.md` at the root as maintenance authorities.",
)
PUBLIC_PROSE_EDITS = {
    "README.md": (AUTOMATION_TABLE_EDIT,),
    "docs/project/repository-quality.md": (GOVERNANCE_EDIT,),
}

PROSE_EDITS = {
    "docs/site/docs/sdk.md": SDK_EDITS,
    "internal/pigdocs/content/sdk.md": SDK_EDITS,
    "README.md": (
        ("Public coding-agent SDK, extension contracts, Package support, and Piglet support.", "Internal Session runtime, extension contracts, Package support, and Piglet support."),
        ("Public terminal components, rendering, input, and terminal lifecycle.", "Internal terminal components, rendering, input, and terminal lifecycle."),
        ("Small extension and embedding examples.", "Extension examples and in-repository Session integration examples."),
        AUTOMATION_TABLE_EDIT,
        ("PiG aligns public package boundaries with Pi's `agent`, `ai`, `coding-agent`,\nand `tui` packages. Go files within a package follow cohesive implementation\nresponsibilities instead of mirroring TypeScript file mechanics. `PORT_MAP.md`\nrecords every source correspondence.", "PiG follows Go's [module layout guidance](https://go.dev/doc/modules/layout). The implementation packages mirror Pi's agent, AI, coding-agent, and TUI responsibilities under `internal/`. The public Go extension API remains the separate `extensions/sdk` module. `PORT_MAP.md` records every source correspondence."),
    ),
    "docs/project/repository-quality.md": (
        ("Keep the established Go package roots:", "Follow Go's official module layout guidance at <https://go.dev/doc/modules/layout>. Keep implementation packages under `internal/`:"),
        GOVERNANCE_EDIT,
    ),
    "docs/site/docs/cli-integration.md": (
        ("The [Go SDK](/docs/latest/sdk) is not a CLI mode. It runs sessions inside a Go program without a separate process. Pi's SDK is a TypeScript library instead.", "The public [Go extension SDK](/docs/latest/sdk) is not a CLI mode. It defines the extension API. External programs control Sessions through RPC; in-process Session construction is internal to this repository. Pi's embedding SDK is a TypeScript library."),
        ("### Go client", "### In-repository Go client"),
        ("Go programs can use `github.com/MichaelKinsy/PiG/coding/rpcclient`, the Go port of Pi's TypeScript `RpcClient`.", "Code inside PiG's import tree can use `github.com/MichaelKinsy/PiG/internal/coding/rpcclient`, the internal Go port of Pi's TypeScript `RpcClient`. External Go applications use the JSON Lines protocol directly."),
        ("- [Go SDK](/docs/latest/sdk) runs sessions inside a Go program.", "- [Go SDK](/docs/latest/sdk) distinguishes the public extension API from internal Session integration."),
    ),
    "docs/site/docs/rpc.md": (
        ("Use the [PiG Go SDK](/docs/latest/sdk) when your Go application does not need a process boundary. Use RPC mode for another language, an IDE, or a separate process.", "Use RPC mode for an external application, including a Go program or IDE. The [public Go SDK](/docs/latest/sdk) is for extension authors; in-process Session construction is internal to PiG."),
        ("Go programs can use `github.com/MichaelKinsy/PiG/coding/rpcclient`, the port of\nPi's TypeScript `RpcClient`.", "Code inside PiG's import tree can use `github.com/MichaelKinsy/PiG/internal/coding/rpcclient`, the internal port of Pi's TypeScript `RpcClient`. External Go applications use the JSONL protocol directly."),
    ),
    "docs/site/docs/how-pig-works.md": (
        ("| Go SDK | `github.com/MichaelKinsy/PiG/coding` | Runs sessions inside a Go program. |", "| Internal Go integration | `github.com/MichaelKinsy/PiG/internal/coding` | Runs Sessions for code inside PiG's import tree; not a public embedding API. |"),
        ("Pi's SDK is a TypeScript library. PiG's SDK is a Go module.", "Pi's embedding SDK is a TypeScript library. PiG's public Go module is the extension SDK at `extensions/sdk`; external applications control Sessions through RPC."),
    ),
    "docs/site/docs/message-types.md": (
        ("The Go definitions live in two packages:", "The host's Go definitions live in two internal packages. External applications use the JSON protocol rather than importing these packages:"),
        ("- [Go SDK](/docs/latest/sdk) reads and sends these types.", "- [Go SDK](/docs/latest/sdk) distinguishes the public extension API from internal Session integration."),
    ),
    "internal/pigdocs/content/README.md": (
        ("| embed Pig in a program |", "| integrate an external program with Pig |"),
    ),
}


def rewrite_prose(text, path, keep_public_libraries=False):
    edits = PUBLIC_PROSE_EDITS if keep_public_libraries else PROSE_EDITS
    for old, new in edits.get(path, ()):
        if new in text and old not in text:
            continue
        if text.count(old) != 1 or new in text:
            raise ValueError(f"{path}: prose contract changed; review replacement for {old.splitlines()[0]!r}")
        text = text.replace(old, new)
    return text
