# Lane D1: type-level upstream cases (owner rule: designed out only with a verified Go mechanism, else a runtime test)

Branch `fix/follow-d1c` on `fix/follow-pi-next` 8a853d6ea3. Compile-error texts below come from scratch files run on xd670 in tern-ci:v6 (Go 1.27.3) and deleted afterwards. "Red" for tests of behavior the production code already has is a mutation of the production code in a throwaway tree copy (the shared tree never held a mutation); the characterization tests of `data` pass at once.

## Summary

| case | result |
|---|---|
| contract.test: Implementation (one variant) / Implementation of a local contract is flat / ParamsOf | ported: `TestDerivedShapes` (Go does not type them: `VariantImplementation` is untyped maps, `ParamsOf` a `*delta.JsonObject`) |
| declare.test: remote without a host address | ported: `TestDeclarationTypeChecksAtRuntime` (`RemoteDeclaration{}` without `HostAddress` compiles) |
| declare.test: keyed member in a remote contract | designedOut, mechanism: `Keyed()` returns `*KeyedMember`, not a `TSchema`, so `Prop`/`VariantDeclaration.State`/`RemoteEditor.State` reject it at compile time; the upstream runtime throw is unreachable |
| evolve.test: the types track every step | ported: `TestEvolveHistory` |
| host-kind.test: hostAddress typed by params | ported: `TestHostAddressParams` |
| history.test (3) | ported: `TestRuntimeHistory` (2, pre-existing), `TestChangesAfter` |
| args.test: call replacement's arguments are values | designedOut, mechanism: `CheckedArgs.Call` is `contracts.CallFunc`; wrong signatures fail to compile (below); `TestCallReplacementArgumentsAreValues` pins it |
| errors.test: tagged union, wrong pairing is a type error | ported (runtime half) and **NOT enforced by Go**: `ChordErrorInit{Code: CodeInvalid}` etc. compile; upstream's constructor does not check at runtime either; `ChordErrorFromJson` rejects them (`TestChordErrorConstructorKeepsAWrongPairing`) |
| validator.test: leave nothing in TypeBox's build accumulators | ported: `TestValidatorCompileLeavesNoBuildState` (no module-level build state in `validator.go` `compile`) |
| native-source.test: types a derive's sources | moved from designedOut to **missing** (host `Self.derive`/`Self.open` typing and the "not a Chord watchable" refusal belong to the host port); state half ported: `TestDeriveRefusesUnresolvedSource`; `NewDerive` takes `[]any`, so Go does not enforce the upstream constraint |

Details per case follow (agent reports, verbatim).

## contracts

# D1TLContracts (chord/next/contracts) — all new tests in chord/next/contracts/derived_runtime_test.go
Owner-rule finding: Go does NOT type Implementation/ParamsOf/HostAddress/history (VariantImplementation = maps of untyped
values, MethodFunction takes delta.JsonValue, HostAddress takes *delta.JsonObject, Versions is []VersionData), so these are
PORTED as runtime tests, none "designedOut". Red = temporary mutation of production in a throwaway copy tree
(xd670 follow-d1c-mutcontracts, since deleted); the shared tree never held a mutation. Green: `go test -race -count=1
./chord/next/contracts/` => ok.

contract.test.ts › derived types › Implementation: one variant with its members, state watchables and methods taking args and a context
  ported: TestDerivedShapes/"Implementation: one variant with its members, state members and methods taking args" (variants editor/reader, editor state notes + methods add/clear, TakesNoArgs, reader has state only)
  red: TakesNoArgs mutated (contract.go Len()==1 -> 2): "derived_runtime_test.go:42: clear takes args"
  green: ok

contract.test.ts › derived types › Implementation of a local contract is flat
  ported: TestDerivedShapes/"Implementation of a local contract is flat" (LocalVariant only; title/blob/save; reset/total no args, maybe takes args; bytes flag)
  red: same mutation: "derived_runtime_test.go:85: takesNoArgs is wrong for reset, total or maybe"
  green: ok

contract.test.ts › derived types › ParamsOf: the params object
  ported: TestDerivedShapes/"ParamsOf: the params object" (ParamsData.Fields/Normal string+integer; data.CheckParams accepts {session:string,page:int}, refuses wrong type/missing/extra with invalid/params)
  red: data/params.go:34 check disabled: 4x "error = <nil>, want invalid/params"
  green: ok

