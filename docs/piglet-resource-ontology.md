# Pig ecosystem ontology

This is the normative maintainer reference for Pig Resources, Packages, Piglets,
outputs, environments, and external product bindings. Return to the
[maintainer router](README.md) for implementation details.

## The shortest model

> Packages distribute and make Resources available. Piglets select and scope them. A source
> Piglet runs with `pig --piglet`; an optional generated script is only a thin
> source entry point. Piglet Binaries execute directly and verify exact records. Piglet Image is the reserved OCI carrier. Deployment platforms supply their bindings outside portable Piglet source.

## Entities and outputs

| Term | Contract | Authored? |
|---|---|---:|
| Resource | one extension, skill, prompt, theme, hook, MCP definition/client, or agent environment | yes |
| Plugin | cross-harness Agent Plugins package plus distinct vendor facets | yes |
| Package | optional Pi/Pig distribution envelope for Resources; never decides Piglet activation | yes |
| Piglet | one named whole-agent composition and policy | yes |
| Piglet release | versioned distribution joining source identity, exact closure, records, and optional artifacts | generated publication state |
| source entry script | executable POSIX wrapper around `pig --piglet <canonical-source>` | generated convenience output |
| Piglet Binary | target-native executable for one immutable Piglet composition | build artifact |
| Piglet Image | reserved OCI materialization of Piglet/runtime/environment closure; no producer exists | build artifact |

The script is a user-owned source entry point, outside Piglet artifact and
managed inventory. Pig writes it only to an explicit `--out` path or stdout and
never removes it later.

## Execution carriers

| Carrier | Invocation | Contract |
|---|---|---|
| Host Piglet | `pig --piglet <name|path>` or generated source script | current Pig process resolves source and enters required environment |
| Piglet Binary | execute the Binary path | target-native Pig verifies its baked Piglet and registered closure |
| Piglet Image | planned: run the image digest/reference | reserved exact Pig, Resource, runtime, and environment closure |

No ProfileInstance exists. Piglet execution uses exactly the three carriers in
the table above. A local run is a Session. Remote-agent identity belongs to the consuming platform.

## Package responsibility

Upstream-compatible `pig install|list|remove|update|config` controls Package
settings and discovery. Installation may materialize and record a Package. It
must not:

- copy or activate a Piglet;
- copy Agent declarations into another store;
- merge MCP configuration;
- create aliases, scripts, launchers, or shell state;
- activate Pig-only Resources outside normal Package discovery/filtering.

Pig may provide side-effect-free `pig package list --json` and
`pig package validate <dir>`. Ordinary `package.json` is the authoring format;
Pig does not need a second Package membership editor.

A Piglet may materialize a named Package source without mutating Package
settings. Only Piglet entries that explicitly select Package members activate
them.

## Lean Piglet contract

The closed root vocabulary is:

```text
version name description extends packages extensions skills tools model
systemPrompt discovery agentEnv secrets release build
```

Removed pre-release fields receive no aliases:

```text
builtin agentPrompts mcpServers mcpConfigPaths extendsPolicy top-level remove
harness settings extension commands extension shortcuts
```

### Tools

Root tools are an exact allowlist of Pig built-in model tools:

```yaml
tools: [read, grep, find, ls]
```

| Shape | Meaning |
|---|---|
| omitted | Pig's normal built-in tool set |
| `[]` | no built-in model tools |
| list | exact built-in ceiling |

Extension tools use the same list shape:

```yaml
extensions:
  - name: web
    origins: [package:web]
    tools: [web_search, fetch_content]
```

Effective availability is the intersection of runtime registration, Piglet
allowlists, explicit CLI narrowing, and platform authorization. CLI/platform
inputs never silently widen a Piglet ceiling.

Slash commands and shortcuts are not Piglet capability policy. They are removed
from source until one enforced cross-runtime contract is justified.

### Discovery

Discovery governs ambient Resources only:

```yaml
discovery:
  extensions: [workspace, user]
  skills: [workspace]
```

Allowed sources initially are `workspace` and `user`. Empty or omitted lists in
an active Piglet mean no ambient Resources of that kind. Explicit Piglet
Resources and explicit caller additions are unaffected and remain subject to
the current runtime's policy. Piglets do not add composition identity to
Session files. Bare Pig retains upstream ambient behavior.

Harness-format discovery belongs to a selected harness extension. That extension
may read `.claude`, `.github`, `.pi`, and `.pig` according to its own namespaced
configuration. Raw Piglet schema does not encode one extension's options.

### Packages and origins

Package dependencies are aliases:

```yaml
packages:
  review: npm:@acme/review@^1
```

Resource entries carry one ordered typed-string origin vocabulary:

```yaml
extensions:
  - name: review-tools
    origins:
      - package:review
      - local:./extensions/review-tools
```

The shared source parser owns local/Git/npm/HTTP/catalog/contributed identity,
anchoring, ordering, and materialization. Package order never implies activation.

### Derivation

All derivation behavior is one object:

```yaml
extends:
  source: npm:@acme/base@^1
  version: "^1"
  allowWiden: false
  remove:
    skills: [inherited]
```

Depth is eight. Each source resolves its own Piglet/workspace paths before
merge. Whole fields replace; named collections replace/add/remove by identity;
order is deterministic; absent removals and unsafe widening fail. Release/build/
extends metadata does not inherit into the effective Piglet.

