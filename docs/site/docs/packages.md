# Packages

A package is a versioned PiG/Pi distribution envelope for resources: extensions,
skills, prompts, themes, hooks, MCP definitions, agent environments, and
compatible cross-tool agents. Install packages to add capabilities to PiG. Use
an independent [piglet](/docs/latest/piglets) to select and scope capabilities into one
named agent.

Return to the [docs router](/docs/latest) or read the short
[concept model](/docs/latest/concepts).

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

PiG-specific membership uses the additive `pig` block without changing Pi's
fields:

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
`pi` block and optional PiG Resource fields above. Validate without installing:

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

A Git subdirectory selector is `#subdirectory=<URL-escaped-relative-path>`. PiG
clones one repository checkout under the canonical Git root, resolves the
selected Package root inside it, rejects missing or symlink-escaping selections,
and keeps the checkout while another configured Package still uses a sibling
subdirectory.

A custom npm registry selector is
`?registry=<URL-escaped-absolute-HTTPS-URL>`. PiG gives each registry a distinct
managed root and passes the non-secret URL to npm, pnpm, or Bun. Registry URLs
with embedded credentials, query strings, or fragments are rejected. Configure
tokens and custom CAs through that package manager's normal `.npmrc`, environment,
or certificate mechanism; PiG never accepts or writes a registry token.

Products may add source schemes through PiG's install-resolver contract. For
example, a product-enabled PiG may accept a `marketplace:` source. Raw PiG does
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

PiG materializes managed sources once:

| Scope/source | Root |
|---|---|
| user npm | `$PIG_CODING_AGENT_DIR/npm` (default `~/.pig/agent/npm`) |
| user Git | `$PIG_CODING_AGENT_DIR/git` |
| downloaded catalog bundle | `$PIG_CODING_AGENT_DIR/catalog` |
| project npm | `<workspace>/.pig/npm` |
| project Git | `<workspace>/.pig/git` |
| local path | remains at its authored path |

Plugin and Package records may refer to the same materialized source root; PiG
does not duplicate it into separate caches.

## Share a Package

Publish the Package to npm or to a Git host. Other users install it from that source with `pig install npm:<name>` or `pig install git:<url>`. See [Install](#install).

Next up: a Package catalog on pi-in-go.dev that lists npm packages carrying the `pig-package` keyword. The catalog does not exist yet. Until it does, share the install source directly.

## Manage installed Packages

PiG retains upstream Pi's top-level Package verbs:

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

Bare update, `--self`, and the `self`/`pig` positional targets update PiG.
`--extensions` updates installed Packages only; `--all` updates Packages before
PiG. `--extension` names one Package and `--force` reinstalls the current PiG
release. Conflicting targets fail before either update path starts.

PiG's owned management surface uses full-word noun/verb commands such as
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
settings. It does not update the Package's `package.json`. An enabled missing
member still stops startup. A disabled missing member does not stop startup.
`pig package validate` remains strict for publication.

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

## Hand off a Piglet

To give a Piglet to someone else, distribute its source or a Piglet Binary that you built with `pig piglet build`. Use infrastructure you manage, such as a Git repository or a file share. The recipient installs a source with `pig piglet add`, or runs the Binary directly.

Publish signed Binaries as one GitHub Release with `pig piglet publish <name> --to github --repo <owner/repo> --sign-key <key>`. Publication is a dry run until `--yes` is present. The recipient installs a signed Binary with `pig piglet pull <release-index-url|github:owner/repo@version>`. See [Piglet Binaries](/docs/latest/piglet-binaries#publish-to-github-releases).

Source publication with `--to npm`, pull by installed Piglet name, and Piglet-specific updates remain planned. See [Piglets](/docs/latest/piglets#planned-not-in-this-release-source-publication-named-updates-and-image-artifacts).

## Packages and piglets

| Package | Piglet |
|---|---|
| distributes source/Resources | selects/scopes one independent named agent and may build a Binary artifact; Image is reserved |
| installed and updated as a unit | runs through Host or Binary carrier; Image output is reserved |
| never owns a Piglet | may reference zero or more Packages plus direct origins |
| preserves source provenance | records agent defaults and exact component realization/materialization |

Piglets remain independent from Packages. A Piglet release publishes signed Binary facets to GitHub Releases with `pig piglet publish --to github`. `pig piglet build --format image` is reserved and does not produce an Image.

## Authoring safety

Do not copy every resource from a running session into a new package implicitly.
A session may contain third-party resources whose licenses and provenance do not
permit rebundling. Package authoring should add explicit resources and preserve
their source/version information. Piglet creation from a session is safer
because it writes references rather than copying source.
