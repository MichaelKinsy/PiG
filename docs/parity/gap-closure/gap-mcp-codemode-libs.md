# Pi 1.0.4 `packages/mcp` and `packages/codemode`: file map, test map and closure

Scope: the two upstream packages as libraries (Go packages `mcp`, `mcp/oauth`, `mcp/mcptest`, `codemode`). The coding-agent extensions that use them belong to other lanes. Pinned upstream: 1.0.4 (`.upstream/current`). PORT_MAP.md is not edited here; the integration step adds the rows below.

## Method

- Every upstream source file is mapped to its Go file with a `// Ports packages/...` marker line.
- Pi's tests were already ported (section "Test map"). Ported tests prove the cases Pi's authors chose, not the behavior of Pi's functions. This lane added a differential oracle: `mcp/testdata/upstream-oracle.mjs` and `codemode/testdata/upstream-oracle.mjs` load Pi's pure functions from the mirror with Node 24 (native type stripping), run them on a corpus plus seeded random inputs, and write `upstream-golden.json`. The Go tests `mcp/upstream_oracle_test.go`, `mcp/oauth/upstream_oracle_test.go` and `codemode/upstream_oracle_test.go` compare every case and report all differences at once.
- Regenerate: `node mcp/testdata/upstream-oracle.mjs > mcp/testdata/upstream-golden.json` and `node codemode/testdata/upstream-oracle.mjs > codemode/testdata/upstream-golden.json` from the repository root.

## Percentages by the PORT_MAP file rule

The rule is ported / (ported + partial + broken + not started); designed-out (`n/a`) rows leave the denominator.

| scope | before | after |
|---|---|---|
| these two packages, 28 source files, 6 designed out, 22 intended | not in PORT_MAP (0 rows counted). Re-measured with the oracle: 16 ported, 6 partial of 22 | 17 ported, 5 partial of 22 (review: `client.ts` is partial, R1) |
| PORT_MAP overall (AGENTS.md block: 379 of 493) adding these rows | 379 / 493 = 76.9% as published; 395 / 515 = 76.7% with the 22 rows at their pre-lane state | 396 / 515 = 76.9% |

## Source file map

Status: ported = the oracle and ported tests show no difference; partial = a residual listed in "Open residuals"; n/a = designed out.

| upstream file | Go file (evidence) | status |
|---|---|---|
| `mcp/src/auth-provider.ts` | `mcp/auth_provider.go:9` | ported |
| `mcp/src/client.ts` | `mcp/client.go:17` (validators 213-326, pagination 638-654, `connect` 354-402) | ported (R1 closed, residuals a-c) |
| `mcp/src/protocol/content.ts` | `mcp/content.go:12`, `ToLLMContent` 170 | ported (R1 closed, residuals a-c) |
| `mcp/src/protocol/jsonrpc.ts` | `mcp/jsonrpc.go:12`, `ParseJSONRPCMessage`, `IsRequest/IsNotification/IsResponse` | ported (fixed in this lane) |
| `mcp/src/protocol/types.ts` | `mcp/types.go:8` | ported |
| `mcp/src/transports/transport.ts` | `mcp/transport.go:7` | ported |
| `mcp/src/transports/stdio.ts` | `mcp/stdio.go:22`, `stdio_unix.go`, `stdio_windows.go` | ported |
| `mcp/src/transports/streamable-http.ts` | `mcp/streamable_http.go:23`, `mcp/sse.go:11` | ported |
| `mcp/src/transports/in-memory.ts` | `mcp/mcptest/inmemory.go:12` | ported |
| `mcp/src/oauth/types.ts` | `mcp/oauth/types.go:16`, `jsonextra.go` | partial (R2, R4) |
| `mcp/src/oauth/discovery.ts` | `mcp/oauth/discovery.go:19` | partial (R2, R4) |
| `mcp/src/oauth/flow.ts` | `mcp/oauth/flow.go:21` | ported |
| `mcp/src/oauth/provider.ts` | `mcp/oauth/provider.go:13` | ported |
| `mcp/src/oauth/callback.ts` | `mcp/oauth/callback.go:16` | ported |
| `mcp/src/oauth/errors.ts` | `mcp/oauth/errors.go:8` | ported |
| `mcp/src/index.ts`, `mcp/src/oauth/index.ts`, `mcp/src/testing/index.ts` | package exports of `mcp`, `mcp/oauth`, `mcp/mcptest` | n/a (TypeScript re-export barrels) |
| `codemode/src/declarations.ts` | `codemode/declarations.go:22-331` | ported |
| `codemode/src/identifier.ts` | `codemode/identifier.go:8` | ported |
| `codemode/src/source.ts` | `codemode/source.go:15,47,117` | ported (fixed in this lane) |
| `codemode/src/types.ts` | `codemode/types.go:10` | ported (`Tool.Signature` is `*string` since this lane) |
| `codemode/src/wasm.ts` | `codemode/assets.go`, `SandboxOptions.Wasm` (`types.go:121`) | ported (no per-path cache: the module compiles once per `Sandbox`) |
| `codemode/src/runtime/host.ts` | `codemode/sandbox.go:14-156`, `execution.go:39-577` | ported |
| `codemode/src/runtime/prelude-source.ts` | `codemode/assets/prelude.js` (SHA-256 `8fcea803…`, byte-equal to `PRELUDE_SOURCE`, checked in this lane), `assets.go:9` | ported |
| `codemode/src/runtime/worker.ts` | `codemode/engine.go` (QuickJS on wazero in the host process) | n/a: no worker thread; `haltcheck.go` and an atomic interrupt replace the SharedArrayBuffer interrupt |
| `codemode/src/runtime/protocol.ts` | none | n/a: worker message protocol; host and engine share a process |
| `codemode/src/index.ts` | package exports of `codemode` | n/a (barrel) |

