# Piglets

A Piglet defines one named agent application. It selects and scopes Resources, tools, prompts, model preferences, discovery, secrets, and environment requirements.

A Piglet is configuration source. It is not a Package and it is not a separate fork of PiG.

## Run a Piglet

Run registered Piglet source by name:

```bash
pig --piglet research
```

Run a Piglet directly from a file:

```bash
pig --piglet ./agents/research.yaml
```

You can also select source with an environment variable:

```bash
PIG_PIGLET_PATH=./agents/research.yaml pig
PIG_PIGLET_NAME=research pig
```

A bare `pig` invocation does not select PiG Standard or another Piglet automatically.

## Piglets are agent applications

Treat a Piglet as the application boundary for one agent experience. It answers these questions:

- Which capabilities belong to this agent?
- Which tools can the model use?
- Which extensions and skills load?
- Can ambient discovery add more Resources?
- Which system prompt applies?
- Which model does the application prefer?
- Which secrets does it require?
- Which environment must contain the whole process?
- How must its extensions run in a Binary?

This boundary lets a team define several agent applications without creating several PiG forks.

## Example

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
      - local:./extensions/web-access
    tools: [web_search, web_fetch]

skills:
  - name: source-review
    origins: [local:./skills/source-review]

tools: [read, write, bash, edit, grep, find, ls]

discovery:
  extensions: []
  skills: [workspace]

build:
  targets: [linux/amd64, darwin/arm64]
  outputName: pig-research
```

## What a Piglet controls

| Facet | Meaning |
|---|---|
| `packages` | Name Package sources that selected Resources can use. |
| `extensions` | Select executable capabilities and limit their model tools. |
| `skills` | Select reusable task instructions. |
| `tools` | Set the built-in model-tool ceiling. |
| `model` | Set a provider, model, context, or thinking preference. |
| `systemPrompt` | Select one inline or file-based system prompt. |
| `discovery` | Permit or reject ambient workspace and user Resources. |
| `secrets` | Declare logical secret requirements without storing values. |
| `agentEnv` | Require an image, Dev Container, or typed environment source. |
| `slots` | Replace a part of PiG with a fused Go member. `frontend` is the only slot (D91); see [Piglet Binaries](piglet-binaries.md#frontend-members). |
| `strip` | Strip built-in tools, slash commands, built-in extensions, model APIs, and features; a Piglet Binary compiles most of them out (D92). |
| `release` | Set release identity. |
| `build` | Set portable Binary target, output, and realization requirements. |

## Model selection

Leave `model` out when any enabled model is acceptable. PiG then uses its normal explicit, current, and default model selection.

A present model is a preference. An explicit command-line model can override it under PiG's normal precedence rules.

Do not write `model: any` or `model: default`.

## Tool scope

Omit root `tools` to use PiG's normal built-in tools. Use an empty list to expose no built-in model tools:

```yaml
tools: []
```

Limit built-in tools with an exact list:

```yaml
tools: [read, grep, find]
```

Omit an extension's `tools` field to expose all tools registered by that extension. Use an empty list to load the extension but expose none of its model tools:

```yaml
extensions:
  - name: review-ui
    origins: [local:./extensions/review-ui]
    tools: []
```

Read-only role with extension tools:

```yaml
name: researcher
tools: [read]
extensions:
  - name: search
    origins: [local:./extensions/search]
    tools: [web_search]
