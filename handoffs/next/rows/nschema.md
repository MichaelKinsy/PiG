# N-SCHEMA rows (new package pi-schema), delivered by NSchema

Branch `fix/follow-nschema` (base `dp/fix/follow-pi-next` @ `65411f70dd`). Upstream: Pi `packages/schema` at **`8ffea8a1bfdc32523ab056549b26141018c1e700`** (2026-10-31T21:59Z, "feat(schema): faster object checks and compile, less memory"; the range `fab2c535` .. `8ffea8a1` is ported at its final state; docs commits `81f3cd41`, `6d4b7443` are specs only).

New Go packages: `schema` (root, `github.com/MichaelKinsy/PiG/schema`), `schema/formats`, `schema/internal/ecmaregex` (PiG-only: the ECMAScript regexp engine `shared.ts` and `formats/typebox/regex.ts` get from V8), `schema/formats/internal/idna`. Nothing in ai/coding/chord uses the package (Pi does not use it yet; `ai/validate.go` still uses `internal/typebox`).

## (a) Upstream files
| path | disposition | PiG files | evidence | rationale |
|---|---|---|---|---|
| packages/schema/src/annotations.ts | ported | schema/annotations.go | test:schema/*_test.go#TestConformanceSuite (unevaluatedProperties/unevaluatedItems files), TestHandWrittenCaseErrorsMatchPi | closure compiler ported 1:1 (see design notes) |
| packages/schema/src/builders.ts | ported | schema/builders.go | test:schema/*_test.go#TestBuildersProducePlainJSONSchema, TestBuildersProduceEveryLeaf, TestBuildersAcceptEveryAnnotationAndBound | closure compiler ported 1:1 (see design notes) |
| packages/schema/src/checks.ts | ported | schema/checks.go | test:schema/*_test.go#TestConformanceSuite, TestErrorsMatchPi | closure compiler ported 1:1 (see design notes) |
| packages/schema/src/compiler.ts | ported | schema/compiler.go | test:schema/*_test.go#TestConformanceSuite, TestErrorsMatchPi, TestHandWrittenCaseErrorsMatchPi, BenchmarkCheck | closure compiler ported 1:1 (see design notes) |
| packages/schema/src/explain.ts | ported | schema/explain.go | test:schema/*_test.go#TestErrors, TestErrorsMatchPi, TestHandWrittenCaseErrorsMatchPi, TestSpeedCaseErrorsMatchPi | closure compiler ported 1:1 (see design notes) |
| packages/schema/src/resolver.ts | ported | schema/resolver.go | test:schema/*_test.go#TestConformanceSuite (ref*, id, anchor, dynamicRef files), TestHandWrittenCaseErrorsMatchPi (URL cases) | closure compiler ported 1:1 (see design notes) |
| packages/schema/src/shared.ts | ported | schema/shared.go + schema/internal/ecmaregex/*.go | test:schema/*_test.go#TestSharedChecks, TestGoldenRegex (ecmaregex) | closure compiler ported 1:1 (see design notes) |
| packages/schema/src/types.ts | ported | schema/types.go | test:schema/*_test.go#compile-time through every test | closure compiler ported 1:1 (see design notes) |
| packages/schema/src/validator.ts | ported | schema/validator.go | test:schema/*_test.go#TestErrors, TestValues, TestOptions, TestSchemas | closure compiler ported 1:1 (see design notes) |
| packages/schema/src/index.ts | designed-out | n/a | n/a | re-export barrel: the Go exports are the package-level identifiers of schema |
| packages/schema/src/formats/index.ts | ported | schema/formats/index.go | test:schema/formats/formats_test.go#TestFormatsNames, #TestFormatsReturnsFreshMap | `formats` (frozen record) becomes `Formats()`, a fresh map per call |
| packages/schema/src/formats/typebox/date.ts | ported | schema/formats/typebox_date.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/conformance_test.go#TestConformance | `Is{Name}` as Pi exports it |
| packages/schema/src/formats/typebox/date_time.ts | ported | schema/formats/typebox_date_time.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/conformance_test.go#TestConformance | `Is{Name}` as Pi exports it |
| packages/schema/src/formats/typebox/duration.ts | ported | schema/formats/typebox_duration.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/conformance_test.go#TestConformance | `Is{Name}` as Pi exports it |
| packages/schema/src/formats/typebox/email.ts | ported | schema/formats/typebox_email.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/conformance_test.go#TestConformance | `Is{Name}` as Pi exports it |
| packages/schema/src/formats/typebox/hostname.ts | ported | schema/formats/typebox_hostname.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/conformance_test.go#TestConformance | `Is{Name}` as Pi exports it |
| packages/schema/src/formats/typebox/idn_email.ts | ported | schema/formats/typebox_idn_email.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/conformance_test.go#TestConformance | `Is{Name}` as Pi exports it |
| packages/schema/src/formats/typebox/idn_hostname.ts | ported | schema/formats/typebox_idn_hostname.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/conformance_test.go#TestConformance | `Is{Name}` as Pi exports it |
| packages/schema/src/formats/typebox/ipv4.ts | ported | schema/formats/typebox_ipv4.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/conformance_test.go#TestConformance | `Is{Name}` as Pi exports it |
| packages/schema/src/formats/typebox/ipv6.ts | ported | schema/formats/typebox_ipv6.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/conformance_test.go#TestConformance | `Is{Name}` as Pi exports it |
| packages/schema/src/formats/typebox/iri.ts | ported | schema/formats/typebox_iri.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/conformance_test.go#TestConformance | `Is{Name}` as Pi exports it |
| packages/schema/src/formats/typebox/iri_reference.ts | ported | schema/formats/typebox_iri_reference.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/conformance_test.go#TestConformance | `Is{Name}` as Pi exports it |
| packages/schema/src/formats/typebox/json_pointer.ts | ported | schema/formats/typebox_json_pointer.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/conformance_test.go#TestConformance | `Is{Name}` as Pi exports it |
| packages/schema/src/formats/typebox/regex.ts | ported | schema/formats/typebox_regex.go | test:schema/formats/conformance_test.go#TestConformance (regex.json), schema/internal/ecmaregex#TestFormatRegex | `Is{Name}` as Pi exports it |
| packages/schema/src/formats/typebox/relative_json_pointer.ts | ported | schema/formats/typebox_relative_json_pointer.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/conformance_test.go#TestConformance | `Is{Name}` as Pi exports it |
| packages/schema/src/formats/typebox/time.ts | ported | schema/formats/typebox_time.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/conformance_test.go#TestConformance | `Is{Name}` as Pi exports it ; `IsTimeWithZone(value, strictTimeZone)` carries Pi's optional second argument |
| packages/schema/src/formats/typebox/uri.ts | ported | schema/formats/typebox_uri.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/conformance_test.go#TestConformance | `Is{Name}` as Pi exports it |
| packages/schema/src/formats/typebox/uri_reference.ts | ported | schema/formats/typebox_uri_reference.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/conformance_test.go#TestConformance | `Is{Name}` as Pi exports it |
| packages/schema/src/formats/typebox/uri_template.ts | ported | schema/formats/typebox_uri_template.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/conformance_test.go#TestConformance | `Is{Name}` as Pi exports it |
| packages/schema/src/formats/typebox/uuid.ts | ported | schema/formats/typebox_uuid.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/conformance_test.go#TestConformance | `Is{Name}` as Pi exports it |
| packages/schema/src/formats/typebox/idna/hostname.ts | ported | schema/formats/internal/idna/hostname.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/internal/idna/classes_test.go#TestPatternClassesMatchPi | idna is `schema/formats/internal/idna` because `idna/hostname.ts` and `hostname.ts` both export `IsHostname` |
| packages/schema/src/formats/typebox/idna/idn-hostname.ts | ported | schema/formats/internal/idna/idn_hostname.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/internal/idna/classes_test.go#TestPatternClassesMatchPi | idna is `schema/formats/internal/idna` because `idna/hostname.ts` and `hostname.ts` both export `IsHostname` |
| packages/schema/src/formats/typebox/idna/label/ascii.ts | ported | schema/formats/internal/idna/label_ascii.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/internal/idna/classes_test.go#TestPatternClassesMatchPi | idna is `schema/formats/internal/idna` because `idna/hostname.ts` and `hostname.ts` both export `IsHostname` |
| packages/schema/src/formats/typebox/idna/label/puny.ts | ported | schema/formats/internal/idna/label_puny.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/internal/idna/classes_test.go#TestPatternClassesMatchPi | idna is `schema/formats/internal/idna` because `idna/hostname.ts` and `hostname.ts` both export `IsHostname` |
| packages/schema/src/formats/typebox/idna/label/unicode.ts | ported | schema/formats/internal/idna/label_unicode.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/internal/idna/classes_test.go#TestPatternClassesMatchPi | idna is `schema/formats/internal/idna` because `idna/hostname.ts` and `hostname.ts` both export `IsHostname` |
| packages/schema/src/formats/typebox/idna/format/bidi.ts | ported | schema/formats/internal/idna/format_bidi.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/internal/idna/classes_test.go#TestPatternClassesMatchPi | idna is `schema/formats/internal/idna` because `idna/hostname.ts` and `hostname.ts` both export `IsHostname` |
| packages/schema/src/formats/typebox/idna/format/puny.ts | ported | schema/formats/internal/idna/format_puny.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/internal/idna/classes_test.go#TestPatternClassesMatchPi | idna is `schema/formats/internal/idna` because `idna/hostname.ts` and `hostname.ts` both export `IsHostname` |
| packages/schema/src/formats/typebox/idna/pattern/pattern.ts | ported | schema/formats/internal/idna/pattern.go | test:schema/formats/oracle_test.go#TestOracleCorpus, schema/formats/internal/idna/classes_test.go#TestPatternClassesMatchPi | idna is `schema/formats/internal/idna` because `idna/hostname.ts` and `hostname.ts` both export `IsHostname` |
| packages/schema/src/formats/typebox/idna/index.ts | designed-out | n/a | n/a | re-export of idna/hostname.ts and idna/idn-hostname.ts; Go package idna exports them directly |

All 39 `packages/schema/src/**` files are covered (10 root, 29 formats). Not tracked source and not ported: `packages/schema/{README.md,CHANGELOG.md,package.json,tsconfig.build.json,vitest.config.ts,LICENSES}` (the Go package has its own `schema/README.md`; TypeBox's license text is already in THIRD_PARTY_NOTICES.md).

## (b) PORT_MAP rows (new section; add `schema` to `test/parity/upstreampackages/packages.json` and the package scope list)
Section heading: ## `packages/schema/src/`; columns as the other sections.

| upstream | pig | status |
|---|---|---|
| `packages/schema/src/annotations.ts` | `schema/annotations.go` | ✅ |
| `packages/schema/src/builders.ts` | `schema/builders.go` | ✅ |
| `packages/schema/src/checks.ts` | `schema/checks.go` | ✅ |
| `packages/schema/src/compiler.ts` | `schema/compiler.go` | ✅ |
| `packages/schema/src/explain.ts` | `schema/explain.go` | ✅ |
| `packages/schema/src/resolver.ts` | `schema/resolver.go` | ✅ |
| `packages/schema/src/shared.ts` | `schema/shared.go + schema/internal/ecmaregex/*.go` | ✅ |
| `packages/schema/src/types.ts` | `schema/types.go` | ✅ |
| `packages/schema/src/validator.ts` | `schema/validator.go` | ✅ |
| `packages/schema/src/index.ts` | `(re-export barrel: the exports are the Go package surface)` | n/a |
| `packages/schema/src/formats/index.ts` | `schema/formats/index.go` | ✅ |
| `packages/schema/src/formats/typebox/date.ts` | `schema/formats/typebox_date.go` | ✅ |
| `packages/schema/src/formats/typebox/date_time.ts` | `schema/formats/typebox_date_time.go` | ✅ |
| `packages/schema/src/formats/typebox/duration.ts` | `schema/formats/typebox_duration.go` | ✅ |
| `packages/schema/src/formats/typebox/email.ts` | `schema/formats/typebox_email.go` | ✅ |
| `packages/schema/src/formats/typebox/hostname.ts` | `schema/formats/typebox_hostname.go` | ✅ |
| `packages/schema/src/formats/typebox/idn_email.ts` | `schema/formats/typebox_idn_email.go` | ✅ |
| `packages/schema/src/formats/typebox/idn_hostname.ts` | `schema/formats/typebox_idn_hostname.go` | ✅ |
| `packages/schema/src/formats/typebox/ipv4.ts` | `schema/formats/typebox_ipv4.go` | ✅ |
| `packages/schema/src/formats/typebox/ipv6.ts` | `schema/formats/typebox_ipv6.go` | ✅ |
| `packages/schema/src/formats/typebox/iri.ts` | `schema/formats/typebox_iri.go` | ✅ |
| `packages/schema/src/formats/typebox/iri_reference.ts` | `schema/formats/typebox_iri_reference.go` | ✅ |
| `packages/schema/src/formats/typebox/json_pointer.ts` | `schema/formats/typebox_json_pointer.go` | ✅ |
| `packages/schema/src/formats/typebox/regex.ts` | `schema/formats/typebox_regex.go` | ✅ |
| `packages/schema/src/formats/typebox/relative_json_pointer.ts` | `schema/formats/typebox_relative_json_pointer.go` | ✅ |
| `packages/schema/src/formats/typebox/time.ts` | `schema/formats/typebox_time.go` | ✅ |
| `packages/schema/src/formats/typebox/uri.ts` | `schema/formats/typebox_uri.go` | ✅ |
| `packages/schema/src/formats/typebox/uri_reference.ts` | `schema/formats/typebox_uri_reference.go` | ✅ |
| `packages/schema/src/formats/typebox/uri_template.ts` | `schema/formats/typebox_uri_template.go` | ✅ |
| `packages/schema/src/formats/typebox/uuid.ts` | `schema/formats/typebox_uuid.go` | ✅ |
| `packages/schema/src/formats/typebox/idna/hostname.ts` | `schema/formats/internal/idna/hostname.go` | ✅ |
| `packages/schema/src/formats/typebox/idna/idn-hostname.ts` | `schema/formats/internal/idna/idn_hostname.go` | ✅ |
| `packages/schema/src/formats/typebox/idna/label/ascii.ts` | `schema/formats/internal/idna/label_ascii.go` | ✅ |
| `packages/schema/src/formats/typebox/idna/label/puny.ts` | `schema/formats/internal/idna/label_puny.go` | ✅ |
| `packages/schema/src/formats/typebox/idna/label/unicode.ts` | `schema/formats/internal/idna/label_unicode.go` | ✅ |
| `packages/schema/src/formats/typebox/idna/format/bidi.ts` | `schema/formats/internal/idna/format_bidi.go` | ✅ |
| `packages/schema/src/formats/typebox/idna/format/puny.ts` | `schema/formats/internal/idna/format_puny.go` | ✅ |
| `packages/schema/src/formats/typebox/idna/pattern/pattern.ts` | `schema/formats/internal/idna/pattern.go` | ✅ |
| `packages/schema/src/formats/typebox/idna/index.ts` | `(re-export barrel: the exports are the Go package surface)` | n/a |

## (c) Upstream tests
| upstream test | disposition | PiG test | note |
|---|---|---|---|
| packages/schema/test/conformance.test.ts | ported | schema/conformance_upstream_test.go#TestConformanceSuite | same KNOWN_FAILURES list (9 tests, must fail); each case also checks "errors agree with check". Same pass/fail per case as Pi |
| packages/schema/test/suite.ts | ported | schema/suite_test.go | fixture loader |
| packages/schema/test/fixtures/** (JSON-Schema-Test-Suite, meta-schemas) | ported | schema/testdata/{json-schema-test-suite,meta-schemas} | vendored unmodified (192 files); REUSE.toml + THIRD_PARTY_NOTICES.md updated |
| packages/schema/test/validator.test.ts | ported (2 adaptations) | schema/validator_upstream_test.go#TestErrors, #TestValues, #TestOptions, #TestSchemas | "treats a property set to undefined as absent": a Go value has no `undefined`; the absent-member case is ported instead. TypeBox library test: the JSON TypeBox emits for the same `Type.Object` is compiled (TypeBox's runtime is a TS library) |
| packages/schema/test/builders.test.ts | ported (type-level parts designed-out) | schema/builders_upstream_test.go | the `expectTypeOf<Static<...>>` assertions are TypeScript compile-time checks with no Go form; their runtime halves (the check narrows, JSON shapes, compile semantics) are ported |
| packages/schema/test/shared.test.ts (at 8ffea8a1) | ported | schema/shared_upstream_test.go#TestSharedChecks | module-level bare-type check shared (function identity by code pointer); 50 patterns cached, freed by GC with their validators except the one a live validator still uses (`weak` + `runtime.AddCleanup`; bounded GC polling because cleanup is asynchronous) |
| packages/schema/test/bench/{cases,speed,setup,memory,corpus}.ts | ported | schema/bench_test.go (BenchmarkCheck, BenchmarkSetup, BenchmarkMemory), schema/corpus_test.go | cases come from `testdata/errors-golden.json` (generated from `cases.ts`); the corpus generator is ported and `TestCorpusMatchesPi` compares sha256 digests of corpus(200,42), (100,7), (1000,99) with Pi's |
| packages/schema/test/bench/{candidates,conformance,size,all}.ts | designed-out | n/a | cross-library comparison with TypeBox/cfworker/omptype, the bundle size, the no-eval run and the runner script have no Go counterpart; Go has no eval or bundles; conformance is TestConformanceSuite |

PiG-only evidence tests: `schema/errors_golden_test.go` (TestErrorsMatchPi: 4,189 results, TestSpeedCaseErrorsMatchPi, TestHandWrittenCaseErrorsMatchPi: 47 hand-written cases) replays Pi's exact check results and errors (paths, keywords, messages, order, compile-error text) from `schema/testdata/errors-golden.json`, generated by `schema/testdata/gen-errors.mjs` under Node 24 against Pi 8ffea8a1; `schema/formats` (oracle corpus of 7,847 Pi rows, Unicode class tables vs Node), `schema/internal/ecmaregex` (1,964 patterns x 153 inputs and 1,714 property expressions vs Node 24.22.1). A mutation of a message and of the anyOf rule fails the golden tests.

## (d) Async contracts
None: `packages/schema/src/**` has no `async`, Promise, timer or event-loop use (WeakRef and FinalizationRegistry in `shared.ts` are synchronous GC hooks; Go form: `weak.Pointer` + `runtime.AddCleanup`). Concurrency contract added by the Go form: `Validator` is safe for concurrent use; a validator whose schema has a bookended `$dynamicRef` serializes `Check` (the dynamic scope stack is shared mutable state), and `Errors` always serializes the explanation (compiler caches).

## (e) New public Go API (interface IDs; all PiG-only except the Pi exports mapped below)
`schema`: `Compile(schema, CompileOptions) (*Validator, error)`, `Validator` (`Check(value) (bool, error)`, `Errors(value) ([]ValidationError, error)`), `ObjOption`, `CompileOptions`, `Draft`, `Draft202012`, `Draft7`, `ValidationError`, `Check`, `FormatCheck`, `JsonSchema`, `Object`, `Schema`, `Option`, `Title`, `Description`, `Examples`, `Deprecated`, `Default`, `MinLength`, `MaxLength`, `Pattern`, `Format`, `Minimum`, `Maximum`, `ExclusiveMinimum`, `ExclusiveMaximum`, `MultipleOf`, `MinItems`, `MaxItems`, `UniqueItems`, `Open`, `Str`, `Num`, `Int`, `Bool`, `Nul`, `Unknown`, `Lit`, `OneOf`, `Union`, `Nullable`, `Arr`, `Tuple`, `Map`, `Bytes`, `Obj`, `Property`, `Properties`, `Prop`, `Opt`. `schema/formats`: `Formats`, `IsDate`, `IsDateTime`, `IsDuration`, `IsEmail`, `IsHostname`, `IsIdnEmail`, `IsIdnHostname`, `IsIPv4`, `IsIPv6`, `IsIri`, `IsIriReference`, `IsJsonPointer`, `IsRegex`, `IsRelativeJsonPointer`, `IsTime`, `IsTimeWithZone`, `IsUri`, `IsUriReference`, `IsUriTemplate`, `IsUuid`.

Pi exports to Go: `compile` -> `Compile(schema, CompileOptions) (*Validator, error)` (throws -> error); `Validator.check` -> `Validator.Check(value) (bool, error)`; `Validator.errors` -> `Validator.Errors(value) ([]ValidationError, error)` (the error is exactly where Pi's check/errors throw: see note 8); builders `str num int bool nul unknown lit oneOf arr tuple map union nullable bytes obj opt` -> `Str Num Int Bool Nul Unknown Lit OneOf Arr Tuple Map Union Nullable Bytes Obj Prop/Opt`; `JsonSchema`, `FormatCheck`, `CompileOptions` (`draft`, `formats`, `schemas` -> `Draft`, `Formats`, `Schemas`), `ValidationError`, `Check`, `Schema`. `designed-out` type-only exports (TypeScript compile-time types): `Static`, `StaticObject`, `Annotations`, `LengthBounds`, `NumberBounds`, `Properties` (Go `Properties` is an ordered slice, because Go maps have no order), `Optional` (Go: `Property.Optional`, `Opt`).

## Design notes and open questions
1. **`compiler.ts` is not a code generator**: Pi's compiler already builds closures without `eval` (that is the package's point), so it is ported as closures, file for file, and the observable results are proven against Pi (golden). What is designed out is V8 machinery: leaf-code inlining and the `INHERITED`/Object.prototype flags (a Go map has no prototype), and `declaredObject` looks declared names up instead of walking `for...in` (same results, no allocation).
2. **Value model**: nil, bool, float64 (any Go numeric kind), string, []any, `*Object` (= `chordjson.Object`, JS property order, the model `chord.JsonValue` uses), `map[string]any` (keys in JS own-key order: array-index keys ascending then the rest ascending, since a map has no insertion order), `[]byte` = Uint8Array. No `undefined`. Strings are UTF-8 (a lone surrogate cannot occur). Schemas and values share the model; object identity (Pi's `Map<object,...>`) is pointer identity of the `*Object` or map.
3. **Builders**: Pi's option objects spread into the schema; Go uses `Option` values applied in call order (members appear in the order written, as in the JS spread). `Obj` takes an ordered `Properties` slice (`Prop`, `Opt`) because the order of `properties`, `required` and of reported errors is observable.
4. **Regular expressions** (`schema/internal/ecmaregex`): ES `u` flag, then Annex B fallback as `sharedRegex` does, translated to `dlclark/regexp2`; ES2025 modifier groups, V8's mid-surrogate-pair start rule, V8's repeated-group-name rule, lone surrogates (WTF-8), V8's exact `Invalid regular expression: /src/flags: Reason` errors; zero allocations per match. Unicode properties from Go's tables (Unicode 17.0.0 under the tern-ci toolchain go1.27.3, equal to Node 24.22.1's; the property and case goldens log `runtime.Version()` and `unicode.Version` and name both on failure) plus generated Script_Extensions deltas and 16 properties Go lacks (Emoji*, Changes_When_*, XID_*, ...). Goldens: 3,821 patterns x 219 inputs, 1,714 property expressions, case-fold classes, all generated under Node 24.22.1. No `MatchTimeout` is set (parity with V8; catastrophic backtracking on untrusted patterns is possible, as in Pi).
   **Remaining differences from V8 (pinned by `TestKnownGaps`; exact rows in the package's `knownGaps`; the owner takes any divergence):**
   - Backreference inside a case-insensitive modifier group compares with regexp2's `unicode.ToLower`, V8 with Canonicalize. V8 true / ours false: `^(?i:(\w)\1)$` on "s\u017f" (u); `^(?i:(s)\1)$` and `^(?i:(\u017f)\1)$` on "s\u017f" (u); `^(?i:(\u03c3)\1)$` and `^(?i:(\u03c2)\1)$` on "\u03c3\u03c2" and "\u03c2\u03c3" (u and Annex B). V8 false / ours true: `^(?i:(\w)\1)$` and `^(?i:(i)\1)$` on "i\u0130" (u, Annex B); `^(?i:(\u0130)\1)$` on "\u0130i"; `^(?i:(\w)\1)$`, `^(?i:(k)\1)$` on "k\u212a" and `^(?i:(\u212a)\1)$` on "\u212ak" (Annex B). Cannot be expressed in a translation: the comparison is inside regexp2.
   - Three Annex B patterns whose V8 result has no deterministic rule in V8's source: `(?i:[\n\p{Lu}\x62]|[^a-c\u{1F600}x-z])\W\u0061` on "A\u03c3a\u00b5" (V8 true, ours false); `(?im-s:(?<n1>(?:)()\s{|\u212a[\n\0K]()?)(?:()))` on "\u212ak" (V8 false, ours true); `(?ims:(?m:(?=\aΩ\c){,2}\n)(?<n1>\b(?=xx??)\d)|\/(\w)[aA]|[\u0061a-c\cA])+[^\ude00]` on "AB" (V8 false, ours true).
   - V8 decides some modifier-group results by whether the subject is stored one byte per unit; ours uses "all units <= U+00FF". A subject V8 holds as two-byte with only Latin-1 units can answer differently (`(?i-:[\-c\D]?\u212a{2})` on "kK").
5. **URL resolution** (`resolveUri`): `nodeurl.ResolveReference` (new in `internal/nodeurl/reference_any.go`: `new URL(input, base)` for special, file, non-special hierarchical and opaque bases), compared with Node 24.22.1 on 1,081 base/reference probes (`TestResolveReferenceMatchesNode`: drive letters, `file://localhost`, backslashes, dot segments, default ports, URNs). Schema golden cases cover file:///C:, backslash, file://localhost, custom schemes and URNs.
6. **Concurrency**: see (d). Go has no single-threaded guarantee; the weak caches use mutexes.
7. **Owner calls / integrator**: (i) `ai/validate.go` and chord keep `internal/typebox`; Pi announced the switch to pi-schema (no TypeBox re-export) but it is not in the range; the API (`Compile` -> `Check`/`Errors` with JSON Pointer paths) is shaped for that move. (ii) The `ecmaregex` files carry no `// Ports` header (there is no single upstream file; the package doc names `shared.ts` and `formats/typebox/regex.ts`): add the PORT_MAP `shared.ts` row note if wanted. (iii) `make generate` first failing step and gate results are in the lane report. (iv) Touched outside the package, a new file only: `internal/nodeurl/reference_any.go` and `reference_any_oracle_test.go` (generalizes `ResolveURL` to every base; `ResolveURL` is unchanged). (v) The pin: next's `.upstream/current` is e7a0df27; the schema rows need the integrator's pin bump to 8ffea8a1 (or later) before `port-map-drift`/`upstream-delta` can see `packages/schema/src`.
8. **Pi's throws become errors at the same operation.** `Check` and `Errors` return an error exactly where Pi's `check` and `errors` throw, so no panic leaves the package for a schema `Compile` accepted: a `$dynamicRef` target compiled on first use that is malformed or unresolved (`Check`: `{"$id":"https://ex/root","$ref":"list","$defs":{"bad":{"$dynamicAnchor":"items","$ref":"#/nope"},...}}` gives `Unresolved $ref #/nope in https://ex/root`), and the explanation reading a subschema the check never compiled (`{"if":5,"minimum":3}`: `Errors(1)` gives `A schema must be an object or a boolean, got 5`). Both are golden cases generated from Pi. A failed lazy compile resets the validator's compile state. Owner call: the error returns make `Check` unusable as a bare predicate (`filter(check)`); the alternative is a `bool`-only `Check` that moves these two throws into `Compile`, which Pi does not do.
9. **Strings.** A lone UTF-16 unit is WTF-8 in a Go string (the `internal/jsstring` convention); lengths, the `.` of a pattern and messages treat it as JavaScript does (golden cases from Node 24). The code point count mirrors Pi's `codePoints` exactly, including its quirk: any high surrogate unit consumes the next unit (`"\uD800a"` counts 1; `maxLength: 1` accepts `"\uD800\uD800"`). Parity kept; flag as an upstream bug if the owner wants it reported.
10. **Review follow-ups** (`handoffs/next/reviews/nschema.md` at b8f2f13183): B1, B2, S1, S2, S3 (benchmarks: 0 allocs/op on every speed case; before: nested object 1 alloc/144 B, MCP create issue 2/72 B, MCP edit file 1/48 B, MCP create page 2/192 B), S4, S5 (`Compile(schema, options)`), N1-N3, N5-N10 done; N4 in the package doc; N11 (`ecmaregex` to `internal/`) left to the owner.

## DRAFT divergence text for the owner (not recorded in docs/parity/DIVERGENCES.md; no `D<N>` assigned)

**Scope:** `schema/internal/ecmaregex`, the ECMAScript regular-expression engine under `pattern` and `patternProperties` (Pi: `packages/schema/src/shared.ts`) and the `regex` format (`formats/typebox/regex.ts`). PiG translates a pattern to `dlclark/regexp2`; Pi runs V8. Oracle: Node 24.22.1 (`.node-version`). `TestKnownGaps` pins each row below on both sides, so a change of either side fails the test. Every other row of the 3,821-pattern golden agrees with V8.

**Observable difference.** A schema whose `pattern` or `patternProperties` key uses a case-insensitive modifier group `(?i:...)` (ES2025; Node 22 rejects it, so Pi on Node 22 throws at compile) with a backreference, or one of the three Annex B patterns below, can accept or reject a string that Pi accepts or rejects. No pattern outside modifier groups and no u-mode pattern without `(?i:)` differs.

**1. Backreference inside a case-insensitive modifier group.** V8 compares the captured text with the backreference by ECMAScript Canonicalize; regexp2 compares with `unicode.ToLower`. A translation cannot change the comparison, because it runs inside regexp2.

| mode | pattern | input | V8 | PiG |
|---|---|---|---|---|
| u | `^(?i:(\w)\1)$` | `"s\u017f"` | true | false |
| u | `^(?i:(s)\1)$` | `"s\u017f"` | true | false |
| u | `^(?i:(\u017f)\1)$` | `"s\u017f"` | true | false |
| u, annexB | `^(?i:(\u03c3)\1)$` | `"\u03c3\u03c2"` | true | false |
| u, annexB | `^(?i:(\u03c2)\1)$` | `"\u03c2\u03c3"` | true | false |
| u, annexB | `^(?i:(.)\1)$` | `"\u03c3\u03c2"`, `"\u03c2\u03c3"` (and `"s\u017f"` in u) | true | false |
| u, annexB | `^(?i:(\w)\1)$` | `"i\u0130"` | false | true |
| u, annexB | `^(?i:(i)\1)$` | `"i\u0130"` | false | true |
| u, annexB | `^(?i:(\u0130)\1)$` | `"\u0130i"` | false | true |
| u, annexB | `^(?i:(.)\1)$` | `"i\u0130"`, `"\u0130i"` | false | true |
| annexB | `^(?i:(\w)\1)$` | `"k\u212a"` | false | true |
| annexB | `^(?i:(k)\1)$` | `"k\u212a"` | false | true |
| annexB | `^(?i:(\u212a)\1)$` | `"\u212ak"` | false | true |
| annexB | `^(?i:(.)\1)$` | `"k\u212a"`, `"\u212ak"` | false | true |

**2. Three Annex B patterns with no deterministic V8 rule.** The author read V8's regexp source and found no rule that explains these results.

| mode | pattern | input | V8 | PiG |
|---|---|---|---|---|
| annexB | `(?i:[\n\p{Lu}\x62]\|[^a-c😀x-z])\W\u0061` | `"A\u03c3a\u00b5"` | true | false |
| annexB | `(?im-s:(?<n1>(?:)()\s{\|\u212a[\n\0K]()?)(?:()))` | `"\u212ak"` | false | true |
| annexB | `(?ims:(?m:(?=\aΩ\c){,2}\n)(?<n1>\b(?=xx??)\d)\|\/(\w)[aA]\|[\u0061a-c\cA])+[^\ude00]` | `"AB"` | false | true |

These need `(?i:...)` with Annex B syntax (a lone `{`, `\a`, `\c`, `\ude00`); they only arise when the u-mode compile of the same source fails.

**3. One-byte subject storage.** V8 decides some modifier-group results by whether the subject string is stored one byte per unit. PiG uses "every unit is <= U+00FF". A subject V8 holds as two-byte although it has only Latin-1 units (for example one built from a wider string) can get the other answer: `(?i-:[\-c\D]?\u212a{2})` on `"kK"` gave both results in one V8 run depending on how the subject was built. There is no PiG result to compare, because the V8 result is not a function of the string value.

**Proposed disposition.** Record one numbered divergence for `schema/internal/ecmaregex`: "`(?i:...)` backreferences compare by simple lowercase, not by Canonicalize; three contrived Annex B patterns and one-byte-storage-dependent results differ". Remove-when: regexp2 gains Canonicalize-based case-insensitive backreference comparison, or PiG gets its own backtracking engine for the ECMAScript grammar. Source markers: `// pig divergence (D<N>): ...` at the case-insensitive backreference translation in `schema/internal/ecmaregex/emit.go` and `casefold.go`. Parity coverage: `TestKnownGaps` (`schema/internal/ecmaregex/golden_test.go`) plus the golden replay. Alternative: leave undocumented as ordinary translation limits, as the ledger does for the Unicode property tables; the owner decides.