## Test map

109 upstream cases in 8 files: 106 ported, 3 designed out. Per-file closure evidence lives in `test/parity/interfaces/test-mapping-v1.0.4.json`.

| upstream test file | cases | Go tests | note |
|---|---|---|---|
| `mcp/test/client.test.ts` | 12 | `mcp/client_test.go` `TestClient…` (one per case) | 12 ported |
| `mcp/test/content.test.ts` | 2 | `mcp/content_test.go` | 2 ported |
| `mcp/test/stdio.test.ts` | 2 | `mcp/stdio_test.go`, `mcp/stdio_unix_test.go` | 2 ported (the second is `it.skipIf(win32)` upstream and unix-only here) |
| `mcp/test/streamable-http.test.ts` | 14 | `mcp/streamable_http_test.go` | 13 ported; "calls fetch without a receiver" is designed out (Go has no receiver-binding failure; `TestStreamableHTTPTransportRoutesEveryRequestThroughTheFetchInUse` covers routing) |
| `mcp/test/oauth.test.ts` | 11 | `mcp/oauth/oauth_test.go` | 11 ported |
| `codemode/test/declarations.test.ts` | 12 | `codemode/declarations_test.go` | 12 ported |
| `codemode/test/source.test.ts` | 4 | `codemode/source_test.go` | 4 ported |
| `codemode/test/sandbox.test.ts` | 52 | `codemode/sandbox_test.go`, `bridge_test.go`, `worker_config_upstream_test.go` | 50 ported; "accepts a worker path string" and "reports a missing worker file as a sandbox error" designed out (approved 2026-10-01: no worker file exists) |
| helpers and fixtures: `mcp/test/helpers.ts`, `fixtures/stdio-server.mjs`, `fixtures/stubborn-server.mjs`, `codemode/test/fixtures/raw-worker.ts` | n/a | `mcp/testutil_test.go`, `mcp/fixtures_test.go`; `raw-worker.ts` has no Go counterpart (worker fixture) | support files |

New in this lane, beyond Pi's cases: three oracle suites with about 3,000 compared cases. The review added fixed cases for the percent-encoded pathname comparison in `selectResource` and for JavaScript's Unicode `\s` in challenges: neither claimed fix had a case before (a mutation reverting each passed).

## Bugs found and fixed at the source

Each was invisible to the ported tests and found by the oracle. Red counts are the cases that differed before the fix.

| cause | fix | where |
|---|---|---|
| `@options` JSON errors carried Go's message; Pi appends V8's `JSON.parse` text (65 of 179 source cases) | validate with `internal/jsonparse` | `codemode/source.go` |
| JSON string escapes naming an unpaired surrogate became U+FFFD; Pi keeps the unit and `JSON.stringify` prints `\ud800` (2 schema cases) | own JSON decoder keeps WTF-8; `jsQuote` and `compareUTF16` read UTF-16 units | `codemode/jsvalue.go` |
| explicit empty `signature` treated as absent; Pi renders `declare function b;` (7 declaration cases) | `Tool.Signature` is `*string` | `codemode/types.go`, `declarations.go` |
| `WWW-Authenticate` field value with an inner quote returned empty; `\s` and `/i` used RE2 ASCII rules (6 cases) | JavaScript `\s` class, ASCII-only folding, Pi's empty-value rule | `mcp/oauth/discovery.go` |
| URLs read with `net/url`: default ports, dot segments, `\`, punycode, tab stripping, `https:///x`, port 65536, scheme-relative `//` path, origin `null` for other schemes (21 discovery and 306 resource cases) | WHATWG http/https serializer `nodeurl.ParseHTTPHref`; `parseURL` uses it; escaped paths compared | `internal/nodeurl/httpurl.go`, `mcp/oauth/types.go`, `discovery.go` |
| empty `scopes_supported`-style arrays dropped on re-marshal (9 cases) | `omitzero` instead of `omitempty` | `mcp/oauth/types.go` |
| `expires_in` via a narrow number reader (arrays, `0x10`, booleans rejected) (4 cases) | `jsnumber.FromJSON`, Pi's order of checks | `mcp/oauth/types.go` |
| `client_*_at: null` read as 0; unmodeled or ill-typed registration members dropped (21 cases) | `null` absent; `OAuthClientInformation.Extra` keeps Pi's `...input` spread | `mcp/oauth/types.go` |
| request or notification with an invalid `error` member rejected; `"method": null` accepted (27 cases) | `hasError` flag; method must be a JSON string | `mcp/jsonrpc.go` |
| (review) one ill-typed member of a registration response made `json.Unmarshal` drop every typed member, so `token_endpoint_auth_method` was lost and `selectClientAuthMethod` (flow.ts:125) chose another method; members differing in case filled typed fields | each member decodes on its own and only under its exact name | `mcp/oauth/types.go` `ParseClientInformation`; `TestParseClientInformationKeepsTypedMembersBesideAnIllTypedOne`, `TestParseClientInformationReadsExactMemberNames` |
| (review) a metadata member differing in case from a modeled one (`Scopes_Supported: 5`) failed `ParseProtectedResourceMetadata` | `unmarshalWithExtra` decodes only exact names | `mcp/oauth/jsonextra.go`; 3 fixed oracle cases |
| (review) `error.code` or `error.message` `null` accepted; `error.code` beyond the float64 range rejected (4 of 8 fixed cases) | JSON number token and JSON string checks | `mcp/jsonrpc.go`; 8 fixed oracle cases |

