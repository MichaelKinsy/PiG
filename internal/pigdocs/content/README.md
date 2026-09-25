# Pig docs

This is Pig's user manual and the canonical local reference for coding agents
helping users manage Pig. It is embedded in the binary and materialized under
`~/.pig/docs` by `pig docs`. Pig also checks the bundle marker at startup. A
newer binary replaces older synchronized docs, and an older binary does not
downgrade newer docs. Do not edit the materialized copy; edit the bundled source
and sync again.

Pig is the Go-native port of upstream Pi. Numbered differences are summarized in
[divergences](divergences.md).

## Start by task

Read the one row that matches the task, then follow only its direct links.

| I want to... | Start here | Then read |
|---|---|---|
| start from zero, guided | [Onboarding](onboarding.md) | [Install](install.md) |
| install Pig and run a first session | [Install](install.md) | [Providers](providers.md) |
| understand the small mental model | [Concepts](concepts.md) | [Packages](packages.md) or [Piglets](piglets.md) |
| install or share capabilities | [Packages](packages.md) | [Extensions](extensions.md) or [Skills](skills.md) |
| shape, launch, build, or hand off an agent | [Piglets](piglets.md) | [Runtime cells](runtime-cells.md) for internals |
| write an extension | [Extensions](extensions.md) | [Extension API](extension-api.md) |
| configure models and credentials | [Providers](providers.md) | [Models](models.md) |
| look up a command or path | [Commands](commands.md) | [Config](config.md) |
| run Pig in CI or a container | [Environment variables](environment-variables.md) | [Config](config.md) |
| change a setting | [Settings](settings.md) | [Project trust](trust.md) |
| resume, branch, or export work | [Sessions](sessions.md) | [Compaction](compaction.md) |
| change or look up a key | [Keybindings](keybindings.md) | [Terminal setup](terminal-setup.md) |
| understand hooks/events | [Events](events.md) | [Extensions](extensions.md) |
| write reusable prompts | [Prompt templates](prompt-templates.md) | [Skills](skills.md) |
| change colors | [Themes](themes.md) | [Settings](settings.md) |
| build terminal UI for an extension | [TUI components](tui.md) | [Extension API](extension-api.md) |
| embed Pig in a program | [SDK](sdk.md) | [Extensions](extensions.md) |
| add a provider or model endpoint | [Custom providers](custom-provider.md) | [Models](models.md) |
| understand Stock Pig and explicit compositions | [Stock Pig and PiG Standard](built-in-extensions.md) | [Divergences](divergences.md) |
| map entities, paths, and the command that inspects each | [Knowledge graph](knowledge-graph.md) | [Concepts](concepts.md) |
| fix an install, trust, toolchain, or display problem | [Troubleshooting](troubleshooting.md) | [Install](install.md) |

## The model in one table

| Pole | Job | Example |
|---|---|---|
| Resource | add one capability or instruction set | extension, skill, prompt, hook, MCP definition, theme, environment |
| Package | distribute available Resources | install from local/git/npm or a contributed resolver |
| Piglet | select/scope Resources and define one named agent | `pig --piglet research` |
| Piglet release | pin exact Resource closure and executable component plan plus optional artifacts | source plus Piglet Binary/Piglet Image facets |
| Piglet Binary | target-native Pig executable for one immutable Piglet composition | may use fused, subprocess, and external components |
| Piglet Image | OCI materialization of Piglet component/environment closure | environment-complete carrier |

The two authored management concepts to remember are **Package** and **Piglet**.
Read [Concepts](concepts.md) before operational pages to keep authored entities,
carriers, component realization, materialization, and environments distinct.

## Authoritative summary

Pig is one Go binary whose config root defaults to `~/.pig` and can be overridden
with `PIG_HOME`. Normal extensions run as subprocesses over the current subprocess wire; compatible
Go factories may also fuse into a Piglet Binary. These are per-component
execution choices, not different binary kinds. Piglet releases record exact
Resource origins and whether each component is fused, subprocess-hosted, or
external and supplied by the binary, release closure, `agentEnv`, or explicit
mapping.

Extension identity comes from its selected source. Runtime registration declares capabilities.
Packages distribute resources. Piglets select and scope them into agents.
Products may contribute authentication targets and install
resolvers, and publishing commands without becoming part of raw Pig.

## Conventions

- `~` means the user's home directory.
- Shell examples assume bash/zsh and `pig` on `PATH`.
- "Upstream" means upstream Pi; "Pig" means this Go binary.
- "Carrier" means Host Piglet, Piglet Binary, or Piglet Image.
- "Realization" means fused, subprocess, or external component execution.
- "Materialization" means binary, release, environment, or external delivery.
- "Cell" is an internal extension-host scheduling/process unit, not a user entity.
- Tables are normative reference; prose explains rationale and boundaries.

## Guidance for coding agents

The system prompt points here instead of carrying this advice in every request.
Read it when a user asks you to configure Pig or to extend it.

Configuring Pig: a Package makes Resources available; a Piglet selects
Resources, built-in tools, discovery, defaults, and environment; a Piglet
Binary is a direct executable build output. Packages never contain or activate
Piglets. A Piglet without a model uses normal model selection, and a Piglet
model is a preference, not a requirement. Piglet Binaries prebuild Go and Rust
extension cells, but MCP services, interpreted runtimes, configuration, and
secrets stay external requirements. Recommend `pig install` for Package
capabilities, `pig --piglet` for source execution, and
`pig piglet build <name> --format binary --out <path>` only for a direct Binary
handoff. Read [Concepts](concepts.md) before advising.

Extending Pig:

- Read [Extensions](extensions.md) first (factory and standalone forms,
  lifecycle, discovery, validation), then [Extension API](extension-api.md) for
  the host API and the per-language SDK quickstart.
- This bundle is a sufficient reference. Do not look for a Pig source checkout
  or installed extensions to copy unless the user asks you to inspect Pig
  internals. Author the extension as a self-contained project in the current
  workspace, then validate it with `pig install <path> --validate-only --json`.
- Scaffold with `pig extension init <dir>`. Prefer Go (native, fusible into a
  Piglet Binary), then Rust (native subprocess, needs cargo), then Python
  (needs a host runtime). Check toolchains in that order and ask before
  installing one; prefer an existing version manager. See [Install](install.md).
- Pick the simplest dev loop: packaged artifact, subprocess wrapper, or
  linked/channel only when a restart or binary update is acceptable.
- Do not add Wasm, embedded JavaScript, dynamic Go plugins, or in-process
  production runtimes without an approved Pig spec or divergence.
- Change Pig core only for upstream parity, provider or session behavior,
  package or runtime infrastructure, or generic extension host APIs.

