# schema

JSON Schema builders and validation without code generation, a Go port of Pi's `@earendil-works/pi-schema` (`packages/schema`). A schema compiles once into plain closures, so a value checks fast. Dependencies: `github.com/dlclark/regexp2` for `pattern` (through `schema/internal/ecmaregex`) and `golang.org/x/text` for the internationalized format checks.

- Drafts 2020-12 and 7. The vendored official [JSON-Schema-Test-Suite](https://github.com/json-schema-org/JSON-Schema-Test-Suite) gives the same pass and fail result for every case as Pi's package (`TestConformanceSuite`), including the nine tests Pi lists as known failures.
- Builders return plain JSON Schema objects.
- Errors are JSON Pointer paths. A missing or unknown field is reported at the field. The errors, their order and their messages equal Pi's (`TestErrorsMatchPi`).
- A schema is read, never modified.
- `Check` is the fast path: a boolean, no allocation for a schema without `pattern` or `format`. `Errors` explains a failure and returns nothing for a valid value. It is built on the first failure and kept.
- Format checks are an optional import (`schema/formats`); without them `format` is an annotation, as 2020-12 specifies.

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

`Compile` takes any JSON Schema (a `bool`, an `*Object` or a `map[string]any`), built with the builders or not, and returns an error for a malformed schema or a `$ref` that names nothing.

`Check` and `Errors` return an error only where Pi's `check` and `errors` throw: a part of the schema that `Compile` never compiled and that is malformed or names nothing (a `$dynamicRef` target compiled on first use; an `if` that is neither an object nor a boolean, read by the explanation). No panic leaves the package for a schema `Compile` accepted.

### Values

JSON data is `nil`, `bool`, `float64` (every Go numeric kind reads as a number), `string`, `[]any`, `*Object` and `map[string]any`. `[]byte` is Pi's `Uint8Array`: `{ "type": "bytes", "minLength": 1, "maxLength": 4 }` matches a byte slice of that length.

`*Object` keeps JavaScript's property order, so builders and `Errors` order fields as Pi does. A Go map has no order; its keys are read in own-key order (array-index keys ascending, then the rest ascending).

Strings are UTF-8; a lone UTF-16 surrogate is held as WTF-8, as `internal/jsstring` holds it, and counts as one code point, matches `.` in a pattern and is escaped as `\\uD800` in messages, as in JavaScript. A Go value has no `undefined`: an absent member is absent.

### Builders

`Str`, `Num`, `Int`, `Bool`, `Nul`, `Unknown`, `Lit`, `OneOf`, `Obj` (with `Prop`, `Opt`, `Open`), `Arr`, `Tuple`, `Map`, `Union`, `Nullable` and `Bytes` return the same JSON Schema as Pi's builders. Options (`Title`, `Description`, `Examples`, `Deprecated`, `Default`, `MinLength`, `MaxLength`, `Pattern`, `Format`, `Minimum`, `Maximum`, `ExclusiveMinimum`, `ExclusiveMaximum`, `MultipleOf`, `MinItems`, `MaxItems`, `UniqueItems`) are applied in call order. A `Default` is an annotation; validation never inserts it.

The static types of Pi's builders (`Static<S>`) are TypeScript compile-time types and have no Go form.

### Concurrency

A `Validator` is safe for concurrent use. A validator whose schema has a bookended `$dynamicRef` tracks the dynamic scope in state a check mutates, so its checks run one at a time.

## Regular expressions

`pattern` and `patternProperties` are ECMAScript regular expressions with the `u` flag, as in Pi; a source that is invalid with it compiles without it. `schema/internal/ecmaregex` translates them into `github.com/dlclark/regexp2` syntax.

## Not supported

As in Pi: meta-schemas that switch vocabularies on or off (`$vocabulary`), `contentMediaType` and `contentEncoding` assertions, and draft 2019-09's `$recursiveRef`.

## Benchmarks

`go test ./schema -run '^$' -bench . -benchmem` ports Pi's speed, setup and memory benchmarks (`test/bench`) over the same cases and a corpus generator verified against Pi's output. The comparison with other libraries, the bundle size and the no-eval run have no Go counterpart.