### MCP

Raw Piglet has no MCP endpoint, command, args, headers, credentials, or adapter
config path. MCP clients and portable definitions are Resources selected through
extensions and Packages. A deployment platform can supply MCP endpoints,
authorization, and credentials at runtime. Tool names may be scoped through the
owning adapter extension.

### Product bindings

A product-specific Piglet uses the same schema as every other Piglet. It can
select product extensions, adapters, skills, tools, discovery, and environment
requirements. The product injects remote identity, endpoints, credentials,
trace context, and deployment policy. No second Piglet schema exists.

Remote-agent creation and update belong to the consuming platform. Piglet publication creates reusable release state and no remote-agent state.

## Component realization and materialization

Executable components record independent facts:

| Axis | Values | Meaning |
|---|---|---|
| realization | fused, subprocess, external | how the component executes |
| materialization | binary, release, `agentEnv`, external | where required bytes/runtime come from |

Skills/prompts/themes remain exact Resource graph entries without fake process
realization. Subprocess use does not create another Binary kind.

## Records and inventory

Piglet records use one strict current unversioned shape with two current kinds and one reserved kind:

```text
piglet-resolution
piglet-binary
`piglet-image` (reserved; no Image producer exists)
```

Records bind exact source/effective/graph, Resource/component plan, target,
builder/toolchain, environment/external requirements, artifact digest, and
verification. They verify artifacts and inventory; they never choose what a
shell executes.

Inventory reports source and Binary facets by target/version, including active, stale, missing, unverified, or tampered state. Image inventory is reserved until an Image producer exists. Inventory never discovers artifacts from PATH.

## Build surface

```text
pig piglet build <name> --format script --out <path|->
pig piglet build <name> --format binary --out <path>
```

Script output is a validated Host Piglet wrapper and accepts only source-script options. Binary builds use exact records and execute directly. Each current build emits one explicitly selected format.

## Planned (not in this release): Image and locked artifact builds

```text
pig piglet build <name> --format image --out <reference>
pig piglet build <name> --format binary|image --locked
pig piglet build <name> --format binary|image --record <path>
```

These flags are reserved in the CLI and currently fail without producing an artifact.

## Environment responsibility

`agentEnv` constrains the whole agent independent of carrier and component
realization:

- omission preserves host execution;
- presence requires Host Piglet, script, Binary, Image, and managed deployments to
  enter or verify the same environment before startup;
- only explicit local one-run `--unsafe-host` may bypass where permitted;
- code-execution sandbox remains separate.

Secrets are logical external requirements and never enter portable source
values, records, sessions, argv, logs, or image layers.

## State layout

Pig writes only Pig roots and never `.pi`/`~/.pi`:

```text
~/.pig/
  agent/                  # settings, sessions, npm, git, catalog
  piglets/               # editable Piglet source
  artifacts/piglets/     # managed Binary artifacts; Image artifacts are reserved
  receipts/piglets/      # Piglet records
  docs/
  state/<extension-id>/   # contributed extension/product state
```

Workspace state stays under `.pig/`. Directories are created only when needed.
Branch-only extra roots and Pig-managed bin launchers are absent. Explicitly
selected import extensions may read Pi state but never write it.

## Command grammar

| Verb | One meaning |
|---|---|
| `add` | validate and register one independent Piglet YAML source |
| `remove` | delete explicitly selected local source or Binary facets |
| `pull` | acquire one signed Binary from an explicit release index or GitHub release reference |
| `build` | emit a source script or produce a Binary artifact |
| `publish` | publish signed Piglet Binaries as one GitHub Release; never Agent state |

```text
pig --piglet <name|path>

pig piglet schema
pig piglet validate <name|path>
pig piglet list
pig piglet show <name>
pig piglet show --record <path>
pig piglet add <path|npm:ref|git:ref>
pig piglet pull <release-index-url|github:owner/repo@version>
pig piglet remove <name> --source|--binary|--all
pig piglet build ...
pig piglet publish <name> --to github --repo <owner/repo> --sign-key <key>
```

A contributed catalog ref requires an installed product resolver. Stock PiG has no `marketplace:` or `catalog:` Piglet source resolver.

### Planned (not in this release): registered-release management

| Verb | Planned meaning |
|---|---|
| `pull <name>` | acquire a registered release and artifact closure by installed Piglet name |
| `publish` | distribute reusable Piglet source through npm; never Agent state |
| `update` | update a registered remote source or pulled Binary with rollback |

```text
pig piglet pull <name>
pig piglet publish <name> --to npm
pig piglet update [<name>]
```

Source registration accepts npm or Git. Signed per-target Binaries publish to and pull from GitHub Releases. npm source publication, named pulls, and Piglet-specific updates remain planned. The workflow does not use pi.dev or a pi-in-go.dev upload API.

Remote runtime management belongs to the consuming platform. Stock PiG has no
product deployment command.

## Vocabulary rules

- Package makes Resources available; Piglet selects them.
- Script is a source entry point; Binary/Image are artifacts.
- Record verifies; direct path/reference executes.
- Release distributes; carrier executes.
- Environment constrains the whole agent.
- Status describes observed inventory and creates no entity.
- Products supply bindings without changing the portable Piglet schema.

Avoid “self-contained” unless the record proves no release/environment/external
requirements. Keep code, JSON, records, API, UI, and docs on this closed
vocabulary.
