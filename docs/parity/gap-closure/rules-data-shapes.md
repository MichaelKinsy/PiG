# Rules sprint: data shapes (family 6) and the missing-field generator

Lane `lg-ca-rest-a`, base `ledger-autobind` (rules registry). Code: `test/parity/interface-closure/autobind/rules/datashapes.go` (+ `_test.go`), `test/parity/interface-closure/stubfields/`.

## Rules (`rules/datashapes.go`)

Every rule accepts only when the Go shape is provably equivalent and otherwise stays silent. The verdict text starts with the rule label.

| rule | point | upstream | Go | why equivalent |
|---|---|---|---|---|
| DS1 | type | `number` | `atomic.Int32`, `Int64`, `Uint32`, `Uint64` | `Load` reads and `Store` writes the number; the atomic type is Go's concurrency-safe integer. `atomic.Bool` is refused. |
| DS2 | type | `typeof fetch`, or an object type of call signatures returning `Promise<Response>` | `*http.Client`, `http.RoundTripper`, `func(*http.Request) (*http.Response, error)` | each sends a request and returns the response; any other function is refused. |
| DS3 | type | `Record<string, V>`, `Partial<Record<string, V>>`, `{ [k: string]: V }` (`V` may be `\| null`) | a named slice of two-field entries (string key, value) with `MarshalJSON` and `UnmarshalJSON` | a JS object keeps insertion order; a Go map does not. The JSON methods write and read an object. Refused without both methods, with a third field, or when `V` does not agree with the value field. |
| DS4 | member | a property inherited from a base interface | the field of a struct field that holds the base | the upstream owner and the base must both list the property. **Needs one engine change**: `registeredMember` accepts only members of the owner, so a member of a held struct is dropped. Embedded fields are already promoted by the detector. |
| DS5 | member | `message`, `cause` of a class that extends `Error` | `Error()`; `Unwrap()` or `Cause()` of a Go error type | `Error()` is the message; `Unwrap`/`Cause` is the cause. `name` and `stack` stay member-missing when the Go type has none (no JS stack exists to derive). |
| DS6 | type | `[A, B]`, `readonly [a: A, b: B]` | `[2]T` | a fixed tuple is a fixed-length array. Refused for a slice, a different length, an optional element or a rest element. |
| DS7 | type | `bigint` | `int64`, `uint64`, `int`, `uint`, `*big.Int` | holds 64 bits or more; `int32` is refused. |

## Generator: `make go-stub-fields`

`make interface-gaps` writes `build/interface-gaps/gaps.tsv`. `make go-stub-fields` reads its `member-missing` rows whose upstream owner is an **interface** (plain data; a class member is behaviour or an accessor) and whose Go owner is a struct, and adds one field per property. `APPLY=1` writes them; the default is a report (`ADD` and `SKIP` lines with the reason). It never edits a method, a function or an existing field, and a second run adds nothing.

| rule | what |
|---|---|
| F1 | name: upstream name exported, initialisms in capitals (`apiKey` is `APIKey`, `sessionId` is `SessionID`). Skipped when the owner already has the name, holds a struct field that has it, or another generated field takes it, and when a documented rename exists for the row (the rename is wrong, not the struct). |
| F2 | type: `string`, `bool`, `int` for a number whose name ends in `Ms`, `Count`, `Tokens`, `Index`, `Length`, `Bytes`, `Lines`, `Port`, `Retries`, `Limit`, `Size`, `Width`, `Height`, `Seconds`, `Attempts` or `Depth`, otherwise `float64`; `[]T`; `map[string]T`; `any`; a union of string literals is `string`; a named type is used only when the owner's package declares it. Functions, conditional, mapped and generic types are left for a person. |
| F3 | optional or `\| null`: pointer for `string`, `bool`, numbers and structs; slices, maps, `any` and named string types stay values. |
| F4 | tag: the upstream name; `omitempty` when optional. The comment cites the Pi property and its declaration line. |

Report on this base (`ds-base` gap list): 45 fields added, 164 properties left for a person. The most common reasons: owner is a Go interface (`ViewportTUI`, `ExtensionUIContext`, `ResourceLoader`, `AgentTool`), function-typed property, named type the owner's package does not declare, the owner already holds the base.

Not generated, listed for porting lanes: class members and function-typed properties (behaviour), and a property whose documented rename does not resolve (the report names the rename to fix).

## Result (reach file and hand ledger as in the rules README)

| | before | after |
|---|---:|---:|
| NOT-A-GAP | 5078 | 5121 |
| GAP | 7156 | 7113 |
| trust check 1: hand-closed rows that agree | 3004 of 3489 (86.1%) | 3115 of 3489 (89.3%) |
| child-gap | 4727 | 4713 |
| member-missing | 668 | 660 |
| type-mismatch | 47 | 23 |
| not-exercised | 126 | 129 |
| undecidable | 357 | 360 |
| no-go-symbol | 1077 | 1074 |

Trust check 2 (`go test ./test/parity/interface-closure/autobind/...`) and golangci-lint on `test/parity/interface-closure/...` pass. The few rows that moved to `not-exercised` and `undecidable` are rows whose type now agrees, so the next rule (exercise, or the alias rule) is what blocks them, not the shape.

## Generator rework after review (stubfields)

The first generated fields were rejected: nothing in production set or read them and some duplicated a member under another name. `make go-stub-fields` now:
- touches only wire structs (a JSON-tagged field or a MarshalJSON method); an option struct gets a rename request or a consumer port instead;
- refuses an owner that already has the property under its JSON name or a name equal after folding case and separators;
- cites the pinned upstream TypeScript source (`packages/<pkg>/src/<file>:<line>`), never a node_modules `.d.ts`; a property it cannot find there is refused;
- under `-apply` writes a field only for a row listed in `-wiring` (ID, production file) whose non-test file selects or sets that field, so it scaffolds a field next to its consumer and never closes a row alone.
