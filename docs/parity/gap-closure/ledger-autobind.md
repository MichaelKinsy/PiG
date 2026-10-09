# Interface gap detector (`autobind`)

`test/parity/interface-closure/autobind` decides, for every interface ID of `mapping-v<version>.json` (`<version>` is `pigversion.UpstreamVersion`, 1.1.0; package IDs and CLI IDs, whatever their disposition), either **NOT-A-GAP** or **GAP(reason)**. It is a deterministic rule set over go/packages and go/types: no score, no network, no model. It never edits the mapping and never closes a row. Its job is to remove the non-gaps so that lanes work only real gaps.

```text
make interface-gaps          # writes build/interface-gaps/{gaps.tsv,not-a-gap.tsv,summary.md}; fails when a gap is not in the baseline
make interface-gaps-update   # rewrites the baseline after a reviewed change
make ledger-check            # the same verdict through the shared derivation cache (lanes use this)
make ledger-update           # interface-gaps-update through the shared cache
go run ./test/parity/interface-closure/autobind -closed-check   # trust check 1
go test ./test/parity/interface-closure/autobind                  # unit tests and trust check 2
```

The same input gives the same output (two runs are byte-identical; the fixture test asserts it). A run from scratch takes under a minute on an idle host: `prodreach`, go/packages with tests over the module, then the rules. Gap list: `test/parity/interface-closure/gaps/interface-gaps-v<version>.tsv` (ID, package, reason, candidate Go symbol), sorted by ID. `build/interface-gaps/gaps.tsv` adds the rule that fired. `not-a-gap.tsv` lists the row, the Go symbol and the evidence (a test or a reachable caller) for each non-gap.

## The derivation cache

`make ledger-check` and `make ledger-update` (`autobind -cache auto`) return what `make interface-gaps` and `make interface-gaps-update` return. They differ in where the two expensive stages come from. CI keeps the uncached gate.

| stage | cost from scratch | content key | stored under |
|---|---|---|---|
| reach graph (`cmd/prodreach`, both build configurations) | about 20 s alone, 60 CPU-seconds | toolchain, build tags, patterns, the source of every package `cmd/pig` and `cmd/pig-experimental` link, `go.mod`, `go.sum`, the prodreach source | `reach-<key>/reach.json` |
| decisions of the rules (index, 12k rows) | about 30 s alone, 60 CPU-seconds | toolchain, the content of the upstream mirror's sources (`.upstream/current/packages`, not just the version link, because a mirror filled wrongly can be repaired in place), every Go source (tests included), `go.mod`, `go.sum`, `test/parity/interfaces/`, `test/parity/behavior-contracts.toml`, `docs/parity/PORT_MAP.md`, the whole `autobind` directory (engine and reviewed inputs) | `derive-<key>/derivation.json.gz` |

The directory is `PIG_LEDGER_CACHE`, else `build/ledger-cache` in the checkout. A host that runs several lanes sets `PIG_LEDGER_CACHE` to one directory they all use. Each entry is published by renaming a finished scratch directory, so a reader sees one whole or none; a per-key advisory lock makes concurrent lanes compute a key once; an entry nobody reads for a week is pruned. To share the cache between hosts, rsync the directory: a key is a content hash, so an entry is valid on any host with the same toolchain.

A hit is exact because the key covers every file the derivation reads (`inputFiles` in `cache.go`) and the cached derivation is the rules' output before the reviewed gaps and holds apply, which are applied on every run from the files in the tree. The output of a hit, a miss and a derivation from scratch is byte-identical: `TestCachedDerivationEqualsFull` (fixture) and `make ledger-equivalence BRANCHES="ref ref ref"` (`TestCacheEquivalenceOnLaneBranches`, real lane branches) compare the gap list, the not-a-gap list, the derived mapping and the frontier.

Why there is no per-package incremental derivation: a row's decision reads the declarations of every Go package its Go types reach, and every test and reachable caller of the symbol it picks, anywhere in the module. `cmd/pig` links all of it, so the sound footprint of any upstream package's rows is nearly the whole module, and a smaller footprint would be a guess that can silently keep a stale verdict. The cache reuses only what is provably equal.

