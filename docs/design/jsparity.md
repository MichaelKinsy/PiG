# jsparity: typed JSON encoders with proven Pi byte parity

Status: accepted. Review rev-go-idioms-design: ACCEPT-WITH-CHANGES; the required changes are folded into the sections below and listed in "Review changes" at the end. The lead gave GO for implementation: primitives and the Node differential first, then the hottest paths.

## Problem

PiG writes JSON that must equal the bytes Pi writes with `JSON.stringify`: session JSONL lines, provider request bodies, RPC frames, extension IPC frames and Durable records. Today PiG reaches those bytes through general machinery that each pays a pass over the output:

- Nested `MarshalJSON` methods splice a discriminant in front of a second encoding (`ai/messages.go:343` `marshalMessage`, `ai/types.go:178` `marshalContent`). The v1 engine validates and compacts the output of every `Marshaler` it calls, so a record nested N levels deep is scanned and copied N times.
- `internal/jsstring/stringify.go:11` `MarshalJSON` encodes into one buffer, then rewrites it in a second pass to undo HTML escapes and turn `-0` into `0`.
- `internal/jsonstringify.Canonicalize` scans (and on a miss, decodes and re-encodes) text that PiG produced itself.
- `internal/orderedjson.Object` stores a Go map plus a key slice; `MarshalInSourceOrder` re-parses the source text to recover order; `ai/schema_object_order.go` keeps a side table of key orders for tool arguments and schemas.
- Request bodies are merged by `json.Marshal` → `map[string]any` → `json.Marshal` (`ai/openai.go:1611-1626`). Go sorts map keys at every depth, so this path is also wrong: with sampling parameters, every key of the body is sorted, including `messages[*]` (`{"content":…,"role":…}`). Pi does `Object.assign(params, samplingParams)` (`packages/ai/src/api/openai-completions.ts:1004-1007`), which keeps the existing key positions and appends new keys. openai-responses has the same merge (`ai/openai_responses.go:1237-1251`; Pi `packages/ai/src/api/openai-responses.ts:383-385`, `azure-openai-responses.ts:243-245`). The order of the appended keys is lost before the merge as well: `SamplingParams` is `map[string]any` (`ai/types.go:586`) and `ResolveSamplingParams` merges with `maps.Copy` (`ai/simple_options.go:197-212`), while Pi's `{ ...model.samplingParams, ...thinkingLevelParams, ...requestParams }` (`packages/ai/src/api/simple-options.ts:31-33`) keeps the first position of each key. The top-level order and the `Object.assign` positions are fixed (`assignSamplingParams`, `ai/sampling_params_assign.go`); the keys of one Go-map source are still enumerated sorted, and tool schema members in `tools[]` are still sorted. Fixing those therefore also needs an ordered `SamplingParams` from the models.json and request decode, which is a public type change (additive per the lead's public API ruling).
- Provider request bodies are written with `encoding/json.Marshal` (`ai/openai.go:1635`, `ai/openai_responses.go:1263`, `ai/anthropic_client.go:368`, `ai/google.go:147`, `ai/mistral.go:532`). It escapes `<`, `>`, `&`, U+2028 and U+2029 in every string. Pi's provider SDKs send `JSON.stringify(body)` (openai 7.19.0 `internal/request-options.js:29`), which writes them literally. Probe: a user message `a <b> & c` + U+2028 goes out as `a \u003cb\u003e \u0026 c \u2028`. Fixed for these five characters by `marshalRequestBody` (`ai/request_body_jsonv2.go`). A lone UTF-16 unit is still lost there: `encoding/json` writes each byte of a WTF-8 surrogate as U+FFFD, while `JSON.stringify` writes `\udxxx` (review probe: `"a\xed\xa0\xbdb"` is sent as three U+FFFD characters). Step 3's `AppendString` closes this.
- Values are deep-copied by a JSON round trip (`ai/transcript.go` `cloneJSONValue`; 28 clone-helper uses in 7 files). The `ai` history instance is owned by lane audit-followups; this design supplies the mechanism that replaces it.

## Measured cost

