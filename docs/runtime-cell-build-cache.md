# Runtime-cell build and cache

This page covers how Pig classifies extension sets, generates cell runners,
builds native artifacts, and keys caches. Start with the
[runtime-cell overview](extension-runtime-cells.md).

## Planner inputs

The planner consumes normalized extension configs/specs, not piglet/package
syntax directly. Relevant facts include:

| Input | Use |
|---|---|
| extension name/source/path | stable member identity and build input |
| runtime kind/language | select builder or isolate |
| entrypoint mode/package | determine whether a factory can be generated |
| isolation | `shared-ok` permits packing; other values isolate |
| SDK/protocol compatibility | ensure members can share a generated runner |
| quarantine state | force fission after a packed failure |
| content/source hash | invalidate changed artifacts |

Piglets and package settings resolve to concrete extension configs before
planning.

## Placement rules

A packed candidate must satisfy all current host conditions:

```text
runtime.kind == subprocess
runtime.language in {go, rust, python, node}
conventional factory source form
isolation == shared-ok
not quarantined
language-specific source layout supported
```

Candidates are grouped only with compatible same-language members. Anything
else remains isolated. A Node/TypeScript factory packs into one Node cell the
same way (N8): its subprocess launcher script runs `cell.mjs`, which loads
every member's entry through the same type-stripping loader an isolated Node
extension uses, in config order. A standalone Node script (an exact
executable) is never packed.

A selected Go factory package may use helper and `internal` packages from its
containing module or workspace. Pig retains that module or workspace as compiler
closure. A module or workspace root containing multiple factory packages is
ambiguous. Select one exact factory package directory.

## Generated runners

| Language | Generated shape |
|---|---|
| Go | temporary module/workspace plus `main.go` calling SDK factories |
| Rust | temporary Cargo project/workspace plus generated `main.rs` |
| Python | generated runner importing SDK factories |
| Node | shell launcher execing `node` on `cell.mjs` plus a manifest (no generated source; each member's own TS/JS is read at start) |

Each packed member still receives its own socket and performs the ordinary
registration handshake. There is no multi-register handshake.

## Hashes

Validation planning and build artifacts use related but distinct hashes.

### Extension content identity

Includes the extension source/spec and relevant dependency files while excluding
transient/generated directories such as `.git`, `target`, `node_modules`,
`.venv`, build output, logs, and coverage.

### Packed artifact identity

Includes all inputs that can change generated runner behavior:

- runtime language and toolchain/runtime version;
- staged SDK source/version;
- protocol version;
- ordered member identities/content hashes;
- entrypoint/package/factory metadata;
- generated runner templates.

A cache hit skips compilation but runtime startup and registration still
validate the artifact.

A Go source compiler or type-check diagnostic is cached under the same packed artifact identity.
An unchanged source and staged SDK report the saved diagnostic without running `go build` again.
A source or SDK change selects a new identity and rebuilds.
Module resolution, network, toolchain, cancellation, filesystem, and other setup failures remain retryable.
Normal cache retention may remove an old failure entry.

## Cache layout

Representative current paths:

```text
<cacheRoot>/ext/<name>-<hash>               isolated source builds
<cacheRoot>/cells/go/<hash>/runner[.exe]    packed Go
<cacheRoot>/cells/rust/<hash>/runner[.exe]  packed Rust project/artifact
<cacheRoot>/cells/python/<hash>/runner.py   packed Python
<cacheRoot>/toolchains/<probe>.json         compiler/runtime version probe
<cacheRoot>/cells/node/<hash>/runner        packed Node (runtime copy + manifest.json, no compiled artifact)
```

Cache paths are rebuildable implementation details. They are not package
versions, Piglet source identity, or Piglet release records.

Toolchain version records are keyed by the resolved executable's file identity and selector state such as Go, rustup, and Python version files. An unchanged warm lookup does not spawn the tool. Replacing the executable or selector state re-probes it, and a failed compiled build invalidates the relevant record before the next attempt.

## Piglet component build

`pig piglet build` resolves the Piglet's component set and invokes the same
builders eagerly. Compatible Go factories may fuse; Go/Rust/Python/Node or
isolation-required components may be materialized as exact subprocess closure in
the binary, registered release, or required `agentEnv`.

The Piglet record stores exact source pins/content hashes, Pig/core and
toolchain identities, target, component realization/materialization plan,
artifact/closure digests, and verification. Reusing a cache is acceptable only
when its full identity matches that record.

## Validation

```bash
pig install ./extension --validate-only --json
pig install --validate-only --set ./ext-a,./ext-b --json
```

Individual validation exercises spec resolution, build, startup, protocol
handshake, registration, and declared contribution checks. Set validation also
exercises placement, grouping, generated runners, cross-extension collisions,
and fallback/fission behavior.

In automatic placement, a pack conflict may fall back to isolated cells with a
structured warning. A command that explicitly requires packing fails instead of
silently changing process placement. Piglet artifact planning records the final
realization rather than exposing a user-facing fusion tier.

## Diagnostics

Structured reports should expose facts, not policy:

| Field | Example |
|---|---|
| strategy | `isolated`, `packed-go`, `packed-rust`, `packed-python` |
| members | extension names in deterministic order |
| reason | planner/fission explanation |
| cache | hit or cold build plus duration |
| artifact | resolved executable/runner path |
| warning | code, member/target, message, remedy |

A consuming product may use these facts in validation and policy, but Pig does not embed
product policy in the planner.

## Cache lifecycle

Pig manages only `<config-root>/cache/ext`, `<config-root>/cache/cells`, and the bounded version records under `<config-root>/cache/toolchains`. It does not manage Sessions, credentials, trust, receipts, SpecOps state, Piglet artifacts, or Piglet Binaries.

Each published entry keeps immutable integrity data in `ready.json`. Pig writes
the last successful use to the separate atomic `usage.json` sidecar. A cache
hit, publication, process start, and unchanged reload count as successful use.
Inspection and dry-run do not update use.

Pig classifies each entry in this order:

1. active process lease;
2. current startup resolution;
3. active build lock;
4. inactive and retained;
5. inactive and expired;
6. incomplete or invalid.

The first three classes are hard roots. Age and size pressure cannot delete a
hard root. Process use holds a shared advisory lock. Garbage collection must
acquire the exclusive lock before it renames an entry to a same-filesystem
tombstone and removes it. Lock files remain in place. Process termination
releases the operating-system lock.

Automatic pruning removes obsolete SDK fingerprints, entries older than 30
days since successful use, and incomplete entries after the crash-safety grace.
Automatic pruning applies no size limit. It runs after successful extension
resolution at most once per day and outside the TUI input and render loops.

Use these commands for inspection and explicit pressure:

```bash
pig extensions cache stats [--json]
pig extensions cache prune [--retention <duration>] [--max-size <bytes>] [--dry-run] [--json]
```

Explicit size pressure removes inactive entries from oldest successful use to
newest. If hard roots exceed the limit, the JSON report sets
`limitSatisfied` to `false` and reports `protectedBytes`. Pig does not delete a
hard root to satisfy the requested limit.