```

Root `tools` restricts built-in tools only, so this role cannot write, edit, or run bash. Extension tools are listed per extension under `extensions[].tools`; here the active tools are exactly `read` and `web_search`.

Command-line and platform policy can narrow these lists. They do not silently widen a Piglet.

## Strip built-ins

A Piglet can disable PiG built-ins with `strip` (D92). Pi has no equivalent; a Stock PiG run without a Piglet strip list is unchanged.

`slots` and `strip` are one model. Every part of PiG a Piglet can change is a slot, and a Piglet does one of three things with it: it keeps PiG's own part (the default), replaces it with a fused member under `slots` (today only `frontend`, D91), or strips it under `strip`. Both inherit down the `extends` chain and both come off with `extends.remove`: `remove.slots` restores PiG's part, and `remove.strip` re-enables a stripped built-in.

```yaml
name: lean-reviewer
tools: [read, grep, find, ls, bash]
strip:
  tools: [bash]
  commands: [/share, /export]
  extensions: [mcp, codemode]
  features: [themes, experimental-server]
```

Each entry is a stable ID. The IDs come from a generated table (`internal/pigstrip/ids_generated.go`) that `make generate` rebuilds from the registrations, so an unknown or renamed ID fails when the Piglet resolves, and the error names the ID.

| List | IDs | Effect |
|---|---|---|
| `tools` | built-in tool names: `bash`, `edit`, `find`, `grep`, `ls`, `powershell`, `read`, `write` | The tool joins the `--exclude-tools` denylist. It leaves the tool registry, the active tools, and the system prompt. The effective built-in set is root `tools` minus `strip.tools`. An extension tool with the same name is excluded too. |
| `commands` | built-in slash commands with the leading slash, for example `/share`, and the active Piglet's `/piglet` | The command leaves the command registry and autocomplete; a stripped `/piglet` also leaves RPC `get_commands` and an extension's `getCommands`. Typed text such as `/share` goes to the model like any unknown slash command, so an extension can register the name. |
| `extensions` | built-in extensions: `codemode`, `llama.cpp`, `mcp`, `pig-login`, `tool-search` | The extension does not load: a settings entry or `-e builtin:<name>` is dropped silently, as Pi's `--no-mcp` drops MCP. A stripped `llama.cpp` starts no llama host. A stripped `mcp` makes no Radius MCP offer after a Radius `/login`. A Binary compiles each one out; `pig mcp` in a Binary without MCP reports the strip. |
| `apis` | `bedrock-converse-stream`, `google-vertex`, `mistral-conversations` | The API's models are offered nowhere: not by `--list-models`, `/model` and the model selector, model cycling, the `--models` scope, the startup default model, RPC `get_available_models`, or an extension's model registry. `--model` or `--provider` naming one, and building one, fail with the strip message: `Provider amazon-bedrock: API bedrock-converse-stream is stripped from this Piglet (strip.apis: bedrock-converse-stream)`. A Binary compiles the implementation out (for Bedrock, the whole AWS SDK). |
| `features` | `themes`, `skills`, `prompt-templates`, `experimental-server`, `node-extensions`, `extension-sdk-go`, `extension-sdk-rust`, `extension-sdk-python`, `syntax-highlight`, `word-dictionaries`, `mermaid`, `export-html`, `self-update`, `changelog`, `docs`, `piglet-builder` | `themes`, `skills`, and `prompt-templates` turn on Pi's `--no-themes`, `--no-skills`, and `--no-prompt-templates` and drop `--theme`, `--skill`, and `--prompt-template` paths; a Piglet cannot strip `skills` and also declare `skills` entries. `experimental-server` leaves `server` and `client` to the stable CLI. `node-extensions` and the `extension-sdk-*` IDs remove the runtime for TypeScript and JavaScript extensions and the SDKs for Go, Rust and Python source extensions; such an extension then fails to load with a strip message, and a Piglet cannot strip a runtime its own extensions need. `syntax-highlight` renders code blocks without token colors, `word-dictionaries` treats a run of CJK, Thai, Lao, Khmer or Burmese text as one word, `mermaid` leaves mermaid fences raw. `export-html`, `self-update`, `docs` and `piglet-builder` make HTML export, `pig update self`, `pig docs` and `pig piglet build`/`publish` report the strip; `docs` also drops the docs section of the system prompt. `changelog` removes `/changelog` and the startup What's New notice. |

A Piglet can't strip every tool and `/quit`. Stripping `/quit` alone is allowed, since Ctrl+D and a double Ctrl+C still exit, and so is stripping every built-in tool alone, which makes a chat-only Piglet. A strip list that names every built-in tool and `/quit` together, on its own or through its `extends` chain, fails when the Piglet resolves, so `pig piglet validate`, `pig piglet build` and `pig --piglet` refuse it:

```text
strip.tools and strip.commands: a Piglet can't strip every tool and /quit (strip.tools: bash, edit, find, grep, ls, powershell, read, write; strip.commands: /quit); keep at least one tool or /quit
```

### Keep mode

A list under `strip.keep` turns that list around: it names the built-ins that stay, and every other ID of that list is stripped, including the IDs a later PiG adds. Use keep mode for a minimal base that should stay minimal as PiG and Pi gain built-ins; use the deny lists above for a Piglet that should take up new built-ins.

```yaml
name: pig-core
strip:
  commands: [/share]              # deny mode: strip these
  keep:                           # keep mode: strip everything else in the list
    tools: [read, bash, edit, write]
    extensions: []                # no built-in extension, now or later