All numbers: one 24-thread Xeon 6746E host, Go 1.27.2, `GOMAXPROCS=24`, prototype at `docs/plan/evidence/jsparity-prototype_test.go.txt` (its header says how to run it). `TestShape` and `TestToolResultShape` assert that the writer bytes equal the current Pi-facing bytes: the assistant message as `json.Marshal` writes it, and the tool result as the `message` member of its session line. `json.Marshal(AgentMessage)` is not Pi-facing for the tool result: it writes `<`, `>` and `&` as `\u003c`, `\u003e` and `\u0026`, so the second row compares the writer with a path whose bytes differ (the session-line rows are the Pi-facing comparison).

| value | current path | ns/op | B/op | allocs/op | append writer ns/op | B/op | allocs/op |
|---|---|---:|---:|---:|---:|---:|---:|
| AssistantMessage, 3 blocks, 528 B (`agent.BenchmarkAgentMessageMarshalJSON` shape) | `json.Marshal(AgentMessage)` | 14,921 | 4,839 | 48 | 449 | 0 | 0 |
| ToolResult message, 50 KB text | `json.Marshal(AgentMessage)` | 1,141,922 | 446,149 | 21 | 102,813 | 0 | 0 |
| session JSONL line, assistant | `sessionentry.MessageEntry.MarshalJSON` | 23,404 | 5,644 | 49 | (as above) | | |
| session JSONL line, 50 KB tool result | `sessionentry.MessageEntry.MarshalJSON` | 1,395,882 | 526,464 | 23 | (as above) | | |

The 50 KB session line allocates 10.5 times its own size. In the bolt-bench 75-prompt long session profile (operator-held, not in the tree), `extensions/sdk/json.appendCompact` (the per-`Marshaler` compaction) is 29.5% of all CPU and `stateInString` 16.5%; most of that sits under the `ai` history clone (audit-followups), and the rest under request and session encoding.

## Inventory of parity-driven slow spots

| mechanism | Pi behavior it preserves | sites (non-test) | cost source |
|---|---|---:|---|
| `marshalMessage` / `marshalContent` / `encodeFields` splice | discriminant first (`role`, `type`), member order of the Pi object | 24 uses, 217 `MarshalJSON` methods repo-wide | one extra encode + validate + copy per nesting level |
| `jsstring.MarshalJSON` post-pass | no HTML escaping, `-0` → `0`, lone UTF-16 units as `\udxxx`, U+2028/2029 literal | 17 | second full pass, two buffers |
| `SetEscapeHTML(false)` encoders | no HTML escaping | 32 in 28 files | `bytes.Buffer` + `Encoder` per call |
| `jsonstringify.Canonicalize` | `JSON.stringify(JSON.parse(x))`: escapes, numbers, duplicate keys (first position, last value), integer keys first | 53 in 42 files | scan + copy; decode + re-encode on a miss |
| `orderedjson` (`Object`, `MarshalMap`, `MarshalInSourceOrder`) | insertion order of open objects (MCP schemas, tool arguments) | 148 in 30 files | map + key slice; re-parse of source text |
| `schemaObjectOrder` / `argumentOrder` | key order of tool arguments and JSON Schema | 43 in 14 files | side table rebuilt by a token decode |
| `ErrorBeforeTimestamp` flags | late-assigned `errorMessage` lands before or after `timestamp` | 15 in 7 files | a second struct shape per variant |
| `map[string]any` merge round trip | `Object.assign` order (PiG currently sorts: finding) | 2 | marshal + boxed decode + marshal |
| JSON round-trip clone | structural copy of a JSON value | 28 uses in 7 files | marshal + decode per copy |

`map[string]any` appears 1,240 times in ai, coding, internal, tui, agent and durable; only the members that cross a Pi-facing JSON boundary belong to this design. Dynamic extension payloads stay open (as `jsparity.Object`, below).

## Design

### Package layout

