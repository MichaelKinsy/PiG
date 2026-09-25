# Plugin, package, piglet, and marketplace boundary

Stock PiG owns generic Package and resolver registration contracts. A consuming product owns contributed catalog hosting, authentication, transport, and policy. Piglet publishing through npm and GitHub Releases is planned and is not available in this release.

Return to the [maintainer docs router](README.md). Read the
[ecosystem ontology](piglet-resource-ontology.md) first, then
[packages versus piglets](pig-packages-vs-piglets.md) for the entity model and
[package structure](pig-package-spec.md) for source layout.

## Vocabulary

| Term | Owner | Purpose |
|---|---|---|
| Resource | Pig/harness | extension, skill, prompt, theme, hook, MCP definition, agent environment, or cross-tool agent declaration |
| Plugin | cross-harness protocol | Agent Plugins 1.0.0 package plus Claude, VS Code/Copilot, or compatible vendor overlays |
| Package | Pi/Pig package layer | versioned Pig/Pi distribution envelope for resources |
| Piglet | Pig | independent named agent composition and build source |
| Piglet Binary | Pig Piglet lifecycle | target-native Pig executable for one immutable Piglet composition; component plan may be fused/subprocess/external |
| Piglet Image | Pig Piglet lifecycle | OCI materialization of Piglet component/environment closure |
| Resolver | generic Pig contract | normalize a supported source/index format to a typed, materialized package root plus stable source identity |
| Piglet source resolver | installed product contribution | resolve one contributed scheme to a local Piglet YAML path; Stock PiG registers none |
| Publisher | planned stock Piglet command | publish source through npm or Git and signed per-target Binaries through GitHub Releases |
| Marketplace | product/discovery layer | multi-kind catalog, authentication, validation, curation, and distribution policy |

## Generic Pig boundary

Raw Pig owns:

- Pi-compatible local, git, npm, and HTTP package management;
- package manifests and resource discovery;
- `pig install` and package inventory;
- generic resolver registration for additional source schemes;
- generic Piglet record/artifact/component-closure validation.

Raw Pig does not own:

- product host configuration, authentication, or API endpoints;
- authenticated catalog search, download, or publication transport;
- product validation and approval policy;
- product-specific catalog types, CRDs, or UI.

## Extension source and Package manifests

Pig extensions use a conventional factory or exact standalone source form.
Runtime registration declares identity and capabilities. Cross-tool Package
metadata uses Agent Plugins `plugin.json`, vendor files such as
`.plugin/plugin.json` and `.claude-plugin/plugin.json`, `.pig-plugin/plugin.json`,
or the upstream-compatible `package.json` `pi` section. Package metadata selects
exact extension members but does not define their runtime capabilities.

A package may group:

| Resource | Typical path/manifest entry |
|---|---|
| extensions | `extensions/` or explicit `pi.extensions` entries |
| skills | `skills/` or explicit `pi.skills` entries |
| prompts | `prompts/` or explicit `pi.prompts` entries |
| themes | `themes/` or explicit `pi.themes` entries |
| cross-tool agents | `agents/` or plugin metadata |
| MCP definitions | `.mcp.json`, `mcp/`, plugin metadata, or governed Pig membership |
| hooks | `hooks/` through governed Pig membership |
| agent environments | standard Dev Container definition/closure through governed Pig membership |

Resources sit directly under the package root; do not add a nested `resources/`
hop. A Package does not carry a Piglet, Binary, or Image manifest; validation
rejects Piglet membership.

## Planned (not in this release): npm Piglet catalog and public pages

The planned pi-in-go.dev catalog will index npm packages carrying `pig-piglet` for Piglets and `pig-package` for PiG Packages. It will label community listings as unreviewed and apply a checked-in denylist. It will not define a `marketplace:` transport or accept uploads.

The planned public site uses flat catalog routes:

```text
/packages
/piglets
/piglets/<name>
```

`/piglets/<name>` will present source, dependencies, environment, verification, records, component closure, and Piglet Binary builds. Piglet Images remain reserved until an Image producer exists. Cross-harness Plugins remain separate catalog entities.

## Resolver contract

A contributed Piglet resolver may handle an additional source scheme after installed product code registers it. Stock PiG currently registers no Piglet source resolver, so `marketplace:` and `catalog:` Piglet refs fail clearly.

Package installation independently supports local paths, npm, Git, and HTTP. Plugin manifest discovery runs only after one of those sources has been materialized. Stock PiG does not read a marketplace index.

Requirements:

- core Package source forms win;
- duplicate contributed schemes fail deterministically;
- product authentication and API details stay inside the contributing product;
- a contributed Piglet resolver returns a local YAML path;
- Piglet acquisition rejects non-portable dependencies;
- resolution and ordinary listing do not initialize a model or Session.

## Publisher contract

Signed per-target Piglet Binaries publish through GitHub Releases with SHA256SUMS and a signed release index. The reusable workflow `docs/examples/piglet-release.yml` adds build provenance.

```text
pig piglet publish <name> --to github --repo <owner/repo> --sign-key <key> [--yes]
```

Publication defaults to a dry run unless `--yes` is present. PiG invokes `gh` so it never handles registry tokens, and it does not contact pi.dev or upload third-party bytes to pi-in-go.dev.

### Planned (not in this release): source publication

Piglet source will publish to npm with the `pig-piglet` keyword or use a Git ref.

```text
pig piglet publish <name> --to npm
```

Source publication will also default to a dry run and invoke `npm`, so PiG never handles the npm token.

A live remote agent is created or updated through the consuming platform. Piglet publication does not create or mutate remote-agent state.

## Product implementation boundary

A product extension can contribute catalog authentication and a Piglet source resolver. Product API URLs, policy resources, and deployment state remain outside Stock PiG. The stock publisher uses the GitHub transport directly, and the planned npm transport will too, rather than a product-contributed nested command.

## Implementation invariants

- core Package source parsing remains independent from contributed schemes;
- contributed Piglet resolvers are unavailable until installed code registers them;
- Piglet acquisition rejects non-portable dependencies and never routes Piglet entries through `pig install`;
- Package-member origins use the product-neutral source materializer and `coding/packagecontent`;
- product credentials, authentication, and API transport remain outside Stock PiG;
- Stock PiG reads no marketplace index and exposes no Piglet publisher in this release;
- provenance and license facts remain in Piglet records.

## Validation commands

Generic local validation remains:

```bash
pig install ./extension --validate-only --json
pig install --validate-only --set ./ext-a,./ext-b --json
```

Package manifests and Piglet YAML validate through their focused management
surfaces. A product can invoke the same generic validation against materialized
source, then apply product policy outside PiG.