```

- One list is in deny mode or in keep mode in one Piglet file, never both: `strip.tools` and `strip.keep.tools` together fail.
- `strip.keep` has no `keep` of its own, and keep IDs come from the same table as deny IDs, so an unknown or renamed keep ID fails and the error names it (`strip.keep.extensions[0]: unknown extension ID ...`).
- `keep.<list>: []` keeps nothing from that list. A keep list with no value (`extensions:` alone) fails instead of keeping everything: write `[]`.
- PiG expands keep mode when the Piglet resolves, against the strip table of the PiG that runs it: the stripped IDs are the list's IDs minus the kept ones. Everything after that (the runtime strip, a Binary's build tags, the record, `pig piglet show`) works on the expanded list, and the floor below holds for it: `keep.tools: []` with `commands: [/quit]` fails.
- `strip.keep.tools` removes the other tools from the registry and the prompt. The root `tools:` allow-list only narrows the active set.

Strip lists merge down the `extends` chain list by list:

| Base | Child | Result | Widening? |
|---|---|---|---|
| deny `D` | `strip.<list>: [x]` | deny `D` and `x` | no |
| keep `K` | `strip.<list>: [x]` | keep `K` without `x` | no |
| keep `K` | `keep.<list>: K2` | keep `K2`; naming an ID outside `K` fails and points to `extends.remove.strip` | no |
| deny `D` | `keep.<list>: K2` | keep `K2`; naming an ID in `D` fails the same way | no |
| keep `K` | `extends.remove.strip.<list>: [x]` | keep `K` and `x` | yes: needs `extends.allowWiden: true` |
| keep `K` | nothing for the list | keep `K`; IDs a later PiG adds stay stripped down the whole chain | no |

A child can't return a keep-mode list to deny mode, because that would let in every built-in a later PiG adds. `extends.remove.strip.keep.<list>` fails and names the list; re-enable single IDs with `extends.remove.strip.<list>` instead.

### Inheritance and `pig piglet show`

Deny lists union down the `extends` chain. A child adds entries with its own `strip`. To re-enable an inherited entry, in either mode, name it under `extends.remove.strip` and set `extends.allowWiden: true`; without `allowWiden`, resolution fails with a capability widening error.

```yaml
name: reviewer-with-shell
extends:
  source: local:./lean-reviewer.yaml
  allowWiden: true
  remove:
    strip:
      tools: [bash]
```

`pig piglet show` lists the changed slots in one `Slots` section, each named by its manifest path. A keep-mode list shows its kept IDs above the expanded rows:

```text
Slots:
  frontend  replaced(/work/pig-tern/frontend)
  extensions  keep([])
  tools.bash  stripped(runtime)
  commands./share  stripped(runtime)
  extensions.codemode  stripped(binary)
  extensions.llama.cpp  stripped(binary)
  extensions.mcp  stripped(binary)
  extensions.pig-login  stripped(binary)
  extensions.tool-search  stripped(binary)
