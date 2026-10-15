# Piglet design: lean composition and direct outputs

This is the normative maintainer contract for Piglet source, validation, registration, invocation, build outputs, records, environments, and external product bindings. Read the [ecosystem ontology](piglet-resource-ontology.md) first. Package details are in [Package structure](pig-package-spec.md); extension hosting is in [runtime cells](extension-runtime-cells.md).

## Purpose

A Piglet turns explicit Pig Resources and policy into one named agent without changing upstream Package behavior. It is the only authored whole-agent entity. A Piglet must stay understandable enough to write/read directly while retaining advanced derivation, origin, discovery, environment, and secret controls.

## Closed source shape

```yaml
name: reviewer
description: Focused code-review agent

extends:
  source: npm:@acme/base-review@^1.0.0
  version: "^1.0.0"
  allowWiden: false
  remove:
    extensions: [secondary-linter]
    skills: [old-review]

packages:
  review: npm:@acme/review-resources@^1.0.0

extensions:
  - name: review-tools
    origins:
      - package:review
      - local:./extensions/review-tools
    tools: [review_diff, inspect_tests]

skills:
  - name: code-review
    origins: [package:review]

tools: [read, grep, find, ls]

model:
  provider: anthropic
  name: claude-sonnet-4
  thinking: high

systemPrompt:
  file: prompts/review.md

discovery:
  extensions: []
  skills: [workspace]

agentEnv:
  source: package:review
  pigRuntime:
    mode: inject
    version: latest
  policy:
    preset: standard

secrets:
  - name: source-token
    from:
      ref: vault:source-token

release:
  version: 1.2.0

build:
  targets: [darwin/arm64, linux/amd64]
  outputName: pig-reviewer
```

Allowed root fields are exactly:

```text
name description extends packages extensions skills tools model
systemPrompt discovery agentEnv secrets release build slots strip
```

The schema is closed: every field outside that root vocabulary fails without an alias. `pig piglet schema` prints it as JSON Schema.

## Packages and origins

`packages` maps an author alias to one typed source. Declaring it materializes no member and mutates no Package settings. Extension/skill `origins` are ordered typed source strings. The first exact successful origin wins and enters resolution identity.

Supported source families use the shared parser:

```text
package:<alias>
local:<relative-path>
npm:<package>
git:<repository>
http(s):<source>
<contributed-scheme>:<locator>
```

A contributed scheme works only when installed product code registers its resolver. Stock PiG does not provide a `marketplace:` or `catalog:` resolver.

Local Piglet-owned origins resolve from the declaring Piglet's directory. `agentEnv.devContainer` resolves from invocation `workspace:`. Absolute, escaping, and symlink-escaping paths fail.

## Tools

Root `tools` controls Pig built-in model tools. Extension `tools` controls only tools registered by that extension. Both are exact lists:

| Shape | Root | Extension |
|---|---|---|
| omitted | normal Pig built-ins | all extension tools |
| `[]` | no built-in tools | load extension, expose no tools |
| list | exact built-in ceiling | exact extension tool ceiling |

Effective tools are:

```text
runtime registration ∩ Piglet allowlists ∩ CLI narrowing ∩ platform authorization
```

CLI/platform inputs never silently widen a Piglet. Slash commands and shortcuts are not Piglet capability policy. A Piglet can only remove built-in slash commands, through `strip.commands`.

## Strip lists

`strip` disables built-ins (D92). It has five lists: `tools`, `commands` (with the leading slash; `/piglet` included), `extensions`, `apis` (model API implementations), and `features`. `internal/pigstrip/ids_generated.go` holds the valid IDs, and `make piglet-strip-ids` regenerates it. A list is in deny mode (the IDs it names are stripped) or in keep mode through `strip.keep.<list>` (every ID it does not name is stripped, including IDs a later PiG adds). One file cannot use both modes for one list.

Strip lists inherit through `extends`. `extends.remove.strip` re-enables IDs and requires `extends.allowWiden`. A child can narrow keep mode but cannot leave it. Validation refuses a strip list that removes every built-in tool together with `/quit`.

A Piglet Binary leaves the code of a stripped built-in extension, model API, or feature out of the executable. Binary records keep the strip table of the PiG that built them (`stripTable`) and the keep lists (`stripKeep`). `pig piglet build` and `pig piglet show` print a strip delta report when this PiG's table has new IDs since that record. The user guide is `site/docs/piglets.md`, section "Strip built-ins".

