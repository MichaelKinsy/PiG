# AGENTS.md

Operating rules for AI agents maintaining `pig`, the Go port of upstream `pi`. This file is context, not human marketing. Keep it small, current, and enforceable.

## Mission

`pig` must match upstream `pi` observable behavior unless `docs/parity/DIVERGENCES.md` records a numbered, scrutinized exception. Treat the port as a compiler from upstream TypeScript behavior to Go behavior: `docs/parity/PORT_MAP.md` defines the file map, parity scenarios define behavior, `test/parity/coverage.md` reports proof, and gates keep the claims honest.

Source of truth:
- `.upstream/current/` is the upstream mirror.
- `internal/coding/pigversion/pigversion.go` pins the upstream version (re-exported by `coding/upstream.go` as `coding.UpstreamVersion`).
- `docs/parity/PORT_MAP.md` maps upstream files to Go files, deferred entries, or designed-out entries.
- `test/parity/scenarios/<family>/*.toml` define observed behavior.
- `test/parity/interfaces/upstream-v<version>.json` is the compiler-derived public
  package interface denominator; observable adapter inventories such as
  `cli-v<version>.json` extend it for non-package user contracts;
  `mapping-v<version>.json` records each Pig disposition and closure evidence. Generated inventory drift and strict mapping
  are Foundation gates. PORT_MAP remains the file navigation roll-up until
  Foundation I finishes generating its statuses from these ledgers.
- `test/parity/upstream-sync/v<version>.toml` accounts for every changed tracked source file in a version leap.
- `test/parity/async-contracts.toml` accounts for Promise/async semantics across the complete pinned upstream source tree.
- `test/parity/coverage.md` and the generated block below report verification.
- `docs/parity/DIVERGENCES.md` records intentional differences.

No prose status claim overrides those files.

If you change exported Go API, CLI flags, settings, parity scenarios, or docs mirrors, run `make generate` and commit the result. CI's drift gates compare these files. Never hand-edit generated files; regenerate.

When landing a new port, record its full upstream source path in a production
`// Ports packages/.../file.ts` comment and update PORT_MAP in the same change.
The drift gate rejects explicit port claims left not-started, deferred, or n/a.
Use partial until behavioral closure is reviewed; source existence is not proof.

## Product-extension boundary

Stock PiG contains upstream-parity behavior and the inert, product-neutral mechanisms needed to discover, validate, select, build, verify, load, isolate, and run Packages, Resources, extensions, Piglets, and Piglet Binaries. A Piglet selects and activates product behavior. PiG Standard owns branded presentation, opinionated defaults, selected extensions, onboarding, games, and other optional product workflows.

Classify every proposed additive capability before implementation:

1. `required substrate` remains in Stock PiG.
2. `inert capability` remains in Stock PiG only when an extension or Piglet must select it.
3. Composition, presentation, policy, and branded behavior belong in PiG Standard or another explicit Piglet or Package.
4. Caller-free or unneeded behavior is deleted.
5. Behavior that overlaps Pi but differs observably is fixed or recorded in `docs/parity/DIVERGENCES.md` after explicit approval.

Use `product-neutral diagnostics` for generic reports that describe Stock mechanisms. Use `explicit unsafe opt-in` only for a capability that stays off by default, is visible at configuration boundaries, and must not be selected by PiG Standard.

A Piglet cannot supply the parser, resolver, verifier, SDK bridge, isolation boundary, or build machinery needed to load itself. Do not move those bootstrap mechanisms into PiG Standard. Do not activate Standard behavior in Stock PiG. Do not put product UI, Piglet-specific recommendations, workflow-specific logic, or linked extension products under `internal/` or `cmd/` except for the minimum generic host capability.

Additive records use `docs/additive-features.md`. Observable Stock PiG differences use `docs/parity/DIVERGENCES.md`. Place one short typed source marker at each production decision point. Tests do not satisfy the source-marker requirement.

Change the version only with `make set-version VERSION=x.y.z`.

## Pig-owned format and update policy

Pig-owned source, manifest, record, graph/plan, builder, Marketplace,
publication, and subprocess formats have one strict current unversioned shape
while Pig and its clients upgrade atomically. Release/content identity, exact
upstream-owned schema versions, and external-standard versions remain. Do not
add a Pig-owned format discriminator, negotiation, compatibility reader,
migration, redirect, or dual write unless exact upstream regularly versions the
equivalent schema or the user explicitly approves independently deployed
compatibility.

Self-update preserves Pi's command routing but uses one proven native ownership
tier per operation. Standalone download, package-manager, immutable
Piglet Binary, OCI/Image, and unsupported/read-only paths must
not substitute for one another after work starts. Downloads require complete
checksum and overflow-safe size verification; startup checks remain bounded and
best-effort, while explicit update failures remain actionable.

## Extension API porting rules

The pig extension system is a **port** of the upstream pi extension API,
not a replacement for it. Two downstream-only constructs exist so we can
stay subprocess-only without giving up the pi extension API:

