# Pig architecture

This page maps Pig's parity core, governed additions, extension runtime, package
and Piglet surfaces, local state, and product boundary. Read the
[ecosystem ontology](piglet-resource-ontology.md) first so entity, carrier,
component-realization, materialization, and environment axes stay separate.
Return to the [maintainer docs router](README.md) for focused references.

## Layer map

```text
pig binary
├── parity core
│   ├── CLI/TUI/session/agent loop
│   ├── providers and message conversion
│   ├── builtin tools
│   └── Pi-compatible package management
├── generic Pig additions
│   ├── Piglets: compose, scope, launch, build, inventory
│   ├── extension SDK, host, and runtime-cell support
│   └── embedded reference documentation
├── extension host
│   ├── subprocess wire
│   ├── isolated and packed runtime cells
│   └── neutral Piglet runtime: fused registrations, subprocess assets, component closure
├── piglet runtime
│   ├── schema/discovery/inventory
│   ├── agentEnv image/Dev Container/package-source resolution
│   ├── native Docker/Podman simple runtime + external full-standard adapter
│   ├── Pig runtime injection and effective permission policy
│   └── explicit YAML validation/registration and secure whole-process launch
├── PiG Standard (explicit Piglet source; never active in Stock Pig)
│   └── product identity and optional extension Resources
└── external product packages
    ├── authentication and provider adapters
    ├── catalog and publication transport
    ├── workflow, tracing, MCP, and remote-agent integration
    └── product runtime bindings
```

## Public package map

PiG keeps Go package boundaries close to the public Pi packages:

| Pi package | PiG package | Purpose |
|---|---|---|
| `packages/agent` | `agent` | Agent loop, messages, tools, and Session primitives |
| `packages/ai` | `ai` | Models, providers, authentication, and stream types |
| `packages/coding-agent` | `coding` and `cmd/pig` | Embedding API and command-line application |
| `packages/tui` | `tui` | Terminal UI components and renderer |

Private command-line implementation stays under `internal/codingagent`. PiG does
not copy TypeScript `src` directories or barrel modules because those are npm
package mechanics.

PiG does not currently implement Pi's experimental remote Session packages or
its standalone telemetry and evaluation packages. `PORT_MAP.md` defines the
exact package scope.

## User entity map

| Pole | Role |
|---|---|
| Resource | one extension, skill, prompt, theme, hook, MCP definition, cross-tool agent, or agent environment |
| Plugin | cross-harness capability product with Agent Plugins/vendor protocol metadata |
| Package | optionally group/distribute Pig/Pi resources; never contain piglets |
| Piglet | consume direct, Plugin, and/or Package resources; compose, launch, build, and ship an agent |
| Piglet release | pin source/component closure and group optional binary/image artifacts |
| Piglet Binary | target-native Pig executable for one immutable Piglet composition; components may be fused, subprocess, or external |
| Piglet Image | OCI artifact materializing the same Piglet/component plan and environment closure |

See [packages versus piglets](pig-packages-vs-piglets.md) and
[piglet design](pig-piglet-spec.md).

## Extension runtime

Normal third-party extensions use subprocess hosting. A Piglet
Binary is still Pig and may host the same subprocess extensions while additionally
fusing compatible Go factories. Piglet planning records realization
(`fused|subprocess|external`) and materialization
(`binary|release|agentEnv|external`) per component. These are execution and
delivery facts, not new authoring entities or binary kinds.

| Runtime responsibility | Correct home |
|---|---|
| Piglet build/component-plan orchestration | Piglet-owned artifact code |
| cell build/cache/planning | generic extension host/runtime-cell packages |
| subprocess component registration/materialization | neutral extension runtime plus registered Piglet release store |
| fused Go registration | neutral extension runtime, dormant in stock Pig unless selected by a Piglet plan |
| closure verification/pull remediation | shared Piglet run and direct-binary bootstrap path |
| product HTTP or remote-agent transport | external product extension |

For details read [extension runtime cells](extension-runtime-cells.md).

## Local and deployed roots

Pig separates writable product state from the agent resource/configuration
root. Local defaults nest them; deployed products may mount them separately:

```text
$PIG_HOME/                         # default ~/.pig; writable
├── piglets/                     # editable user piglet YAML
├── artifacts/piglets/           # content-addressed managed binary/image outputs
├── receipts/piglets/            # resolution/artifact records and locks
├── state/<extension-id>/         # namespaced additive capability state
└── docs/                         # managed embedded user manual

$PIG_CODING_AGENT_DIR/            # default $PIG_HOME/agent
├── settings.json
├── sessions/
├── npm/                          # upstream npm package materialization
├── git/                          # upstream Git package materialization
├── catalog/                      # downloaded catalog bundle roots
├── extensions/
├── skills/
├── prompts/
├── themes/
├── agents/
├── hooks/
├── mcp/
├── models.json
└── SYSTEM.md
```

Project-local resources mirror the agent root under the workspace:

```text
<workspace>/.pig/
├── settings.json
├── npm/
├── git/
├── piglets/
├── extensions/
├── skills/
├── prompts/
├── agents/
├── hooks/
├── mcp/
└── state/<extension-id>/
```

Materialize npm and Git sources once in their upstream-compatible directories.
Catalog-only archives use `catalog/`. Package and Plugin records may point to
the same materialized source root while retaining separate protocol identities;
Pig does not copy that root into parallel caches.

A managed deployment can mount an immutable release bundle and point
`PIG_PIGLET_PATH` at its Piglet. It keeps `PIG_HOME` and
`PIG_CODING_AGENT_DIR` writable. Piglet origins and the dependency lock refer
to immutable Package or Plugin roots. Sessions, settings, dynamic installs, and
caches stay in the writable agent tree. This uses existing configuration roles
and does not require one directory to be both immutable product input and
writable runtime state.

Piglet records do not live in source discovery directories. Cache and
materialization paths never become source or reproducibility identity.

## Boundary invariants

- Upstream-compatible behavior stays in parity core unless a numbered divergence
  says otherwise.
- Generic Pig-only mechanics live under `coding/`, `internal/`, and the public
  extension SDKs. Stock Pig activates no product Resource.
- PiG Standard Resources live under `piglets/standard/` and load only when the
  user explicitly selects that Piglet.
- Pig emits facts, plans, Piglet records, and generic artifacts; products add policy,
  authentication, transport, scanning, signing, and curation.
- Core command paths win. Products may contribute only unclaimed exact paths.
- A Piglet runtime and its records contain no literal secrets.
- Building a source entry script writes only the explicit output; Binary/Image builds never mutate shell configuration or PATH.
- Direct Piglet Binary/Image execution verifies component closure before model/session/tools; no `piglet run` indirection exists.
- The extension wire has no independent version. Wire changes land atomically across the running binary, staged SDKs, generated cells, validation, and conformance.
- Contract changes land atomically across code, schemas, clients, tests, and docs.
