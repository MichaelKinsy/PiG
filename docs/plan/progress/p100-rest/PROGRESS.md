# p100-rest: the remaining Pi 1.0.0 packages and designed-out rows

READY. Scope here is server plus the 14 small rows; telemetry and evals moved to sg-telemetry-evals.

Branch `team/smc1/p100-rest` from `agg-100` (`8f20f112f`). Upstream `.upstream/v1.0.0`.

## Handoff to sg-telemetry-evals

The owner moved the unstarted sections to generator-fed lanes. `telemetry` and `evals` now belong to sg-telemetry-evals. This lane keeps `server` and the 14 small rows.

- **telemetry** is committed here and needs no more work from this lane: `telemetry/schema.go` (schema vocabulary, `CreateTypedSpanStarter`), `telemetry/telemetrytest/` (the adapter conformance suite), `telemetry/telemetry_test.go`, `telemetry/conformance_test.go`, and the nil-attribute fix in `telemetry/memory.go`. Both test-mapping rows are `ported` with the Go-impossible parts (Proxy payloads, type-level inference) recorded in their rationales. Review them before regenerating stubs: the generator reports `telemetry/` at 12 of 41 declarations, and the stubs for the rest are the type-level inference aliases that have no Go form.
- **evals** was read in full and not started in the tree. Nothing is committed. The generator ran into a scratch directory (`gen-go-stubs --package evals --scope module`, 39 declarations, 6 test skeletons, 43 cases) and compiled. Notes for the takeover:
  - `harness.test.ts`, `plan.test.ts`, `comparison.test.ts` and `report.test.ts` have no external dependency beyond `plan.ts`/`report.ts` pure functions, except that `readTaskObservation` reads Vitest JSON through `@vitest-evals/core` (`readReportWorkspace`, `readVitestJsonReportFile`). That package is not vendored in `.upstream/v1.0.0`, so its report-workspace shape must be derived from the `meta` blocks the tests write (`eval.avgScore`, `harness.run.{usage,timings,artifacts,errors}`).
  - `acme-server.test.ts` and `configured-runtime.test.ts` need the Go model runtime (`inspectProvider`, `inspectAddedModel` over `models.json`) and an `httptest` server; the fixture constants and handlers are in `evals/acme-server.ts`.
  - `harness.ts` and `docker.ts` drive `AgentSession`, UID dropping and Docker; only `resolveModelSelection`, `applyIsolatedEnvironment`, `resolveDocumentationVariant`, `excludePiDocumentation`, `verifySystemPrompt` and the sandbox-identity check are exercised by the tests.
  - The six `packages/evals/test/*` rows are still `designed-out` with the old scope rationale; flip them with evidence when ported.
- Go package location was not decided. `evals/` beside `telemetry/` is the obvious home.

## Done here (rows flipped in `test-mapping-v1.0.0.json`)

| Row | Result |
|---|---|
| `packages/server/test/*` (6) | already `ported` by agg-100; all of `src/` is cited by `// Ports` comments in `internal/experimental/routing`; tests green |
| `packages/telemetry/test/*` (2) | `ported` (see above) |
| `ai/test/uuid` | `ported`: `ai/uuid.go` with an injectable clock and random source; `agent/harness/session.UUIDv7` now calls it; PORT_MAP row `✅` |
| `agent/test/proxy` | `ported`: `agent/proxy.go` already ported `streamProxy`, the old reason was wrong |
| `ai/test/message-types` | `ported`: runtime half over `NormalizeContext`; the compile-time half is Go-impossible |
| `ai/test/mistral-tool-schema` | `ported`: symbol-key assertions are Go-impossible, strict payload and no validation failure ported |
| `ai/test/openai-responses-partial-json-cleanup` | `ported`: a Go `ToolCall` does carry scratch `partialJson`; the old reason was wrong |
| `coding-agent/test/documentation` | `ported` over `docs/site/docs`; PiG's docs pass |
| `coding-agent/test/test-harness` | `ported` (16 cases) over `recoveryHarness`; `scriptedProvider.wrap` added |
| `coding-agent/test/version-check` | `ported` over the signed update manifest; bug found and fixed: the manifest request sent no `Accept` header and Go's default `User-Agent` (now the D65 identity; D65 call sites updated); PORT_MAP row `🟡` |
| `ai/test/lazy-module-load`, `codemode-worker-config`, `package-distribution`, `restore-sandbox-env`, `8237`, `9540` | stay `designed-out`; each rationale now names the Node, Bun or npm mechanism that no Go test can observe |

## Evidence

Red/green for the fixes: `telemetry` nil attributes (recording case red before), `version-check` identity headers (red before). Mutations that turned the new cases red are recorded in each row's rationale. `go run ./test/parity/cmd/testinventorycheck` passes (610 files: 521 ported, 1 partial, 56 designed-out, 31 pending). `make port-map-drift` is clean. Coverage was regenerated with `make coverage RESULTS=` because the default results file is stamped 0.87.1.

Known failures on the base, not caused here:

- Six `coding` oracle tests stamped 0.99.2 (R2 in the agg-100 progress file).
- `cmd/pig` `TestRPCMessageUpdateStreamingVariantBytesMatchPi`: fails the same way on a clean checkout of `agg-100` (`8f20f112f`). The Pi oracle script exits with `ERR_MODULE_NOT_FOUND` for `extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/modes/json-event.js`, because the worktree has no `extensions/sdk-ts/node_modules`. It is a missing local install (the `make parity-deps` step), not a code failure. The same install is why the generator run needed a symlink to another lane's `node_modules`.

## Not run

`make test-porting-release`, `make lint` (full), `make parity` and the CI shards were not run in this lane. Package-level `golangci-lint` on `./telemetry/... ./ai/ ./agent/ ./coding/ ./internal/codingagent/ ./test/docs-drift/` reports only the pre-existing `newAnthropicTestProvider` unused finding in `ai`.
