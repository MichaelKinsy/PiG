# Packages

A package is a versioned Pig/Pi distribution envelope for resources: extensions,
skills, prompts, themes, hooks, MCP definitions, agent environments, and
compatible cross-tool agents. Install packages to add capabilities to Pig. Use
an independent [piglet](piglets.md) to select and scope capabilities into one
named agent.

Return to the [docs router](README.md) or read the short
[concept model](concepts.md).

## What a package may carry

| Resource | Purpose |
|---|---|
| Extension | executable capability: tools, commands, hooks, providers, UI |
| Skill | task instructions |
| Prompt | reusable prompt/template |
| Theme | terminal appearance |
| Hook | event command metadata |
| MCP definition | external tool-server configuration |
| Agent environment | standard Dev Container definition and portable local closure |
| Cross-tool agent | compatible plugin roots may expose agent declarations |

A Package does not author Piglet, Piglet Binary, or Piglet Image manifests.

## Package source layout

Put owned resources directly under the package root:

```text
base-coding/
├── package.json
├── extensions/
├── skills/
├── prompts/
├── themes/
├── hooks/
├── mcp/
└── .devcontainer/
```

Do not add `resources/` between the package root and those directories. The
upstream-compatible `package.json` `pi` block may select explicit members:

```json
{
  "name": "@example/base-coding",
  "version": "1.0.0",
  "pi": {
    "extensions": ["extensions/*"],
    "skills": ["skills/*"],
    "prompts": ["prompts/*.md"],
    "themes": ["themes/*.json"]
  }
}
```

Pig-specific membership uses the additive `pig` block without changing Pi's
fields. As in Pi, a package with a `pi` block loads only what it declares: its
hooks, MCP servers, and agent environments come from the `pig` block, not from
conventional directories.

```json
{
  "pig": {
    "hooks": ["hooks/*.json"],
    "mcpServers": ["mcp/*.json"],
    "agentEnvironments": [".devcontainer/go/devcontainer.json"]
  }
}
```

## Author and validate

Author Package membership directly in ordinary `package.json`, using the standard
`pi` block and optional Pig Resource fields above. Validate without installing:

```bash
pig package validate ./base-coding
pig package validate ./base-coding --json
```

Validation rejects malformed manifests and JSON Resources, missing members,
duplicate public names, lexical/symlink escape, missing Compose services, and
absolute or escaping bind mounts.

Direct Package installation preserves upstream behavior: it records the Package
in settings and normal discovery exposes enabled members, defaulting to all
unless Package filters narrow them. Installation does not copy Piglets or
Agents, merge MCP configuration, create diagnostics, or generate entry points.
A Piglet Package dependency resolves through the same materializer without
mutating global/project settings; only explicit member selections activate.

## Install

```bash
pig install ./local-package
pig install git:https://github.com/example/package.git
pig install 'git:https://github.com/example/monorepo.git@v2#subdirectory=packages%2Freview'
pig install npm:@example/package
pig install 'npm:@example/private@1.2.3?registry=https%3A%2F%2Fnpm.example.com%2Fteam'
```

A Git subdirectory selector is `#subdirectory=<URL-escaped-relative-path>`. Pig
clones one repository checkout under the canonical Git root, resolves the
selected Package root inside it, rejects missing or symlink-escaping selections,
and keeps the checkout while another configured Package still uses a sibling
subdirectory.

A custom npm registry selector is
`?registry=<URL-escaped-absolute-HTTPS-URL>`. Pig gives each registry a distinct
managed root and passes the non-secret URL to npm, pnpm, or Bun. Registry URLs
with embedded credentials, query strings, or fragments are rejected. Configure
tokens and custom CAs through that package manager's normal `.npmrc`, environment,
or certificate mechanism; Pig never accepts or writes a registry token.

Products may add source schemes through Pig's install-resolver contract. For
example, a product-enabled PiG may accept a `marketplace:` source. Raw Pig does
not need to know that product's HTTP or authentication protocol.

## Validate without installing

```bash
pig install ./extension --validate-only --json
pig install --validate-only --set ./ext-a,./ext-b --json
```

Validation resolves, builds, starts, and registers the selected extension set
without changing package settings. Use it before publishing or before claiming
that a piglet's extensions work together.