`-frontier <file>` (`ledger-check` writes `build/interface-gaps/ledger-frontier.tsv`; `make ledger-frontier` writes `$(LEDGER_LOGS)/ledger-frontier.tsv`, the shared lane log directory when `LEDGER_LOGS` is set) ranks the root gaps (every gap whose reason is its own) by the child-gap rows they hold open: `unblocks_alone` counts the rows for which the gap is the only open root gap beneath or above them, `blocks_total` every child-gap row that has it among its blockers. Columns: rank, ID, package, kind (the gap reason: `member-missing`, `signature-mismatch`, `no-go-symbol`, `not-exercised`, ...), the two counts, the Pi declaration `file:line` the inventory records (the published `.d.ts`, or the source file for a package the inventory reads from source), the Pi source `file:line` found by name in `.upstream/current` (blank when not found), the closest Go symbol and the detail.

## Decision

A row is NOT-A-GAP only when all three hold; a rule that cannot decide is a gap with reason `undecidable`, never a pass.

| step | rules |
|---|---|
| **Symbol exists** | N1: the Go name is the upstream name, or the upstream name with an upper-case first letter. N2: equal after folding case and separators (`Http`/`HTTP`, `CODEMODE_OPTIONS_PREFIX`). N3: a class constructor is `New<Name>`. N4: a documented rename in `autobind/renames.json`. N5: when an exported Go declaration satisfies the name rules, unexported ones are not candidates. Candidates come from the package's directory seeds (`symbols.go`), in seed order; the first candidate with the fewest gaps below it wins. A property is a field (name, N1/N2 or json tag) or a method. |
| **Shape compatible** | Types T0-T14: string, number, boolean, literal unions, arrays, records, sets, function types, `AbortSignal` as `context.Context`, `Date`, `Error`, `Uint8Array`, `Promise<T>`, type parameters, named types (same name, documented rename, or an upstream alias resolved to its body from the pinned sources), unions of JSON shapes as `any` or `json.RawMessage`, closed unions as an interface with methods, object literals field by field. Calls S1-S6: a parameter whose type is `AbortSignal` (optionally `\| undefined`) and an options bag that holds only a signal are the leading context; an object or function type that merely mentions `AbortSignal` is an ordinary parameter; parameters correspond one to one; a variadic tail takes optional parameters; a different parameter count is a gap; Promise results are the value plus an optional error; `T \| undefined` is `T`, `*T` or `(T, bool)`. Members M1-M4: every upstream property has a Go member; a data property may be an accessor; a function property needs a Go method or function field, and an optional function property the inventory records without a call row is judged by its function type; a property whose type is `AbortSignal` is the context argument. Values V1-V3: a literal equals the Go constant; otherwise the type rules; a non-callable value realised as a Go function needs no parameters (or only a context) and a first result of the value's type. Aliases A1-A5: a union of string literals needs a Go string type with a constant for each literal; an open string union is any Go string type; any other body is undecidable. Every call overload and every property is its own row, decided against the same Go symbol; a parent whose child is a gap is `child-gap`. |
| **Exercised** | E1: a Go test (directly, or through a helper within three calls) references the symbol and the test or its helpers report failures through `testing.T` or testify. E2: the symbol is in, or is referenced from (body or signature), a function reachable from `cmd/pig` or `cmd/pig-experimental` (`prodreach`, rapid type analysis). E3: a type is exercised through its fields, methods, `New<Name>` constructor or typed constants. Calls through a repository interface count for every type that implements it. |

Reasons: `no-go-symbol`, `member-missing`, `signature-mismatch`, `type-mismatch`, `not-exercised`, `undecidable`, `child-gap` (derived; the cause is on the row below), `cli-row` (the 65 CLI IDs have no package symbol rule). A row the ledger marks `designed-out` is NOT-A-GAP by that decision (2 rows).

