# How Pig fits together

Pig is a small coding agent that you shape for the work in front of you. The
shortest accurate model is:

> Packages distribute available Resources. Piglets select and scope them.
> Piglet releases pin exact Resource closure and executable component plans.
> Carriers execute the same Piglet.

Return to the [docs router](README.md) at any time.

## Authored concepts

| Concept | What it does |
|---|---|
| Resource | contributes one extension, skill, prompt, hook, MCP definition, theme, compatible agent declaration, context input, or agent environment |
| Package | optionally distributes a versioned collection of Resources |
| Plugin | packages cross-harness capabilities using Agent Plugins/vendor formats |
| Piglet | defines one named agent by selecting/scoping Resources and declaring defaults/environment requirements |

Packages and Piglets are the two main management concepts. Packages never own
new Piglets.

## Package: distribute Resources

`pig install` adds a Package to settings, so normal upstream-compatible discovery
loads its enabled members:

```bash
pig install ./my-package
pig install git:https://github.com/example/my-package.git
```

A Piglet can also reference a Package without changing settings and activate
only explicitly selected members. See [Packages](packages.md).

## Piglet: shape and run an agent

A Piglet selects extensions and skills, scopes tools/commands, sets model and
prompt defaults, connects MCP servers, controls discovery, and may require an
agent environment:

```bash
pig --piglet research
```

The Piglet remains editable source and points at exact Resource origins instead
of copying them. See [Piglets](piglets.md).

## Piglet carriers and releases

A Piglet may have these execution carriers:

| Carrier | Meaning |
|---|---|
| Host Piglet | Pig resolves and runs Piglet source on the host or inside its required environment |
| Piglet Binary | target-native Pig executable built for one immutable Piglet composition |
| Piglet Image | OCI artifact materializing the Piglet and required runtime environment |

Binary and image are build outputs, not files you author or peer management
namespaces. Piglet source and several target artifacts may coexist in one
Piglet release.

## Component execution is not an artifact kind

Raw Pig normally hosts extensions as subprocesses. A Piglet Binary is still Pig
and can use the same subprocess host while additionally fusing compatible Go
factories.

Each component has two independent facts:

| Axis | Values | Meaning |
|---|---|---|
| realization | fused, subprocess, external | how it executes |
| materialization | binary, Piglet release, `agentEnv`, external | where its bytes/runtime come from |

A subprocess component does not make a different kind of Piglet Binary.
Package/Resource origin tracking and the Piglet release bind the exact component
version, digest, target/runtime, and location.

For example, one Piglet Binary may fuse a Go review extension, start a locked
Python analysis extension from its release/environment closure, and connect to an
external MCP service. Direct binary execution verifies that closure before model,
session, or tools start. It does not accept an arbitrary same-named extension
from `PATH` or ambient Package discovery.

## Required environment

`agentEnv` constrains the whole agent, independent of carrier and component
realization:

- no `agentEnv`: run on the current compatible host;
- required `agentEnv`: Host Piglet, Piglet Binary, Piglet Image, and managed deployments
  enter or verify it before startup;
- only explicit local one-run `--unsafe-host` may bypass it.

The code-execution sandbox is separate from the whole-agent environment.

## The full flow

```text
install/reference Resources
          |
          v
create/run Piglet
          |
          +---- keep using Host Piglet
          |
          v
build optional Binary/Image
          |
          v
publish one Piglet release with exact Resource closure and executable plan
```

Every stage after Resource acquisition is optional. You can use bare Pig, run a
Piglet indefinitely without building, or publish selected target artifacts.

## Packages versus Piglets

| Question | Package | Piglet |
|---|---|---|
| Job | distribute Resources | compose, launch, build, and ship an agent |
| Activation | settings/discovery for direct install | only selected/scoped Piglet members |
| Contains/references | owned Resources | direct/Plugin/Package origins plus model/prompt/MCP/environment/build defaults |
| Runs by itself | no | yes through Host, Binary, or Image carrier |
| Marketplace kind | Package | independent Piglet release |

## Version and compatibility

- Piglet schema uses top-level `version: 1`.
- Piglet release SemVer uses `release.version`.
- The first production Piglet record schema is version 1.
- The extension subprocess wire has one current unversioned shape. It changes
  atomically with Pig and the staged SDKs.
- Undeployed interfaces may change directly. Deployed external contracts need an
  explicit compatibility decision.

## Where to go next

- [Packages](packages.md) for installation and Package provenance.
- [Piglets](piglets.md) for composition, carriers, and environments.
- [Extensions](extensions.md) for capability authoring.
- [Skills](skills.md) for reusable instructions.
