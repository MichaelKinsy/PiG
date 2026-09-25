# Pig maintainer documentation

This directory contains PiG's maintainer references and public documentation site. Public user documentation is authored under `site/docs/` and rendered by the static application under `site/`. The concise agent and offline reference is authored separately under `internal/pigdocs/content/`, embedded in the binary, and synced to `~/.pig/docs`.

Start here, choose one row, and read that focused document completely. Do not
load every document for an unrelated task.

## Task router

| If you are changing or deciding... | Read first | Then read |
|---|---|---|
| repository writing, comment, automation, and initial-publication quality | [Repository quality standard](project/repository-quality.md) | `AGENTS.md`, then the owning package or document |
| stock versus PiG Standard composition boundary | [Architecture](pig-architecture.md) | [`piglets/standard/README.md`](../piglets/standard/README.md), then [Piglet design](pig-piglet-spec.md) |
| ecosystem entities, carriers, component realization/materialization, or compatibility vocabulary | [Ecosystem ontology](piglet-resource-ontology.md) | [Architecture](pig-architecture.md), then [Piglet design](pig-piglet-spec.md) |
| overall process/runtime boundaries | [Ecosystem ontology](piglet-resource-ontology.md) | [Architecture](pig-architecture.md), then [Runtime-cell overview](extension-runtime-cells.md) |
| packages versus piglets | [Ecosystem ontology](piglet-resource-ontology.md) | [Packages versus piglets](pig-packages-vs-piglets.md), then [Package structure](pig-package-spec.md) |
| piglet schema, CLI, artifacts, records, or component closure | [Ecosystem ontology](piglet-resource-ontology.md) | [Piglet design](pig-piglet-spec.md), then [Extension runtime cells](extension-runtime-cells.md) |
| package structure, manifests, discovery, or member identity | [Package structure](pig-package-spec.md) | [Marketplace boundary](pig-plugin-marketplace-spec.md) |
| package/install/marketplace vocabulary | [Marketplace boundary](pig-plugin-marketplace-spec.md) | [Package structure](pig-package-spec.md) |
| exact Pi behavior, TypeScript/JavaScript semantics, Go representation, interfaces, or performance proof | [TypeScript-to-Go porting](typescript-to-go-porting.md) | `AGENTS.md`, then the owning parity family and upstream source |
| extension source forms and discovery | [Extensions](extensions.md) | [Extension authoring](extension-authoring.md) |
| public extension host APIs or an SDK | [Extension API parity](extension-api-parity.md) | [Extension authoring](extension-authoring.md) |
| packed cells, placement, generated builds, or cache identity | [Runtime-cell overview](extension-runtime-cells.md) | [Build and cache](runtime-cell-build-cache.md) |
| reload transactions, quarantine/fission, or runtime reports | [Reload and operations](runtime-cell-reload.md) | [Runtime-cell overview](extension-runtime-cells.md) |
| release inventory, SBOMs, vulnerability evidence, licensing, signing, or provenance | [Supply-chain evidence](supply-chain.md) | [Releasing](project/RELEASING.md), then [Third-party notices](../THIRD_PARTY_NOTICES.md) |
| badges, OpenSSF Scorecard and Best Practices criteria, and version-pin sources | [Compliance evidence](project/compliance.md) | `make compliance`, then [Supply-chain evidence](supply-chain.md) |
| security boundaries, assets, threats, or mitigations | [Threat model](threat-model.md) | [Security policy](../SECURITY.md) |

## Information hierarchy

| Pole | Meaning | User-facing home |
|---|---|---|
| Resource | extension, skill, prompt, theme, hook, MCP definition, or agent environment | its focused resource guide |
| Plugin | cross-harness capability product | Agent Plugins package plus vendor catalog/manifest overlays |
| Package | Pig/Pi distribution envelope for resources | package docs and `pig package` |
| Piglet | independent named agent definition, selection/scoping, environment requirement, and portable build defaults | Piglet docs and `pig piglet` |
| Piglet release | exact source/component closure plus optional binary/image artifacts | Piglet release inventory/Marketplace |
| Piglet Binary | target-native Pig executable for one immutable Piglet; components may be fused, subprocess, or external | Piglet artifact facet |
| Piglet Image | OCI materialization of the Piglet, component plan, and environment closure | Piglet artifact facet |

The maintainer and user information architectures preserve these entities while
keeping carrier, component realization, materialization, environment, and status
as independent axes. Do not introduce peer executable entities or use process
topology as an artifact kind.

## Documentation rules

1. Keep one focused concept per file.
2. Put fields, commands, states, and compatibility matrices in tables.
3. Use prose for rationale, invariants, and failure boundaries.
4. Mark current and target behavior explicitly in design documents.
5. User docs describe shipped behavior only and change atomically with commands.
6. Link to direct neighbors and this router instead of repeating their content.
7. A removed embedded page must be pruned by `pig docs sync` so stale files do
   not survive in `~/.pig/docs`.
8. Define ontology before commands: Package distributes, Piglet selects,
   release materializes, carrier executes, component realization explains process.
9. Keep code/JSON/records/API/UI/prose vocabulary identical and gate stale terms.

## Source of truth

- Public user documentation and website: `site/docs/` and `site/`.
- Embedded binary and agent reference: `../internal/pigdocs/content/`.
- Upstream Pi behavior: `.upstream/current/` plus `PORT_MAP.md`.
- Intentional behavioral differences: `DIVERGENCES.md`.
- Pig-only additions: `docs/additive-features.md`.
- Extension protocol: `coding/extension/host/subprocess/protocol.go` and the
  conformance suite.
- Piglet/Package/artifact ontology: `piglet-resource-ontology.md`.
- Piglet runtime and artifact contract: `pig-piglet-spec.md`.