`internal/jsparity` holds the primitives and the runtime types. It imports only `math`, `strconv`, `unicode/utf8` and `unicode/utf16`: no `reflect`, `fmt`, `encoding/json`, goroutines or map iteration, so `durable/core` can use it under TinyGo. `internal/jsparity/jsonv1` adapts generated types to `encoding/json` and `extensions/sdk/json` callers. `internal/jsparity/cmd/jsparitygen` is the Go half of the generator.

`durable/core/jv` already implements these primitives for the TinyGo core: `AppendString` (WTF-8 lone units as `\udxxx`), `AppendNumber` (Number::toString layout over `strconv.AppendFloat(…, 'e', -1, 64)`), `IsIndexKey` (canonical array index, 0..2^32-2) and an ordered `Object` with ECMAScript key order. `jv` is built by `make -C durable/core build-tinygo` and checked against Node by `TestParseStringifyMatchesNode` (corpus `durable/core/jv/testdata/gen.mjs`, `corpus.jsonl`). `internal/jsstring.AppendString` (`internal/jsstring/stringify.go:79`) and `internal/jsnumber.String` / `JSON` (`internal/jsnumber/number.go:219`, `:235`) are two more writers, with Node oracles in `internal/jsnumber/number_oracle_test.go` and `internal/jsonstringify/fast_node_test.go`; the existing oracles are extended, not replaced by a new harness. Step 1 moves those primitives into `internal/jsparity`, with their tests, and makes `jv` a caller. It does not write a second implementation. The move is coordinated with the Durable core owner (dcore-perf); question 3 asks the lead where the single copy lives.

### Primitives (`JSON.stringify` exact)

```go
func AppendString(dst []byte, s string) []byte  // QuoteJSONString: ", \, \b \f \n \r \t short forms; other C0 as \u00xx lowercase; a WTF-8 high unit directly followed by a WTF-8 low unit is one code point and prints literally (4-byte UTF-8); any other WTF-8 surrogate as \udxxx lowercase; everything else literal (no HTML escaping, U+2028/2029 and U+007F literal)
func AppendNumber(dst []byte, f float64) []byte // Number::toString: shortest round trip, exponent form when e >= 21 or e <= -7 ("1e+21", "1e-7"); -0 -> 0; NaN and ±Infinity -> null
func AppendInt(dst []byte, n int64) []byte      // exact when |n| <= 2^53; beyond that, the double JS holds (AppendNumber(float64(n)))
func AppendBool(dst []byte, b bool) []byte
func AppendRaw(dst []byte, r Raw) []byte        // bytes already in JSON.stringify form (checked when the Raw was built)
```

A Go string can hold a surrogate pair as two WTF-8 halves when two slices of a JavaScript string are concatenated in Go. JavaScript joins `"\ud83d" + "\ude00"` into one pair, and `JSON.stringify` prints `"😀"`. `jv.AppendString` prints `"\ud83d\ude00"` for the same input today (review probe against Node 24). `jsstring.Canonical` already joins such halves, and `AppendString` joins them while it writes. The Node proof corpus includes split pairs.

Invalid UTF-8 that is not a WTF-8 surrogate cannot come from a JavaScript string; it encodes as U+FFFD, as `extensions/sdk/json` does today. `internal/jsnumber.String`, `jsstring.MarshalJSON` and `jsonstringify`'s string writer become callers of these functions, so one implementation serves every encoder.

`jsontext.Encoder` is not a valid sink for Pi-facing bytes: `WriteValue` rejects `"\ud800"`, and with `AllowInvalidUTF8(true)` it replaces the lone unit with U+FFFD (probe: `docs/plan/evidence/jsparity-jsontext-surrogate-probe.go.txt`, Go 1.27.2). It writes U+2028 literally, from a literal or a `\u2028` escape, as `JSON.stringify` does; the probe prints with `%q`, which shows it as `\u2028`, so read its bytes with `% x`. Generated encoders therefore write into `[]byte`; the `MarshalJSONTo` adapter exists only for embedding in a v2 encoder that already accepts that loss.

### Three-state member: `Opt[T]`

```go
type Opt[T any] struct { state uint8; value T } // Absent (undefined, omitted), Null (null), Set
func Some[T any](v T) Opt[T]
func Null[T any]() Opt[T]
func (o Opt[T]) Get() (T, bool); IsNull() bool; Present() bool
```

