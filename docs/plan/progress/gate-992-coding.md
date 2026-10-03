# gate-992-coding progress

Slice: close the `make test-porting-release` gate for the carried (pending at 0.99.1, test unchanged in 0.99.2) upstream test files of packages/agent and packages/coding-agent at upstream 0.99.2. Mirror: `.upstream/v0.99.2` (extracted from the cached release tarball; `.upstream` is git-ignored).

## Split with gate-992-coding2 (lead)

The 57 carried files sorted by path. **This lane (gate-992-coding) takes the first 29, through `coding-agent/test/remote-catalog-provider.test.ts` (the odd middle file goes to the first half). gate-992-coding2 takes the last 28, from `coding-agent/test/resource-loader-theme.test.ts`.** This lane does not touch the second half. The changed/new files owned by port-992-mcp and port-992-core are not in either list.

### First half (this lane)
1. `agent/test/agent-loop.test.ts`
2. `agent/test/agent.test.ts`
3. `agent/test/harness/pico3/legacy-tracker.test.ts`
4. `agent/test/harness/pico3/spec-view-events.test.ts`
5. `coding-agent/test/agent-session-concurrent.test.ts`
6. `coding-agent/test/agent-session-dynamic-tools.test.ts`
7. `coding-agent/test/clipboard-image-native-errors.test.ts`
8. `coding-agent/test/clipboard-image.test.ts`
9. `coding-agent/test/clipboard-paste-file-paths.test.ts`
10. `coding-agent/test/compaction-nested-calls.test.ts`
11. `coding-agent/test/edit-tool-legacy-input.test.ts`
12. `coding-agent/test/experimental-cli-entry.test.ts`
13. `coding-agent/test/extensions-discovery.test.ts`
14. `coding-agent/test/extensions-runner.test.ts`
15. `coding-agent/test/footer-width.test.ts`
16. `coding-agent/test/image-resize-callers.test.ts`
17. `coding-agent/test/interactive-tui.test.ts`
18. `coding-agent/test/jev-router-example.test.ts`
19. `coding-agent/test/llama-extension.test.ts`
20. `coding-agent/test/mcp-oauth-refresh.test.ts`
21. `coding-agent/test/model-catalog-protocol.test.ts`
22. `coding-agent/test/model-resolver.test.ts`
23. `coding-agent/test/model-runtime-classifiers.test.ts`
24. `coding-agent/test/model-runtime-cloudflare-compat.test.ts`
25. `coding-agent/test/model-runtime-images.test.ts`
26. `coding-agent/test/nested-tool-calls.test.ts`
27. `coding-agent/test/package-command-paths.test.ts`
28. `coding-agent/test/package-manager.test.ts`
29. `coding-agent/test/remote-catalog-provider.test.ts`

### Second half (gate-992-coding2, not touched here)
30. `coding-agent/test/resource-loader-theme.test.ts`
31. `coding-agent/test/resource-loader.test.ts`
32. `coding-agent/test/rpc-prompt-response-semantics.test.ts`
33. `coding-agent/test/sdk-stream-options.test.ts`
34. `coding-agent/test/session-file-invalid.test.ts`
35. `coding-agent/test/session-id-readonly.test.ts`
36. `coding-agent/test/session-manager/file-operations.test.ts`
37. `coding-agent/test/session-manager/tree-traversal.test.ts`
38. `coding-agent/test/settings-manager.test.ts`
39. `coding-agent/test/settings-selector.test.ts`
40. `coding-agent/test/startup-session-name.test.ts`
41. `coding-agent/test/stdout-cleanliness.test.ts`
42. `coding-agent/test/suite/agent-session-compaction.test.ts`
43. `coding-agent/test/suite/agent-session-runtime.test.ts`
44. `coding-agent/test/suite/agent-session-tool-orchestration.test.ts`
45. `coding-agent/test/suite/regressions/5943-session-start-notify.test.ts`
46. `coding-agent/test/suite/regressions/7150-rpc-prompt-during-compaction.test.ts`
47. `coding-agent/test/suite/virtual-models.test.ts`
48. `coding-agent/test/syntax-highlight.test.ts`
49. `coding-agent/test/system-prompt.test.ts`
50. `coding-agent/test/system-theme.test.ts`
51. `coding-agent/test/theme-controller.test.ts`
52. `coding-agent/test/theme-detection.test.ts`
53. `coding-agent/test/theme-export.test.ts`
54. `coding-agent/test/theme-style.test.ts`
55. `coding-agent/test/themed-text.test.ts`
56. `coding-agent/test/tool-execution-component.test.ts`
57. `coding-agent/test/tools.test.ts`

