# PiG docs

This is PiG's user manual and the canonical local reference for coding agents
helping users manage PiG.

> **Differs from Pi, or easy to get wrong**
> - Config lives in `~/.pig` and `.pig/`, never `~/.pi` or `.pi/` (D2).
> - Project files load only in a trusted project. With `-p` there is no prompt and PiG skips them: pass `--approve` ([trust](trust.md)).
> - A Piglet's `tools:` is a ceiling, not a list to turn on ([strip built-ins](piglet-strip.md)).
> - Skills load from `~/.pig/agent/skills/` and `~/.agents/skills/`, not `~/.pig/skills/` ([skills](skills.md)).
> - Only top-level `.ts` or `.js` files load from an extensions folder. Go, Rust, Python, and Lisp are the main languages ([extensions](extensions.md)).

PiG is the Go-native port of upstream Pi. Numbered differences are summarized in
[divergences](divergences.md). `divergences.md` has one row per line, so search it with `grep -n -i <word>` instead of reading it whole.

## Start by task

Read the one row that matches the task, then follow only its direct links.

| I want to... | Start here | Then read |
|---|---|---|
| make PiG do something it does not do, or choose how to customize it | [Extend and modify PiG](extend-pig.md) (the customize chooser) | [Extensions](extensions.md) or [Piglets](piglets.md) |
| a tool, command, extension, or model is missing | [Strip built-ins and limit tools](piglet-strip.md) | [Commands](commands.md#tools) |
| which Piglet is active and what it limits | [Strip built-ins and limit tools](piglet-strip.md#inspect-the-active-piglet) | [Piglets](piglets.md) |
| check a Pi behavior before assuming it in an area PiG changed (config paths, trust, tools, extensions) | [Divergences](divergences.md) (`grep -n -i <word> divergences.md`) | The "Differs from Pi" boxes in [Settings](settings.md), [Extensions](extensions.md), and [Skills](skills.md) |
| start from zero, guided | [Onboarding](onboarding.md) | [Install](install.md) |
| do everyday work for a user who does not program (PDFs, spreadsheets, documents, reports, files) | [Applied use cases](applied-use-cases.md) | [Sessions](sessions.md) |
| install PiG and run a first session | [Install](install.md) | [Providers](providers.md) |
| learn how PiG runs a turn: the agent loop, context, sessions, tools, and interfaces | [How PiG works](how-pig-works.md) | [Concepts](concepts.md) |
| look up what a PiG term means (Resource, Package, Piglet, Piglet Binary, Provider, ...) | [Glossary](glossary.md) | [Concepts](concepts.md) |
| understand the small mental model | [Concepts](concepts.md) | [Packages](packages.md) or [Piglets](piglets.md) |
| install or share capabilities | [Packages](packages.md) | [Extensions](extensions.md) or [Skills](skills.md) |
| publish a Package or Piglet to npm | [Publishing](publishing.md) | [Packages](packages.md) or [Piglets](piglets.md) |
| shape, launch, build, or hand off an agent | [Piglets](piglets.md) | [Runtime cells](runtime-cells.md) for internals |
| take a Piglet from a file to an installed, signed, published agent (author, validate, run, inspect, register, build, sign, publish, install, update) | [Piglet lifecycle](piglet-lifecycle.md) | [Piglets](piglets.md) |
| learn why extension cells are packed into host processes | [Runtime cells](runtime-cells.md) | [Extensions](extensions.md) |
| write an extension | [Extensions](extensions.md) | [Extension API](extension-api.md) |
| look up a host API call, the wire protocol, or an SDK quickstart | [Extension API](extension-api.md) | [Message types](message-types.md) |
| configure models and credentials | [Providers](providers.md) | [Models](models.md) |
| choose a model, scope the model cycle, or add my own model to `models.json` | [Models](models.md) | [Providers](providers.md) |
| look up a command or path | [Commands](commands.md) | [Config](config.md) |
| find a configuration directory, project directory, or context file | [Configuration](config.md) | [Settings](settings.md) |
| run PiG in CI or a container | [Environment variables](environment-variables.md) | [Config](config.md) |
| change a setting | [Settings](settings.md) | [Project trust](trust.md) |
| decide whether to trust a project's Resources | [Project trust](trust.md) | [Security](security.md) |
| resume, branch, or export work | [Sessions](sessions.md) | [Compaction](compaction.md) |
| understand when PiG compacts a long session and tune it | [Compaction](compaction.md) | [Settings](settings.md) |
| change or look up a key | [Keybindings](keybindings.md) | [Terminal setup](terminal-setup.md) |
| fix keys or images that fail in my terminal | [Terminal setup](terminal-setup.md) | [Troubleshooting](troubleshooting.md) |
| understand hooks/events | [Events](events.md) | [Extensions](extensions.md) |
| write reusable prompts | [Prompt templates](prompt-templates.md) | [Skills](skills.md) |
| write a skill | [Skills](skills.md) | [Packages](packages.md) |
| connect an MCP server | [MCP servers](mcp.md) | [Codemode](codemode.md) |
| run scripts that call tools | [Codemode](codemode.md) | [MCP servers](mcp.md) |
| route requests to different models | [Virtual models](virtual-models.md) | [Extensions](extensions.md) |
| change colors | [Themes](themes.md) | [Settings](settings.md) |
| build terminal UI for an extension | [TUI components](tui.md) | [Extension API](extension-api.md) |
| embed PiG in a program | [SDK](sdk.md) | [Extensions](extensions.md) |
| add a provider or model endpoint | [Custom providers](custom-provider.md) | [Models](models.md) |
| look up a PiG-only feature for authors by its D number | [Additive features](additive-features.md) | [Divergences](divergences.md) |
| understand Stock Pig and explicit compositions | [Stock Pig and PiG Standard](built-in-extensions.md) | [Divergences](divergences.md) |
| map entities, paths, and the command that inspects each | [Knowledge graph](knowledge-graph.md) | [Concepts](concepts.md) |
| run PiG from a script or another program | [CLI integration](cli-integration.md) | [JSON mode](json.md) or [RPC](rpc.md) |
| use JSON mode or RPC | [JSON mode](json.md), [RPC](rpc.md) | [RPC commands](rpc-commands.md), [RPC extension UI](rpc-extension-ui.md) |
| send or look up an RPC command | [RPC commands](rpc-commands.md) | [RPC](rpc.md) |
| answer extension UI requests from an RPC client | [RPC extension UI](rpc-extension-ui.md) | [RPC](rpc.md) |
| read the file and message formats | [Session format](session-format.md) | [Message types](message-types.md) |
| look up a message or content block shape | [Message types](message-types.md) | [Session format](session-format.md) |
| run a local llama.cpp model | [llama.cpp](llama-cpp.md) | [Models](models.md) |
| check what PiG can reach and run | [Security](security.md) | [Project trust](trust.md) |
| see usage and cost | [Usage](usage.md) | [Settings](settings.md) |
| fix an install, trust, toolchain, or display problem | [Troubleshooting](troubleshooting.md) | [Install](install.md) |

The platform pages on the site (tmux, Termux, Windows, containerization, shell aliases) are not in this bundle on purpose. They are setup guides for a particular terminal or host, not behavior of the program, and the site keeps them. `troubleshooting.md` and `terminal-setup.md` link them. This is a documentation choice, not a numbered divergence.

## Where this bundle lives

It is embedded in the binary and materialized under
`~/.pig/docs/bundle-<hash>/` by `pig docs`, one directory per build, named by
the content hash of its pages. PiG also checks the bundle marker at startup.
Two PiG builds that share a config root each keep their own bundle, so neither
rewrites the other's. `pig docs path` prints this build's directory. Do not edit
the materialized copy; edit the bundled source and sync again.

## The model in one table

| Pole | Job | Example |
|---|---|---|
| Resource | add one capability or instruction set | extension, skill, prompt, hook, MCP definition, theme, environment |
| Package | distribute available Resources | install from local/git/npm or a contributed resolver |
| Piglet | select/scope Resources and define one named agent | `pig --piglet research` |
| Piglet release | pin exact Resource closure and executable component plan plus optional artifacts | source plus Piglet Binary facets (a Piglet Image facet is planned) |
| Piglet Binary | target-native PiG executable for one immutable Piglet composition | may use fused, subprocess, and external components |
| Piglet Image | planned, not in this release: OCI materialization of Piglet component/environment closure; no producer exists | none yet |

The two authored management concepts to remember are **Package** and **Piglet**.
Read [Concepts](concepts.md) before operational pages to keep authored entities,
carriers, component realization, materialization, and environments distinct.

## Authoritative summary

PiG is one Go binary whose config root is `PIG_HOME`, else `$XDG_CONFIG_HOME/pig`,
else `~/.pig` (`configroot.Resolve`). Normal extensions run as subprocesses over the current subprocess wire; compatible
Go factories may also fuse into a Piglet Binary. These are per-component
execution choices, not different binary kinds. Piglet releases record exact
Resource origins and whether each component is fused, subprocess-hosted, or
external and supplied by the binary, release closure, `agentEnv`, or explicit
mapping.

Extension identity comes from its selected source. Runtime registration declares capabilities.
Packages distribute resources. Piglets select and scope them into agents.
Products may contribute authentication targets and install
resolvers, and publishing commands without becoming part of raw PiG.

## Conventions

- `~` means the user's home directory.
- Shell examples assume bash/zsh and `pig` on `PATH`.
- "Upstream" means upstream Pi; "PiG" means this Go binary.
- "Carrier" means Host Piglet, Piglet Binary, or Piglet Image (planned).
- "Realization" means fused, subprocess, or external component execution.
- "Materialization" means binary, release, environment, or external delivery.
- "Cell" is an internal extension-host scheduling/process unit, not a user entity.
- Tables are normative reference; prose explains rationale and boundaries.

## Guidance for coding agents

The system prompt points here instead of carrying this advice in every request.
Read it when a user asks you to configure PiG or to extend it.

Configuring PiG: a Package makes Resources available; a Piglet selects
Resources, built-in tools, discovery, defaults, and environment; a Piglet
Binary is a direct executable build output. Packages never contain or activate
Piglets. A Piglet without a model uses normal model selection, and a Piglet
model is a preference, not a requirement. Piglet Binaries prebuild Go and Rust
extension cells, but MCP services, interpreted runtimes, configuration, and
secrets stay external requirements. Recommend `pig install` for Package
capabilities, `pig --piglet` for source execution, and
`pig piglet build <name> --format binary --out <path>` only for a direct Binary
handoff. Read [Concepts](concepts.md) before advising.

Extending PiG:

- Read [Extensions](extensions.md) first (factory and standalone forms,
  lifecycle, discovery, validation), then [Extension API](extension-api.md) for
  the host API and the per-language SDK quickstart.
- This bundle is a sufficient reference. Do not look for a PiG source checkout
  or installed extensions to copy unless the user asks you to inspect PiG
  internals. Author the extension as a self-contained project in the current
  workspace, then validate it with `pig install <path> --validate-only --json`.
- Scaffold with `pig extension init <dir>`. Prefer Go (native, fusible into a
  Piglet Binary), then Rust (native subprocess, needs cargo), then Python
  (needs a host runtime). Check toolchains in that order and ask before
  installing one; prefer an existing version manager. See [Install](install.md).
- Pick the simplest dev loop: packaged artifact, subprocess wrapper, or
  linked/channel only when a restart or binary update is acceptable.
- Do not add Wasm, embedded JavaScript, dynamic Go plugins, or in-process
  production runtimes without an approved PiG spec or divergence.
- Change PiG core only for upstream parity, provider or session behavior,
  package or runtime infrastructure, or generic extension host APIs.

