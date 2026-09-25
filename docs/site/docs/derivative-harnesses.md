# Build a derivative harness

A derivative PiG harness is normally an agent application built from a Piglet and ordinary Resources. It should not require a fork of Stock PiG.

Use this model when a product, team, or workflow needs its own identity and capabilities.

## Architecture

```text
Stock PiG
   |
   +-- Package A -------- extensions and skills
   |
   +-- Package B -------- prompts and themes
   |
   +-- Piglet ----------- one agent application
          |
          +-- source execution
          |
          +-- Piglet Binary
```

Stock PiG owns generic execution. Packages distribute capabilities. The Piglet selects one application. The Piglet Binary is an optional delivery form.

## Why use a Piglet instead of a new harness

A new harness executable often duplicates:

- model and session behavior;
- extension discovery;
- configuration parsing;
- security controls;
- update behavior;
- release tooling;
- documentation;
- failure recovery.

The duplicate then drifts from its base project.

A Piglet keeps application choices in one explicit composition while reusing the verified PiG engine.

## What belongs in the application

Put application-specific behavior in ordinary Resources:

| Application need | Resource or field |
|---|---|
| Tools, commands, providers, and interactive behavior | Extension |
| Task procedures | Skill |
| Application instructions | `systemPrompt` or Prompt Resource |
| Visual identity | Login or theme extension Resource |
| Built-in tool policy | Piglet `tools` |
| Model preference | Piglet `model` |
| Ambient Resource policy | Piglet `discovery` |
| Runtime requirements | Piglet `agentEnv` and `secrets` |

Do not add these choices to `cmd/pig` or Stock PiG startup.

## What belongs in Stock PiG

Change Stock PiG only for a generic requirement such as:

- behavior required for Pi compatibility;
- a stable public extension capability;
- Package, Piglet, or Binary infrastructure;
- provider or session behavior used by all compositions;
- generic lifecycle, security, or verification support.

A product command, product login, private API, or application workflow does not belong in Stock PiG.

## Build the application

### 1. Create extensions

Create one focused Go factory for each executable capability:

```bash
pig extension init ./extensions/review --lang go
```

Validate it without installing it:

```bash
pig install ./extensions/review --validate-only --json
```

Use more than one extension when the capabilities have independent state, release, or failure ownership. Do not split one cohesive capability only to create more packages.

### 2. Create the Piglet

```yaml
name: review-agent
description: "Repository review application"

extensions:
  - name: review
    origins: [local:./extensions/review]

skills:
  - name: review-policy
    origins: [local:./skills/review-policy]

tools: [read, grep, find]

discovery:
  extensions: []
  skills: []

systemPrompt:
  file: ./prompts/review.md
```

Explicit empty discovery lists prevent user or workspace Resources from changing this application.

### 3. Run from source

```bash
pig piglet validate ./review-agent.yaml
pig --piglet ./review-agent.yaml
```

Use source execution during development. It provides normal extension validation, process isolation, and reload behavior.

### 4. Build a Binary

```bash
pig piglet build ./review-agent.yaml \
  --format binary \
  --out ./pig-review-agent
```

Run the resulting agent application directly:

```bash
./pig-review-agent
```

## Native derivative applications

Require all selected extensions to compile into the Binary:

```yaml
build:
  extensionRealization: fused
```

This requirement is appropriate when you need one native executable and every application extension is a reviewed Go factory.

The build fails if any selected extension cannot fuse. It does not silently produce a mixed application.

## Mixed derivative applications

Omit `extensionRealization` when the application intentionally uses another language, a native subprocess, or an external service.

The component plan then records each realization and materialization. The application remains one Piglet even when its implementation spans processes.

## Package the Resources

Use a Package when several applications or teams need the same Resources:

```text
review-resources/
├── package.json
├── extensions/
├── skills/
└── prompts/
```

A Package makes the Resources available. Each Piglet still selects the members it needs.

Do not put Piglet activation in Package installation. Installing a capability must not change which agent application starts.

## Application identity

An application can select an extension that sets its login or header identity. Stock PiG provides the generic host API but does not own the derivative artwork.

PiG Standard follows this rule. Its `piglogin` extension owns the PiG Standard identity and `/sprite` command. Its `pigrunner` extension owns `/runner`, `/pig-runner`, and Runner state.

## Failure boundaries

Choose realization with the failure model in mind.

### Subprocess

Use a subprocess when you need language portability or process isolation. PiG can detect transport loss, cancel pending calls, quarantine failed cells, and retain a previous working extension set during failed reload.

The current operation fails. PiG does not replay it automatically.

### Fused

Use fusion when you need one native executable and accept a shared process boundary. A fused extension has lower process overhead but requires stricter review because a panic, deadlock, or resource leak can affect the whole application.

### External

Use an external component when another service owns deployment, scaling, credentials, and availability. Keep its endpoint and secret values outside portable Piglet source.

## Avoid these patterns

Do not:

- fork PiG only to register application extensions;
- add a product switch to Stock PiG;
- make Package installation select a Piglet;
- hide application behavior in a launcher script;
- discover every installed Resource implicitly;
- call an external component “embedded”;
- claim a Binary is self-contained when it still has external requirements;
- replay interrupted tool calls automatically.

## PiG Standard as the reference composition

PiG Standard is the repository's derivative-harness example. It uses:

- an explicit Piglet;
- ordinary public extension contracts;
- application-owned identity;
- application-owned timer-driven Runner UI;
- no automatic Stock PiG activation;
- an all-fused Binary requirement.

Future PiG Standard capabilities must follow the same boundary. They cannot return as built-in Stock PiG commands.

## Related documentation

- [PiG concepts](/docs/latest/concepts)
- [Extensions](/docs/latest/extensions)
- [Packages](/docs/latest/packages)
- [Piglets](/docs/latest/piglets)
- [Piglet Binaries](/docs/latest/piglet-binaries)