## Slots

`slots` names members that replace a part of PiG. `slots.frontend` is the only slot: a Go module whose root package exports `func Frontend() frontend.Frontend` (`extensions/sdk/frontend`). `pig piglet build --format binary` fuses it, and the member draws the interactive mode in place of the ANSI renderer (D91). A child inherits the slot, replaces it by naming it, or removes it with `extends.remove.slots`. Source invocation and Stock PiG fuse no frontend member. The user guide is `site/docs/piglet-binaries.md`, section "Frontend members".

## Discovery

Discovery controls ambient additions only:

```yaml
discovery:
  extensions: [workspace, user]
  skills: [workspace]
```

Allowed sources initially are `workspace` and `user`. Empty or omitted lists in an active Piglet mean no ambient Resources of that kind. Explicit Piglet entries always load; explicit caller additions remain subject to current runtime policy and artifact restrictions. Bare Pig without a Piglet keeps upstream Package/discovery behavior.

External-harness adapters belong to the harness extension and its own namespaced config, not Piglet schema.

## Derivation

`extends` owns source, version constraint, widening intent, and removals. Depth is eight with cycle detection and exact source digests. Every base/child resolves its own paths before merge. Whole fields replace; named collections preserve base order, replace by identity, append new identities, and remove only existing identities. Unsafe widening and dangling Package aliases fail. Release/build/ derivation metadata does not inherit into effective composition.

## Model and prompt

Omitted `model` means unconstrained consumer selection. Present model is a simple preference using provider/public name, context window, and thinking level. It is not a hard policy. `any`, `default`, capability, fallback, and policy fields are rejected until a concrete enforced requirement is approved.

`systemPrompt` accepts inline text or one Piglet-anchored file. Additional prompt Resources should be selected through Packages/extensions rather than a dormant `agentPrompts` array.

## MCP boundary

Piglet source contains no MCP endpoint, command, args, headers, credentials, or config path. MCP clients and portable definitions are Resources selected through extensions and Packages. A deployment can supply MCP bindings, authorization, and credentials at runtime. MCP tools are scoped through their owning adapter extension's `tools` list.

## Product bindings

A product uses the same Piglet schema as any other consumer. It can select product extensions, adapters, skills, tools, discovery, and environment requirements. Product URLs, remote identities, model bindings, MCP endpoints, credentials, trace context, and deployment policy stay outside portable Piglet YAML.

## Secrets

Secrets are logical declarations with one `env|file|ref` source. Consumers use typed secret bindings. Values never enter Piglet source metadata, records, sessions, logs, argv, image layers, or hashes. Raw Pig has no built-in secret store; contributed resolvers handle `ref` values. Missing or unauthorized required values fail before startup. `--unsafe-host` never bypasses secret requirements.

## Environments

`agentEnv` accepts exactly one of direct image, standard Dev Container path, or typed source resolving one environment. Omission preserves host execution. Presence constrains direct source invocation, generated script, and Piglet Binary. A Piglet Image or managed deployment must honor the same requirement. The whole PiG process enters or verifies it before startup; code-execution sandbox is separate.

## Sessions

Piglets do not change the upstream Session header or bind a transcript to a Piglet, carrier, component plan, or environment. Resume, clone, and fork load the selected conversation under the current process composition; current tool, secret, environment, and platform policy governs every new action. `/piglet` inspects the Piglet active in this process and writes nothing to the Session; a Piglet that strips it (`strip.commands: [/piglet]`) runs without it. A different Piglet is selected with another `pig --piglet` invocation, which may resume the same Session when the caller chooses it.

## YAML lifecycle

Piglet YAML is authored directly by a user, editor, or agent. The closed schema is the power-user surface; Pig applies no inferred directory, Session, or installed-Resource selection policy.

```text
pig piglet schema
pig piglet validate <name|path>
pig --piglet <name|path>
pig piglet add <path|npm:ref|git:ref>
```

`piglet add` validates one Piglet and its portable closure, then copies them without replacement into `~/.pig/piglets/`. An npm or Git source is materialized without changing Package settings and retains an adjacent origin record with its exact version or commit. The original source, shell state, Sessions, and artifact inventory remain unchanged. Editing means editing the YAML source directly.

A product may register a contributed Piglet source resolver. Catalog refs work only while that product resolver is installed.

### npm and Git Piglet registration

```text
pig piglet add npm:<package>
pig piglet add git:<repository>
```

