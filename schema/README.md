# schema

JSON Schema builders and validation without code generation, a Go port of Pi's `@earendil-works/pi-schema` (`packages/schema`). A schema compiles once into plain closures, so a value checks fast. Dependencies: `github.com/dlclark/regexp2` for `pattern` (through `schema/internal/ecmaregex`) and `golang.org/x/text` for the internationalized format checks.

- Drafts 2020-12 and 7. The vendored official [JSON-Schema-Test-Suite](https://github.com/json-schema-org/JSON-Schema-Test-Suite) gives the same pass and fail result for every case as Pi's package (`TestConformanceSuite`), including the nine tests Pi lists as known failures.
- Builders return plain JSON Schema objects.
- Errors are JSON Pointer paths. A missing or unknown field is reported at the field. The errors, their order and their messages equal Pi's (`TestErrorsMatchPi`).
- A schema is read, never modified.
- `Check` is the fast path: a boolean, no allocation for a schema without `pattern` or `format`. `Errors` explains a failure and returns nothing for a valid value. It is built on the first failure and kept.
- Format checks are an optional import (`schema/formats`; every format 2020-12 defines, plus `url` and `json-pointer-uri-fragment`, which TypeBox and ajv-formats also check); without them `format` is an annotation, as 2020-12 specifies.
- `CompileCoercing` adds `Coerce` and `Parse` for loosely typed input such as a model's tool call arguments: the results equal Pi's (`TestCoerceMatchesPi`, Pi's `coerce.test.ts` and TypeBox `Value.Convert` cases), copy-on-write, and no allocation for a value that needs no change.

## Usage

```go
import (
	"github.com/MichaelKinsy/PiG/schema"
	"github.com/MichaelKinsy/PiG/schema/formats"
)

read, err := schema.Compile(schema.Obj(schema.Properties{
	schema.Prop("path", schema.Str(schema.Description("Path to the file"), schema.MinLength(1))),
	schema.Opt("offset", schema.Int(schema.Minimum(1))),
	schema.Prop("mode", schema.OneOf([]any{"text", "binary"})),
}), schema.CompileOptions{Formats: formats.Formats()})

ok, err := read.Check(value)    // bool
errs, err := read.Errors(value) // []ValidationError{{Path: "/path", Keyword: "required", Message: "missing"}, ...}
```

`Compile` takes any JSON Schema (a `bool`, an `*Object` or a `map[string]any`), built with the builders or not, and returns an error for a malformed schema or a `$ref` that names nothing. With `CompileOptions{UnresolvedRefs: schema.UnresolvedRefsReject}` such a `$ref` compiles as `false` instead: only values at that place fail, with the error `unresolved reference <ref>`, and the rest of the schema works. Use it for schemas from other systems, such as MCP servers, whose references may dangle.

`Check` and `Errors` return an error only where Pi's `check` and `errors` throw: a part of the schema that `Compile` never compiled and that is malformed or names nothing (a `$dynamicRef` target compiled on first use; an `if` that is neither an object nor a boolean, read by the explanation). No panic leaves the package for a schema `Compile` accepted.

### Values

JSON data is `nil`, `bool`, `float64` (every Go numeric kind reads as a number), `string`, `[]any`, `*Object` and `map[string]any`. `[]byte` is Pi's `Uint8Array`: `{ "type": "bytes", "minLength": 1, "maxLength": 4 }` matches a byte slice of that length.

`*Object` keeps JavaScript's property order, so builders and `Errors` order fields as Pi does. A Go map has no order; its keys are read in own-key order (array-index keys ascending, then the rest ascending).

Strings are UTF-8; a lone UTF-16 surrogate is held as WTF-8, as `internal/jsstring` holds it, and counts as one code point, matches `.` in a pattern and is escaped as `\\uD800` in messages, as in JavaScript. A Go value has no `undefined`: an absent member is absent.

### Builders

`Str`, `Num`, `Int`, `Bool`, `Nul`, `Unknown`, `Lit`, `OneOf`, `Obj` (with `Prop`, `Opt`, `Open`), `Arr`, `Tuple`, `Map`, `Union`, `Nullable` and `Bytes` return the same JSON Schema as Pi's builders, member order included. Options are applied in call order, as the members of the options object Pi's builders spread are, so `Str(Description("d"), MinLength(1))` and `Str(MinLength(1), Description("d"))` write their members in that order. Each builder takes the options that fit its type, and a misplaced option does not compile (`Str(MinItems(1))`, `Num(Open())`):

| option | builders |
|---|---|
| `Title`, `Description`, `Examples`, `Deprecated`, `Default` | every builder |
| `MinLength`, `MaxLength` | `Str`, `Bytes` |
| `Pattern`, `Format` | `Str` |
| `Minimum`, `Maximum`, `ExclusiveMinimum`, `ExclusiveMaximum`, `MultipleOf` | `Num`, `Int` |
| `MinItems`, `MaxItems`, `UniqueItems` | `Arr` |
| `Open` | `Obj` |

A `Default` is an annotation; validation never inserts it. `Obj` takes its properties as a `Properties` slice in declaration order; its `properties` and `required` follow JavaScript's own-key order (array-index names first), as Pi's `obj()` reads `Object.entries`.

The static types of Pi's builders (`Static<S>`) are TypeScript compile-time types and have no Go form.

### Coercion

`CompileCoercing(schema, options)` returns a `*CoercingValidator`: a `Validator` that also has `Coerce` and `Parse`, for loosely typed input such as a model's tool call arguments (Pi's `compile(schema, { coerce: true })`). A plain `Validator` has neither, and its `Check` pays nothing for them: the coercion rules are built on the first `Coerce` or `Parse`.

```go
read, err := schema.CompileCoercing(readSchema, schema.CompileOptions{})

value, err := read.Coerce(args) // {"path": "a.txt", "offset": "10"} becomes {"path": "a.txt", "offset": 10}

result, err := read.Parse(args) // ParseResult{OK, Value, Errors}
if result.OK {
	use(result.Value)
} else {
	show(result.Errors) // [{Path: "/offset", Keyword: "type", Message: "expected integer, got string"}]
}
```

`Coerce(value)` returns the coerced value. It copies only the paths it changes (an `*Object` or `map[string]any` keeps its member order in the copy), returns `value` itself when nothing changes, and never modifies `value`. A value no rule applies to stays as it is. `Parse(value)` coerces, then checks: `ParseResult{OK: true, Value}` with the coerced value, or `ParseResult{Errors}` with the errors of the coerced value. The errors returned are those `Check` and `Errors` return.

Three passes run in order:

1. **Optional nulls.** A `null` in an optional property whose schema rejects `null` is dropped, as if omitted.
2. **Conversions**, ported from TypeBox 1.3.27's `Value.Convert` and keyed on JSON Schema keywords (the first that applies to a schema): `$ref` follows the reference; `const` and `enum` take the member the value converts to; `anyOf`/`oneOf` keep a value one branch accepts, else take the first branch's conversion the whole schema accepts; `allOf` converts with each member in turn; then by `type`:
   - `string`: numbers and booleans to their text, `null` to `"null"`.
   - `number`: numeric strings (`""` is 0), `"true"`/`"false"`, booleans to 1/0, `null` to 0.
   - `integer`: as `number`, then truncated (`"3.5"` and `3.5` become 3).
   - `boolean`: `"true"`/`"false"` (any case), `"1"`/`"0"`, 1/0, `null` to `false`.
   - `null`: `"null"`, `"undefined"`, `""`, `"0"`, 0 and `false`.
   - `array`: a single value becomes a one-item array, then each item converts; a tuple (`prefixItems` or an `items` array) converts its items and never wraps.
   - `object`: each property converts by `properties`, `patternProperties` or `additionalProperties`.
3. **Primitive coercion by type**, as Ajv's `coerceTypes`, for what pass 2 leaves (type arrays such as `["integer", "null"]`): numbers from numeric strings and booleans, booleans from `"true"`/`"false"` and 1/0, strings from numbers and booleans, `null` from `""`, 0 and `false`, and `null` to `""`, 0 or `false`. A value that already has one of a type array's types stays.

Where passes 2 and 3 disagree, pass 2 wins: `null` for a required string becomes `"null"`, not `""`. A union's branches convert in order and only until one is accepted; a union node that also declares properties converts them into each branch's candidate before checking it. A recursive schema (a tree through `$ref`) that meets a value it cannot convert leaves the value as it is, so `Check` reports it, and results through a `$ref` are reused within one call, so a recursive union does not convert the same child once per branch.

Pass 1 drops optional nulls inside the values of `properties`, `patternProperties` and `additionalProperties` alike; a pattern or additional key itself is never dropped. Items past a draft-7 `items` array (`additionalItems`) are not coerced by any pass.

What a string reads as is JavaScript's: a number is what `+value` gives (`" 1 "`, `"0x1F"`, `"1e3"` and `"\u00a01"` are numbers; `"1_000"` and `"Infinity"` are not), a trim removes JavaScript whitespace, and "any case" is `toLowerCase`. A Go value has no `undefined` and no bigint, so the conversions of those have no Go form; the string forms `"12n"` and `"12.5"` still read as a safe integer for a number, as in Pi.

### Concurrency

A `Validator` is safe for concurrent use. A validator whose schema has a bookended `$dynamicRef` tracks the dynamic scope in state a check mutates, so its checks run one at a time.

A `CoercingValidator` is safe for concurrent use too. The state of one `Coerce` call is its own, so calls run in parallel; what they share (the compiler of the checks a union conversion asks for, the parsed keywords of each schema node) is built on first use under a lock. A `Coerce` of a value that needs no change allocates nothing. For a schema with a bookended `$dynamicRef`, the checks `Coerce` asks for run one at a time, as `Check` does.

## Regular expressions

`pattern` and `patternProperties` are ECMAScript regular expressions with the `u` flag, as in Pi; a source that is invalid with it compiles without it. `schema/internal/ecmaregex` translates them into `github.com/dlclark/regexp2` syntax.

## Not supported

As in Pi: meta-schemas that switch vocabularies on or off (`$vocabulary`), `contentMediaType` and `contentEncoding` assertions, and draft 2019-09's `$recursiveRef`.

## Benchmarks

`go test ./schema -run '^$' -bench . -benchmem` ports Pi's speed, setup and memory benchmarks (`test/bench`) over the same cases and a corpus generator verified against Pi's output. The comparison with other libraries, the bundle size and the no-eval run have no Go counterpart.