## Open residuals (not divergences: Go can match each; see the review below)

- R1 (closed in `mcp`; residuals below) `validateCallToolResult` and `ToLLMContent` with a server block that omits a member its type requires or gives it another JSON type. `ContentBlock` and `CallToolResult` now decode leniently (`mcp/content.go` `UnmarshalJSON`): a member of another JSON type, an absent member and a block that is not an object are not decode errors, `isError` is read for its JavaScript truthiness (client.ts passes it through and tools.ts tests `result.isError`), and `ToLLMContent` renders an absent member as `undefined` and another type as a JavaScript template literal does (`[audio undefined omitted]`, `[audio [object Object] omitted]`, `undefined: a,,2`, an empty `mimeType` is kept, a null one is `unknown type`). `TestClientPassesLenientToolResultBlocksThroughLikePi` holds the Node-measured results of Pi's `toLlmContent`. Residuals: (a) `{type:"text", text: 5}` or `text: null` is copied through by Pi as a number or null, and `LLMContent.Text` is a Go string, so it holds "5" or "null"; (b) a `null` block or a `resource` block whose `resource` is not an object is a `TypeError` in Pi's `toLlmContent`, which has no Go result, so Pig renders the unsupported placeholder; (c) `coding/mcpext` `ToModelContent` reads the typed fields, so a `resource_link` without `uri` renders `[Resource  ""]` where Pi's tools.ts renders `[Resource undefined "undefined"]`.
- R2 OAuth URLs with a special scheme other than http and https (`ftp:`, `ws:`, `wss:`, `blob:`, `file:`): Pi accepts them through WHATWG `URL`; Pig keeps `net/url`. MCP OAuth endpoints must be http(s) (`secureEndpoint`), so no flow completes with them, but discovery fails earlier in Pig (`Invalid URL`) than in Pi (the fetch fails). Fix: `nodeurl.ParseHTTPHref` already implements the special-authority parser; `ftp`, `ws` and `wss` differ only in their default ports (21, 80, 443), and `blob:` takes the origin of its inner URL.
- R3 (closed) `schemaToType`: a `$ref` segment with malformed percent-encoding (`%FF`, `%2`, an encoded surrogate) makes Pi's `decodeURIComponent` throw `URIError` out of `renderDeclarations` and `renderToolSample`. `SchemaToType`, `RenderToolOutputType`, `RenderToolSignature`, `RenderToolSample` and `RenderDeclarations` now return that failure as their error; `CreateDescription` and the codemode `Execute` return it, and `prepareLoadout` panics with it as a thrown hook (the session reports a `prepare_loadout` extension error). `TestSchemaToTypeMatchesPi` requires the error for the three golden cases and `TestMalformedRefPercentEncodingIsAnError` covers every render function.
- R4 (review) http(s) URLs whose WHATWG serialization `url.URL.String()` cannot spell: `|` in a path, `{`, `|` or `` ` `` in a fragment, an empty fragment (`https://e.com/x#`), and a path with malformed percent-encoding (`https://e.com/%zz`). `urlFromHTTP` stores the WHATWG pathname in `RawPath`, but `url.URL.EscapedPath` rejects it and re-encodes (`/a%7Cb` for Pi's `/a|b`); for `%zz` `url.PathUnescape` fails and the path is dropped, so discovery and the resource URL use the bare origin. Fix: carry the WHATWG href next to the `url.URL` in `DiscoveryURL`, `OAuthChallenge.ResourceMetadataURL` and `ResourceURLFromServerURL` (an API change), or record a numbered divergence.

## Gates

Run in this worktree with `PIG_CODING_AGENT_DIR`, `PI_CODING_AGENT_DIR` pointing at temp directories: `go test -race ./mcp/... ./codemode/... ./internal/nodeurl/...`, `go vet` (plus `GOOS=windows` and `GOOS=darwin`), `golangci-lint` on the touched packages, `make divergence-guard`, source-hygiene.