A Pi member typed `x?: T` is `Opt[T]` only when Pi ever writes it as `null` or when `undefined` and the zero value are both observable; otherwise the generator keeps the existing Go field and an `omitempty`-equivalent rule. Public Go fields keep their current types (RULINGS: public API additive); `Opt` is used in new fields and internal wire types.

The declaration cannot show that Pi never writes the zero value of an optional member: `durationMs` is `Math.max(0, …)` (`packages/ai/src/utils/event-stream.ts:128`), and `endTurn` can be `false`. The generator therefore omits a zero value only when the field carries a reviewed `jsparity:"omitzero <Pi file:line>"` tag that cites why Pi never writes it. Without that tag an optional member needs a presence-carrying Go type (`Opt[T]` or a pointer), and generation fails. `Opt` also covers `null` members that Pi writes on purpose; a `null` value inside an open record, such as a removed section in `SystemMessage.sections` (`packages/ai/src/types.ts:533-538`), stays a `Raw` `null` in an `Object`. An absent `Opt` element of an array prints `null`, as `JSON.stringify` prints `undefined` in an array.

### Member order: creation order recorded per value

`JSON.stringify` writes properties in creation order, not TypeScript declaration order. Pi's `AssistantMessage` is declared with `errorMessage` before `timestamp` (`packages/ai/src/types.ts:552-576`), but the stream builders create `timestamp` in the literal (`packages/ai/src/api/openai-completions.ts:313-329`) and assign `errorMessage` later (`:586`, `:719`), so those records put `errorMessage` last; PiG's `ErrorBeforeTimestamp` exists because both orders occur.

Creation order is a runtime property of each value, not a property of the type:

- Late members are created in the order the stream delivers data. openai-completions creates `responseId` at the first chunk (`output.responseId ||= chunk.id`, `:564`), even when `chunk.id` is `undefined`, because `||=` creates the property with an `undefined` value. It then creates `responseModel` (`:566`), `rawStopReason` (`:582`) and `errorMessage` (`:586` or `:719`). The event stream adds `durationMs` (`packages/ai/src/utils/event-stream.ts:127-128`), and the agent loop adds `thinkingLevel` (`packages/agent/src/agent-loop.ts:409`, `Object.assign`).
- Frame-reduced messages are rebuilt with conditional spreads, which puts `responseModel` and `responseId` before `usage` (`packages/ai/src/utils/assistant-message-frame.ts:77-91`).
- A decoded record keeps the order of its source text, as `JSON.parse` does.

Review probe (Pi 1.1.0 `stream` from `dist/api/openai-completions.js` against a local SSE server; chunk with `id` and another `model`, then `finish_reason: "content_filter"`): Pi writes `…,"timestamp","responseId","responseModel","rawStopReason","errorMessage","durationMs"`. PiG's single static order (`agent/message_json.go:131-160`) writes `…,"timestamp","responseModel","responseId","errorMessage","rawStopReason","durationMs"` for the same members. This is open item B3 of the e2e-journeys review. A static order with named variants cannot express all of these orders: the number of orders grows with every conditional late assignment.

The generated type therefore records creation order the way JavaScript does:

- members that a Pi literal creates have the literal's static order, taken from the Pi construction site;
- every late member has a generated setter (`SetErrorMessage`) that appends its member ID to a small per-value order list (`order []uint8`, or a packed `uint64` when the type has at most 16 late members) the first time it is created; a later set keeps the position, as `[[Set]]` does. A Go port of `x ||= y` calls the setter even when `y` is absent, because Pi creates the property there;
- `AppendJS` writes the literal members, then the late members in the recorded order;
- decoding records the source order in the same list, so a decoded record re-encodes in its own order with canonical escapes and numbers, as Pi's `JSON.parse` + `JSON.stringify` does. No source bytes are copied back.

Go code that assigns a late field directly, without the setter, gets the default order (the order of first assignment in the main Pi construction site). The jsparity analyzer reports a direct write to a late field outside the generated package. `ErrorBeforeTimestamp` becomes one case of the order list and is deleted.