- **Multi-language SDK bridges** (`extensions/sdk`, `extensions/sdk-rs`,
  `extensions/sdk-py`): see `docs/additive-features.md` D19 and
  `docs/extension-api-parity.md` “Parity boundary”. All three SDKs target the
  **same current wire contract** in
  `coding/extension/host/subprocess/protocol.go` and the same
  registration/host-call/tool-result shapes. The wire has no independent
  version or compatibility negotiation. The conformance suite in
  `test/extension-conformance/` is the
  authority: a new SDK feature is only “done” when it matches the
  in-process Go reference there.

  Every SDK exposes the same extension API. Separate the three axes:

  - **Interface**: the set of capabilities and their parameters and return
    meaning. Identical across SDKs, modulo the naming convention.
  - **Behavior**: what a call returns for a given host state, including
    error and edge cases. Identical across SDKs.
  - **Implementation**: how an SDK produces that answer: host call versus
    replicated state, sync versus async internals, threading, allocation,
    borrowing, error representation. Free to differ per language.

  Implementation variance is expected and needs no record. Interface or
  behavior variance is drift. Returning a constant where another SDK
  returns host data is a behavior divergence even though the interface
  matches, and it is worse than a missing method: a missing method fails
  loudly at the call site, while a plausible constant is silently believed.
  A stub is therefore not an acceptable resting state, and "the host call
  is async but this API is sync" is a reason to replicate the value into
  state, not a reason to fabricate one.

  An extension is portable between SDKs by rewriting it, not by
  rearchitecting it or dropping a feature. A capability landing in one SDK
  is unfinished until it lands in all of them with a conformance row that
  fails when any one is broken. Bind that row to a value distinguishable
  from each SDK's fallback, or an SDK that never makes the call passes by
  defaulting.
- **Packed runtime cells** (`coding/extension/host/subprocess/cell_plan.go`,
  `coding/extension/host/runtimecell/`): see `docs/additive-features.md` D20. These
  are a host-side optimization. Each contained extension keeps its own
  socket and its own `register` handshake. Packed mode must
  be observationally identical to isolated mode from the extension’s
  point of view; if a difference appears, fix the host or add a numbered
  divergence: do not change the SDK API to paper over it.

Do not, without an explicit approved spec:
- add a WASM or embedded-JS extension runtime,
- add a dynamic Go plugin / `plugin.Open` extension loader,
- add a linked/in-process production extension runtime,
- introduce multi-register (`RegisterPayload.Extensions`,
  `RequestPayload.TargetExtension`): packed cells deliberately stay on
  one socket and one registration per extension,
- diverge SDK semantics from the in-process Go reference without first
  numbering the divergence and citing a failing conformance test.

When adding a host-side feature, first read `docs/extension-authoring.md`.
The implementation order is: upstream pi behavior → protocol shape in
`protocol.go` → host wiring in `coding/extension/host/subprocess/` → SDK
ergonomics in each `extensions/sdk*/` → conformance test row → parity
matrix row in `docs/extension-api-parity.md` → numbered divergence if
needed.

`extensions/sdk-ts` is a declaration-only adapter. It re-exports the exact
pinned Pi extension declarations and augments them with PiG-only types. The
Node subprocess runtime remains the implementation, so do not duplicate Pi's
TypeScript runtime in this package. Keep its Pi dependency equal to
`coding.UpstreamVersion` and verify it with `make test-sdk-ts`.

Extension authoring has two source forms. A conventional factory uses the one
standard symbol for its language; an exact standalone is always isolated. Pig
derives wire, SDK, runner, placement, and registration details. Runtime
registration owns identity and capabilities. Do not add an authored extension
manifest or another global extension configuration plane. Isolated, packed, and
fused realizations of one factory must pass the same conformance behavior.

## TypeScript-to-Go semantic translation

Read `docs/typescript-to-go-porting.md` before translating a new behavior
family or changing a parity-facing Go representation. Port observed semantics,
not TypeScript syntax: account explicitly for omitted/undefined/null state,
closed unions, insertion order, UTF-16/code-point/terminal-cell units, numeric
conversion, exceptions, callback ownership, Node path/URL/regex/JSON behavior,
and every state or wire effect that callers observe. A TypeScript `interface`
does not imply a Go interface; use a struct for data and a small consumer-owned
Go interface only for a real behavioral contract. Constructors normally return
concrete types. "Port semantics, not syntax" governs behavior, not naming: it is
not license to rename. Mirror upstream identifiers and structure 1:1 (camelCase →
exported PascalCase, same literal union values, same field names), and diverge
only where a Go language mechanic forces it (no `Symbol.for`, no field-bearing
interfaces, discriminated union → sealed interface), recording each forced case.
A needless rename is drift that inflates the next upstream sync and obscures
correspondence. Do not change a public extension or TUI contract for presumed
speed: profile the representative path and prove allocations/inlining and
byte-faithful behavior before and after.

## Faithful, general implementations

Port Pi's design, not one case's output. Use Pi's shared data (model catalog, `defaultModelPerProvider`, auth metadata) and one shared function where Pi has one. Never hard-code a provider or model in shared code; cite the Pi line when Pi itself special-cases one. Test shared paths with several provider shapes, never only Copilot or `test-faux`, and fix a special case's siblings together.

## TypeScript async/Promise parity