declare.test.ts › declaration checks › refuse a remote declaration without a host address at the type level
  ported: TestDeclarationTypeChecksAtRuntime/"refuse a remote declaration without a host address" (RemoteDeclaration{} omitting HostAddress COMPILES — nil func — so runtime-only; same input/message as upstream). Also table row TestDeclarationChecks/remote_refuses_a_missing_host_address.
  red: declare.go:76 nil-check disabled: "expected an error containing \"n.hostAddress: expected a function of the params\", got none"
  green: ok

declare.test.ts › keyed members (slice 6a §12) › refuse a keyed member in a remote contract, at the type level and at declaration
  mechanism: VariantDeclaration.State is []Property{Name; Value TSchema} (declare.go:19, schema.go Property); contracts.Keyed returns *KeyedMember (schema.go:321), which is not a TSchema. Scratch compile run (go test -gcflags=-e, scratch deleted):
    zz_scratch_test.go:10:56: cannot use contracts.Keyed(contracts.Str()) (value of type *contracts.KeyedMember) as contracts.TSchema value in argument to contracts.Prop
    :16:28 ... as contracts.TSchema value in variable declaration ; :17:82 ... in struct literal
  Remote editors (RemoteEditor.State/AddState) also take TSchema only (evolve.go:72-78). LocalMember is sealed (unexported localMember): `mine does not implement contracts.LocalMember (missing method localMember)`; LocalDeclaration has no HostAddress: `unknown field HostAddress in struct literal of type contracts.LocalDeclaration`.
  Upstream's runtime throw ("keyed members are only in local contracts") has NO Go counterpart: the violating call is unrepresentable, and no runtime Go test ported it (earlier claim that the runtime half was ported was wrong; the local-side keyed positive case is TestKeyedMembers/"declare a keyed member in a local contract...").

evolve.test.ts › long histories › the types track every step with its changes
  ported: TestEvolveHistory/"the history tracks every step with its changes" (len(Versions)==2; step 2 Changes == editor.count state, reader.clear args, reader.clear result; step 1 count integer, step 2 string)
  red: fingerprint.go:35 Changes: nil: "step 2 changes = [], want [{editor count state} {reader clear args} {reader clear result}]"
  green: ok

host-kind.test.ts › hostAddress › is typed by the contract's params
  ported: TestHostAddressParams/"is typed by the contract's params: an address from an undeclared param is refused where routed" (reading undeclared `id` compiles; declaration succeeds; address refused by data.CheckAddress with invalid/host_address, declared param routes)
  red: data/params.go:63 check disabled: "error = <nil>, want invalid/host_address"
  green: ok

history.test.ts › type-level history › increments decimal versions
  ported (pre-existing runtime): TestRuntimeHistory/"increments decimal versions" (history_test.go; 1->2, 9->10, 19->20, 99->100, 1099->1100 via Evolve); green: ok

history.test.ts › type-level history › finds the current step, older versions and steps by version
  ported (pre-existing runtime): TestRuntimeHistory/"finds the current step, older versions and the changes after a version" (versions [9,10,11], per-step Changes); green: ok

history.test.ts › type-level history › collects every change after a version
  ported: TestChangesAfter/{"after 9: both later changes need a mapping","after 10: the later change alone","after 11: nothing"} (observed through CheckAccepts, whose needsOf unions Versions[i+1:].Changes)
  red: fingerprint.go Changes: nil -> "h accepts[9].state.s: unnecessary mapping" / "accepts[10].state.t: unnecessary mapping"; accepts.go Versions[index+1:] -> last only -> "accepts[9].state.s: unnecessary mapping"
  green: ok

## data

case id: args.test.ts 'checkArgs › types a call replacement's arguments as values'
mechanism: data.CheckedArgs.Call is contracts.CallFunc (chord/next/contracts/accepts.go:26, func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error); delta.JsonValue = any, data.Value = any) used at data/args.go:43. Scratch assignment of wrong signatures to checked.Call, compiler output (go test, xd670):
  zz_scratch_test.go:12:17: cannot use func(args string, ctx context.Context) (delta.JsonValue, error) {…} ... as contracts.CallFunc value in assignment
  :13:17 same for a func returning (string, error); :14:17 same for func(args delta.JsonValue) (one parameter).
  An identically-typed func(Value, Context)(Value, error) assigns fine. Scratch deleted (confirmed).
ported (also, runtime/reflect pin): TestCallReplacementArgumentsAreValues in chord/next/data/typelevel_test.go. red: first run failed only on my test bug (named type: "CheckedArgs.Call has type contracts.CallFunc, want func(interface {}, ...)"), fixed to compare parameter/result types; green: ok.

