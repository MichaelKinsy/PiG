# gate-992-coding2 progress

Slice: close the `make test-porting-release` gate for the second half of the packages/agent and packages/coding-agent pending upstream test files at upstream 0.99.2. Mirror: `.upstream/v0.99.2` (symlink to the extracted release tarball; `.upstream` is git-ignored).

## Split

`make test-porting-release` reports 207 pending files. Filtered to packages/agent and packages/coding-agent there are 70 pending or partial rows. Lane gate-992-coding published its split first (docs/plan/progress/gate-992-coding.md on its branch): it takes the first 29 carried files through `coding-agent/test/remote-catalog-provider.test.ts`, and this lane takes the 28 files below, starting at `resource-loader-theme.test.ts`. Rows outside both lists (codemode, mcp, tool-search, tool-renderer-examples, virtual-models at the top level, default-tools-setting, model-runtime-modify-models-compat, 2860 partial) belong to port-992-mcp or port-992-core.

## Second half (this lane)

1. `coding-agent/test/resource-loader-theme.test.ts`
2. `coding-agent/test/resource-loader.test.ts`
3. `coding-agent/test/rpc-prompt-response-semantics.test.ts`
4. `coding-agent/test/sdk-stream-options.test.ts`
5. `coding-agent/test/session-file-invalid.test.ts`
6. `coding-agent/test/session-id-readonly.test.ts`
7. `coding-agent/test/session-manager/file-operations.test.ts`
8. `coding-agent/test/session-manager/tree-traversal.test.ts`
9. `coding-agent/test/settings-manager.test.ts`
10. `coding-agent/test/settings-selector.test.ts`
11. `coding-agent/test/startup-session-name.test.ts`
12. `coding-agent/test/stdout-cleanliness.test.ts`
13. `coding-agent/test/suite/agent-session-compaction.test.ts`
14. `coding-agent/test/suite/agent-session-runtime.test.ts`
15. `coding-agent/test/suite/agent-session-tool-orchestration.test.ts`
16. `coding-agent/test/suite/regressions/5943-session-start-notify.test.ts`
17. `coding-agent/test/suite/regressions/7150-rpc-prompt-during-compaction.test.ts`
18. `coding-agent/test/suite/virtual-models.test.ts`
19. `coding-agent/test/syntax-highlight.test.ts`
20. `coding-agent/test/system-prompt.test.ts`
21. `coding-agent/test/system-theme.test.ts`
22. `coding-agent/test/theme-controller.test.ts`
23. `coding-agent/test/theme-detection.test.ts`
24. `coding-agent/test/theme-export.test.ts`
25. `coding-agent/test/theme-style.test.ts`
26. `coding-agent/test/themed-text.test.ts`
27. `coding-agent/test/tool-execution-component.test.ts`
28. `coding-agent/test/tools.test.ts`

## Pending rows in the area not in either half (other lanes)