The remote-source path materializes published Piglet source, validates its portable closure, and retains its origin in an adjacent `<name>.origin.json` record with the exact npm version/integrity or Git commit. It reads `pig.piglet` from `package.json`, or `piglet.yaml` at the source root when that field is absent. It rejects local Resource sources and credential-bearing Git URLs. `PIG_OFFLINE` or `PI_OFFLINE` forbids fetching remote sources.

## Invocation and outputs

Source invocation is direct:

```bash
pig --piglet reviewer
pig --piglet ./reviewer.yaml
```

Build outputs:

```text
pig piglet build reviewer --format script --out ~/.local/bin/reviewer
pig piglet build reviewer --format script --out -
pig piglet build reviewer --format binary --out ./pig-reviewer
```

On Unix a script is an executable POSIX wrapper. On Windows it is a two-line cmd.exe launcher (D69). The Unix form is:

```sh
#!/bin/sh
exec pig --piglet '/canonical/reviewer.yaml' "$@"
```

It is untracked, receives no record, and is never removed by Pig. A Binary executes directly and uses Piglet records. Each current build invocation requires exactly one `script|binary` format.

A Binary build of a Piglet that lists no extensions produces Stock PiG's own parts with that Piglet baked in. A Piglet that lists extensions that resolve to none fails with `piglet resolves to no extensions`. A Binary embeds the sources of each TypeScript or JavaScript extension and runs them with the installed Node (22.13 or newer), as Stock PiG does.

### Reserved: Image and locked artifact builds

```text
pig piglet build reviewer --format image --out registry/reviewer
pig piglet build reviewer --format binary|image --locked
pig piglet build reviewer --format binary|image --record <path>
```

`--format image`, `--locked`, and `--record` are reserved CLI shapes. They fail without writing output.

Set `build.extensionRealization: fused` when every selected extension must be compiled into the Piglet Binary. A Binary build then rejects every extension that is not a fuse-compatible Go factory. It does not fall back to a subprocess component. This requirement does not change source invocation.

## Records and inventory

Piglet records use one strict current unversioned shape with current kinds `piglet-resolution` and `piglet-binary`; `piglet-image` is reserved until an Image producer exists. They bind source/effective/graph, Resource/component closure, target, builder/toolchain, environment/external requirements, artifact digest, and verification. Records verify; they do not launch.

```text
pig piglet list [--json]
pig piglet show <name>
pig piglet show --record <path>
pig piglet tree [name] [--depth <n>] [--json]
pig piglet lineage <name|path|binary> [--source <name|path>] [--json]
pig piglet verify <binary> [--source <name|path>]
pig piglet remove <name> --source|--binary|--all
pig piglet prune [--keep <n>] [--max-size <size>] [--dry-run]
```

`prune` removes old built Binaries and keeps the newest `n` of each Piglet and target (default 2). Pulled installs stay. `verify` checks a Binary's signature, and with `--source` its recorded Piglet digests against that source, without running it. `pig piglet keygen <key-path>` creates an Ed25519 signing key pair, and `pig piglet trust [list|add|revoke|require]` manages trusted signer keys.

### Pull signed Binary releases

```text
pig piglet pull <release-index-url|github:owner/repo@version> [--target os/arch] [--version version] [--accept-signer key-id]
```

A direct pull downloads one signed release index and the Binary selected for the requested target. The default target is the current operating system and architecture. Distribution uses HTTPS, including GitHub Releases, and does not delegate to Pi services (D18).

The index is an Ed25519 DSSE envelope with payload type `application/vnd.pig.piglet-release+json`. Its unversioned payload contains `piglet`, release `version`, `pigVersion`, `sourceRef`, `signer`, and `binaries`. Each target entry contains `url`, `sha256`, and `size`. The checksum covers the entire downloaded file, including its Binary signature trailer.

Release URL policy failures do not quote rejected references. HTTP transport diagnostics omit private URL text while retaining cancellation and error identity for API callers.

Pull verifies the index signature, local trust policy, signer continuity, complete asset size and checksum, Binary signature, and matching Piglet, release, target, and PiG identities before publishing managed files. A receipt retains the signed index and Binary manifest. Receipt verification binds the artifact checksum and size to that signed index. The current pointer identifies the selected artifact, receipt, and signer. The first successful pull pins the signer. A later signer change requires an exact `--accept-signer` value and must still satisfy local trust policy.