Treat every upstream `async`, `Promise`, `.then`, `Promise.all`, `Promise.race`, and intentionally unawaited call as control-flow behavior, not syntax to erase. Before porting it, record whether the caller waits, what ordering is guaranteed, how cancellation and errors propagate, whether work is concurrent, and which executor/event loop owns callbacks. Then preserve that contract in Go:

- an awaited Promise normally maps to a blocking Go call that returns its value/error; do not add `go func()` merely because TypeScript says `async`;
- `Promise.all`/`allSettled` maps to owned, joined concurrency with deterministic result/error handling, not detached goroutines;
- an intentionally unawaited Promise maps only to an owned background task with cancellation, error reporting, and shutdown draining; naked fire-and-forget goroutines are not equivalent;
- Promise rejection maps to a surfaced Go error on the same operation unless upstream explicitly handles it;
- `AbortSignal` and request/session lifetime map to `context.Context` or an equivalent cancellation channel;
- async TUI/event handlers perform slow work off the UI loop and marshal UI mutation back through the loop's approved run-on-main path.

Probe upstream call sites, not only the declared return type: an `async` function with no `await` may still create a microtask boundary, while a Promise-returning callback may be awaited by its host. Regression evidence must prove completion ordering, cancellation, and error propagation; output-only happy-path tests do not prove async parity. When adding or changing an extension API, record the async contract in `docs/extension-api-parity.md` and exercise isolated and packed subprocess modes where applicable.

## Reward function

Highest-value work, in order:
1. Find a pig-vs-pi behavior difference and fix it at the source, or number it in `docs/parity/DIVERGENCES.md`.
2. Tighten an existing scenario comparator: `wait_contains` → `output_normalized_equal` → `output_equal` → `escaped_output_equal`, or increase `runs` where flake risk matters.
3. Promote silent drift to a numbered divergence with call-site markers, remove condition, and parity coverage or explicit allowance.
4. Add honest file coverage where the asserted output actually exercises the claimed `covers` paths.

Low-value or misleading work: green check counts, scenario counts, `covers` entries not exercised by the assertion, normalized output hiding a real rendering difference, `runs = 1` for non-trivial behavior, renderer band-aids when the bug is upstream-source logic, and stopping because `make check` is green.

## Loop smells

Stop and report instead of claiming success when:
- Everything passed on the first try.
- A scenario passed before and after the supposed fix.
- The loop produced zero bugs, zero comparator tightenings, and zero numbered divergences.
- A scenario claims coverage but asserts only banner, prompt, or ready text.
- ANSI or whitespace was normalized to hide a real difference.
- `family-gaps` is green only because one broad scenario claims many files.
- A comment explains a rendering difference but no divergence number exists.

Investigation is the work. Green is necessary, not sufficient.

## Loop self-report

Before `signal_loop_success`, include this block in the final assistant message and fill it honestly:

```md
### Loop self-report

Family: <name>

Bugs found and fixed at the source (count: N):
  - <cause> → <fix> @ <file:line>

Comparators tightened (count: N):
  - <scenario>: <before> → <after>

Divergences numbered in docs/parity/DIVERGENCES.md (count: N):
  - D<N> <one-line>

Lint suppressions added or changed (count: N):
  - <rule> @ <file/config>: <why source fix would be less faithful/correct>

New file coverage (count: N PORT_MAP entries newly ✅):
  - <upstream path>

Band-aids consciously chosen (count: N, should be 0):
  - <site>: <symptom>: <why root fix deferred>: <follow-up>

Scope expansion (count: N files outside nominal family):
  - <file>: <reason>: <call sites audited>

Cross-family scenarios re-probed/tightened (count: N):
  - <family>/<scenario>: <what changed and why>

Smells investigated (count: N):
  - <smell> → <finding>

Smells deferred to user (count: N, should be 0 unless blocked):
  - <smell>: <blocker>
```

A report with all zero counts is a failed loop unless the user explicitly accepts that outcome.

## Coverage

The block below is generated by `make coverage
make foundation-check
make interface-inventory
make interface-inventory-test
make interface-inventory-drift
make interface-go-drift
make interface-recommendations-drift
make interface-recommendations
make interface-mapping-quality
make interface-mapping-strict
make upstream-delta
make async-contracts` or `make verify` from `docs/parity/PORT_MAP.md` and `test/parity/scenarios/**/*.toml`. Do not hand-edit between the markers. Fix `test/parity/cmd/coverage/main.go` if the math is wrong.

<!-- BEGIN COVERAGE -->
<!--
  Machine-generated by `make coverage`. Do not hand-edit.
  This block is the ONLY status claim AGENTS.md makes about the
  port. Everything else is invariant rule, not progress narrative.
-->

**Porting:** 379 / 493 intended-portable entries ✅ (76.9%); **Verification:** 355 behavioral (93.7%), 3 weak-only (no behavioral verification), 21 untested.
Raw PORT_MAP rows: 550. Breakdown: 57 n/a (designed out) · 102 🟡 partial · 12 ⬜ not started. See docs/parity/DIVERGENCES.md for the documented exceptions.
Behavioral evidence includes paired scenarios and reviewed mutation-proven unit tests; the family table below counts paired scenarios only.
Weak scenarios not counted as behavioral verification: 4 boot-only, 4 registration-only, 1 smoke-only.

