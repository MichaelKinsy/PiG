# TypeScript-to-Go porting for Pig

Pig ports the observable behavior of the exact Pi release pinned by
`coding/pigversion/pigversion.go`; it does not transliterate TypeScript
syntax. Read the
upstream implementation, its callers, tests, and published declarations before
choosing a Go representation. If idiomatic Go and exact Pi behavior conflict,
preserve behavior unless `DIVERGENCES.md` records an approved exception.

This guide complements the async-specific rules in
[Extension authoring](extension-authoring.md) and the parity process in
`AGENTS.md`.

## Translation worksheet

Before editing Go, record the upstream contract that matters for the family.
Do not infer these properties from a declaration alone.

| TypeScript/JavaScript surface | Questions that must be answered | Typical Go representation |
|---|---|---|
| `async`, `Promise`, `.then` | Does the caller await it? What is ordered, concurrent, cancellable, or intentionally detached? Where do errors go? | blocking result/error, owned joined concurrency, or an explicitly owned background task |
| `AbortSignal` | Who owns cancellation? Can cancellation happen before and during work? | `context.Context` or a lifetime-owned cancellation channel |
| object/interface type | Is this data, a behavioral protocol, a union member, or only a compile-time shape? | struct for data; a small consumer-owned interface only for real substitutability |
| union/discriminant | Is the set closed? What happens for an unknown tag on the wire? | tagged struct or sealed interface plus exhaustive boundary decoding |
| optional property | Are omitted, `undefined`, `null`, empty, and zero observably distinct? | pointer, presence boolean, tagged option, or custom JSON type; not automatically `*T` |
| array/tuple | Are order, holes, mutation, or fixed arity observable? | slice or explicit struct; preserve order |
| object/`Map`/`Set` | Is insertion order observable? Are keys strings, identities, or values? | map only when order is irrelevant; otherwise pair slice plus lookup index |
| `string` indexing | Does upstream count UTF-16 code units, Unicode code points, bytes, terminal cells, or grapheme clusters? | choose bytes/runes/UTF-16/cell-width deliberately and test non-ASCII input |
| `number` | Is the value integral? What are overflow, precision, NaN, and infinity semantics? | a width-specific integer or float with checked conversion at the boundary |
| exception/rejection | Where is it caught, transformed, displayed, or allowed to terminate? | returned error on the same operation unless upstream handles it there |
| callback/closure | What state is captured? Is callback identity, `this`, or registration order observable? | closure or named function with explicit captured state and ownership |
| class/prototype | Is identity or inheritance observable, or is it only a data/behavior grouping? | struct plus methods and composition; do not manufacture inheritance |
| regex/path/URL/JSON | Does Node behavior differ from Go's standard library for the exercised input? | probe exact Pi, then select or wrap the Go primitive at the boundary |

JavaScript object-property and `Map` insertion order frequently reaches model
lists, registries, commands, and rendering. Go map iteration is not ordered.
Never add a secondary sort merely to make output deterministic when upstream
preserves insertion order; carry the order explicitly.

TypeScript structural compatibility also does not require a corresponding Go
interface. A published TypeScript `interface` may become a Go struct, tagged
union, callback type, concrete host object, or wire schema. The semantic
inventory records the consumer-visible shape; it does not prescribe the Go
mechanism.

## Interface policy

Interfaces are not banned. They are a dispatch and design mechanism, not a
performance defect by themselves.

Use an interface when a consumer has a real behavioral contract with multiple
implementations or needs a narrow injected collaborator. Define it at the
consumer when possible, keep it unexported unless it is itself a public
protocol, and include only methods that consumer calls. Constructors normally
return concrete types.

Prefer a concrete struct when:

- one implementation exists and no consumer requires substitution;
- the value is data rather than behavior;
- an interface would exist only to mirror a TypeScript declaration or to make a
  mock;
- a hot loop can use the concrete type without weakening a real boundary.

Large Pig interfaces need case-specific treatment:

- `coding/extension.API` and `coding/extension.UIContext` represent an upstream
  extension protocol. Splitting or replacing them may change the public API and
  must follow the semantic inventory, protocol, SDK, and conformance process.
- `tui.Component` is a one-method heterogeneous component contract.
  Replacing it with a closed struct union would make the renderer less
  extensible and is not justified without piglet evidence.
- provider, terminal, storage, and process boundaries may legitimately need
  substitutability. Keep consumer contracts narrow instead of imposing one
  repository-wide implementation interface.

An interface call is normally an indirect call with a small dispatch cost. The
larger performance risk is that an unresolved indirect call can prevent
inlining and escape analysis. Go can statically devirtualize calls when the
concrete type is known, and piglet-guided optimization can conditionally
devirtualize hot interface calls. Therefore:

1. do not replace interfaces based on folklore or a microbenchmark alone;
2. first piglet the representative production path;
3. inspect allocation and inlining effects, not only nanoseconds per dispatch;
4. change a parity-facing API only when behavior remains exact and the measured
   gain is material.

`iface` is a lint gate for unused or duplicate interface declarations, opaque
interface returns with a single concrete implementation, and unexported
interfaces leaked through exported APIs. It does not forbid useful interfaces. `ireturn` and `interfacebloat` are intentionally
not repository gates: Pig has legitimate factory/union/protocol interface
returns, and the upstream extension API is intentionally broad. Their findings
may be used as review inventories, not automatic refactoring instructions.

