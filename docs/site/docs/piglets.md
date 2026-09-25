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

Command-line and platform policy can narrow these lists. They do not silently widen a Piglet.

## Discovery

Discovery controls ambient additions only:

```yaml
discovery:
  extensions: [workspace, user]
  skills: [workspace]
```

Explicit entries always load when their origins resolve. In an active Piglet, omitted or empty discovery lists mean no ambient Resources of that type. Bare Stock PiG keeps its normal discovery behavior.

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

A remote Piglet must be portable. PiG refuses a Piglet that uses local sources, and a Git URL that contains credentials. The command fails before it fetches anything when `PIG_OFFLINE` or `PI_OFFLINE` is set.

A Piglet with relative local Resource origins remains source-bound. Run that source directly unless all required relative content is registered with it.

## Inspect the active Piglet

An active Piglet adds a read-only command:

```text
/piglet
```

Bare Stock PiG does not register this command because no Piglet is active.

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

Publish signed per-target Binaries, `SHA256SUMS`, and a signed release index as one GitHub Release:

```bash
pig piglet publish research --to github --repo acme/research --sign-key ./research-signing.key
```

Publish is a dry run until you add `--yes`. See [Publish to GitHub Releases](/docs/latest/piglet-binaries#publish-to-github-releases).

## Planned (not in this release): source publication, named updates, and Image artifacts

Piglet source will publish through npm with the `pig-piglet` keyword or through a Git ref. Signed per-target Piglet Binaries already publish to GitHub Releases with `pig piglet publish --to github` and install from a direct signed-index URL or `github:` ref with [`pig piglet pull`](/docs/latest/piglet-binaries#pull-a-published-binary). The pi-in-go.dev catalog will index npm daily and label community listings as unreviewed; it will not accept uploads.

```bash
pig piglet publish <name> --to npm
pig piglet pull <name>
pig piglet update [<name>]
pig piglet build <name> --format image --out <reference>
pig piglet build <name> --format binary|image --locked
pig piglet build <name> --format binary|image --record <path>
```

The source publication and named update commands above and reserved artifact flags are not available in this release. `publish --to npm` will default to a dry run unless `--yes` is present, as `publish --to github` does. The planned `/piglets` catalog and `/piglets/<name>` detail page will show npm source, targets, signing, and provenance without using pi.dev data.

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