| family | scenarios | behavioral | boot-only | weak | deferred | upstream behavioral covered | last run |
|---|---:|---:|---:|---:|---:|---:|---|
| `_top` | 1 | 1 | 0 | 0 | 0 | 1 | not run |
| `ai-sdk` | 3 | 3 | 0 | 0 | 0 | 90 | not run |
| `autocomplete` | 13 | 13 | 0 | 0 | 0 | 5 | not run |
| `cli-utils` | 25 | 25 | 0 | 0 | 0 | 13 | not run |
| `clipboard-images` | 7 | 7 | 0 | 0 | 2 | 11 | not run |
| `compaction` | 14 | 14 | 0 | 0 | 0 | 10 | not run |
| `export-html` | 6 | 6 | 0 | 0 | 0 | 1 | not run |
| `extension-host` | 1 | 1 | 0 | 0 | 0 | 2 | not run |
| `extensions-runtime` | 103 | 102 | 0 | 1 | 0 | 58 | not run |
| `footer` | 11 | 11 | 0 | 0 | 0 | 9 | not run |
| `fullscreen` | 12 | 12 | 0 | 0 | 0 | 9 | not run |
| `interactive-rendering` | 52 | 51 | 1 | 0 | 0 | 35 | not run |
| `json` | 10 | 10 | 0 | 0 | 0 | 7 | not run |
| `model-resolver-selector` | 24 | 24 | 0 | 0 | 0 | 15 | not run |
| `model-runtime-store-catalog` | 25 | 25 | 0 | 0 | 0 | 24 | not run |
| `oauth` | 29 | 29 | 0 | 0 | 0 | 18 | not run |
| `print` | 7 | 7 | 0 | 0 | 0 | 6 | not run |
| `project-trust` | 23 | 23 | 0 | 0 | 0 | 18 | not run |
| `providers-faux-streaming` | 65 | 64 | 0 | 1 | 0 | 48 | not run |
| `providers-registry` | 7 | 4 | 0 | 3 | 0 | 26 | not run |
| `rpc` | 49 | 49 | 0 | 0 | 0 | 23 | not run |
| `selectors` | 14 | 14 | 0 | 0 | 1 | 15 | not run |
| `session` | 40 | 40 | 0 | 0 | 0 | 19 | not run |
| `settings` | 14 | 14 | 0 | 0 | 0 | 19 | not run |
| `slash-commands` | 16 | 15 | 1 | 0 | 0 | 16 | not run |
| `startup` | 18 | 17 | 1 | 0 | 0 | 10 | not run |
| `tools` | 29 | 29 | 0 | 0 | 0 | 31 | not run |
| `tree` | 8 | 7 | 1 | 0 | 0 | 5 | not run |
| `tui-components` | 26 | 26 | 0 | 0 | 0 | 27 | not run |

Full per-file detail: `test/parity/coverage.md`.

<!-- END COVERAGE -->

## Reliability contexts

Define the upstream contract, complete user path, and material failure modes before implementation. Follow the verification sequence below. Use the complete PiG path as the primary proof for behavior that crosses a Provider, Session, mode, Extension Host, process, or terminal boundary. Retain repeatable evidence.

Test empty, ordinary, boundary, and representative stress inputs. Verify ordering, cancellation, replacement, cleanup, and retained state. Measure latency, memory, allocation, and backpressure on critical paths. A small-input pass does not prove bounded behavior.

Use a term from `docs/project/CONTEXT.md` when that file defines it. Do not redefine ratified terms in a module instruction. Read the nearest module `AGENTS.md` before work in a known reliability context. The root owns shared invariants. The module owns its contract, failure model, and evidence. Store each rule once.

| Context | Instructions |
|---|---|
| Transcript, Provider, Event Stream | `ai/AGENTS.md` |
| Main Screen, terminal rendering, input editing | `tui/AGENTS.md` |
| Session, Model Runtime, execution mode | `coding/AGENTS.md` |
| Extension Host, SDK, topology, lifecycle | `coding/extension/AGENTS.md` |
| upstream oracle, scenario, comparator, evidence | `test/parity/AGENTS.md` |
| Stock PiG artifact, PiG Standard Piglet release, provenance, publication | `.github/AGENTS.md` |

Apply these rules to new work. Do not delete or weaken accepted tests to conform. Coordinate with the lane owner before changing evidence. Brief or restart active agents after an instruction change.

## Upstream test parity

A production file is `ported` only when every upstream test file covering it is ported with the same inputs and expected results, or designed out with a numbered, reviewed reason. Record test locations and current upstream hashes in `test/parity/interfaces/test-mapping-v<version>.json`. A `partial` disposition enumerates the missing cases; file-name similarity, existing code, and a passing smoke test do not close them. Port the covering upstream tests in the same change whenever you port or change a production file. Preserve ordering, cancellation, errors, retained state, and caller effects, not only happy-path output.

