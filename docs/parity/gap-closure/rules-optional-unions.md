# Rule families 2 and 3: optionality and unions

Package `test/parity/interface-closure/autobind/rules` (`optionality.go`, `unions.go`, `unions_ast.go`). The checks are pure functions from an upstream TypeScript shape (the strings of the upstream inventory) and a Go shape (`go/types`) to a `Finding` (`Result` as the registry's `Tri`, the rule id, the reason and notes); `Finding.Verdict()` converts it for the registry. `No` and `Unknown` stay gaps. The package does not import the engine.

Two checks are registered type rules (consulted only when the built-in verdict is not yes): `optionality/O2w` (an upstream type that is optional or nullable, but not both, against a presence wrapper `Opt[T]` that has `MarshalJSON` and `UnmarshalJSON`) and `unions/U1t` (a literal union against a typed constant set; it also refutes a bare `int`/`string` for a closed union). A type rule does not see the struct tag, so `**T` has no type rule: `encoding/json` decodes `{"x":null}` into a nil outer pointer, and null reads back as absent. Only the tag-aware O4 can accept `**T`, and only for a field that is not serialized. The refuting checks (O1, O3, O5, O6, U3-U5) are library functions for the anti-pattern lint, because the registry never weakens a built-in yes; the engine hooks they need are in the lane notes (alias A3 routing, `Env.Properties`, package syntax for `ASTTags`).

## Optionality (family 2)

`ParseOptionality(declaredOptional, type)` splits an upstream type into optional (`x?:` or an `undefined` member), nullable (`null` member) and the base type. `ParseResultOptionality` also unwraps `Promise<T>`.

| rule | upstream | accepted Go shape | refuted |
|---|---|---|---|
| O1 | required property | a value without `omitempty`/`omitzero` | pointer, presence wrapper, omit option |
| O2 | `x?: T`, `T \| undefined` | `*T`; `Opt[T]`; `omitempty` or `omitzero`; a non-serialized nilable (`func`, `chan`, `json:"-"`) | plain value; `**T`; a serialized slice, map or interface without an omit option (marshals `null`) |
| O3 | `T \| null` | `*T`; slice, map, interface, `json.RawMessage` without an omit option; wrapper | plain value; omit option (null becomes absent) |
| O4 | `x?: T \| null` | wrapper; `**T`; `json.RawMessage` with `omitempty` | plain value; Unknown for a single nil state (it cannot keep null apart from absent) |
| O5 | result `T \| undefined`, `T \| null` | `*T`, nilable `T`, `(T, bool)`, each with an optional trailing `error` | plain `T`; a required result as `*T` or `(T, bool)` |
| O6 | parameter `x?: T` | `*T`, nilable, wrapper, or the variadic tail | plain value; a required parameter as pointer or variadic tail |

`OptionalOptions.ZeroMeansUnset` accepts an untagged scalar field for an optional property when the Go documentation says the zero value is unset; it is off by default because the zero value of a string, number or bool is also a value upstream can send. An `omitempty` scalar is accepted with the note that its zero value cannot be sent. Wrapper names default to `Opt`, `Option`, `Optional`, `Maybe` and `Nullable` (generic types only).

## Unions (family 3)

| rule | upstream | accepted Go shape |
|---|---|---|
| U1 | union of string or number literals | a named type with string or numeric underlying type and one constant of that type for every literal; extra constants are notes; a bare `string` is refuted unless `AllowBareString` |
| U2 | open union (`string`, `string & {}`) | any Go string type |
| U3 | discriminated union of object types | parse only: one property present in every member whose type is one distinct string literal; preferred names `type`, `kind`, `role`, `tag`, `_tag`, `event`, `op`, `name`, then alphabetical; other candidates are notes |
| U4 | discriminated union | an interface with at least one method plus one concrete struct per member; a `TagResolver` reads each struct's discriminator value and it must equal the member's literal; two structs with one value are refuted; a struct whose value is not readable is a note, or Unknown when a member has no readable carrier |
| U5 | discriminated union | one tagged struct: a discriminator field whose type satisfies U1 and a field for every property of every member |
| U6 | `ASTTags` (the `TagResolver`) | a method named after the discriminator that returns one string constant, or a `MarshalJSON` whose body names the discriminator key (constant, JSON object constant or `json:"key"` tag) and holds exactly one other string constant |

`ParseLiteralUnion(type, alias)` resolves alias bodies (depth four) and accepts only literals, open strings and aliases of them. `ConstsOf(type)` lists the constants of a type in its package. `Implementers(iface, pkgs...)` lists the struct types that implement an interface by value or pointer.

## Using the rules from the engine

The engine's `checker.agree` drops `undefined` and `null` today (T0), so optionality is not judged. To apply O1-O6, call `CheckOptionalProperty` from `detector.memberShape` for a `*types.Var` member with the struct tag of its field, and `CheckOptionalResult` and `CheckOptionalParam` from `checker.signature`. To apply U1-U2, call `CheckLiteralUnion` from the alias rule A1 and from property types whose upstream type is a literal union. To apply U3-U5, call `ParseDiscriminated` where `checker.union` returns Unknown for T10 and then `CheckSealedInterface` or `CheckTaggedStruct`; the sealed interface rule needs the package syntax (`packages.Package.Syntax` and `TypesInfo`) for `NewASTTags`, which the index does not keep today.

## Evidence

`rules/optional_test.go` and `rules/unions_test.go` hold the table tests (every rule has an accepting and a refuting case and the boundaries: omit options on required properties, bare strings, duplicate discriminators, unreadable discriminators). Each rule was mutated once (flip the condition) and a test failed. `TestRulesFamilyAudit` in the engine package is trust check 1: it applies the rules to the rows closed by hand and writes the disagreements; run it with `AUTOBIND_RULES_AUDIT=<dir> go test ./test/parity/interface-closure/autobind -run TestRulesFamilyAudit -v`.

## Trust check 1 result (hand-closed rows, v1.0.4)

Applied to the 1,999 hand-closed (`ported`) property rows with a resolvable Go field and 56 literal-union aliases; disagreements are in `test/parity/interface-closure/gaps/rules-optional-unions-audit-v1.0.4.tsv` (ID, family, outcome, rule, hand target, reason).

| family | agree | disagree |
|---|---:|---:|
| optional property (O1-O4) | 1563 (O1 978, O2 571, O3 13, O4 1) | 436 (O2 no 265, O2 unknown 141, O1 no 23, O3 no 4, O4 unknown 3) |
| literal union (U1, U2) | 51 | 5 (`GrammarFormat`, `InputSource`, `ModelSelectSource`, `ToolExecutionMode`: a bare Go `string` stands for a closed literal union) |

Reading the disagreements: 265 O2 refutations are optional upstream properties that Go models as an untagged or tagged scalar whose zero value means unset (`ZeroMeansUnset` would accept the untagged scalars); 141 O2 unknowns are untagged slices, maps or interfaces (nil is the unset state but no json tag states the encoding); 23 O1 refutations are required upstream properties held as a pointer (17) or with `omitempty` (6), including `OAuthCredential` `access`, `expires` and `refresh`; the 5 U1 refutations are the stringly-typed unions the anti-pattern lint is meant to flag. None of these is a rule bug found by inspection of the samples; whether the engine should treat them as gaps, accept them with a note or lint them is the integrator's decision. Trust check 2 (seeded gaps) is the rule tests: each rule's refuting case is a seeded gap, and one mutation per rule family was killed.

## Detector totals with the families registered (v1.0.4 ledger, same tree)

| | closed-check agreement | NOT-A-GAP | GAP |
|---|---:|---:|---:|
| before (registry without these families) | 2909/3489 (83.4%) | 4630 | 7604 |
| after | 2909/3489 (83.4%) | 4630 | 7604 |

No row changes between gap and not-a-gap. Two rows change reason (`SettingsManager.getOutputPad` and `setOutputPad`, upstream `0 | 1`): `undecidable` (T10) becomes `signature-mismatch` (U1: a closed union of literals is the bare Go type `int`). That is a real stringly-typed finding, not a closure; the Go side needs a named type with constants for 0 and 1.