### Unknown-member carrier: `Object`

```go
type Object struct { raw []byte; index []member; edits []edit; canonical bool } // member = key/value spans into raw
func ParseObject(raw []byte) (Object, error)  // validates once, records spans, notes whether raw is already canonical
func (o Object) Get(key string) (Raw, bool)   // lazy: no value is decoded until asked
func (o *Object) Set(key string, v Raw)       // existing key keeps its position; a new key is appended (CreateDataProperty: "__proto__" is an ordinary own key)
func (o *Object) Delete(key string)
func (o Object) AppendJS(dst []byte) []byte   // unmodified and canonical: copies raw; otherwise canonical array-index keys (`0` to `4294967294`, no leading zero, as `jv.IsIndexKey`) first ascending, then the other keys in creation order (`"01"` and `"4294967295"` stay in creation order, as Node shows); a duplicate key keeps its first position and its last value; a deleted key that is set again is appended
```

`Get` on a duplicated key returns the last value, as `JSON.parse` keeps it. `Raw` values are owned: `Set` and the `Raw` constructors copy the bytes they receive, because hot writers reuse their buffers. `ParseObject` finds duplicates with a key index once the object has more than a small number of members (`jv` keeps a map index above 12 members and builds under TinyGo; a lookup index does not make output order depend on map iteration), so a large tool-argument object is not parsed in quadratic time. A port of `Object.assign(target, source)` uses `Set` and drops a `"__proto__"` key, because `[[Set]]` of `__proto__` changes the prototype and creates no own property.

Typed structs that Pi keeps open (session entries, MCP schemas, tool arguments, models.json entries per owner ruling A) embed `Object` for the members the struct does not name. `Object` replaces `orderedjson.Object`, `MarshalInSourceOrder`, `schemaObjectOrder` and the `Canonicalize` calls that exist only to normalize text PiG produced.

### Immutable values and explicit `Clone`

Generated types get a structural `Clone()` (slices and nested structs copied, `Raw` and `Object.raw` shared because they are never mutated). No value is copied through JSON. A `Frozen` marker field (question 4) is zero-size in normal builds and, if kept, is the first field of the struct (a trailing zero-size field adds padding: `struct{x int64; f struct{}}` is 16 bytes); under the `jsparitycheck` build tag (set by `make test-changed` for migrated packages) `Freeze()` stores a 64-bit digest of the encoded bytes, and every generated `AppendJS` and `Clone` recomputes it and panics with the type name when a frozen value changed. Tests therefore catch a mutation of a shared transcript value without a runtime cost in release builds.

### Generated encoders

For each listed Pi type the generator emits, next to the hand-written Go struct (`zz_jsparity_<pkg>.go`):

- `func (v *T) AppendJS(dst []byte) []byte`: constant key prefixes (`,"model":`), one branch per optional member, the late members in the recorded order, union members dispatched by their discriminant (an unknown discriminant, such as a `CustomAgentMessages` member an extension declares, is written from its `Object`);
- `MarshalJSON()` that calls `AppendJS`. A v1 or `sdkjson` caller still compacts and HTML-escapes the result unless its encoder disabled escaping (`extensions/sdk/json/encode.go:207` `Marshal` sets `escapeHTML: true`; `:487` `appendCompact` in `marshalerEncoder`), and the compaction pass is the 29.5% of long-session CPU above. Pi-facing writers therefore call `AppendJS` at the top level, and the jsparity analyzer reports `encoding/json.Marshal` or an HTML-escaping `Encoder` on a value whose type contains a generated type. Fields tagged `json:"-"` are runtime state and are not encoded;
- `Clone()` and, where needed, `Freeze()`;
- field-ID constants for `Object` edits.

Encoders write into a caller-owned buffer. Hot writers (session append, request body, frame writer) keep one buffer per owner and reuse it; no `sync.Pool` across owners.

### Codegen pipeline and drift gate