No release may treat a pending or partial upstream test tagged `hot-path` as closed. `make ci-contracts` enforces the reviewed path tags in `test/parity/interfaces/test-porting-policy-v<version>.json` and rejects a decrease in the total ported-file count against the committed baseline. The owner-approved 0.3.x exception records explicit `deferred-0.3.x` rows without changing their partial/pending mappings, hot-path tags, assertions or baseline; [the release-policy procedure](test/parity/README.md#approved-03x-test-porting-gaps) defines the required approval, missing-case reference and follow-up. All unlisted pending/partial hot paths remain release blockers. Keep tag review and the ported baseline current in the same change; never remove a hot-path tag or lower the baseline to pass the gate. A credential or toolchain requirement is not a scope exclusion. Dispatch test-file ownership through `docs/parity/pending-tests-batches.md`; coordinate production files and shared evidence separately.

## Verification-driven development

For core parity surfaces (agent loop, message normalization, provider payload conversion, session persistence, auth, tool execution, interactive rendering), use a red → green → refactor loop. Apply [Faithful, general implementations](#faithful-general-implementations).

Bug fixes require a regression guard in the same PR. Add a new unit test, integration test, or parity scenario that would fail on the observed bug, or name the existing failing test that already proves it. If no automated guard is practical, say why in the PR and file a follow-up before merge. Do not rely on manual repro alone for a fixed bug.

1. **Reproduce first.** Add or identify a test/scenario that fails on the current code before changing behavior. If you already patched before writing the test, prove the test is meaningful by describing the exact pre-fix failure it would have caught.
2. **Compare to upstream.** Read the upstream TypeScript implementation and encode the observable rule in the test name/comment. For provider/message conversion, prefer table-driven tests that assert the serialized request shape, not just successful return. For interactive rendering, assert the runtime event path, not only the leaf component.
3. **Fix at the source.** Patch the lowest shared layer that matches upstream semantics. Avoid UI/provider band-aids when message normalization or session conversion is the source.
4. **Harden both boundaries.** For cross-layer bugs, add at least one unit test at the pure conversion layer and one regression at the nearest caller boundary when practical (e.g. NormalizeMessages + OpenAI converter; session loop + print/interactive error surfacing).
5. **Verify poisoned history.** When a bug involves bad persisted sessions, test with historical/poisoned messages: errored assistant turns, aborted assistant turns, empty text blocks, nil content, orphaned tool calls, and model/provider switches.
6. **No accepted flake.** Do not document “rerun standalone” as success. Fix root causes instead of hiding failures with longer time budgets, retries, sleeps, env overrides, skips, or normalization. Use timeouts and retries only for faults outside our control, and surface those faults. An unavailable external dependency is a blocker, not passing evidence.
7. **Report the red/green evidence.** Final summaries for fixes to core parity must state: failing symptom, new regression test names, upstream rule mirrored, and commands run. If a test was added after the fix, say it was not red-proven.
8. **Production path required.** A new production API/method used to fix behavior must have at least one production call site or a test that drives the production path. Test-only call sites do not prove the product behavior changed.
9. **Coverage quality over coverage quantity.** A PORT_MAP entry is not accepted as verified by a weak scenario. Boot-only, smoke-only, and deferred scenarios may stay in the report, but they do not count as behavioral verification. Registration-only scenarios count only for registry/catalog/auth-wiring paths, never for provider stream/payload conversion files.

Use this acceptance matrix for shared provider, model, auth, and tool regressions. Explain inapplicable shapes from the upstream contract, not available credentials. Do not use `--list-models` as stream/payload proof.

Cover both OpenAI-compatible API paths when the change reaches both:

```text
ai/openai.go             # openai-completions
ai/openai_responses.go   # openai-responses
agent/transform.go       # upstream transform-messages parity before provider conversion
```

Shared-path acceptance matrix for regressions:

| required axis | minimum cases |
|---|---|
| provider shape | OAuth, API key, OpenAI-compatible with a custom base URL, and no default model, as the path allows |
| API path | each API kind reached by the shared behavior, including `openai-completions` and `openai-responses` when both can apply |
| mode | pure shared-function/converter unit test plus nearest caller (`print`, interactive, or RPC, as applicable) |
| history | fresh plus poisoned persisted history when relevant |
| assistant prior turn | error, aborted, empty text, tool-call/orphaned tool-call when relevant |
| auth/network | hermetic success and hermetic failure when touching auth refresh/error display |

## Done criteria

A surface is done only when:
1. a canonical scenario exists under `test/parity/scenarios/<family>/`;
2. `covers = [...]` names exact upstream paths from `docs/parity/PORT_MAP.md` **and** those paths are exercised by the asserted behavior;
3. the scenario has behavioral verification quality (not boot-only, registration-only, smoke-only, or deferred) unless the surface is explicitly accepted as weak-only;
4. the scenario was derived from upstream first;
5. drift exposed by the scenario is fixed or recorded as a numbered divergence;
6. legacy duplicate or weak coverage claims are removed;
7. pure unit tests and caller-path tests exist for cross-layer bugs;
8. `go test ./...` and the relevant parity gate pass.

9. for an upstream leap, `make upstream-delta` proves every changed tracked source file has a non-pending disposition and durable evidence.

10. `make interface-inventory-drift` proves the public package export graph and
    exact published declaration inventory are current, and
    `make interface-mapping-strict` proves every stable interface ID has complete
    mapping/reachability/conformance/evidence closure.

11. `make async-contracts` proves every current upstream Promise/async source has an explicit ordering/cancellation/error contract and durable evidence.

12. interaction and state-machine behavior found during probing is closed in
    `test/parity/behavior-contracts.toml` when declaration shape cannot express it.
    Contracts bind a hashed current-upstream source range and its complete
    keybinding/event inventory to Pig targets, a failing unit regression, and a
    behavioral parity scenario. Boundary transitions such as first/last
    wraparound, empty/singleton states, cancellation, ordering, and persistence
    are separate obligations; interface presence does not prove them.

`test/parity/interfaces/behavior-inputs-v<version>.json` is the compiler-derived
denominator for upstream input/action state machines and render surfaces. It
accounts for keybinding declarations, platform defaults, consumers, raw input,
branch/state effects, theme calls, glyph/string tokens, width/layout calls,
numeric positioning literals, and referenced file constants. Its reviewed
mapping assigns each surface to one owner family and records pending, partial,
ported, deferred, divergence, or designed-out closure. `make
behavior-contracts` rejects source/facet drift, declaration/consumer mismatch,
missing mappings, invalid family ownership, and stale evidence. `make
behavior-contracts-strict` rejects pending or partial surfaces. Generated
facets identify review obligations; they never prove branch semantics, visual
output, or promote a mapping.

Parallel porter campaigns may run only read-only `inventory` and `verify` jobs
against disposable snapshots via `make porter-campaign`. Reports
must be tied to one commit, upstream version, and owner family, and are proposals
for the coordinator to re-probe. Production/evidence edits (`family` and
`tighten`) remain serialized in the canonical workspace.

Choose the option that makes the next upstream sync smaller. Do not embed pig-specific behavior in mirrored code unless it is a documented divergence.

## Parity scenario rules

Probe upstream first. Record enough evidence in comments for the next maintainer to see what was measured. Prefer strong, byte-faithful comparators. Use substring checks only when no stronger stable assertion exists.

Scenario quality rules:
- `covers` must name behavior exercised by the asserted crop/output, not merely code touched by startup, registration, or import/linking.
- Boot-only scenarios may exist but cannot claim deep file coverage and do not count as behavioral verification.
- Registration-only scenarios (for example `--list-models`) prove catalog visibility only. They must be tagged `registration-only`; they may cover registry/catalog/auth-wiring paths but must not claim provider stream/payload conversion files.
- Smoke-only scenarios must be tagged `smoke-only`; they are useful tripwires but not acceptance evidence.
- If output differs, fix pig or cite a numbered divergence. Comments alone are not enough.
- Do not weaken a comparator to keep a pig bug green.
- Scenario comments must explain any `normalize_replace`, `lint-known-gap`, `skip`, `serial`, group override, or weak-quality tag.
- Keep `covers` narrow. Broad claims inflate the dashboard and hide untested behavior.
- Re-probe affected cross-family scenarios when a fix changes shared behavior.

Isolation rules for parallel parity:
- Agent/home dirs are ephemeral copies. Never write into checked-in fixture roots.
- Use `{{TEMP}}` for writable paths; do not hardcode `/tmp` paths.
- tmux session names must be unique and cleanup may kill only `parity-*` sessions.
- `PIG_CODING_AGENT_DIR` and `PI_CODING_AGENT_DIR` win over `PIG_HOME`, and every child process inherits them, including a tmux session started from your shell. A manual probe or test that runs pig must set or clear all of `PIG_CODING_AGENT_DIR`, `PI_CODING_AGENT_DIR`, `PIG_HOME`, `PI_HOME` and `HOME`, or `/logout` and login rewrite the real credentials (the 2026-10-06 lane `auth.json` wipe). A package whose `TestMain` calls `testenv.ScopeTempDir` or `testenv.RunScoped` replaces the agent-directory variables. Other packages inherit them, so a plain `go test` run from an agent shell sets `PIG_CODING_AGENT_DIR`, `PI_CODING_AGENT_DIR`, `PIG_HOME` and `PI_HOME` to scratch directories first. `make test` runs each group under seeded directories and fails a group that adds, removes or changes any file or directory in them (`automation/ci/agent-dir-guard.sh`).
- `pre_clear_paths` is deprecated; prefer fresh temp dirs.
- Use the default scheduler groups unless a justified `group = "..."` or `serial` tag is required.
- Deferred scenarios stay visible in coverage; do not use skip/defer to hide known drift.

## Divergences

A prior divergence record is investigation evidence, not permanent approval.
Re-probe it against the exact current upstream and current production path at
every upstream leap and foundation/release review. Delete or reclassify entries
that are language mechanics, additive behavior, test limitations,
unreachable/equal behavior, stale scaffolds, or caller-free code. IDs are global
across `docs/parity/DIVERGENCES.md` and additive ledgers; duplicate IDs fail even when they
live in different files.

A divergence is allowed only when it is user-visible or interop-relevant and cannot or should not be made faithful now. Every active divergence must have:
- `D<N>` id in `docs/parity/DIVERGENCES.md`;
- `SCRUTINIZED:approved`;
- remove-when condition;
- `// pig divergence (D<N>): ...` at each call site;
- parity coverage or an explicit parity allowance.

`make divergence-guard` (part of `make check`) catches divergences nobody recorded. It flags, in the agent, ai, coding, internal/codingagent, tui and cmd/pig packages: duration and cap literals with no upstream counterpart (`magic-literal`), event sends that race a context-done case (`cancel-drop-send`), non-blocking sends that drop their value (`default-drop-send`), decode errors skipped inside loops (`stream-parse-continue`), unknown or missing stop reasons mapped to success (`stop-reason-success`), SSE fields parsed outside `ai/sse.go` (`hand-rolled-sse`), `recover()` that discards the panic (`bare-recover`), discarded persistence errors (`discarded-io-error`), hooks that proceed when their handler fails (`hook-fail-open`), errors classified by status-code substrings (`error-status-substring`), and agent `Send`/`Continue` outside `coding/session*.go` (`agent-run-outside-session`). A hit passes only with a `// pig divergence (D<N>): ...` marker on its line or the line above, or, when Pi really does the same, `// upstream: <file>:<symbol>` there; the file must exist in `.upstream/current` or be a SHA-256-verified dependency snapshot whose exact version matches a dependency pin in that mirror (`test/parity/dependency-sources.json`; see `docs/parity/dependency-source-proof.md`), the symbol must appear in it, and for a literal the value must appear too. A literal whose value appears in an upstream file that `docs/parity/PORT_MAP.md` maps to the Go file also passes. Every current hit is listed in `automation/ci/divguard/baseline.toml` with its finding and owning slice. The gate compares the scan with that file in both directions: a new hit fails, and so does an entry whose hit is gone, so a fix deletes its entries in the same change. It does not compare the file with its previous version, so keeping it shrink-only is a review rule: never add an entry for new code. A green guard shows these patterns are absent or accounted for, not that the code matches Pi; allow markers prove only that the cited file and symbol exist (and, for literals, that the value appears there).

Do not record ordinary TS-to-Go mechanics, test harness differences, or an
SDK's implementation of a capability every SDK has (naming convention, `Result`
vs `(T, error)` vs raised exception, ownership and borrowing, host call versus
replicated state).

That exemption covers implementation only. An SDK that lacks a capability
another SDK has, or that answers the same call differently, is drift regardless
of how faithfully its interface is spelled. Record it and close it.

## Code and lint policy

Apply [Faithful, general implementations](#faithful-general-implementations) to code review and provider/model literal guard exceptions; each exception requires a Pi source citation.

Fix valid findings at the source. Preserve upstream behavior over stylistic lint suggestions. Use the narrowest suppression for real false positives and explain why a source fix would be less faithful or less correct. `nolintlint` is enabled; stale or unexplained pragmas fail. Security suppressions must name the CLI threat-model reason.

Code and interface style:
- Production comments state only current behavior and non-obvious protocol, concurrency, security, compatibility, or behavioral invariants. Delete comments that restate code, narrate an implementation pass, preserve project history, or point to `docs/parity/PORT_MAP.md`, a phase, a future row, or another status ledger instead of stating the current contract. Keep migration and parity rationale in the owning ledger or specification.
- Do not hard-wrap Markdown prose or source comments to fit a terminal pane. Editors can display-wrap long lines. Preserve structural line breaks in code blocks, tables, quoted material, poetry, generated files, and formats with an enforced line-length contract.
- Roadmap phases, workstream labels, future-work promises, internal issue ownership, and placeholder implementation plans fail the source-hygiene gate; keep them in tracked specifications instead.
- Historical comments, phase labels, placeholders, compatibility paths, manual
  parity lists, test-only public implementations, and prior approvals have no
  grandfather status. Revalidate against current upstream and production use;
  update the current rationale, bind the full production path, reclassify, or
  delete.
- Production-grade Go means concrete validated data, minimal consumer-owned
  interfaces, deterministic order, bounded resources, explicit lifetime/error
  ownership, and one mechanism per concept. Do not mirror TypeScript internals
  or add abstraction merely to look structurally similar; preserve observable
  Pi semantics and portable wire/session/source behavior. Quality archaeology is
  not a style sweep: change debt only with current contract/caller evidence and
  leave accurate provenance and healthy out-of-family code alone.
- `iface` rejects unused/duplicate interface declarations, opaque interface
  returns with one concrete implementation, and unexported contracts leaked
  through exported APIs. Do not suppress it
  by default; delete an unneeded interface or keep the one consumer-owned
  contract.
- Do not enable or apply `ireturn` or `interfacebloat` mechanically. Extension
  protocol interfaces, tagged unions, providers, and factories have legitimate
  interface returns, and an upstream public protocol may be broad. Review those
  surfaces against `docs/typescript-to-go-porting.md` and measure hot paths.
- Performance claims require a representative benchmark with allocations and a
  CPU/allocation profile. Interface dispatch is not a defect without evidence;
  prioritize the measured cost center and preserve upstream behavior.

- Exact-count assertions must derive their expectation from an independent denominator or identify an explicitly reviewed generated snapshot and use its owning regeneration command. A newly observed number proves that output changed, not that the new number is correct.
- A runtime mutation must compile before its behavioral failure counts as evidence. A compiler failure counts only when compilation is the contract under test.
- For extension, TUI, model/provider, Session/RPC, Package/Resource, and startup/reload critical paths, capture a representative benchmark/profile and resource-lifetime disposition with the owning behavior family. No blocking extension IPC or unbounded work may run on the TUI input/render loop.
- Ordinary editor and transient popup changes must not clear or replay transcript history. No TUI input handler may synchronously copy, decode, or scan unbounded Session history.
- Every known bug found in scope is fixed with red/mutation evidence or remains
  an explicit user-owned blocker. Passing unrelated gates does not close it.

Go style:
- Build, CI, release-candidate, security-analysis, and documented setup commands use Go 1.27.1.
- Maintained modules declare Go 1.26 or 1.26.0 as their language floor unless a concrete language requirement is approved. Do not raise a `go` directive merely to match the build toolchain.
- Use language features available at the module floor. Prefer applicable modern standard-library APIs such as `slices.Sort`, `slices.SortFunc`, `cmp.Compare`, `maps.Copy`, `slices.Sorted(maps.Keys(m))`, `strings.Cut`, `CutPrefix`, `SplitSeq`, `for i := range n`, `wg.Go`, `min`, and `max`.
- `go fix -diff ./...` should be empty.
- When observable behavior changes, update the doc comment in the same commit.

Lint commands when touching lint/config/code:
```bash
go tool golangci-lint config verify
make lint-changed
make lint
```

Use `make lint-changed` in the development loop. It runs every configured linter over whole changed Go files in changed packages relative to the merge base with `main`. Use `make lint` for the full-repository gate. `make check` always runs the full-repository gate.

## Commands

Run `make generate` after changing exported Go API, CLI flags, settings, parity scenarios, or docs mirrors. Commit the result. Never hand-edit generated files; regenerate.

Primary gates:
```bash
make generate
make build
make vet
make lint
make test
make lint-scenarios
make parity-fast
make parity
make check
make verify
make coverage
```

Other gates as needed:
```bash
make schedule-report
make test-stress
make parity-stress
make parity-perf
make parity-live
make release-check
go tool govulncheck ./...
go tool deadcode ./...
go fix -diff ./...
```

`make test` requires and exercises the Go, Node, Python, and Rust toolchains; fixture build failures are fatal. `make check` combines deterministic gates with one strict Pig/Pi pair per hermetic scenario. `make verify` is the end-of-loop gate: it runs each scenario's declared durability and regenerates coverage. `make parity` runs declared scenario pairs in parallel with default concurrency groups and suppresses `runtime_ratio_max` because parallel CPU contention makes that metric unreliable. `make parity-perf` runs serially and enforces runtime ratios; use it for release gates or cron, not normal loops.

## Documentation language

Write instructions, references, procedures, and operational documentation as simple, direct technical English. Write one instruction per sentence, in the present tense and the active voice. Keep one term for each thing. Do not apply controlled technical English mechanically to personal essays, origin stories, interviews, or release narratives. Preserve the author's vocabulary, humor, uncertainty, and cadence in those forms.

Do not hard-wrap Markdown prose to a terminal width. Use structural newlines only where Markdown or the content requires them.

Where Pig differs from Pi in a way a user can observe, state the difference at
the point of use and cite its `D<N>`. An agent carrying Pi pretraining does the
Pi thing wherever Pig's difference is undocumented.

## Public project writing

Write about PiG as a faithful Go implementation of Pi. Name Pi as the reference
implementation and link to its repository and documentation. Credit Michael
Kinsy as PiG's creator and state that PiG was originally developed at Hewlett
Packard Enterprise. Keep those facts separate from legal notices.

Do not call PiG an official Pi project or imply endorsement by Pi's maintainers.
Do not claim HPE sponsorship, Open Program Office approval, public release,
community governance, certification, or trademark approval until the relevant
decision is recorded and public. Do not use HPE logos, wordmarks, or exact
corporate styling without recorded Brand approval. Keep Stock PiG branding
product-neutral.

Use direct technical prose. Avoid slogans, superlatives, invented motives,
competitive framing, and promises of future collaboration. Prefer verifiable
facts: the pinned Pi version, observed parity, named divergences, supported
artifacts, and published evidence.

## Commit hygiene

Every commit requires both a DCO `Signed-off-by` line and a signature GitHub marks **Verified**. Use `git commit --signoff -S`. Sign-off is not a cryptographic signature. See `.github/CONTRIBUTING.md` for SSH signing setup and unsigned-commit repair. If signing is unavailable, a maintainer lands the change in a maintainer-signed commit with a `Co-authored-by:` trailer.

Before committing, run `git diff --stat HEAD` and `git status --short`, then stage only files intentionally changed. Never `git add .`, `git add -A`, or add a whole directory without inspecting contents. Testdata dirs accumulate temp files.

Forbidden commands:
- `git reset --hard`
- `git checkout .`
- `git clean -fd`
- `git stash`
- `git commit --no-verify`
- `git push -f`
- `tmux kill-server`
- `go install ./cmd/pig`

Only `tmux kill-session -t <name>` is allowed. Commit only files changed in this session.

Do not dispatch subagents to read local files available in this worktree. Read them yourself.

## When unsure

Read upstream source, read upstream tests, probe upstream behavior, then make pig match it. Ask the user instead of guessing.