## Finding

The 0.99.1 porters already updated most Go tests for the 0.87.1 to 0.99.1 test deltas (they cite `.upstream/v0.99.1/...` lines) but left the ledger rows pending because the upstream test hash changed. Work per file: diff 0.87.1 against 0.99.2, verify the Go evidence covers every changed case, port what is missing, then re-mark the row with qualified evidence.

## Per-file log

Phase 1 (port only; owner-approved method change). Status per upstream file: **credited** (existing Go coverage, mapping row updated with per-case evidence), **ported-passing**, **ported-FAILING**. Verified with `go test` of the evidence tests on this branch at the time of crediting. Oracle-golden failures in `coding` (`TestRPC33*`, `TestFauxAgentObservationOracle`, `TestTestFauxAgentObservationOracle`) and `internal/codingagent/tools` (`TestBashStructuredResultMatchesRecordedUpstream`) pin the previous release and belong to port-992-core; none is in this scope.

| # | Upstream file | Status | Evidence / delta |
|---|---|---|---|
| 1 | agent/test/agent-loop.test.ts | credited | `runToolCall` describe (2 cases) = `TestRunToolCall_*` in agent/agent_loop_upstream_test.go |
| 2 | agent/test/agent.test.ts | credited | `TestAgent_ForwardsProviderStreamEventObserversThroughAgentOptions` |
| 3 | agent/test/harness/pico3/legacy-tracker.test.ts | credited | `TestTrackerPreservesDraftSourcedDescendantEditsWithoutExposingTrackerMetadata` |
| 4 | agent/test/harness/pico3/spec-view-events.test.ts | credited | `TestViewSelfHeadCommitRewritesOneTranscriptEntryWithMatchingEntryHeadEvents` |
| 5 | coding-agent/test/agent-session-concurrent.test.ts | credited | steer/followUp resolve "queued" (session_concurrent_upstream_test.go:235,240) |
| 6 | coding-agent/test/agent-session-dynamic-tools.test.ts | credited | `builtin:read` source path (session_tool_registry_port_test.go:381) |
| 7 | coding-agent/test/clipboard-image-native-errors.test.ts | credited | showError once (clipboard_image_native_errors_upstream_test.go:67) |
| 8 | coding-agent/test/clipboard-image.test.ts | credited | two X11 TARGETS subtests, one xclip call |
| 9 | coding-agent/test/clipboard-paste-file-paths.test.ts | credited | `TestClipboardPasteFilePaths` |
| 10 | coding-agent/test/compaction-nested-calls.test.ts | credited | `TestCompactionFileOperationsIncludeFilesTouchedByNestedCallsRecordedOnToolResults` |
| 11 | coding-agent/test/edit-tool-legacy-input.test.ts | credited | type-only delta |
| 12 | coding-agent/test/experimental-cli-entry.test.ts | credited | file-URL `--import` is a Node launch detail |
| 13 | coding-agent/test/extensions-discovery.test.ts | credited | "does not infer package ownership from ancestor manifests" row |
| 14 | coding-agent/test/extensions-runner.test.ts | credited | harness stub only (`getSettings`) |
| 15 | coding-agent/test/footer-width.test.ts | credited | routed-model and cached-usage subtests |
| 16 | coding-agent/test/image-resize-callers.test.ts | credited | type-only delta |
| 17 | coding-agent/test/interactive-tui.test.ts | credited | `TestInteractiveTuiWheelScrollLinesFromSettingsUpstream` |
| 18 | coding-agent/test/jev-router-example.test.ts | credited | subprocess host tests; the Node twin is skipped (not credited) |
| 19 | coding-agent/test/llama-extension.test.ts | credited + fix | `classifies with selectable models through llama-server` was missing: see Production fix below |
| 20 | coding-agent/test/mcp-oauth-refresh.test.ts | credited | coding/mcpext/oauth_refresh_test.go (2 tests) |
| 21 | coding-agent/test/model-catalog-protocol.test.ts | ported-passing (phase 2) | `TestModelCatalogProtocolWithTheCurrentClient`: `catalog requests = [], want [/api/models/providers/openrouter?types=chat%2Cimage%2Cclassifier]` (PiG sends no catalog request). Stub: `CreateModelRuntimeOptions.CatalogBaseURL`. D65 adaptation: PiG's `pig/` User-Agent gets no `pi-version` redirect, so one request instead of two |
| 22 | coding-agent/test/model-resolver.test.ts | credited | `TestModelResolverDefaultsUpstream` |
| 23 | coding-agent/test/model-runtime-classifiers.test.ts | credited | `TestModelRuntimeClassifiersUpstream` |
| 24 | coding-agent/test/model-runtime-cloudflare-compat.test.ts | credited | fetch-capture rewrite asserted in cloudflare_compat_upstream_test.go |
| 25 | coding-agent/test/model-runtime-images.test.ts | credited | images + classifiers Go tests (7 cases) |
| 26 | coding-agent/test/nested-tool-calls.test.ts | credited | six `TestNested*` tests |
| 27 | coding-agent/test/package-command-paths.test.ts | credited | two built-in config selector tests |
| 28 | coding-agent/test/package-manager.test.ts | credited | builtin resolve, git dependency args, pinned temporary git ref |
| 29 | coding-agent/test/remote-catalog-provider.test.ts | ported-passing (phase 2) | `TestRemoteCatalogProviderUpstream`: 7 subtests fail `provider has no refreshModels`, the stalled-request subtest fails `the first catalog request never started`. Stub: `coding.WithRemoteCatalog` (returns the provider unchanged), `coding.RemoteCatalogModelTypes`. The feature does not exist in PiG (PORT_MAP row ⬜) |