1. `test/parity/interface-extractor/src/extract-json-shapes.mjs` (TypeScript 5.9.3 checker, already pinned there) reads `internal/jsparity/shapes.toml`, which names each Pi type (`package`, `module`, `symbol`) and its Go type. It writes `test/parity/interfaces/json-shapes-v<version>.json`: members with optionality and `null`/`undefined` unions, discriminant literals, element and nested shape references, the construction sites with `file:line` (literal order, late assignments), and a SHA-256 per shape over the normalized shape (members, types, optionality, literal order), not over declaration text, so a comment edit in Pi does not reopen a shape. A construction-site hash is a review trigger, not a proof: the extractor fails closed on a construct it does not model (spread of a non-literal, `Object.assign`, a computed key, an `as any` write, `delete`) and lists it for review instead of skipping it.
2. `go run ./internal/jsparity/cmd/jsparitygen` joins each shape with the Go struct through `go/types` and the `json` tags. It fails when a Pi member has no Go field, when a Go field is not a Pi member and has no `jsparity:"pig-only D<N>"` tag, or when a Pi member name is an array index (typed members cannot reorder). It writes the encoders and `internal/jsparity/shapes.lock` (shape hash, generated file hash).
3. `make jsparity-check` regenerates both stages into a temporary tree and diffs them with the committed files. It fails when Pi changed a shape and the Go encoder was not regenerated, and when a generated file was edited by hand. It needs the Node extractor, so it runs in `ci-contracts` next to `interface-inventory-drift`, not in `ci-drift`; `automation/pin/pin.py ledgers` runs `make jsparity-generate` so pin-follow regenerates the shapes with the other per-pin ledgers.

### Proof

| abstraction | differential proof against Node | CI | nightly |
|---|---|---|---|
| `AppendString` | Go fuzz values (ASCII, C0, U+007F, U+2028/2029, astral pairs, WTF-8 lone units, WTF-8 split pairs, U+FFFD) sent to one Node child per package (TestMain, framed stdin), `JSON.stringify(s)` byte compare | fixed corpus + 2,000 seeded random values in `make test-changed` for touched packages | `go test -fuzz` 30 min (nightly job, not a lane run) |
| `AppendNumber` / `AppendInt` | random float64 bit patterns plus the boundaries (±0, 1e21, 1e-7, 2^53±1, subnormals, NaN, ±Inf) | same | same |
| `Object` | random objects with integer-like keys, duplicates and edits; Node applies the same `JSON.parse` + edits + `JSON.stringify` | same | same |
| each generated shape | the extractor emits a JavaScript builder per construction site from the Pi literal itself (`v => ({role: v.role, content: v.content, ...})`), so the order Node uses is Pi's, not PiG's; random member values go to both sides | per shape changed | all shapes |
| each Pi construction path | the same recorded stream (SSE fixture) drives Pi's provider `stream` function in Node and the PiG provider; `JSON.stringify(result)` and PiG's `AppendJS` of the final message must be equal. The witness builder above is derived from the same construction sites as the encoder, so it cannot find a wrong order; this row can (it finds B3 and the content_filter order) | per provider changed | all providers |
| each generated shape | corpus replay: Pi-written records from parity scenarios (session JSONL, faux-provider request bodies, RPC frames) decode then encode to the same bytes | yes | |
| migration | during migration the old encoder and the generated one run side by side on random values in a test; the test and the old encoder are deleted when the migration of that type completes | per migrated type | |

A byte difference fails the build. One Node process per package, killed in `t.Cleanup`, as the test-economy rules require.

### Migration order

Each step keeps the parity families and golden tests green and records `allocs/op` and `ns/op` before and after (`automation/ci/allocbase` entries updated in the same change):