## Scope

Installs are user-scoped by default. `--local` records a workspace dependency in
`.pig/settings.json`:

```bash
pig install ./workspace-extension --local
```

Pig materializes managed sources once:

| Scope/source | Root |
|---|---|
| user npm | `$PIG_CODING_AGENT_DIR/npm` (default `~/.pig/agent/npm`) |
| user Git | `$PIG_CODING_AGENT_DIR/git` |
| downloaded catalog bundle | `$PIG_CODING_AGENT_DIR/catalog` |
| project npm | `<workspace>/.pig/npm` |
| project Git | `<workspace>/.pig/git` |
| local path | remains at its authored path |

Plugin and Package records may refer to the same materialized source root; Pig
does not duplicate it into separate caches.

## Listing a PiG package

### Planned (not in this release): automatic npm catalog listing

The pi-in-go.dev Package catalog will index npm packages carrying the `pig-package` keyword. It will label community listings as unreviewed and apply a checked-in denylist. The upstream Pi Package section will be rebuilt from npm packages carrying `pi-package`; the indexer will not fetch pi.dev data.

The catalog indexer and listing page are not available in this release. Package installation from a known npm or Git source already works through `pig install`.

## Manage installed Packages

Pig retains upstream Pi's top-level Package verbs:

```bash
pig package list
pig update
pig update --self
pig update <source>
pig update --extension <source>
pig update --extensions
pig update --all
pig update --force
pig remove <source>
```

Bare update, `--self`, and the `self`/`pig` positional targets update Pig.
`--extensions` updates installed Packages only; `--all` updates Packages before
Pig. `--extension` names one Package and `--force` reinstalls the current Pig
release. Conflicting targets fail before either update path starts.

Pig's owned management surface uses full-word noun/verb commands such as
`pig package list`, while retaining upstream-compatible install, remove, and
update behavior. Bare `pig list` retains upstream-compatible Package listing.
Use `pig --help` as the authority for the binary you are running.

### Enable or disable one installed Resource

Use Package filters when you no longer want one Resource without removing the whole Package:

```bash
pig config
pig config --local
```

Use `pig config --local` to edit project-scoped overrides. The TUI updates
settings. It does not update the Package's `package.json`. At startup a
declared extension, skill, prompt, or theme that matches nothing is skipped, as
Pi skips it, and the rest of the Package loads; `pig config` lists it as
missing so you can disable it. `pig package validate` remains strict for
publication.

## Toolchains and first use

Installing materializes source. It does not necessarily build every extension
immediately. A source extension builds into a content-addressed runtime cell on
first use:

| Extension language | Host requirement on first source build |
|---|---|
| Go | Go toolchain |
| Rust | Cargo/Rust toolchain |
| Python | Python and dependencies |
| Node/TypeScript | Node runtime and dependencies |

The cache makes later matching loads fast, but the cache path is not a package
version or reproducibility identity.

## Planned (not in this release): published Piglet releases

The Piglet distribution workflow will publish source through npm or Git and signed per-target Piglet Binaries through GitHub Releases. It will not use a pi-in-go.dev upload API. The publish, pull, and Piglet-specific update commands are not available in this release.

To hand off a Piglet today, distribute its source or a locally built Piglet Binary through infrastructure you manage. See [Piglets](piglets.md#planned-not-in-this-release-remote-distribution-and-image-artifacts) for the planned command surface.

## Packages and piglets

| Package | Piglet |
|---|---|
| distributes source/Resources | selects/scopes one independent named agent and may build a Binary artifact; Image is reserved |
| installed and updated as a unit | runs through Host or Binary carrier; Image is planned |
| never owns a Piglet | may reference zero or more Packages plus direct origins |
| preserves source provenance | records agent defaults and exact component realization/materialization |

Piglets remain independent from Packages. A Piglet release publishes signed Binary facets to GitHub Releases with `pig piglet publish --to github`; Piglet Image publication remains reserved until an Image producer exists.

## Authoring safety

Do not copy every resource from a running session into a new package implicitly.
A session may contain third-party resources whose licenses and provenance do not
permit rebundling. Package authoring should add explicit resources and preserve
their source/version information. Piglet creation from a session is safer
because it writes references rather than copying source.
