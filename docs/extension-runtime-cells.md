# Extension runtime cells

Pig hosts normal third-party extensions as subprocesses over the current subprocess wire. A
**runtime cell** is the host's scheduling/process unit: one cell may run one
extension or pack several compatible factories into one process.

Runtime cells are internal. Authors declare extensions; Piglets and Package
settings choose extension sets; Pig chooses cells. Read the
[ecosystem ontology](piglet-resource-ontology.md) before mapping these internal
process choices into Piglet artifacts.

Return to the [maintainer docs router](README.md).

## Read by task

| Task | Focused reference |
|---|---|
| placement, generated runners, build inputs, cache keys | [Build and cache](runtime-cell-build-cache.md) |
| atomic reload, quarantine/fission, reports, protocol boundary | [Reload and operations](runtime-cell-reload.md) |
| author an extension source | [Extension authoring](extension-authoring.md) |
| compare public APIs across SDKs/hosts | [Extension API parity](extension-api-parity.md) |
| understand Piglet component realization/materialization | [Ecosystem ontology](piglet-resource-ontology.md), then [Piglet design](pig-piglet-spec.md) |

## Invariants

- Normal dynamic extensions are subprocesses.
- Pig does not add WASM, embedded JavaScript, `plugin.Open`, or dynamic Go
  plugin runtimes.
- Every contained extension keeps its own registration socket and
  identity, even when several share one process.
- Packing is an optimization, never an author-facing grouping model.
- Incompatible or quarantined extensions run in isolated cells.
- Reload is atomic: a failed replacement does not remove a working registry.
- Stock Pig carries no Piglet-specific fused registrations or managed component
  closure; generic support is inert until a Piglet build/release populates it.

## Terms

| Term | Meaning |
|---|---|
| extension | one logical capability with runtime registration identity |
| cell | one supervised process hosting one or more extensions |
| isolated | one extension in one process |
| packed | compatible same-language factories in one process |
| release-materialized | prebuilt component stored in a managed Piglet release or `agentEnv` |
| fused | compatible Go factory linked into a Piglet Binary and registered in-process |
| quarantine/fission | after a packed-cell crash, split members back into isolated cells |

## Placement summary

| Language | Source/command isolated | Factory packing | Release/`agentEnv` prebuild | Fuse |
|---|---:|---:|---:|---:|
| Go | yes | yes | yes | yes, compatible factories only |
| Rust | yes | yes | yes | no |
| Python | yes | yes | yes with locked Python/dependencies | no |
| Node/TypeScript | yes | yes | yes with locked Node/dependencies | no |

Packing requires a conventional Go, Rust, Python, or Node factory. All exact
standalones (including a shebang Node script) stay isolated. A Node cell packs
without compiling anything: it caches one copy of the embedded Node runtime
plus a manifest naming each member's resolved entry, and reads every member's
TS/JS source fresh at process start through the same type-stripping loader an
isolated Node extension uses. Authors do not configure placement.

## Runtime and Piglet artifacts

The same component builder can support runtime cache and Piglet release
materialization:

```text
source extension -> first runtime load -> cached cell
source extension -> Piglet build      -> locked release/environment component
```

A matching prebuilt component can satisfy a Piglet Binary request without
rebuilding. Cache reuse is only an optimization; Piglet records preserve
exact source/core/toolchain/target/component identity.

Compatible Go factories may be fused as an in-process optimization. Fused and
subprocess paths must remain observationally equivalent to the current subprocess wire and are
governed by D31 plus extension conformance tests. The Piglet plan, not a binary
kind or user tier, selects the realization.

## Boundaries

Pig owns facts and runtime mechanics:

- conventional source resolution;
- placement decisions and reason strings;
- cell generation/build/cache;
- startup, registration, cancellation, and reload;
- structured validation/reload reports;
- release-materialized/fused component support.

Consuming products own policy and transport:

- source/catalog authentication;
- signature and vulnerability policy;
- publication approval;
- multi-tenant quotas;
- build-environment scheduling;
- artifact signing/provenance policy.

Piglets are Pig-owned composition/build declarations. Product controllers may
materialize piglets/resources and invoke Pig validation, but they do not
duplicate fields from the conventional source and runtime registration contract.

## Source and tests

| Area | Source/tests |
|---|---|
| planner | `coding/extension/host/subprocess/cell_plan.go` and tests |
| generated native cells | `coding/extension/host/runtimecell/` and tests |
| reload transaction/quarantine | `coding/extension/host/subprocess/reload_cells.go`, `packed_quarantine.go` |
| runtime report | `reload_report.go` and tests |
| protocol | `coding/extension/host/subprocess/protocol.go` |
| cross-SDK behavior | `tests/extension-conformance/` |