1. Primitives, `Object`, `Opt`, the generator and the proofs, with no caller changes.
2. Session JSONL lines: `sessionentry` members and the `ai`/`agent` messages and content blocks they contain.
3. Provider request bodies: openai-completions (including the `Object.assign` order fix), openai-responses, anthropic, google, bedrock, mistral. These bodies are typed by the provider SDKs and built by conditional assignments (`openai-completions.ts:802-1009` `buildParams`), not by Pi types, so they are not generated: each body builder is ported statement by statement onto an ordered `Object`, which also removes the HTML escaping (Problem). Proof: the request body Pi's builder produces for the same model, context and options, byte compared.
4. RPC frames (`coding/cli/rpc_events.go`, `rpc_types.go`) and extension IPC frames (`coding/extension/host/subprocess`).
5. Durable records on the native host side (`durable/core/driver`, `durable/*.go`, and the JSON round-trip clones in `durable/storage/clone.go`). The TinyGo core (`durable/core/cmd/corewasm`) already imports no `encoding/json`, `fmt` or PiG `internal` package and encodes through `jv` (`GOOS=wasip1 go list -deps`). After step 1 it calls `internal/jsparity` through `jv`; `make durable-core-budget` must stay within `durable/core/budget/budget.json` for every step that adds code to the core.

Lone surrogates end to end, with steps 2 and 4. A code-mode string holding a lone UTF-16 unit loses it twice: `codemode/engine.go` `toString` applies `strings.ToValidUTF8`, and the session line, print `--mode json` and RPC encoders write invalid UTF-8 as U+FFFD. Pi writes `\ud800`. The fix carries the unit as WTF-8 through `toString` and writes it with the WTF-8-aware `AppendString` when those encoders migrate. Proof: a red test against Pi for the same code-mode result, byte compared on the session line, the print JSON event and the RPC frame.

Lane audit-followups owns the `ai` history clone and validation; it can adopt `Clone` and `Freeze` from step 1. Lane session-scale owns retention; `Object`'s lazy spans fit its offset index.

## Rejected

- A reflection-based ordered encoder: `reflect` per value is the cost being removed, and TinyGo restricts it.
- Routing Pi-facing bytes through `jsontext.Encoder`: lossy for lone surrogates (probe above).
- A canonicalize pass after encoding: the encoder writes canonical bytes, so the pass only costs.
- TypeScript declaration order as the member order: wrong for `AssistantMessage` (above).
- A static member order with named variants chosen at Go construction sites: the orders depend on stream data (Member order), and a Go site cannot know which variant Pi's control flow produced.

## Expected divergences

None. The `samplingParams` key order and the openai-completions, openai-responses and Codex top-level key order were PiG bugs, fixed before step 1 (`TestOpenAICompletionsRequestKeysFollowPiAssignmentOrder`, `TestOpenAIResponsesRequestKeysFollowPiAssignmentOrder`), not divergences. Any difference the proofs cannot close goes to the lead for a number before it lands.

## Questions for the lead

