# Tool argument validation evidence

## Oracle

Regenerate `tool-validation.json` from the pinned installed Pi and TypeBox packages:

```sh
node agent/testdata/tool-validation-oracle.mjs > agent/testdata/tool-validation.json
go test ./agent -run 'TestValidateToolArguments|TestToolArgumentValidationPiOracle'
```

The generator checks Pi 0.99.1 and TypeBox 1.3.27 before it runs. It calls Pi's `validateToolArguments`, not a duplicate validator. The fixture records schemas, model inputs, returned arguments, and complete error messages. Non-enumerable TypeBox kinds remain separate from provider schemas. TypeBox's own evaluator lowers intersections, enums, and template literals for the conversion representation.

The matrix covers `read`, `bash`, `edit`, `write`, `grep`, `find`, and `ls`, plus the opt-in `powershell` tool's schema. It also compares native TypeBox and serialized plain JSON schemas for numbers, integers, booleans, strings, nulls, arrays, tuples, unions, literals, enums, intersections, additional properties, records, defaults, constraints, and nested objects. Boundary probes cover numeric whitespace, radix strings, bigint-like strings, root unions, references, and the legacy `TypeBox.Kind` symbol gate. `TestBuiltinArgumentSchemasMatchPi` binds the built-in portion of the oracle to the production tool schemas.

`agent/validate_upstream_test.go` contains explicit counterparts for all nine cases in `packages/ai/test/validation.test.ts`. The Function-constructor test uses the Go validator directly because Go validation does not generate JavaScript functions.

## Upstream contract

| Rule | Pi 0.99.1 source |
|---|---|
| Clone the input; remove optional nulls only when the property's schema rejects null; do not delete referenced optional nulls | `packages/ai/src/utils/validation.ts:240-269,318-319` |
| Run TypeBox conversion; ignore replacement of the root value but retain interior mutations | `packages/ai/src/utils/validation.ts:320` and TypeBox 1.3.27 `build/value/convert/convert.mjs` |
| Apply the plain JSON Schema pass after conversion, except for the `TypeBox.Kind` symbol gate | `packages/ai/src/utils/validation.ts:194-238,323-334` |
| Preserve already-matching union arms; otherwise test converted clones in arm order | `packages/ai/src/utils/validation.ts:175-192` and TypeBox `build/value/convert/from_union.mjs` |
| Do not insert defaults | `packages/ai/src/utils/validation.ts:318-338` calls normalization, Convert, and Check; it does not call Default |
| Report dotted paths and the original received arguments | `packages/ai/src/utils/validation.ts:282-292,340-349` |
| Give before-tool hooks and execution the returned arguments | `packages/agent/src/agent-loop.ts:726-732,767` |
| Keep pico3 validation strict, without conversion | `packages/agent/src/harness/pico3/kinds/tool.ts:68-70` |

TypeBox's native conversion is not the plain-schema fallback. For example, native integer conversion truncates a fraction, native array conversion wraps a scalar, and native string conversion maps null to `"null"`. Plain-schema integer conversion rejects fractions, plain arrays do not wrap scalars, and plain string conversion maps null to an empty string. Optional non-nullable nulls disappear before either pass. These distinctions are observed by the oracle rather than inferred from JSON Schema syntax.

## Reproduction and mutation evidence

The stock Session path is `coding/session.go` → `agent/tool_execution.go` → `agent/validate.go`. The old implementation passed the model's raw JSON to jsonschema and returned only an error, so it could not deliver converted values to execution.

The pre-fix `tools/13-print-read-argument-coercion` pair sent `{path:"parity-read-target.txt", offset:"2", limit:null, extra:true}`. Pi printed `line two` and `line three`. PiG exited 1 with `jsonschema validation failed`, reporting that `/limit` was null and `/offset` was a string. The initial unit oracle also rejected these inputs on the old implementation.

Disabling the three pre-validation steps in the compiling Go implementation fails the upstream primitive, optional-null, and nullable-union tests, `TestToolArgumentCoercionAcrossSDKs`, and both new paired scenarios. Restoring the steps passes them. A separate red probe found that treating all maps as identical loses a coerced root union candidate; the regression now checks JavaScript object identity rather than map type. Another red probe protects literal backslashes in the original-arguments error text; JSON stringification no longer rewrites a literal `\\u003c` into an invalid escape.

The extension scenario contains native TypeBox and plain JSON subtrees in one registered tool. It compares the actual execution result `[2,true,[3],false,2,true,false]` with Pi. The cross-SDK test drives the production agent bridge for in-process Go, isolated Go/Node/Rust/Python, packed Node, and fused Go. Hooks receive the converted string and no optional null. The retained assistant call keeps the original number and null.

The previous `providers-faux-streaming/05-print-faux-validation-reject` scenario did not prove validation rejection: Pi converts `command:42` to `"42"`, and its faux response was unconditional. It now sends `command:[]` and compares the complete error returned to the model.

## Provider decoding audit

`TestProvidersPreserveNoncanonicalToolArguments` exercises the production decoders with a numeric string split across streamed JSON fragments, a null optional field, and an extra boolean field. It also tests a Responses final argument snapshot that completes a partial delta.

| Provider path | Pi source | PiG path and observation |
|---|---|---|
| OpenAI Chat, OpenRouter, Ollama/OpenAI-compatible local endpoints, Copilot Chat | `packages/ai/src/api/openai-completions.ts:650` | `ai/openai.go` → `ai/stream_builder.go`: parse the argument string; preserve inner string/null values |
| OpenAI Responses, Copilot Responses | `packages/ai/src/api/openai-responses-shared.ts:657-714` | `ai/openai_responses.go` → `ai/stream_builder.go`: parse deltas and final snapshots; preserve inner string/null values |
| Anthropic | `packages/ai/src/api/anthropic-messages.ts:703-740` | `ai/anthropic.go` → `ai/stream_builder.go`: accumulate `partial_json`; preserve inner string/null values |
| Google | `packages/ai/src/api/google-generative-ai.ts:205-218` | `ai/google.go`: the wire supplies an object; serialization into the shared builder preserves its property types |

No provider-specific coercion is needed for the reproduced object-shaped calls. Conversion belongs after tool lookup, where the schema is available, rather than in a provider decoder.

## Resource lifetime

Validation runs on the existing agent worker. It creates no goroutine, timer, or extension request. Per-call schema nodes, clones, and compiled validators become unreachable when the call completes. There is no global schema cache that retains extensions after reload.

`BenchmarkToolArgumentValidation` covers read arguments and one versus 1,000 nested edit entries. A representative linux/amd64 Xeon 6746E run measured about 162 microseconds and 66 KB per read validation, 207 microseconds and 81 KB for one edit, and 7.6 milliseconds and 3.2 MB for 1,000 edits. CPU and allocation profiles identify JSON/schema compilation and per-entry traversal as the work; this is a measurement, not a claim of a speed improvement.