```

Each stripped ID shows the disposition its Piglet Binary gives it: `stripped(binary)` when the Binary does not link the built-in at all, `stripped(runtime)` when it stays compiled in and is disabled at startup. Tools, commands, `themes`, `skills` and `prompt-templates` are runtime; every other ID is binary. When Stock PiG runs the Piglet, it disables every entry at runtime with the same message or fallback, except that the CLI subcommands (`pig docs`, `pig piglet build`, `pig update`, `--export`) are only removed in the Binary.

`strip.tools` names exact built-in tool IDs. It takes no `*` patterns: MCP tool names depend on the servers a user configures and cannot be checked when the Piglet resolves. To drop MCP tools, strip `extensions: [mcp]`.

### Strip delta report

When PiG has built a Binary of the Piglet before, `pig piglet build` and `pig piglet show` compare the strip table of the running PiG with the one that Binary was built against (its record keeps it) and print what changed:

```text
Strip table: PiG 0.4.2 → 0.4.3 adds 3 built-in IDs
  left out (keep mode):     extensions.memory, features.voice
  now included (deny mode): commands./plan
```

"Left out" lists the new IDs a keep-mode list strips; "now included" lists the new IDs a deny-mode list leaves enabled. With no new IDs the report is one line, `Strip table: PiG 0.4.2 → 0.4.3 adds no built-in IDs`. It prints on every build, before the build runs, and `--json` output carries it as `stripDelta`. `pig piglet show` without `--effective` reads the list modes of a child Piglet from the record, since the child's file alone does not state its base's keep mode.

## Discovery

Discovery controls ambient additions only:

```yaml
discovery:
  extensions: [workspace, user]
  skills: [workspace]
```

Explicit entries always load when their origins resolve. An entry whose origins do not resolve is an extension load error: PiG reports each one and exits with status 1, as it does for any extension that fails to load. Like an explicit `-e` path, a Piglet entry still loads with `-ne`, so fix or remove the entry to start. In an active Piglet, omitted or empty discovery lists mean no ambient Resources of that type. Bare Stock PiG keeps its normal discovery behavior.

Use explicit discovery policy to prevent installed or workspace Resources from changing an application unexpectedly.

## Origins

Origins are typed strings. PiG tries them in declaration order and records the first exact successful source.

```text
package:<alias>
local:<relative-path>
npm:<package>
git:<repository>
http:<source>
https:<source>
<contributed-scheme>:<locator>
```

A local origin resolves from the Piglet that declares it. PiG rejects path escape and unsafe symlink resolution.

A Package dependency does not modify user or project Package settings. The Piglet activates only the selected member.

## Required secrets

Declare logical secret names and machine-local sources:

```yaml
secrets:
  - name: github-token
    from:
      env: GITHUB_TOKEN
```

A secret value does not enter portable Piglet source, records, sessions, logs, command arguments, or image layers.

Missing or unauthorized values fail before startup.

## Required agent environment

When `agentEnv` is absent, the Piglet runs on the compatible current host.

A Piglet can require an image:

```yaml
agentEnv:
  image: registry.example/dev@sha256:...
  pigRuntime:
    mode: image
  policy:
    preset: standard