### Production fix made before the method change (own commits)
- `10215d4ee fix(llama): provider classify and registered classifier models (green)`, preceded by the red commit `2fc33062d`. `llama.Provider.Classify` delegates to `ai.ClassifyLlamaCpp` (provider.ts:262) and `Host.SyncRegistration` registers the classifier models and the llama-cpp-classify implementation (provider.ts:204). `host_test.go` `TestHostRefreshPersistsCatalogAndRestoresItCacheOnly` now expects the classifier entry next to the chat model.

### Questions to the lead
- The two catalog files need a disposition decision (feature port, or a D-number). Resolved by the review fixes: the remote catalog overlay is wired.

### Phase 2 (lead ANSWER: implement the remote catalog overlay)
- `feat(models): port withRemoteCatalog (green)`: `internal/codingagent/remote_catalog_provider.go` ports remote-catalog-provider.ts (keyed, array and `{models}` catalogs, `?types`, 4 h TTL, etag and 304, 404/501 unavailable overlay, etag retained on transient failure, last-modified against the bundled generation time, stale refresh bypassed by the Models collection's generations). `TestRemoteCatalogProviderUpstream` moved from `coding` to `internal/codingagent` with it (the registry lives there and `coding` imports it); all 8 subtests pass. `ai.DecodeModelsCatalog` exports the stored-catalog decoder.
- Registry wiring (model-runtime.ts:231): `internal/codingagent/remote_catalog_registry.go` wraps every built-in provider except Radius and except providers an extension replaced; a dedicated `ai.Models` collection refreshes them from `RefreshCatalogs` (cache restore without network, network only with resolved credentials, as Pi's collection does); `GetProviderModelData` and the typed provider base merge the overlay. `CreateModelRuntimeOptions.CatalogBaseURL` selects the endpoint.
- Endpoint (lead): defaults to `https://pi-in-go.dev`; recorded in D64 (the lead wrote D62, which is the `/bug` entry; D64 is the hosted-endpoints entry). The User-Agent keeps D65.
- Known gap: PiG's generated catalogs record no generation time, so `builtinModelDataGeneratedAt()` is nil and a stored remote catalog always applies. Pi compares `Last-Modified` with the bundled `generatedAt`. Fix needs `cmd/gen-models` to emit the manifest timestamp (generated files, a central regeneration).
- Evidence: `TestRegistryAppliesTheRemoteCatalogOverlayToBuiltInProviders` (mutation-checked: dropping the refresh hook or the merge makes it fail), `TestModelCatalogProtocolWithTheCurrentClient`; `-race -count=24 GOMAXPROCS=4` and 60 runs under taskset with CPU burners pass. `go test ./coding ./internal/codingagent/... ./ai` shows only the known oracle-golden failures.
