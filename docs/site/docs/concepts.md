# PiG concepts

PiG separates execution, capability distribution, and agent composition. This separation keeps Stock PiG small and prevents each agent application from becoming a new harness fork.

The short model is:

> Packages distribute Resources. Piglets select and scope Resources. Piglet Binaries deliver a fixed Piglet as a native executable.

## Stock PiG

Stock PiG is the product-neutral coding agent. It provides:

- Pi-compatible agent behavior;
- built-in coding tools;
- the extension host;
- Package installation;
- Piglet loading and validation;
- Piglet Binary construction and verification;
- embedded offline documentation.

Stock PiG does not activate PiG Standard or another product composition. It has no built-in mascot catalogue, marketplace, onboarding workflow, or derivative-harness behavior.

## Resources

A Resource contributes one focused part of an agent application.

| Resource | Purpose |
|---|---|
| Extension | Add executable tools, commands, events, providers, or UI behavior. |
| Skill | Add task instructions that the agent loads when needed. |
| Prompt | Add a reusable prompt or template. |
| Theme | Change terminal appearance. |
| Hook | Describe an event command. |
| MCP definition | Describe an external tool service. |
| Agent environment | Define the environment required by the whole agent. |

A Resource does not activate itself.

## Packages distribute Resources

A Package is an optional distribution envelope. It can contain one or more Resources. Install a Package when you want to make its Resources available:

```bash
pig install ./my-package
pig install git:https://github.com/example/my-package.git
```

Package installation does not select a Piglet. A Package never owns or activates a Piglet.

See [Packages](/docs/latest/packages).

## Piglets define agent applications

A Piglet is one named whole-agent declaration. It can select:

- extensions and skills;
- built-in and extension tool scopes;
- a system prompt;
- a model preference;
- ambient discovery policy;
- secrets and environment requirements;
- Binary build requirements.

Run a Piglet from source:

```bash
pig --piglet ./agents/reviewer.yaml
```

A Piglet is the normal unit for an agent application. It keeps the application configuration explicit without creating a separate PiG fork or wrapper harness.

See [Piglets](/docs/latest/piglets).

## Piglet Binaries deliver fixed applications

A Piglet Binary is a target-native PiG executable for one fixed Piglet composition:

```bash
pig piglet build ./agents/reviewer.yaml \
  --format binary \
  --out ./pig-reviewer
```

A Binary is a delivery form of the same Piglet. It is not another configuration model.

Each executable component records two independent facts:

| Axis | Values | Meaning |
|---|---|---|
| Realization | fused, subprocess, external | How the component runs. |
| Materialization | binary, release, `agentEnv`, external | Where its bytes or runtime come from. |

See [Piglet Binaries](/docs/latest/piglet-binaries).

## PiG Standard

PiG Standard is an explicitly selected Piglet. It demonstrates how a derivative PiG experience can use public extension and Piglet contracts without adding product behavior to Stock PiG.

The Standard source is:

```text
piglets/standard/pig-standard.yaml
```

Its Binary requires every selected extension to be a fuse-compatible Go factory. The build fails if an extension would use a subprocess fallback. The current composition selects `piglogin` for identity and `pigrunner` for timer-driven Runner UI.

## Choose the correct unit

| Need | Use |
|---|---|
| Add one executable capability | Extension |
| Share several Resources | Package |
| Define one complete agent application | Piglet |
| Hand off one native executable | Piglet Binary |
| Change PiG core behavior | Stock PiG change, only when the behavior is generic or required for Pi parity |

## Why this prevents harness sprawl

A conventional derivative harness often combines configuration, extension loading, branding, deployment, and application behavior in a new executable. Each new executable then owns a separate update path and can drift from its base agent.

PiG keeps these responsibilities separate:

```text
Stock PiG
   |
   +-- Packages make Resources available
   |
   +-- Piglet selects one agent application
          |
          +-- run from source
          |
          +-- build as a Piglet Binary
```

This model gives each application a clear identity while keeping one compatible execution engine.