```

It can instead select a workspace Dev Container or another typed source. The environment constrains the whole agent. It is separate from a code-execution sandbox.

PiG must enter or verify a required environment before startup. It must not silently fall back to the host.

## Validate and register

Validate source:

```bash
pig piglet validate ./agents/research.yaml
```

Register portable source by name:

```bash
pig piglet add ./agents/research.yaml
pig piglet validate research
pig --piglet research
```

Add a Piglet from npm or Git:

```bash
pig piglet add npm:@acme/review@^1.0.0
pig piglet add git:https://github.com/acme/review.git@v2.0.0
```

PiG fetches the package or repository, finds the Piglet file, validates it, and copies it into `~/.pig/piglets/`. It writes a `<name>.origin.json` file next to the Piglet that records the source, the resolved version, the npm integrity or Git commit, and the Piglet digest. `pig piglet list` shows the origin.

PiG reads the Piglet path from the `pig.piglet` field of the package's `package.json`. Without that field, it reads `piglet.yaml` at the package or repository root. The path must stay inside the package.

A remote Piglet must be portable. PiG refuses a Git URL that contains credentials. The command fails before it fetches anything when `PIG_OFFLINE` or `PI_OFFLINE` is set.

### Add from a monorepo

Select a repository subdirectory with the existing Git source selector and a full lowercase commit SHA (D18). The Piglet monorepo is `MichaelKinsy/pigpen`. Replace `<full-commit-sha>` with the reviewed commit:

```bash
pig piglet add 'git:https://github.com/MichaelKinsy/pigpen.git@<full-commit-sha>#subdirectory=piglets%2Fpig-with-batteries'
```

PiG reads `pig.piglet` or `piglet.yaml` inside that selected directory. It never falls back to the repository-root Piglet. The checkout must match the pinned commit and contain no modified or untracked files in the selected directory.

For this pinned subdirectory form, PiG copies declared relative local Packages, extensions, skills, and prompt files into `~/.pig/piglets/<name>.source/<commit>/`. It rewrites only their paths in the registered YAML. It preserves explicit empty tool scopes and executable permissions. Each local path must exist beneath the Piglet file's directory. Absolute paths, parent traversal, symlinks, Git metadata, non-regular files, and closures exceeding 4,096 entries or 32 MiB fail before registration. Keep a Resource's required build files inside its declared local directory. Use explicit YAML fields rather than aliases or merge keys for this form. `extends`, local agent environments, Dev Containers, and file-based secrets remain unsupported for remote registration.

The origin record includes the selected source, commit, original and registered Piglet digests, and each copied file's digest. Inventory checks those digests. Removing the source removes its recorded closure without touching sibling Piglets. Other remote source forms still reject local Resource origins.

A Piglet with relative local Package sources or Resource origins remains source-bound, and `piglet add` rejects it. Run that source directly unless all required relative content is registered with it.

## Inspect the active Piglet

An active Piglet adds a read-only command:

```text
/piglet
```

Bare Stock PiG does not register this command because no Piglet is active. A Piglet that strips it (`strip.commands: [/piglet]`) runs without it, so typed `/piglet` goes to the model like any unknown slash command, as in Pi.

Select a different Piglet in a separate `pig` invocation. Piglets do not bind existing session history to one composition. New work in a resumed session uses the current invocation's tools, policy, secrets, and environment.

## Build outputs

Create a source-bound script:

```bash
pig piglet build research --format script --out ~/.local/bin/research
```

On Windows the script is a cmd.exe batch file, so give it a `.cmd` name, for example `--out research.cmd` (D69).

Create a native Piglet Binary:

```bash
pig piglet build research --format binary --out ./pig-research
```

A script is a thin launcher. A Piglet Binary contains PiG and a fixed Piglet composition. See [Piglet Binaries](/docs/latest/piglet-binaries).

The native builder compiles PiG from source with the host Go toolchain. It uses the PiG checkout that contains the current directory, or `PIG_SOURCE_ROOT`. A release binary without a checkout fetches the source of exactly its own version with `go mod download github.com/MichaelKinsy/PiG@v<version>` and prints `fetching PiG v<version> source (cached after first build)`. `GOPROXY` and `GOSUMDB` verify the download, and later builds reuse the Go module cache and a staged copy under `~/.pig/cache/pig-source/`. The fetch needs no Git and no checkout. A development build has no published source for its version, so it reports `source-unavailable` and asks for a checkout or `PIG_SOURCE_ROOT` (D18).

Without a ready native builder, auto selection uses the built-in `container` builder; `--builder container` selects it explicitly. It runs the build in Podman or Docker: `PIG_CONTAINER_ENGINE=docker|podman` selects the engine, and otherwise Podman wins when both are on `PATH`. It pulls the digest-pinned public Go image of PiG's ci-go CI image, installs exactly the running PiG release in it with `go install`, and runs that release's native builder. The host needs no Go and no PiG source, and the target can be any `linux/<arch>` the engine runs; another architecture needs the engine's emulation. Go modules and build output are cached under `~/.pig/cache/container-go/`. The image contains only the Go toolchain, so a Piglet with packed or isolated Rust extensions needs a configured builder image that also has Cargo. A development build cannot install itself in the container, and the container builder does not sign (D18).

Publish signed per-target Binaries, `SHA256SUMS`, and a signed release index as one GitHub Release:

```bash
pig piglet publish research --to github --repo acme/research --sign-key ./research-signing.key
```

Publish is a dry run until you add `--yes`. For `pig-with-batteries` in `MichaelKinsy/pigpen`, pass `--tag-prefix pig-with-batteries/` and pull a published version with `github:MichaelKinsy/pigpen/pig-with-batteries@<version>`. The prefix must match the Piglet name. `pig piglet update pig-with-batteries` updates an installed GitHub Binary within its signed namespace, not the repository-wide latest release. See [Publish to GitHub Releases](/docs/latest/piglet-binaries#publish-to-github-releases).

## Publish source to npm

Publish a Piglet's source to npm with the `pig-piglet` keyword so that anyone can add it with `pig piglet add npm:<name>`:

```bash
pig piglet publish ./agents/reviewer.yaml --to npm
pig piglet publish ./agents/reviewer.yaml --to npm --yes --access public
```

Publish is a dry run until you add `--yes`. PiG validates the Piglet, writes the npm package (`piglet.yaml`, `package.json`, README, LICENSE, and the system prompt file), replaces each local Package by `npm:<name>@^<version>`, refuses a `name@version` that npm already has, and runs the `npm` on your `PATH`. npm authenticates you; PiG never handles an npm token. When this machine published the version's signed Binaries with `--to github`, or `--binaries github:<owner/repo>` names the release, `package.json` records it in `pig.binaries` so a catalog can show `pig piglet pull` beside `pig piglet add`. See [Publish a Package or Piglet](/docs/latest/publishing) for the options, signed Binaries, how pi-in-go.dev finds the package, and trusted publishing in CI. The catalog lists npm packages without review and does not accept uploads.

## Planned (not in this release): named pulls and Image artifacts

```bash
pig piglet pull <name>
pig piglet build <name> --format image --out <reference>
pig piglet build <name> --format binary|image --locked
pig piglet build <name> --format binary|image --record <path>
```

Pull by installed Piglet name and the reserved artifact flags above are not available in this release.

## PiG Standard

PiG Standard is an explicit Piglet in the PiG source tree:

```bash
pig --piglet piglets/standard/pig-standard.yaml
```

It selects the `piglogin` and `pigrunner` extension Resources through this same contract. `piglogin` owns identity and `/sprite`. `pigrunner` owns `/runner`, `/pig-runner`, and high-score state. Stock PiG has no private activation path for Standard.

PiG Standard also sets:

```yaml
build:
  extensionRealization: fused
```

This setting requires every selected extension to be compiled into its Piglet Binary. A non-fusible extension stops the build.

## Related documentation

- [PiG concepts](/docs/latest/concepts)
- [Packages](/docs/latest/packages)
- [Extensions](/docs/latest/extensions)
- [Piglet Binaries](/docs/latest/piglet-binaries)
- [Derivative harnesses](/docs/latest/derivative-harnesses)