- `agent/test/agent-loop.test.ts`
- `agent/test/agent.test.ts`
- `agent/test/harness/pico3/legacy-tracker.test.ts`
- `agent/test/harness/pico3/spec-view-events.test.ts`
- `coding-agent/test/agent-session-concurrent.test.ts`
- `coding-agent/test/agent-session-dynamic-tools.test.ts`
- `coding-agent/test/clipboard-image-native-errors.test.ts`
- `coding-agent/test/clipboard-image.test.ts`
- `coding-agent/test/clipboard-paste-file-paths.test.ts`
- `coding-agent/test/codemode-renderer.test.ts`
- `coding-agent/test/codemode-worker-config.test.ts`
- `coding-agent/test/compaction-nested-calls.test.ts`
- `coding-agent/test/default-tools-setting.test.ts`
- `coding-agent/test/edit-tool-legacy-input.test.ts`
- `coding-agent/test/experimental-cli-entry.test.ts`
- `coding-agent/test/extensions-discovery.test.ts`
- `coding-agent/test/extensions-runner.test.ts`
- `coding-agent/test/footer-width.test.ts`
- `coding-agent/test/image-resize-callers.test.ts`
- `coding-agent/test/interactive-tui.test.ts`
- `coding-agent/test/jev-router-example.test.ts`
- `coding-agent/test/llama-extension.test.ts`
- `coding-agent/test/mcp-command.test.ts`
- `coding-agent/test/mcp-extension.test.ts`
- `coding-agent/test/mcp-oauth-refresh.test.ts`
- `coding-agent/test/model-catalog-protocol.test.ts`
- `coding-agent/test/model-resolver.test.ts`
- `coding-agent/test/model-runtime-classifiers.test.ts`
- `coding-agent/test/model-runtime-cloudflare-compat.test.ts`
- `coding-agent/test/model-runtime-images.test.ts`
- `coding-agent/test/model-runtime-modify-models-compat.test.ts`
- `coding-agent/test/nested-tool-calls.test.ts`
- `coding-agent/test/package-command-paths.test.ts`
- `coding-agent/test/package-manager.test.ts`
- `coding-agent/test/remote-catalog-provider.test.ts`
- `coding-agent/test/suite/agent-session-codemode.test.ts`
- `coding-agent/test/suite/agent-session-mcp-oauth.test.ts`
- `coding-agent/test/suite/agent-session-mcp.test.ts`
- `coding-agent/test/suite/regressions/2860-replaced-session-context.test.ts`
- `coding-agent/test/tool-renderer-examples.test.ts`
- `coding-agent/test/tool-search.test.ts`
- `coding-agent/test/virtual-models.test.ts`

## Phase 1 (port only) result

Method per the owner-approved change: every file is credited to existing Go coverage with per-case evidence in `test/parity/interfaces/test-mapping-v0.99.2.json`, or ported. No production code was fixed. Finding: the 0.99.1 porters already ported every 0.99.x change in these 28 files (the Go tests cite `.upstream/v0.99.1/...` lines) but the rows stayed pending because the upstream hash changed. A full upstream diff of each file (0.87.1 to 0.99.2) against the Go tests found two missing cases, both ported now.

Status per file (all 28 rows are now `ported` in the mapping; `make test-porting-release` no longer lists any of them):

| upstream file (packages/coding-agent/test/) | status |
|---|---|
| `resource-loader-theme.test.ts` | credited (2 cases; ported in 0.99.1 by the f9a lane) |
| `resource-loader.test.ts` | credited (50 sites: 42 from the 0.87.1 review, 8 new ones in cmd/pig/extension_set_upstream_test.go) |
| `rpc-prompt-response-semantics.test.ts` | credited (7 cases) |
| `sdk-stream-options.test.ts` | credited (10 cases) |
| `session-file-invalid.test.ts` | credited (1 case, harness-only change) |
| `session-id-readonly.test.ts` | credited (6 cases, harness-only change) |
| `session-manager/file-operations.test.ts` | credited (31 sites) |
| `session-manager/tree-traversal.test.ts` | credited (31 sites) |
| `settings-manager.test.ts` | credited (51 sites) |
| `settings-selector.test.ts` | credited (4 cases) |
| `startup-session-name.test.ts` | credited (1 case, harness-only change) |
| `stdout-cleanliness.test.ts` | credited (2 cases, harness-only change) |
| `suite/agent-session-compaction.test.ts` | credited (28 sites) |
| `suite/agent-session-runtime.test.ts` | credited (12 sites) |
| `suite/agent-session-tool-orchestration.test.ts` | credited 2 cases + ported-passing case 2 (`TestSessionToolOrchestrationRegistersCodemodeAndToolSearchInactiveUntilNamed`, commit 7480f5c1e) |
| `suite/regressions/5943-session-start-notify.test.ts` | credited (7 cases, harness-only change) |
| `suite/regressions/7150-rpc-prompt-during-compaction.test.ts` | credited (1 case) |
| `suite/virtual-models.test.ts` | credited 12 cases + ported-FAILING 1: `TestVirtualSuiteProjectsTheSessionOncePerRequestUnderAVirtualSelection` fails with `projections = 0, want 4` (the `Session.BuildSessionProjectionCalls` stub in internal/codingagent/session_projection_calls.go returns 0) |
| `syntax-highlight.test.ts` | credited (6 of 8 cases; :37 half and :67 designed out per the earlier lead approval, no D-number) |
| `system-prompt.test.ts` | credited (14 sites) |
| `system-theme.test.ts` | credited (7 cases) |
| `theme-controller.test.ts` | credited (9 cases) |
| `theme-detection.test.ts` | credited (5 cases) |
| `theme-export.test.ts` | credited (3 cases) |
| `theme-style.test.ts` | credited (6 cases) |
| `themed-text.test.ts` | credited (1 case) |
| `tool-execution-component.test.ts` | credited (25 sites) |
| `tools.test.ts` | credited (85 sites) |

