# Piglets

A Piglet is one named whole-agent declaration. It selects/scopes Resources,
sets model/prompt/discovery defaults, and may require an agent environment.

Read [Concepts](concepts.md) first. The stable ontology is:

> Packages distribute available Resources. Piglets select and scope them.
> Piglet releases pin exact Resource closure and executable component plans.
> Host Piglet and Piglet Binary are current carriers for the same Piglet. Piglet Image is the reserved OCI carrier.

Binary and image are build outputs, not files you author or separate management
namespaces.

## Run a Piglet

```bash
pig --piglet research
PIG_PIGLET_NAME=research pig
PIG_PIGLET_PATH=./research.yaml pig
```

A bare `pig` uses ordinary builtins/settings/discovery. Piglets are the
Piglet-specific capability-scoping mechanism.

`pig --piglet` directly selects source semantics. It never searches PATH or
silently selects a built artifact. A source Piglet entering required `agentEnv`
remains the Host Piglet carrier. Piglet Binaries execute directly.

## What a Piglet controls

| Facet | Examples |
|---|---|
| Resources | extensions, skills, prompts, hooks, MCP definitions |
| capability scope | exact built-in and extension model-tool allowlists |
| model | provider/name/context/thinking preference |
| prompt | one system prompt |
| discovery | ambient workspace/user extensions and skills |
| environment | optional required image, Dev Container, or typed source |
| release/build | release SemVer and portable target/output defaults |

### Model can be unconstrained

Leave `model` out when any consumer-enabled model is acceptable. Explicit/current
selection wins, then consumer default. Do not write `model: any` or
`model: default`.

A present model is a preference under Pig's normal CLI-over-Piglet precedence.

## Piglet schema

```yaml
name: research
description: "Web research agent"

model:
  provider: openai
  name: gpt-5
  thinking: high

systemPrompt:
  file: ./prompts/research.md

packages:
  base: npm:@example/base-coding@1.0.0

extensions:
  - name: web-access
    origins:
      - package:base
      - npm:@example/web-access@^1
    tools: [web_search, web_fetch]

skills:
  - name: commit
    origins: [local:./skills/commit]

tools: [read, write, bash, edit, grep, find, ls]

discovery:
  extensions: []
  skills: [workspace]
```

Omitted root `tools` means Pig's normal built-ins; `tools: []` exposes none.
Omitted extension tools means all tools from that extension; an empty list
exposes none. CLI and platform policy may narrow these lists but never widen
them. Discovery controls ambient additions only. In an active Piglet, omitted
or empty discovery lists mean no ambient Resources; bare Pig keeps ordinary
upstream discovery.

A Piglet selects ordinary extensions. This includes an extension that calls
`SetLogin`. Packages make Resources available. Installing a Package does not
activate a Piglet.

Package dependencies materialize without changing user/project Package settings.
Only members selected by kind/name become active. Direct `pig install` keeps
upstream Package discovery behavior.

## Origins, Resource closure, and executable plan

Origins are typed strings tried in declaration order:

- `package:<alias>` selects a named member from a declared Package alias;
- `local:<relative-path>` selects a Piglet-owned local Resource;
- npm, Git, HTTP, catalog, and contributed schemes materialize a source and
  select the named Resource from it.

Piglet build resolution records exact source/version/content identity. The
Piglet artifact contract then assigns each executable component:

- realization: fused, subprocess, or external;
- materialization: binary, Piglet release, `agentEnv`, or external.

A Piglet Binary may use both fused and subprocess extensions because it remains
Pig and retains Pig's subprocess host. Subprocess use does not create
a different binary kind.

Set `build.extensionRealization: fused` to require native compilation of every
selected extension. The Binary build then fails if an extension is not a
fuse-compatible Go factory. It does not use a subprocess fallback. Source
invocation still uses the normal extension host.

Direct-binary startup verifies every non-fused component from its registered
release/environment closure before model/session/tools. It never uses an
arbitrary same-named PATH extension or silently pulls/substitutes content.

## Required agent environment

When `agentEnv` is absent, Piglet execution stays on the current host. When
present, exactly one source is selected:

```yaml
agentEnv:
  image: registry.example/dev@sha256:...
  pigRuntime:
    mode: image
  policy:
    preset: standard
```

or a workspace-anchored Dev Container:

```yaml
agentEnv:
  devContainer: workspace:.devcontainer/devcontainer.json
```

or typed `source`. Piglet-owned files use `piglet:`; Dev Container files use
`workspace:`. `--workspace <dir>` selects the machine-local workspace (default:
invocation directory). Absolute, escaping, symlink-escaping, and wrong-anchor
paths fail.

Secrets are declared by logical name and consumed by typed agent-environment
bindings; values never enter Piglet source or inspection output:

```yaml
secrets:
  - name: github-token
    from: {env: GITHUB_TOKEN}  # or owner-only absolute/~/ file, or resolver ref
agentEnv:
  image: dev:1
  secrets:
    - secretRef: github-token
      target: {env: GITHUB_TOKEN}
```

Missing environment variables, unsafe files, missing/denied contributed
resolvers, and undeclared references fail before model/session/tool startup.

Raw Piglet YAML contains no MCP command, URL, headers, credentials, or adapter
config path. Select an MCP client/definition Package or extension; a deployment platform
supplies platform MCP bindings and authorization externally.

The environment constrains the whole agent across current Host Piglet and Piglet Binary carriers and the planned Piglet Image and managed-deployment carriers. Only explicit local one-run `--unsafe-host` may bypass it.

## Commands

```bash
pig piglet list
pig piglet show <name>
pig piglet show --record <path>
pig piglet show <name> --effective --json
pig piglet validate <name|path>
pig piglet schema
pig piglet add <path|npm:<package>|git:<repository>>
pig piglet pull <release-index-url|github:owner/repo@version> [--target os/arch]
pig piglet publish <name|path> --to github --repo <owner/repo> --sign-key <key> [--targets os/arch,...] [--artifacts <dir>] [--yes]
pig piglet remove <name> [--source|--binary|--all]
pig piglet build <name> --format script --out <path|->
pig piglet build <name> --format binary --out <path>
```

Owned verbs use full words; `ls`, `rm`, and `check` are not aliases.

Binary builds report real phases and elapsed time on stderr (D18). A terminal shows a spinner and dim step text. CI and pipes receive plain phase lines. Add `--verbose` to stream toolchain output with member labels. Packed members share one compiler invocation; fused Go members compile with the final binary. Failures show the current phase, a diagnostic tail, and a hint. The final summary gives the binary path, size, and time after verification and record publication. `--json` keeps stdout as one JSON result. `pig build [--verbose]` uses the same display when building Stock PiG from its source checkout.

Piglet YAML is authored directly with a user editor or coding agent. `schema`,
`validate`, `show`, direct `pig --piglet <path>`, and `build` all use the same
closed source contract.

`piglet add` accepts a local Piglet path, an `npm:` package, or a `git:` repository. For a local path, it validates the source and copyable Piglet-relative prompt files, then copies them without replacement into `~/.pig/piglets/`. It does not infer Resources from a directory or installed state, rewrite source, or change Package settings, shell state, Sessions, or artifact inventory. A contributed catalog ref works only when installed product code registers its resolver; Stock PiG provides no `marketplace:` or `catalog:` Piglet resolver.

Add a Piglet from npm or Git:

```bash
pig piglet add npm:@acme/review@^1.0.0
pig piglet add git:https://github.com/acme/review.git@v2.0.0
```

PiG fetches the package or repository, finds the Piglet file, validates it, and copies it into `~/.pig/piglets/`. It writes a `<name>.origin.json` file next to the Piglet that records the source, the resolved version, the npm integrity or Git commit, and the Piglet digest. `pig piglet list` shows the origin.

PiG reads the Piglet path from the `pig.piglet` field of the package's `package.json`. Without that field, it reads `piglet.yaml` at the package or repository root. The path must stay inside the package.

A remote Piglet must be portable. PiG refuses a Piglet that uses local sources, and a Git URL that contains credentials. The command fails before it fetches anything when `PIG_OFFLINE` or `PI_OFFLINE` is set.

### Planned (not in this release): source publication, named updates, and Image artifacts

```text
pig piglet publish <name> --to npm
pig piglet pull <name>
pig piglet update [<name>]
pig piglet build <name> --format image --out <reference>
pig piglet build <name> --format binary|image --locked
pig piglet build <name> --format binary|image --record <path>
```

Source publication will use npm or Git, and `publish --to npm` will dry-run unless `--yes` is present. Signed per-target Piglet Binaries already publish to GitHub Releases with `publish --to github` and can be pulled by direct signed-index URL or `github:` ref. Pull by installed Piglet name is not implemented. The other commands above and the reserved Image, `--locked`, and `--record` build paths are not available in this release.

### In-session

```text
/piglet                  # inspect the Piglet active in this process; read-only
```

Piglets do not change the Session wire or bind a transcript to one composition.
Resume, clone, and fork use the current invocation's tools, environment, secrets,
and policy for new work. Select another Piglet in a separate Pig invocation;
that invocation may resume the same Session when you explicitly choose it.

## Piglet Binary

A Piglet Binary is a target-native Pig executable for one immutable Piglet composition. Its record captures component realization and materialization.

A Piglet Binary file does not automatically promise that every component is
in-process or contained in that one file. Its Piglet release says what is
fused, what runs as a subprocess, where required bytes/runtimes come from, and
what remains external.