## Performance method

Correctness comes first. Optimize only a representative path with an independent
behavior oracle.

1. Establish the exact Pi output, wire effect, ordering, and error behavior.
2. Add a benchmark or use an existing one that represents the real workload.
3. Run it with `-benchmem` and multiple samples. Go 1.27 benchmarks should use
   `for b.Loop()` where practical so the loop body remains optimizable.
4. Use CPU and allocation piglets to identify the cost center:

   ```bash
   go test ./tui -run '^$' -bench BenchmarkRenderSteadyState \
     -benchmem -cpuprofile cpu.pprof -memprofile mem.pprof
   go tool pprof -top cpu.pprof
   go tool pprof -top -alloc_space mem.pprof
   ```

5. Use compiler diagnostics only for a named candidate, for example
   `go test -gcflags='all=-m=2'`; do not treat noisy whole-repository escape
   output as a performance test.
6. Mutate or revert the proposed optimization and use `benchstat` or equivalent
   repeated measurements to prove the benchmark can detect it.
7. Re-run byte-faithful parity and TUI path tests. A faster renderer with
   different bytes, width, cursor placement, or event ordering is a regression.

For the distributed CLI, a representative CPU piglet is required before
committing `default.pgo`. Official Go guidance warns that microbenchmarks are
usually poor PGO inputs. Pig's parity runtime ratios protect startup/user-path
performance; package benchmarks locate Go-specific CPU and allocation costs.
Neither substitutes for the other.

## No grandfathered port debt

Treat old comments, version annotations, phase labels, placeholders, manual API
lists, compatibility readers, test-only implementations, and divergence records
as hypotheses to verify, not architecture. For the exact current pin:

1. read the current upstream source and callers;
2. locate the current Pig production caller, not only declarations/tests;
3. probe behavior and applicable resource/performance consequences;
4. retain with current rationale/evidence, replace with generated closure,
   reclassify as additive/translation mechanics, or delete.

Do not preserve old Pi internals merely because Pig already copied them, but do
not use cleanup as permission to redesign the observable contract. Remove
history scars while keeping current public source, wire, Session, ordering,
errors, and TUI behavior exact. One current production mechanism is preferable
to compatibility layers or parallel old/new paths unless current upstream has
the same compatibility contract.

## Differential porting loop

The public `microsoft/typescript-go` port provides useful process precedent:
it pins the TypeScript source as a submodule, imports upstream test baselines,
treats reductions in difference baselines as convergence, and keeps intentional
changes explicit. Pig uses the same principles with a frozen Pi oracle,
`.upstream/current`, byte-faithful scenarios, semantic mappings, and
`DIVERGENCES.md` because Pi's interactive/network surfaces cannot all be
imported as compiler golden files.

For each Pig family:

1. Read complete upstream source, callers, tests, docs, and published types.
2. Inventory every semantic member/overload and every async, persistence, wire,
   command, setting, keybinding, and TUI-state contract in scope.
   Compiler-generated input-handler facets are the denominator for event
   branches, callbacks, mutations, delegates, and boundary operators. They are
   review prompts, not semantic proof: `%` can implement wraparound or an
   unrelated calculation, and a keybinding's presence does not prove its state
   transition.
   Compiler-generated render facets similarly inventory theme calls, glyph and
   spacing literals, width/truncation operations, positioning numbers, and
   referenced constants. They detect source drift but do not prove terminal
   cells, ANSI reset scope, wrapping, ordering, or visual equivalence; those
   require byte/cell-faithful render tests and production TUI scenarios.
3. Probe exact Pi first and preserve the raw difference as evidence.
4. Make the current Pig failure red, or mutation-prove an already matching path.
5. Port the smallest behavior unit. Keep data concrete; introduce an interface
   only for a named consumer contract.
6. Re-probe affected callers and tighten the strongest stable comparator.
7. Reduce parity differences. Never accept or normalize a difference merely
   because the Go implementation is internally cleaner.
8. Close reviewed mappings only when production reachability and behavioral
   evidence exist. Generated recommendations remain non-authoritative.
9. Run family durability and performance gates proportionately, then the full
   suite at shared-boundary or campaign milestones.

A TypeScript patch can be useful navigation evidence during an upstream leap,
but identifiers and file paths are not a port. Search split Go files, inspect
callers, and account for the changed behavior rather than mechanically applying
syntax.

## Public references

Consulted against the current Go 1.27.1 toolchain and public sources:

- [Go code review comments: interfaces](https://go.dev/wiki/CodeReviewComments#interfaces)
- [Google Go style: interfaces](https://google.github.io/styleguide/go/decisions#interfaces)
- [Go piglet-guided optimization](https://go.dev/doc/pgo)
- [Go PGO: inlining and devirtualization](https://go.dev/blog/pgo)
- [Go diagnostics](https://go.dev/doc/diagnostics)
- [Go 1.27 release notes](https://go.dev/doc/go1.27)
- [Microsoft TypeScript native port](https://github.com/microsoft/typescript-go)
- [TypeScript native-port announcement](https://devblogs.microsoft.com/typescript/typescript-native-port/)