## Phase 1 failing list

1. `coding` `TestVirtualSuiteProjectsTheSessionOncePerRequestUnderAVirtualSelection` (ports `suite/virtual-models.test.ts:291`): `projections = 0, want 4`. Phase 2 must count calls of `internal/codingagent.Session.BuildSessionProjection` (upstream spies on `buildSessionProjection`, called per request, between turns and for the post-run compaction check) and check that Go's call sites total 4 for the echo-tool scenario. The stub returns 0.

Not failing but not mine: `internal/codingagent/tools` `TestBashStructuredResultMatchesRecordedUpstream` fails (`oracle = 0.99.1 with 6 cases`): the recorded oracle `testdata/bash-structured-oracle.json` is still 0.99.1 and the test expects 0.99.2 (oracle goldens belong to port-992-core; regenerating needs the published 0.99.2 package). The `RPC33*` and `FauxAgentObservationOracle` tests in `coding` fail for the same reason.

## Stubs and notes for other lanes

- `internal/codingagent/session_projection_calls.go`: `Session.BuildSessionProjectionCalls` stub (returns 0). The `make interface-go` output for it (`test/parity/interfaces/pig-go.json`) is in the last commit; `make known-gaps` cannot regenerate while the gate is red, so `docs/parity/KNOWN-GAPS-0.3.x.md` is left to the central run.
- Line citations inside `coding/sdk_stream_options_upstream_test.go` for the unchanged cases use 0.87.1 numbering; the assertions match.
- Environment: the tests ran with a temporary HOME, `PIG_HOME` and agent dir; `extensions/sdk-ts/node_modules` was installed with `npm ci --min-release-age=0` in this worktree.

## Test runs

All named evidence tests (64 in internal/codingagent, 52 in cmd/pig, 53 in coding, 26 in internal/codingagent/tools, 19 in tui and the rest, 229 functions over 11 packages) pass with `go test -count=1` except the one above.

## Phase 2 (green)

`TestVirtualSuiteProjectsTheSessionOncePerRequestUnderAVirtualSelection` is green. Cause: `Session.BuildSessionProjectionCalls` was a stub, and once counted, Go built the projection 6 times (upstream 4: one per request, one between turns, one for the post-run check). `prepareRequest` and `prepareNextTurn` rebuilt the projection inside `exceedsCompactionThreshold`, where upstream passes the projection it already built (`agent-session.ts:725-733` takes `projection`; `:742-810` builds once per request). Fix: `exceedsCompactionThreshold(model, projection)` and `contextFromProjection` in `coding/session_transcript.go`; `Session.projectionCalls` atomic counter in `internal/codingagent/session.go`. Mutations: a stub counter returns 0 (red, phase 1); the old double build gives 6 (red, seen before the fix). Load: `GOMAXPROCS=4 taskset -c 0-3` with four CPU burners, `-race -count=24` over `TestVirtualSuite*`, `TestVirtualModel*` and `TestAgentSessionCompactionSuite*`: pass. `go test -race ./coding ./internal/codingagent`: only the oracle tests named above fail (same as the base tree).