What the rules do not do: judge behaviour, judge whether an asserting test checks the right thing, or follow a rename nobody documented. NOT-A-GAP means "a Go symbol with a compatible shape exists and is exercised"; closing the row still needs the evidence references and a review.

`renames.json` (545 entries) is an input, generated once by `autobind -seed-renames` from the hand-closed rows whose recorded Go target the name rules cannot find (a rename is a recorded decision, not a rule result). Add an entry to document a new rename.

## The ledger is derived

`autobind -update` (`make interface-gaps-update`) also rewrites `test/parity/interfaces/mapping-v<version>.json` from the decisions, and `-check` (`make interface-gaps`, part of `check-contracts-fast` and `ci-contracts`) fails when the mapping differs from that derivation or a gap is not in the baseline.

- NOT-A-GAP becomes a `ported` row. A hand-written `ported` row confirmed NOT-A-GAP is kept as written. Every other NOT-A-GAP row is derived: its rationale starts "Derived by the interface gap detector", its layers are shape, production and behavior, its production ref is `call:`, `public-api:` or `tested:`, and its evidence is `test:` refs or `reach:<call>`. Promise and AbortSignal rows get a standard async contract.
- GAP becomes `pending` (id, disposition, hash only). `divergence` and `designed-out` rows are kept and must be cited. `handleInput` rows stay gaps (rule B1) until a behavior contract is cited, and `ExtensionAPI::property:on::call:` rows need hand-written layers.
- No resets: the inventory generator carries every row whose upstream shape hash is unchanged (`carriedDispositions` is gone), and the detector re-derives status against the new shapes. `TestVersionLeapWithUnchangedShapesKeepsEveryRowClosed` covers a leap with and without a generator that resets everything.
- The validator accepts the production kind `tested:`, the layer status `test-exercised` and the evidence kind `reach:`.

## Rule engine

Rules live in `test/parity/interface-closure/autobind/rules/`, one file per family (naming, optionality, unions, functions, placement, data shapes), each with table tests that include refusals. `rules/rules.go` is the registry (placement, name, member, type and signature rules); `engine.go` consults it after the built-in rules, and only when the built-in verdict is not yes. A rule says Yes or No only when the Go shape is provably equivalent or provably not, otherwise it stays silent. `rules/README.md` is the author guide.

| family | rules | wired into the detector |
|---|---|---|
| naming | NM1-NM8 spellings, getX/isX/hasX accessors, createX to NewX, product prefix | name and member rules |
| optionality | O1-O6 `?`, `\| undefined`, `\| null` against pointers, omitempty, presence wrappers | type rules |
| unions | U1-U6 literal unions and typed string consts, discriminated unions, MarshalJSON discriminators | literal-union type rule; the sealed-interface and tagged-struct checks are not yet wired |
| functions | S1-S11 options bags, overloads, Promise results, callbacks | signature rules, called directly by the detector |
| placement | P1-P4 PORT_MAP directories, methods against functions, generics | library only: the hook does not pass the upstream file |
| data shapes | DS1-DS7 records, tuples, numbers, bigint, inherited and error-base members | type and member rules |