A Piglet Binary is an agent executable, not a Piglet YAML editor. It supports
normal execution and its built-in Piglet. Use raw Pig to validate, register,
and build Piglet source.

## Sign and verify a Piglet Binary

Create an Ed25519 author key and sign during a native Binary build:

```text
pig piglet keygen ./author.key
pig piglet build <name> --format binary --out ./pig-<name> --sign-key ./author.key
```

The private file is mode 0600 and is never replaced. Publish `author.key.pub`, not the private key. The Binary embeds the public key and verifies the signed DSSE manifest and every executable byte before command dispatch.

Check the file without running it and manage local trust explicitly:

```text
pig verify ./pig-<name>
pig piglet verify ./pig-<name>
pig piglet trust add ./author.key.pub
pig piglet trust revoke ed25519:<key-id>
pig piglet trust require on
```

An unsigned Binary reports unsigned. It runs by default, but `trust require on` makes every Piglet Binary require a signature by a trusted, non-revoked key. A valid embedded-key signature proves continuity from that key, not the human identity of its holder.

Pull a published per-target Binary from a signed release index:

```text
pig piglet pull https://github.com/acme/reviewer/releases/download/v1.2.3/piglet-release.json
pig piglet pull github:acme/reviewer@1.2.3 --target linux/amd64
```

Pull verifies the index signature, complete asset checksum and size, Binary signature, and release identities before installing any file. The first pull pins the signer for that Piglet. A different signer is refused unless `--accept-signer ed25519:<key-id>` names the new index key exactly. Revoked keys and an enabled required-signature policy fail before installation. Pulled Binary facets and their signed receipts appear in `pig piglet list` and `show`.

Publish signed Binaries as one GitHub Release named `v<release.version>`:

```text
pig piglet publish reviewer --to github --repo acme/reviewer --sign-key ./reviewer-signing.key
pig piglet publish reviewer --to github --repo acme/reviewer --sign-key ./reviewer-signing.key --artifacts ./dist --yes
```

Publish is a dry run until `--yes` is present: it validates the Piglet, key, and targets, checks that the release does not exist, and prints the assets without building or uploading. With `--yes` it uploads one signed Binary per target named `pig-<name>-<os>-<arch>` (`.exe` for Windows), `SHA256SUMS`, and the signed `piglet-release.json` index. Targets come from `--targets`, else `build.targets`, else the host target. A target without a ready signing builder is listed with each builder's reason, and nothing is uploaded; the native builder builds only the host target, and container builders do not sign. `--artifacts <dir>` publishes prebuilt Binaries instead: each file must be signed by `--sign-key` and built from this Piglet source and `release.version`, with exactly one Binary per target. Publish runs `gh release view` and `gh release create` with the user's `gh` login and never handles a GitHub token. It refuses an existing release. `docs/examples/piglet-release.yml` in the PiG repository is a reusable GitHub Actions workflow that builds each target on a native runner, attests provenance, and publishes with `--artifacts`.

Sigstore keyless provenance is separate CI identity evidence. Verify it explicitly with `pig verify --provenance --repo <owner/name> --signer-workflow <workflow> <binary>`. This invokes `gh attestation verify` and may contact GitHub. Piglet Binary startup never performs that network check; its Ed25519 verification is offline.

### Planned (not in this release): Piglet Image

A Piglet Image will contain exact Pig, redistributable component/runtime closure, and environment. It will perform no first-run code/runtime installation. Network services, secret values, user configuration, and non-redistributable programs will remain explicit requirements.

## Version and records

- `release.version` is Piglet release SemVer;
- Piglet records use one strict current unversioned shape with resolution and binary kinds; the image kind is reserved;
- extension wire changes ship atomically with the running Pig binary, staged SDKs, generated cells, and validation rather than carrying an independent version.

Piglet records live outside source discovery and bind source/effective/graph,
Resource closure, executable component plan, target, builder, artifact/environment,
and verification identity. Runtime environment agreement is machine-local state, not publication
provenance.

## Where to go next

- [Concepts](concepts.md) for the complete entity/carrier/component model.
- [Packages](packages.md) for Resource distribution and activation.
- [Extensions](extensions.md) for subprocess and fused-capable components.
- [Commands](commands.md) for the exact shipped CLI.

## Replace a bundled extension

A Piglet Binary pins every embedded component: fused Go extensions and prebuilt Go or Rust cells. At startup it verifies the registered closure and exits if a component is missing or different, so a bundled extension cannot be swapped inside a built binary. To ship a fix, change the extension source and rebuild the binary with `pig piglet build`.

When two configured origins provide the same extension name, PiG loads neither and names both origins in the error, so no copy wins silently. During development, run Stock `pig` with the Piglet file or the extension source instead of the built binary: edit the source and run `/reload`. Compiled cells are cached under `~/.pig/cache/ext` by source digest, so a changed source is rebuilt on reload.