Publication rechecks the signer under an operating-system lock shared by CLI processes. A busy store fails with a retry diagnostic. The lock is released when its handle closes or its process exits; its file remains under `receipts/piglets/`. Publication and pulled-release inventory reject symlinked managed path components. File operations use opened directory handles so replacing a parent path cannot redirect them outside the managed store. Host policy compares DNS names after IDNA normalization and removal of terminal DNS dots, before requesting indexes, assets, or redirect destinations.

### Published Piglet Binary releases

```text
pig piglet publish <name> --to github --repo <owner/repo> --sign-key <key> [--yes]
pig piglet pull <release-index-url|github:owner/repo@version>
```

`publish --to github` uploads signed per-target Piglet Binaries, `SHA256SUMS`, and a signed release index as one GitHub Release. `pull` verifies a signed release index and installs one target's Binary.

### Published Piglet source on npm

```text
pig piglet publish <name|path> --to npm [--yes] [--tag <dist-tag>] [--access public|restricted] [--otp <code>]
        [--npm-name <name>] [--package-map <alias>=npm:<name>@<range>]... [--binaries github:<owner/repo>|--no-binaries]
```

`publish --to npm` writes one npm package from an authored Piglet and runs `npm publish`. It is a dry run unless `--yes` is present, and it refuses an existing `name@version` (`npm view`). npm authenticates the author, including a one-time password, a passkey, and trusted publishing from CI (`--provenance` is added when `ACTIONS_ID_TOKEN_REQUEST_URL` is set). PiG never reads or stores an npm token.

Publication validates the Piglet as `piglet validate` does and refuses what `piglet add` refuses: `extends`, `agentEnv.devContainer`, file-based secrets, and local Resource origins. The generated package contains `piglet.yaml`, `package.json`, `README.md`, `LICENSE`, and the `systemPrompt.file` file. Its `piglet.yaml` is the authored YAML with every `local:` Package replaced by `npm:<name>@^<version>`, taken from that Package's `package.json` (or `--package-map <alias>=<ref>`), with `?registry=<url>` when that `package.json` sets an HTTPS `publishConfig.registry`. A local Package with no public name and version, or with a registry an `npm:` source cannot name, fails the publication. An `npm:` Package without its own registry that no published version satisfies is a warning in the dry run and fails `--yes`; a registry-qualified Package is not prechecked.

The generated `package.json` takes `name` from `--npm-name`, a `package.json` beside the Piglet, or the Piglet name, and `version` from `release.version`. It adds the keyword `pig-piglet`, so catalogs find the package by npm keyword search, and the block `"pig": {"piglet": "piglet.yaml", "binaries": {"ref": "github:<owner>/<repo>[/<name>]@<version>", "signer": "<key id>"}}`. `pig.piglet` is the path `piglet add` reads. `pig.binaries` is present when the version has a signed GitHub Binary release: `--binaries github:<owner/repo>` reads the signed release index, and otherwise a release this machine published with `publish --to github --yes` is recorded in `receipts/piglet-publications/`. `license`, `author`, `repository`, `homepage`, `bugs`, `funding`, and `publishConfig` come from a `package.json` beside the Piglet; scripts and dependencies do not. `pig piglet add npm:<name>` accepts exactly what publication produces.

### Reserved: pull by name

`pig piglet pull <name>` does not exist. `pig piglet pull` takes an explicit release index URL or GitHub release reference. `pig piglet update <name>` updates an installed signed GitHub Binary release within its signed namespace.

Remote agent creation and update belong to the consuming platform.

## State

Editable source lives in `~/.pig/piglets/`. Managed Binary outputs and records live under `~/.pig/artifacts/piglets/` and `~/.pig/receipts/piglets/`. npm source publications are recorded under `~/.pig/receipts/piglet-publications/`. A signed Binary records a passing startup check in `~/.pig/state/piglet-verify/verified.json`, keyed by its resolved path, file identity, signature block, and trust policy, so a later start with no change skips the whole-file hash. Image artifacts are reserved. Explicit scripts live only at user-selected paths. Pig writes no `.pi` state; managed Piglet state stays within those Piglet roots.

## Current implementation boundary

The lean closed source schema, typed Package/origin vocabulary, derivation object, strip lists, the frontend slot, direct source invocation, Piglet Binary producer, remote npm/Git registration, GitHub Binary publication, npm source publication, signed-index pull, and `pig piglet update <name>` are implemented. Pull by installed Piglet name, locked records, and Piglet Image production do not exist.