Detector-owned rules added in this sprint: V4 (a per-provider model catalog constant is a Go function from the provider name to the catalog slice), S8 (options bag spread over positional parameters), T15 (intersections), T9c (`representations.json`: documented Go spellings of upstream names that have no named Go type), M5 (a string-literal property is the JSON discriminator of a Go type that marshals itself and has a method returning that literal), M6 (a property of a key registry, an interface whose every property has the literal type `true` such as pi-tui `Keybindings`, or whose every property type resolves to one Go type such as ai `ApiOptionsMap`, is the one constant of the registry's Go key type whose value is the property name; P1 applies to that constant, so production code must use it, and a second constant for the same key is a duplicate gap), T12f (a callback member of an object type is a Go func field tagged `json:"-"`, judged by its function type), and `-hand-ledger` for trust check 1 against the ledger as it was closed by hand.

## Result at this commit

Total 12234 IDs (the integrate-042 inventory: 12169 package IDs in fourteen packages and 65 CLI IDs): **10366 NOT-A-GAP**, **1868 GAP** (integrate-042 2479a8c93 plus the reviewed rules, ledger-autobind-int ac45c2add). The earlier 9950-ID tree had no chord, client, durable, env, protocol, server or telemetry package (2284 IDs). Rule P1(b) (library packages close on a Pi-cited, mutation-checked test), S3o (a Go variadic stands for an overload set), T12n (unique-symbol brands) and T9g (generic alias arguments) are in the engine.

| reason | agent | ai | chord | cli | client | codemode | coding-agent | durable | env | mcp | protocol | server | telemetry | tui | total |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| child-gap | 22 | 175 | 48 | 0 | 10 | 2 | 233 | 64 | 4 | 22 | 1 | 14 | 7 | 36 | 638 |
| cli-row | 0 | 0 | 0 | 65 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 65 |
| held | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 3 | 0 | 0 | 0 | 0 | 3 |
| member-missing | 14 | 53 | 24 | 0 | 0 | 1 | 82 | 5 | 2 | 1 | 0 | 1 | 0 | 38 | 221 |
| no-go-symbol | 3 | 37 | 16 | 0 | 0 | 3 | 43 | 8 | 0 | 4 | 0 | 1 | 12 | 3 | 130 |
| not-exercised | 1 | 49 | 37 | 0 | 9 | 0 | 258 | 31 | 0 | 12 | 1 | 7 | 13 | 0 | 418 |
| reviewed-gap | 2 | 29 | 2 | 0 | 2 | 0 | 22 | 9 | 1 | 4 | 1 | 0 | 1 | 7 | 80 |
| signature-mismatch | 6 | 24 | 7 | 0 | 3 | 0 | 75 | 13 | 3 | 3 | 0 | 6 | 2 | 20 | 162 |
| type-mismatch | 8 | 28 | 5 | 0 | 1 | 0 | 12 | 11 | 0 | 5 | 0 | 0 | 0 | 2 | 72 |
| undecidable | 1 | 29 | 3 | 0 | 0 | 1 | 13 | 20 | 0 | 1 | 0 | 1 | 1 | 9 | 79 |

`ledger-totals.log` records the ported count after every re-derive (6325 ported, 2 designed-out at 02631f9ec). The root-cause gaps (everything except `child-gap` and `cli-row`) are the work list for lanes; `child-gap` rows close when the rows below them do. P1 closes a member row when production code reachable from `cmd/pig` or `cmd/pig-experimental` uses it (P1(a)), or through the library route (P1(b), `autobind/library.go`): the upstream package publishes an index and is not the package that ships `pi`, the member's Go package holds a file PORT_MAP records for a file that index publishes, the member is exported, duplicates no other exported or wired member, is not a constant stand-in, is (for a field) read or set by the package's own code, and an asserting test cites a Pi file:line that names it (a file:line in `.upstream/current/` counts; one in a pinned mirror such as `.upstream/v0.87.1/` never does, because CI checks out only the current mirror and the pinned version), runs the Pi oracle, or is named after a case of the Pi test file its file declares it ports (a subtest with the exact title, or a test function named Test + the title, optionally after the last words of a describe title), when that Pi file names the member; or a published Pi suite that names the member (such as durable's `./testing` conformance cases), ported to non-test Go code that uses it, runs from a test that passes the suite its `*testing.T` within three calls. The remaining `not-exercised` rows fail one of those conditions, and the gap detail names which (L1-L6).

## Trust check 1: the rows closed by hand

The hand-closed ledger is the mapping at 549079d4d (`git show 549079d4d:test/parity/interfaces/mapping-v1.0.4.json > /tmp/hand-mapping.json`; run `autobind -hand-ledger /tmp/hand-mapping.json -closed-check -out <dir>`, because the committed mapping is now derived). 3489 rows were `ported`. The rules agree (NOT-A-GAP) on **3115 of the 3489**; the first run agreed on 2800. The 374 disagreements:

| package | agree | disagree |
|---|---:|---:|
| agent | 28 | 0 |
| ai | 1278 | 87 |
| codemode | 85 | 15 |
| coding-agent | 1015 | 199 |
| mcp | 364 | 61 |
| tui | 345 | 12 |

By cause: 231 parents of a gap child; 96 undecidable (59 shapes outside the rules, 36 undocumented type mappings, 1 multi-value result); 44 wrong manual closures (no asserting test and no `cmd/pig` caller for the member); 2 member-missing; 1 signature-mismatch. The reviewer's classification is `handoffs/rev-ledger-autobind-classification.tsv`: wrong manual closures are reopened by the derived ledger (a hand row that no longer agrees becomes `pending`), rule bugs are fixed in rules. `closed-open-children.tsv` lists closed parents whose only gaps are open hand-ledger children and is not counted as disagreement.

Rule bugs found and fixed by this check (each was a false GAP on a hand-closed row): Go 1.27 declares `json.RawMessage` as an alias of `jsontext.Value`, so alias-stripping hid it; a Go variadic parameter is already a slice when upstream passes an array; a function type with a union return was split at the union; `(A | B)[]` and `string | URL` had no rule; an options bag that holds only a signal is the context argument; optional function properties wrapped in `( ... ) | undefined`; documented renames of members whose Go owner differs from the parent's; parameters named after an alias declared in two upstream packages; constructors documented as composite literals; a type exercised only through its constructor or typed constants. Without `renames.json` (`-blind`) the agreement is 69.6%: 10 points of the closure needed a documented rename.

Precision on this set was 80.3% in the first run, 90.5% before P1, and is 95.2% now (3322/3489) on the integrate-042 tree after P1 reopened the rows no reachable code uses (rows the rules would not have flagged). The direction that matters for noise, a false GAP, is explained row by row above; the direction that matters for missing work, a false NOT-A-GAP, is covered by check 2 and by the limits stated above.

## Review corrections

Each was a false NOT-A-GAP, a false GAP or an untested rule; each has a test that fails without the fix.

- M4 matched any property whose type mentioned `AbortSignal`, so optional hook properties such as `Agent.afterToolCall`, `AgentOptions.transformContext` and `ProviderConfig.oauth` passed with no Go member at all. M4 now applies only to a property whose type is `AbortSignal`.
- S1 dropped any parameter whose type mentioned `AbortSignal`, so an options object `{ signal; force? }` matched a Go function that took only a context. S1 now drops only a signal or a signal-only bag.
- An optional function property without a call row (314 rows) was never signature-checked. M3 now checks its function type.
- A non-callable upstream value realised as a Go function was accepted without any check (V3).
- The fewest-gaps choice replaced exported public types with unexported internal ones (`SessionBeforeTreeResult`, `WorkingIndicatorOptions`, `getShellConfig`, `mcp AuthProvider`). N5 prefers exported candidates.
- `Promise<T | undefined> | T | undefined` named T twice and became an undecidable union; a nested function type parameter (`done: (...) => void`) broke the function-type parser.
- A type named only in a reachable function's parameter list was not counted as used (`ProgressCallback`, `UnsupportedStrictSchemaKeywordCheck`).
- `coding/mcpext` was missing from the coding-agent directory seeds (`LoadedMcpConfig`, `McpServerEntry`).
- `TestDesignedOutAndCLIRows` did not exercise designed-out or CLI rows; it does now. The designed-out, fewest-gaps, M2 accessor, V1 value and json-tag rules had no failing test; each has one now.

## Trust check 2: seeded gaps

`fixture_test.go` builds a one-package module with a small inventory (a function, an interface with data, function and hook properties, a class with a constructor, a string alias, a value and a literal constant). The baseline has no gap. Nineteen seeds each break one thing and the test requires exactly the expected gaps and no other: remove a field (`member-missing`, parent `child-gap`), rename a method, change a parameter type, drop a parameter, drop the result, rename the function (`no-go-symbol`), drop the test reference (`not-exercised`), keep the reference but drop the assertion (`not-exercised`), change a field type, substitute an unrelated named type (`undecidable`), remove a literal constant from a string alias (`type-mismatch`), rename the constructor, remove a class accessor, remove a function property that mentions `AbortSignal`, change the signature of a function property with no call row, turn a value into a function with parameters, change a literal constant value, give a data accessor a parameter, and offer an unexported type that fits better than the exported one. **19 of 19 caught.** Further tests: a caller reachable from `cmd/pig` exercises a symbol without a test; a type named only in a reachable signature is used; designed-out and CLI rows; the fewest-gaps candidate across seed directories; a documented rename resolves a renamed function; determinism; the baseline check fails only on growth; the type, signature, signal, name, ID and alias-body rules in tables (`rules_test.go`).

## Limits and next steps

- `member-missing` (678) is dominated by coding-agent (324) and tui (176): Go types that lack the upstream member, the real shape gaps for the lanes (`make go-stub-fields` generates plain data members).
- `undecidable` (355) is mostly an upstream type whose Go counterpart has no documented rename, or a shape outside the rules (`keyof X`, `X[K]`, an options bag passed as one bare Go value). Add the rule or the documented rename; a rename never closes a gap unless the shape and exercise rules also pass.
- `renames.json` holds renames taken from hand closures and is not reviewed beyond that. `renames-reviewed.json` and `representations.json` are reviewed.
- E1 accepts any asserting test that references the symbol; it does not check that the assertion concerns the symbol's behaviour. NOT-A-GAP means a compatible, exercised Go symbol exists, not that the behaviour is correct.
- `checkBaseline` fails only when a gap is not in the baseline; refresh it with `make interface-gaps-update` whenever gaps close. `-check` also requires the committed mapping to equal the derivation.
- Not yet wired: the optionality checks as a drift report (they are stricter than the ledger needs), the sealed-interface and tagged-struct union checks, and the placement family (the engine's placement hook passes only the upstream package, not the file).
- `closed-open-children.tsv` is empty: the 231 parents of a gap child all have a gap child that is itself a hand-closed row, so they stay disagreements until those rows are fixed or reopened.

## ExtensionAPI under rule L7 (decision of the rule owner on lg-help-3 ee3506b19)

`extension.API` is implemented only by `extensiontest.Fake`; real extensions run through the SDKs and the host wire. A test of the Fake never closes an `ExtensionAPI` row (L7). The row closes on a cross-SDK conformance test (`test/extension-conformance/` or `TestConformance*`) whose body uses the upstream member name as an identifier or string literal (for an overload row, also the overload literal such as the event name) and whose comments cite a Pi file:line of a file that names the member. Option 1 of the request (a reviewed placement onto `sdk.Extension` members) is not needed, because the evidence binds the row to the conformance test directly; option 2 (delete `extension.API`) is a break of a public Go API and needs an owner decision, so it is not made here. Rows the conformance suite does not name stay `not-exercised` and are a test or port task for the extension lanes.

## Reviewed decisions are an input

Every hand-reviewed designed-out and divergence row lives in `test/parity/interface-closure/autobind/reviewed-decisions.json`, keyed by interface ID (one line per ID, so two lanes that add different decisions merge without a conflict). `autobind -update` and `-check` apply it to the mapping before they derive the other rows: a merge or a regeneration that leaves such a row pending or ported gets its decision back, so no lane re-applies rows after a merge. A decision carries the row's `upstreamShapeHash` and applies only while the upstream shape is unchanged; a changed shape leaves the row pending and the decision stale. A designed-out or divergence row that is only in the mapping, and that no rule derives, is an error that names the file; `autobind -update -import-decisions` moves such rows into it. Add or edit a decision in the file, not in the mapping.

## T9n: a narrow consumer-owned interface for a class from a package that would import-cycle

A Pi parameter typed as a class whose Go type lives in a package the consumer cannot import is satisfied by a consumer-owned Go interface when `narrow-interfaces.json` holds a reviewed entry keyed `<pkg>:<Class>` and the detector verifies every piece: the interface lists exactly the Go methods the listed Pi members map to (no extra, none missing), every listed Pi member is a member of the Pi class, the Go type of the Pi class implements the interface, a non-test file of that type's package holds `var _ Iface = (*Class)(nil)`, and the entry's `test` (`dir#TestName`) names the Go type of the class, so it passes the real producer. A failed check leaves the row a gap. Fixture table: `TestNarrowInterfaceStandsForAClassParameter` (refusals for each missing piece; every check mutation-checked).

## portmap-check: PORT_MAP ticks are derived, not hand-marked

`make portmap-check` (`test/parity/cmd/portmapcheck`, in `ci-contracts`, `check-contracts-fast` and the lane preflight) derives every ✅ row of `docs/parity/PORT_MAP.md`:

1. every exported member of the Pi file (interface inventory rows whose published declaration file is that file) is ported or designed-out in the ledger, and each ported member's Go target file is one the row cites; a cited non-test Go file that holds no mapped symbol fails;
2. a Pi file with no exported member needs a Go test carrying `// pi: <Pi file path>`;
3. a renamed implementation is allowed only through `test/parity/portmap-reviewed.json` (`{piFile: {reason, members: {interfaceID: "file#Symbol"}}}`), and the check verifies the symbol is defined in a cited file.

A ✅ row the ledger does not prove fails unless `test/parity/portmap-unproven-baseline.txt` lists it; the baseline only shrinks (a new unproven tick fails, so does a stale entry). `make portmap-report` lists every unproven tick with its reason; `go run ./test/parity/cmd/portmapcheck -generate` rewrites the status column (unproven ✅ becomes 🟡). 4. a proven row's evidence tests (the ledger evidence of its members, or its `// pi:` tests) run with `go test -count=1 -coverpkg` over the cited packages and execute statements of every mapped cited file; red evidence tests keep the row unproven. The result is cached under `build/portmap-coverage/`, keyed by a digest of the cited files, the test files and the test names, so the first run after a change re-runs the tests of the affected rows and a cached run is free. 5. a `// pi:` marker row (a Pi file with no ledger member) is proven only by rule 4 plus a red-proof: `test/parity/portmap-mutants.json` (`{piFile: [{file, old, new}]}`) lists reviewed one-site mutants of cited Go files; the check replays each through `go test -overlay` (the work tree is never touched; `old` must occur exactly once; a build error is not a kill) and needs at least one to fail the marker tests. Without a killed mutant the row stays MARKER-UNPROVEN.

## Behaviour-proof classes (owner-approved 2026-10-09)

`make ledger-check` runs `test/parity/interface-closure/breakdown/proofclass.py` after the gap check. Each closed row has a required proof class by kind: D design-out (a D-number or a probe, plus designout-audit parity/maintainability/performance columns in `docs/parity/designout-audit.tsv`), W wire method or event (an asserting test under `test/extension-conformance`, `test/wiring`, `extensions/` or `coding/extension` that names the member), R registration type (a field-survival test), U union (an exhaustive-handling test), F function or behaviour (an asserting Go test that names the symbol and is mutation-checked, via `unit-evidence/*.json` or a `portmap-mutants.json` mutant in the symbol's file, or is a Pi-oracle test), N data shape (none). The script prints the unproven count per package and in total, writes `build/ledger-proof-classes/unproven-behaviour.tsv` (the list that feeds idle lanes; a copy is `unproven-behaviour.tsv` here), and compares each package with `test/parity/interface-closure/gaps/proof-class-baseline.tsv`. It does not fail CI: `--enforce` turns the ratchet into a failure, and `--update-baseline` rewrites the baseline after a reduction. The design-out audit file does not exist on this branch, so every design-out row is unproven until it lands.
