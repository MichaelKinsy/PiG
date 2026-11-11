# Tool argument validation evidence

## Oracle

Pi's own `validateToolArguments` (`packages/ai/src/utils/validation.ts`) is the oracle, not a duplicate validator. It compiles the tool's JSON Schema with pi-schema (`compile(schema, { formats, coerce: true, unresolvedRefs: "reject" })`), coerces the arguments with `parse()` and checks them; a failure is `Validation failed for tool "<name>":`, one `  - <path>: <message>` line per pi-schema error, then the arguments received.

`ai/testdata/validate_tool_args.mjs` runs that function under Node 24 from the upstream mirror (`.upstream/current`, which must be Pi b556cfe5 or later; `make upstream-mirror PI_UPSTREAM_REF=b556cfe5`), resolving `@earendil-works/pi-schema` to its TypeScript source. It answers each probe with the JSON text of the validated arguments or the error message; a probe is identical only when that text, member order included, is byte-identical.

| Test | Probes |
|---|---|
| `agent.TestToolArgumentValidationMatchesPi` | the agent's `prepareToolCall` on built-in-shaped and plain JSON Schema tools against near-miss arguments |
| `ai_test.TestValidateToolArgumentsMatchesPiOnBuiltinTools` | the exact schemas the Go built-in tools declare (bash, edit, find, grep, ls, powershell, read, write, codemode, tool_search) against generated near-miss arguments |
| `ai_test.TestValidateToolArgumentsMatchesPiOnPiProbes` | every case of Pi's `validation-probes.test.ts` |
| `ai_test.TestValidateToolArgumentsMatchesPiOnMCPSchemas` | MCP-style plain schemas: dangling and external `$ref`, enums, formats, unions, patternProperties |
| `ai_test.TestValidateToolArgumentsMatchesPiOnSeededSchemas` | seeded schemas of the keywords tool definitions use |
| `ai_test.TestValidateToolArgumentsMatchesPiVectors` | `test/parity/sdk-vectors/pi-ai/validateToolArguments.json`, Pi's recorded answers |

`ai.TestValidationProbesMatchPi` asserts the outcomes Pi's test file asserts; `testdata/validation-probes.json` holds them (`ai/testdata/validation_probes_corpus.mjs` regenerates the file from Pi's test).

## Upstream contract

| Rule | Pi source |
|---|---|
| Validate `tool.parameters` itself, in its authored member order, with one compiled validator per schema | `packages/ai/src/utils/validation.ts:6-18` |
| Coerce, then check, with pi-schema's `parse`; the arguments are a copy, so the call keeps its own | `packages/ai/src/utils/validation.ts:58-62` |
| Report dotted paths (`/edits/0/oldText` is `edits.0.oldText`, `root` for the value itself), then `Received arguments` | `packages/ai/src/utils/validation.ts:20-28,64-68` |
| A `$ref` that names nothing rejects only the values at its place | `packages/ai/src/utils/validation.ts:13-15` |
| Give before-tool hooks and execution the returned arguments | `packages/agent/src/agent-loop.ts:726-732,767` |

The stock Session path is `coding/session.go` → `agent/tool_execution.go` → `ai/validate.go`. Validation runs on the existing agent worker. It creates no goroutine, timer, or extension request. The compiled validators are cached by schema text, at most 256 of them (D112).

## Benchmarks

`BenchmarkToolArgumentValidation` covers read arguments and one versus 1,000 nested edit entries; `BenchmarkBuiltinToolArgumentValidation` covers valid and coerced arguments of read, edit, bash and write, with and without encoding the declared schema per call (`via-tool`).
