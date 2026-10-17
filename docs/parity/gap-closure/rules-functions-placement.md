# Rules: functions/options and placement

Package `test/parity/interface-closure/autobind/rules` holds the function/options family (`functions.go`, rules S1-S11) and the placement family (`placement.go`, rules P1-P4). The rules take upstream types as text and Go types from `go/types`. They hold no state beyond the `Env` the engine passes. A rule accepts a mapping only when the Go shape is provably equivalent under the rule; anything else is `No` or `Unknown` and stays a gap with its reason.

`verdict.go` (Tri, Verdict, All) and `text.go` (SplitTop, IsFuncType, IsSignalType) are the shared basics the two families need. A rule family written by another helper may move them. The engine delegates to this package: `checker.signature`, `parseFuncType`, `prefixOverload`, `splitTop`, `isFuncType` and `isSignalType` call it, and `param`, `callShape` and `typeParam` are aliases of its types, so the detector and the tests run one implementation.

## Functions and options (S)

| rule | upstream | Go |
|---|---|---|
| S1 | `AbortSignal` parameter, or an options bag whose only member is a signal | leading `context.Context` |
| S2 | other parameters, in order | one Go parameter each, types agree (`Env.Agree`) |
| S3 | trailing optional or rest parameters; a required array | variadic tail; the variadic slice |
| S4 | a different parameter count | gap |
| S5, S5b | `Promise<T>`, `void`, object-literal result | value plus optional error; no value; return values in field order |
| S6 | `T \| undefined`, `T \| null` | `T`, `*T` or `(T, bool)` |
| S7 | overload whose parameters prefix a wider overload | covered when the wider one is satisfied |
| S8 | options-bag parameter | positional parameters named after its members |
| S9 | trailing optional options bag | variadic `func(*T)` options where `T` has a field for every member (types agree) |
| S10 | overload set | every overload satisfied by some Go function of the candidate set |
| S11 | callback | func type, or one-method interface, under the call rules |

`IsAsync` reports a Promise or async-iterable result, for the later anti-pattern lint (an async upstream API needs a context and an error in Go).

## Placement (P)

| rule | decision |
|---|---|
| P1 | `Resolver.Resolve` follows a barrel's re-export chain (`export {A as B} from`, `export *`, `export * as ns`, `export {A}` of an imported name) to the declaring module or external package |
| P2 | `Placement.Dirs` lists the Go directories of the Go files PORT_MAP records for the defining file (siblings when the file is unlisted), then the package seeds |
| P3 | `MethodOfFirstParam`: a free function whose first parameter is `T` is a method on `T`; `FirstParamOfMethod`: a class method is a function whose first parameter after a context is the class |
| P4 | `Generics`: equal type-parameter count, or a documented concrete instantiation |

## Evidence

`go test ./test/parity/interface-closure/autobind/rules` runs table tests for every rule and a trust check over the pinned mirror: every name exported by every `packages/*/src/index.ts` resolves to a declaration or an external package, and PORT_MAP places `loader.ts` and `stdin-buffer.ts`. The trust check found a real defect while the rules were written: a comment stripper read `/*` inside a string as a block comment and dropped declarations, so `ParseModule` now reads only statements that start a line. Mutating each rule's guard fails at least one test (S1 context, S3 required tail, S7 prefix, S8 optional bag, S9 field and type, P1 stars and cycles, P3 optional receiver).