case id: errors.test.ts 'ChordError › is a tagged union: a wrong pairing is a type error'
NOT enforced by Go: scratch NewChordError(ChordErrorInit{Code: CodeInvalid, Message}), {Code: CodeGone, Reason: ReasonArgs}, {Code: CodeUnavailable, Reason: ReasonPart} all COMPILE (ChordErrorInit has plain string Code/Reason fields, errors.go:93). Upstream's constructor does not check at runtime either (no production change made); ChordErrorFromJson does (errors.go:199).
ported: TestChordErrorConstructorKeepsAWrongPairing (chord/next/data/typelevel_test.go): the three wrong pairings construct without panic keeping their fields, and ChordErrorFromJson of their ToJSON rejects each. red: n/a, characterization of existing behavior, passes at once. green: ok. Integrator decision: the compile-time constraint is unenforced in Go.

case id: validator.test.ts 'validators › leave nothing in TypeBox's build accumulators'
mechanism (no module-level build state): data/validator.go compile() (func compile, ~line 105) builds a fresh local jsonschema.NewCompiler() per call and returns a *Validator; it does not call typebox's cached compile (internal/typebox/value.go:80 `compiled` cache is not on this path; it only uses typebox.Plain/RegisterFormats/CompilerSchema, which hold no per-schema state). The only package-level state is validators.byOwner, a cache keyed by weak.Pointer(owner), cleaned via runtime.AddCleanup. Compiled-pattern cache in typebox (pattern.go) is not used by this path.
ported: TestValidatorCompileLeavesNoBuildState (typelevel_test.go): two owners (bytes+int, bytes) compiled in sequence, dropped; owners then validators collected (weak pointers). red: n/a (existing behavior, passes at once); green: ok. Full `go test -race ./chord/next/data/` ok; gofmt clean.
Also updated "Designed out" header comments in args_test.go, errors_test.go, validator_test.go.

## state

# D1TLState — packages/chord/test/next/state/native-source.test.ts
Case: "native sources as derive sources › types a derive's sources: watchables of either kind, never a native's handle (Fable)"

Upstream assertions live on host `Self.derive<const S extends readonly (Watchable|NativeWatchable)[]>` / `Self.open`
(host/attempt.ts:297,370). `chord/next/host` does not exist in Go yet (only the v1 `chord/host`), so Self.derive/open have
no Go counterpart; state-package surface is NewDerive/Attach/DeriveInput/DeriveSource/NativeSourceHandle.

## assertion A: `// @ts-expect-error self.derive([handle], ([n]) => n)` (native `open` handle is no watchable)
NOT designedOut at the state layer: `state.NewDerive(sources []any, ...)` (derive.go:68) takes `[]any`, so passing any value
COMPILES (scratch `_ = state.NewDerive([]any{handle}, nil, h.batch)` produced no error, vet run on xd670). The only
type-level exclusion is `DeriveSource` (watchable.go:96) requiring `Value()`: scratch
`var _ state.DeriveSource = handle` (handle state.NativeSourceHandle) -> compile error
`state.NativeSourceHandle does not implement state.DeriveSource (missing method Value)`; but NativeSourceHandle IS a
DeriveInput (native_source.go:58), the intended case (second assertion), so that is not the upstream constraint.
Runtime refusal upstream: host/members.ts attachDerives resolve -> `<name>: source <i> is not a Chord watchable`
(host/members.test.ts:25,28), a host-layer behavior (not in state; host package unported -> owed by the host lane;
the `open` handle (NativeHandle) is not a NativeSource there).
ported (state half of that runtime refusal): TestDeriveRefusesUnresolvedSource in chord/next/state/derive_sources_own_test.go
  - "Attach returns the resolver's refusal of a source that is no watchable"
  - "a refused Attach follows no source: an earlier source keeps no dependent"
  red: mutation of derive.go Attach (`return err` -> `continue`): `--- FAIL ... panic: nil pointer dereference at derive.go:115`
  (mutation reverted); green: ok (go test -race ./chord/next/state/ passes, gofmt clean).

## assertion B: expectTypeOf(self.derive([self.writable(1), constant("a")], ...)).toEqualTypeOf<Watchable<number>>
Result typing is host's Self.derive generic; Go state has no generic derive. Runtime half (a derive over native writable +
constant computes) already covered: TestNativeSourcesAsDeriveSources in native_own_test.go:391 (JSON+native alike).
Typed constant exists: state.ConstantOf[T] returns chord.Watchable[T] (watchable.go:336). Host typed Derive[T] result to be
ported with the host lane (not expressible in state).

Net: no case marked designedOut; one runtime port; host-layer remainder (typed Self.derive + "not a Chord watchable" message
and `open` handle refusal) belongs to the future next/host port.