1. Go SDK: `extensions/sdk` is its own module and cannot import `internal/jsparity`. Proposal: the SDK keeps `extensions/sdk/json` until step 4, then gets a generated copy of the primitives under `extensions/sdk/internal/jsparity` with a drift check (as `sdk/json` is a checked copy of Go's v1 engine). Accept, or make the primitives a public SDK package?
   Reviewer recommendation: accept the generated copy. A public SDK package adds exported API under `sdk-apidiff`, and the root module requires `extensions/sdk v0.4.1` with no `replace`, so a `GOWORK=off` build (as `durable/core/Makefile` uses) would need a released SDK version before the TinyGo core could use it.
2. `shapes.toml` scope for step 2: `SessionEntry` members, `AgentMessage` members, `AssistantContentBlock` and `ToolResultMessageContent` members, `Usage`. Accept?
   Reviewer recommendation: accept with these additions. Add `SessionHeader`, because a session file is `FileEntry = SessionHeader | SessionEntry` (`packages/coding-agent/src/core/session-manager.ts:183-197`). Add the coding-agent `CustomAgentMessages` members `bashExecution`, `custom`, `branchSummary` and `compactionSummary` (`packages/coding-agent/src/core/messages.ts:71-76`), with an `Object` fallback for members that extensions declare (`packages/agent/src/types.ts:365-367`). Add `SystemMessage` with `Tool` and its schema `Object` (`packages/ai/src/types.ts:528-544`). Members typed `unknown` (`details`, `data`) are `Object` or `Raw`.
3. Primitives: move the `durable/core/jv` writers into `internal/jsparity` with `jv` delegating (needs the Durable lanes' agreement), or keep them in `jv` and have `internal/jsparity` import `durable/core/jv`? Either way one implementation remains. Separately, `jv.Object` holds decoded values while the proposed `Object` holds raw spans: one mechanism or two?
4. `Frozen`: the lead's ruling says never to add a field nothing reads, and in release builds nothing reads the zero-size marker on public structs (a trailing zero-size field also adds padding). Accept the field, or keep the frozen digest in a test-only side table?

## Review changes

Two reviews. rev-go-idioms (ACCEPT-WITH-FIXES, aefb42465f) moved the evidence into `docs/plan/evidence`, made the prototype assert byte equality, and corrected the array-index keys, the v1 adapter claim, step 5, the json-shapes path and the `Object.assign` finding (openai-responses, `SamplingParams`). It added questions 3 and 4. Its note that `jsontext` escapes U+2028 is withdrawn: the probe's `%q` print escapes U+2028 itself, and the bytes are literal (`e2 80 a8`, Go 1.27.2, both from a literal and from a `\u2028` escape). rev-go-idioms-design (ACCEPT-WITH-CHANGES) is merged on top of it. Its changes are listed below, and each is folded into the section named. Its member-order section replaces the control-flow variant rule from rev-go-idioms: that rule cannot express data-dependent orders (point 3).

1. Primitives: step 1 moves `durable/core/jv`'s TinyGo-built, Node-checked primitives into `internal/jsparity` instead of writing new ones (Package layout). The review checked `jv.AppendNumber` against Node `JSON.stringify` on 400,019 values (random bit patterns, scaled integers, the boundaries) with 0 differences. A mutation of the `1e-7` boundary (`-6 < n` to `-7 < n`) made both the review differential and `TestParseStringifyMatchesNode` fail.
2. `AppendString` joins a WTF-8 high unit followed by a WTF-8 low unit into one literal code point. `jv.AppendString` escapes both halves today, and Node prints the literal character (Primitives).
3. Member order is recorded per value at creation time, not chosen from static variants. Pi's openai-completions order differs from PiG's static order today (review probe; B3). Decoded records keep their order through the same list instead of copying source bytes back (Member order).
4. An optional member omits its zero value only with a reviewed `omitzero` citation (`Opt`).
5. `Object`: `Raw` ownership, a duplicate index, array-index key definition, `__proto__` handling under `Object.assign`, re-set after delete (`Object`).
6. v1 nesting still HTML-escapes `MarshalJSON` output; Pi-facing writers call `AppendJS` and an analyzer reports the v1 path (Generated encoders).
7. The codegen drift gate hashes normalized shapes, fails closed on construction-site constructs it does not model, and runs in `ci-contracts` (Codegen pipeline).
8. Proof adds a provider-level stream differential that runs Pi's own construction code, because the witness builder shares the encoder's assumptions (Proof).
9. Inventory adds the HTML and U+2028/2029 escaping of every provider request body and the nested sort of the sampling merge (Problem). Step 3 ports body builders onto `Object` rather than generating them (Migration order).
10. Step 5 is restated: the TinyGo core already has no `encoding/json`; the native Durable host is the target, under the core size budget (Migration order).
11. If the `Frozen` marker stays (question 4), it is the first struct field (Immutable values).
12. Recommendations on both lead questions (Questions for the lead).

Evidence limits: TinyGo is not installed on the review host, so TinyGo compatibility is inferred from `jv`, which uses the same primitive imports (`math`, `strconv`) in the TinyGo-built core, not from a new TinyGo build. The prototype (`docs/plan/evidence/jsparity-prototype_test.go.txt`) reproduces the table above (15.2 us/48 allocs to 0.51 us/0 allocs; 1.44 ms to 0.10 ms). Its `appendNumber` uses `strconv` `'g'` (`1.5e-7` prints `1.5e-07`) and its `appendString` has no surrogate handling, so it is a cost model only, not a conformance reference.
