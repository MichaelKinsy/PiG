# gap-behavior-libs: behavioural evidence for chord, codemode, mcp, telemetry and evals

Base: the integrate-042 branch (d911ac0f9, rebased on 766fa6e33). Evidence file: `test/parity/unit-evidence/gap-behavior-libs.json` (54 records: one per ✅ PORT_MAP row of chord, codemode, evals, mcp and telemetry, and `durable/src/entries.ts`). The lane first also bound protocol, client, server, env and durable; those 89 records duplicated the reviewed `gap-behavior-remote.json` and `gap-behavior-durable-env.json` evidence for the same rows and were removed in review (see Review below), so each row has one owning ledger. PORT_MAP.md and coverage.md are not edited here; the integrator regenerates them.

## Counts (`go run ./test/parity/cmd/coverage`, whole monorepo, 14 packages)

| | before | after |
|---|---|---|
| ported rows with behavioural evidence | 410 / 616 | 554 / 617 |
| ported rows untested | 205 | 62 |

| package | ✅ rows | behavioural before | behavioural after |
|---|---:|---:|---:|
| chord | 26 | 0 | 26 |
| codemode | 6 | 0 | 6 |
| evals | 5 | 0 | 5 |
| mcp | 11 | 0 | 11 |
| telemetry | 5 | 0 | 5 |
| protocol | 6 | 0 | 6 |
| client | 6 | 0 | 6 |
| server | 14 | 0 | 14 |
| env | 5 | 0 | 5 |
| durable | 61 | 0 | 59 |

These counts are the lane's own, before the integrate-042 merge. After the merge and the review deduplication, `go run ./test/parity/cmd/coverage -out -` reports every ported row behavioural for the whole monorepo; protocol, client, server, env and durable are bound by `gap-behavior-remote.json` and `gap-behavior-durable-env.json`.

Still untested (62): 53 coding-agent rows, 4 tui, 3 ai, and 2 durable rows. The two durable rows are `harness/types.ts` and `storage/sqlite/database.ts`, which hold only declarations (a Go struct set and an interface); their behaviour is the one the harness and SQLite storage tests exercise, and no mutation of the declaration file itself failed a test. The 🟡 rows of mcp and codemode are partial, not ✅, and stay out.

The remote-stack rows (protocol, client, server, env) were measured on `./internal/experimental/{protocol,client,routing}` and `./env`, mutating the Go files named in each record; the Node-differential tests of `internal/experimental/interop` need `extensions/sdk-ts/node_modules`, which this worktree lacks, so the records cite the Go tests that run here.

## Durable round

Superseded in review: the durable records duplicated `gap-behavior-durable-env.json` and were removed, with `durable/tools/index_behavior_test.go` (a copy of `index_test.go`) and `TestDurableErrorsCarryPiMessagesAndCauses` (a copy of `errors_test.go`); only the `entries.ts` record and test remain. The original note follows.

`durable` rows were bound with the same mechanical mutations (up to three kills per row, two for the heavier harness and node-env packages) on `./durable`, `./durable/{harness,session,storage,tools,env,...}`. The Pi-Durable-versus-Go interop tests (`durable/interop`) need `extensions/sdk-ts/node_modules` and are not used. Survivors that were real test gaps got tests: `durable/errors_entries_behavior_test.go` (error messages, `StorageRejected` cause chain, `DefineEntry` guard, typed decode, draft, empty kind) and `durable/tools/index_behavior_test.go` (`CodingTools` order and name). `types.ts` is bound by one kill (`AtSeq`); the storage and commit change tag helpers survive because no test asserts the tag strings.

## Method

Each record names Go tests that pass on the base and compiling source mutations that make them fail (`go test -overlay`, one mutation per run, the package's full test set or a stated `-run` set). A mutation counts only when the build succeeds and a named test fails; a mutation that survives is not recorded. For chord, mutations were generated mechanically (flip of a comparison or logical operator, or an altered error-message literal) over the files that port the row, up to four kills per row, and the recorded test list is the set of top-level tests that failed. For the other packages mutations were chosen by hand from the upstream behaviour each row ports. The `upstream_reference` of each record cites the Pi test file or source range.

Mutations were also used to find test gaps. Each survivor below was closed with a new test, then re-run (red before, green after):

| row | survivor | new test |
|---|---|---|
| telemetry/index.ts | typed span starter dropped the start attributes | `TestTypedSpanStarterRecordsStartAttributesAndNestsByCallbackSpan` |
| telemetry/testing/types.ts | fixture disposal after a failing case, joined dispose error, no dispose after a factory failure | `TestConformanceFixtureIsOwnedAndDisposedByEachCase`, `TestConformanceFixtureDisposesAfterAFailingCaseAndJoinsTheDisposeError` |
| evals/plan.ts | `provider/` and `/` model identities | `TestCreateTaskPlanRejectsEveryMalformedModelIdentity` |
| evals/cli.ts | file order by UTF-8 bytes instead of UTF-16 code units | `TestRunEvalCliOrdersRequestedFilesByUTF16CodeUnit` |
| evals/harness.ts | half-set sandbox ids, cwd section before the docs, `Tool failed` text, settlement errors | `harness_behavior_test.go` (4 tests) |
| mcp/transport.ts, in-memory.ts | listener disposal, close once, copy on send, peer rules | `transport_behavior_test.go` (4 tests) |
| mcp/auth-provider.ts | `UnauthorizedContext.ServerURL` never asserted | assertion added to `TestStreamableHTTPTransportHandsUnauthorizedAndInsufficientScope...` |
| mcp/protocol/types.ts | `LatestProtocolVersion` literal compared with itself | `TestProtocolVersionsAreTheOnesMcpTypesDeclare` |
| mcp/streamable-http.ts | 204 acknowledgement, leading BOM of an error body | 202/204 loop, `TestStreamableHTTPTransportDropsALeadingBOMFromAnErrorBody` |
| mcp/oauth/flow.ts | PKCE challenge, S256 refusal, client authentication method policy | `flow_behavior_test.go` (2 tests) |
| mcp/oauth/errors.ts, callback.ts, provider.ts | error messages, state consumption, empty state, headers, `InvalidateCredentials` kinds | `behavior_test.go` (4 tests) |
| codemode/types.ts | failure kind and call status literals | `TestResultKindsStatusesAndOutputTypesAreTheStringsTypesTSDeclares` |
| codemode/host.ts, prelude-source.ts | timeout message, store value and total limits at the boundary | `host_prelude_behavior_test.go` (2 tests) |
| chord/services/loopback.ts | invoke context, admission and subscribe forwarding | `TestLoopbackTransportForwardsInvokeAdmissionAndSubscribeToTheProvider` |

No production defect was found by these tests; every survivor was a missing assertion.

## Survivors left open (not recorded as evidence)

- `codemode/source.ts`: stripping a trailing `\r` from the options line has no observable effect, because `jsstring.Trim` removes it again before parsing (equivalent mutation).
- `mcp/transports/stdio.ts`: the 500 ms stdin grace period is timing-bound; a test would need a fake clock in `StdioTransport`.
- `chordjson.go:100` cycle-error message and `state_overlay.go:146` index bound (`index <= 0` for `index < 0`) survived their first candidate rows; the rows stay bound by other kills.
- `evals/harness_run.go`: `settledResponse` for `toolUse` needs a live agent process; the error branches are covered.

## CI triage groups 6 and 7 (CI run 37466882005 triage)

- Group 6 (`TestRunPiCodingAgentTransformsTheSystemPromptThroughTheBridge`, with_docs, still red on the preview run after the `PIG_HOME` unset): pig materialises its documentation under `codingagent.ConfigRoot()`, which honours `PIG_HOME`, then `XDG_CONFIG_HOME/pig`, then `~/.pig`. CI sets `XDG_CONFIG_HOME`, so the docs landed outside the isolated home and `<AgentDir>/../docs/README.md` was absent. Reproduced red locally with `XDG_CONFIG_HOME` set (`docs on disk = false`); green after `ApplyIsolatedEnvironment` also unsets `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_CACHE_HOME` and `XDG_STATE_HOME` (restored afterwards). Regression guard: `TestApplyIsolatedEnvironmentSendsPiGStateRootsIntoTheIsolatedHome` covers `PIG_HOME`, `PI_HOME` and the four XDG roots.
- Group 7: `sandbox_owner_other_test.go` (`!unix`) made `internal/evals` a Windows-native test package whose end-to-end runner tests need a pig binary without `.exe`, a Unix-socket bridge and POSIX user APIs (the documentation evals run in Linux containers). `harness_run_test.go` is now `//go:build unix`, the `!unix` helper is deleted, and `TestAPIKeyHandOff` moved to `api_key_hand_off_test.go`, which has no constraint and keeps the Windows `shellQuote` branch. `sandbox_other_test.go` (`!unix`) tests the POSIX-required refusal of `enterToolSandbox` and `chownTree` off unix, so `internal/evals` keeps one Windows-native test file; the `!unix` package selector in `test/ci-images` already counts it. `test/ci-images` `buildConstraint`/`windowsOnlyTestPackages` already count `!unix` at the base, so it needed no change; `TestWindowsTestPackagesSelectEveryWindowsOnlyTestPackage` passes. `GOOS=windows`, `darwin` and `linux` vet the package.

## Per-row results

| upstream | package | tests (first three) | mutations killed |
|---|---|---|---:|
| `chord/src/api.ts` | `./internal/chord` | `TestConsumerRejectsMalformedResets`, `TestDeltaRetainedFacetStateSubscriptionFollowsReload`, `TestDeltaRetainedFacetStateValueRejectsRevocation` (+24) | 4 |
| `chord/src/bundler.ts` | `./internal/experimental` | `TestBundleFacetPackageUsesCallerConventionsAndSharedServerCompiler`, `TestBundleFacetsOwnsOutputPathsVersionAndReplacement`, `TestBundleFacetsPreservesRecordValidationOrder` (+10) | 5 |
| `chord/src/context/index.ts` | `./internal/chord/chordctx` | `TestContextInheritsParentCancellationAndIsolatesChildCancellation`, `TestContextStopsWaitingWhenTheInvocationIsCancelled`, `TestContextWithAnAlreadyCancelledSignalIsCancelledOnReturn` | 4 |
| `chord/src/delta/diff.ts` | `./internal/chord/delta` | `TestCheckedImmutableOperationApplication`, `TestDeltaCanonicalStrings`, `TestDeltaDiff` (+16) | 4 |
| `chord/src/delta/index.ts` | `./internal/chord/delta` | `TestDeltaArrayIndexSafety`, `TestDeltaCodec`, `TestDeltaDiff` (+5) | 8 |
| `chord/src/delta/tracker.ts` | `./chord/delta` | `TestArrayReorderRecordsThePermutation`, `TestTrackerArrayRootOperationsMatchPiForRandomizedEdits`, `TestTrackerOperationsMatchPiForRandomizedEdits` (+4) | 6 |
| `chord/src/facets/host.ts` | `./internal/chord` | `TestDeltaStagedKeyedInstancesConnectInInsertionOrder`, `TestFacetHost`, `TestFacetHostExternalServiceSource` (+4) | 12 |
| `chord/src/facets/loader.ts` | `./internal/chord` | `TestCombineFacetLoadersReleasesInReverseOnce`, `TestCombinedLoaderDisposeIsReentrant`, `TestDeltaLoaderPanicCleansEarlierLoads` (+1) | 8 |
| `chord/src/index.ts` | `./internal/chord` | `TestBeginRebindRevokesKeyedHandleBeforeSubscriptionRelease`, `TestBindingAllowlistAndModeValidation`, `TestBindingRebindAndDisposeRejectWithAggregateErrorMessageOnly` (+16) | 6 |
| `chord/src/json.ts` | `./internal/chord/chordjson` | `TestCopy`, `TestOmitsUnsetObjectEntriesWithoutNormalizingArrays`, `TestStored` | 6 |
| `chord/src/node.ts` | `./internal/experimental` | `TestFacetBundles`, `TestNodeFacetsRegisterReplaceRunAndDispose`, `TestReadFacetBundleManifestVersionDiagnostics` | 3 |
| `chord/src/node/bundle-loader.ts` | `./internal/experimental` | `TestNodeFacetObserverForgetsAbortRecords`, `TestNodeFacetObserverReleasesFinishedObservations`, `TestNodeFacetObserverReportsAssimilatedFailures` (+3) | 3 |
| `chord/src/node/bundle.ts` | `./internal/experimental` | `TestBundleFacetPackageUsesCallerConventionsAndSharedServerCompiler`, `TestBundleFacetsOwnsOutputPathsVersionAndReplacement`, `TestBundleFacetsPreservesRecordValidationOrder` (+10) | 5 |
| `chord/src/node/manifest.ts` | `./internal/experimental` | `TestFacetBundleManifestConstantsAndPluginAreTheOnesManifestTSDeclares`, `TestFacetBundles`, `TestReadFacetBundleArtifact` (+1) | 5 |
| `chord/src/node/package.ts` | `./internal/experimental` | `TestBundleFacetPackageUsesCallerConventionsAndSharedServerCompiler`, `TestBundleFacetsPreservesRecordValidationOrder`, `TestFacetBundles` | 3 |
| `chord/src/services/consumer.ts` | `./internal/chord` | `TestBeginRebindFencesBeforeCompletionAndJoinsCancellation`, `TestBeginRebindRevokesKeyedHandleBeforeSubscriptionRelease`, `TestBindingAllowlistAndModeValidation` (+23) | 8 |
| `chord/src/services/errors.ts` | `./internal/chord` | `TestRemoteServiceErrorCodesAreTheStringsErrorsTSDeclares`, `TestBeginRebindRevokesKeyedHandleBeforeSubscriptionRelease`, `TestBindingAllowlistAndModeValidation` (+5) | 4 |
| `chord/src/services/handle.ts` | `./internal/chord` | `TestDeltaRetainedFacetStateSubscriptionFollowsReload`, `TestFacetHostRemoteConsumerAndReloadCutover` | 6 |
| `chord/src/services/instances.ts` | `./internal/chord` | `TestBeginRebindRevokesKeyedHandleBeforeSubscriptionRelease`, `TestContinueObservationReportsOnlyLiveRejection`, `TestFacetHost` (+8) | 6 |
| `chord/src/services/loopback.ts` | `./internal/chord` | `TestFacetHost`, `TestFacetHostOrdersActivationAndDisposesOnce`, `TestFacetHostRemoteConsumerAndReloadCutover` (+1) | 3 |
| `chord/src/services/provider.ts` | `./internal/chord` | `TestBindingRebindAndDisposeRejectWithAggregateErrorMessageOnly`, `TestConcurrentChangesReachReplicaGapFree`, `TestFacetHost` (+12) | 6 |
| `chord/src/services/state-codec.ts` | `./internal/chord` | `TestAnOverflowResetCanMakeASingletonUnavailableBeforeALaterReplacement`, `TestKeyedResetsRetainLiveGenerationsAndReconcileClosedAndReusedKeys`, `TestRebaselinesEveryMemberThroughWireCodecsThenResumesContiguousDeltas` (+1) | 4 |
| `chord/src/services/state-internals.ts` | `./internal/chord` | `TestAnOverflowResetCanMakeASingletonUnavailableBeforeALaterReplacement`, `TestBeginRebindFencesBeforeCompletionAndJoinsCancellation`, `TestBeginRebindRevokesKeyedHandleBeforeSubscriptionRelease` (+27) | 6 |
| `chord/src/services/state.ts` | `./internal/chord` | `TestAuthoritativeReplicatedStateSources`, `TestDeltaRetainedFacetStateSubscriptionFollowsReload`, `TestFacetHostRemoteConsumerAndReloadCutover` (+6) | 6 |
| `chord/src/services/wire.ts` | `./internal/chord` | `TestAnOverflowResetCanMakeASingletonUnavailableBeforeALaterReplacement`, `TestFacetHostRemoteConsumerAndReloadCutover`, `TestKeyedResetsRetainLiveGenerationsAndReconcileClosedAndReusedKeys` (+4) | 8 |
| `chord/src/types.ts` | `./internal/chord` | `TestBeginRebindRevokesKeyedHandleBeforeSubscriptionRelease`, `TestBindingAllowlistAndModeValidation`, `TestDeltaRetainedFacetStateSubscriptionFollowsReload` (+6) | 4 |
| `codemode/src/identifier.ts` | `./codemode` | `TestToCodemodeIdentifierMatchesPi`, `TestExposesToolsUnderNormalizedIdentifiersAndListsThemInALL_TOOLS` | 3 |
| `codemode/src/runtime/host.ts` | `./codemode` | `TestTerminatesASynchronousInfiniteLoopOnTimeout`, `TestAbortsViaContextAndCancelsInFlightCalls`, `TestRejectsExecuteAfterClose` (+10) | 3 |
| `codemode/src/runtime/prelude-source.ts` | `./codemode` | `TestEmbeddedSourcesMatchTheirRecordedHashes`, `TestRejectsInvalidKeysValuesAndOversizedWritesInsideTheScript`, `TestExplainsOversizedWrites` (+4) | 6 |
| `codemode/src/source.ts` | `./codemode` | `TestParseCodemodeSourceReturnsPlainCodeUnchanged`, `TestParseCodemodeSourceParsesTheOptionsLineAndKeepsLineNumbers`, `TestParseCodemodeSourceOnlyTreatsTheFirstLineAsAnOptionsLine` (+2) | 4 |
| `codemode/src/types.ts` | `./codemode` | `TestResultKindsStatusesAndOutputTypesAreTheStringsTypesTSDeclares`, `TestCollectsTextImageAndConsoleOutputInOrder`, `TestTurnsToolErrorsIntoCatchableErrorsInTheScript` (+1) | 6 |
| `codemode/src/wasm.ts` | `./codemode` | `TestReportsAFailingWasmModuleAsASandboxError`, `TestCorruptWasmModuleIsASandboxError`, `TestEmbeddedSourcesMatchTheirRecordedHashes` (+1) | 2 |
| `durable/src/entries.ts` | `./durable` | `TestDefineEntryGuardsByKindAndDecodesTypedData` | 4 |
| `evals/src/cli.ts` | `./internal/evals` | `TestParseEvalCliMatchesUpstream`, `TestRunEvalCliPlansRunsAndReports`, `TestRunEvalCliExitsZeroWhenEveryPairScores` (+3) | 6 |
| `evals/src/docker.ts` | `./internal/evals` | `TestRunEvalCliPlansRunsAndReports`, `TestRunEvalCliFailures`, `TestRequireEvalAuthFileValidatesCredentials` (+1) | 8 |
| `evals/src/harness.ts` | `./internal/evals` | `TestHarnessUpstream`, `TestCreatePiDocumentationEvalHarnessInSandbox`, `TestRunPiCodingAgentAnswersAndRecordsTheRun` (+6) | 9 |
| `evals/src/plan.ts` | `./internal/evals` | `TestPlanUpstream`, `TestCreateTaskPlanRejectsEveryMalformedModelIdentity` | 4 |
| `evals/src/report.ts` | `./internal/evals` | `TestReportUpstream`, `TestSummarizeEvalObservationsMatchesUpstream`, `TestReadTaskObservationMatchesUpstream` | 5 |
| `mcp/src/auth-provider.ts` | `./mcp` | `TestStreamableHTTPTransportHandsUnauthorizedAndInsufficientScopeResponsesToTheAuthProviderWithTheRejectedToken` | 2 |
| `mcp/src/oauth/callback.ts` | `./mcp/oauth` | `TestOAuthCallbackServerPagesRendersPlainTextByDefault`, `TestOAuthCallbackServerPagesRendersPagesThroughRenderPage`, `TestOAuthCallbackServerRejectsAResponseOnAnotherPathThanTheExpectedOne` (+2) | 5 |
| `mcp/src/oauth/errors.ts` | `./mcp/oauth` | `TestOAuthIssuerMismatchErrorNamesAMissingIssAsNone`, `TestOAuthIssuerMismatchErrorQuotesIssuersAsJSONStringify`, `TestTokenRequestsRefuseNonLoopbackHTTPEndpoints` (+1) | 3 |
| `mcp/src/oauth/flow.ts` | `./mcp/oauth` | `TestMCPOAuthDiscoversRegistersAuthorizesWithPKCEAndRefreshesOn401`, `TestMCPOAuthSharesOneRefreshBetweenConcurrent401sWhenRefreshTokensRotate`, `TestMCPOAuthAsksForAuthorizationInsteadOfRefreshingWhenTheServerNeedsMoreScope` (+13) | 6 |
| `mcp/src/oauth/provider.ts` | `./mcp/oauth` | `TestMcpOAuthProviderKeepsFailingAfterAFailedWrite`, `TestMcpOAuthProviderIsNotPoisonedByACancelledCall`, `TestMCPOAuthBindsPersistedCredentialsToTheExactMCPServerURL` (+2) | 4 |
| `mcp/src/protocol/jsonrpc.ts` | `./mcp` | `TestParseJSONRPCMessageMatchesPi` | 3 |
| `mcp/src/protocol/types.ts` | `./mcp` | `TestClientAcceptsServersThatAnswerWithAnOlderProtocolVersion`, `TestClientInitializesTheConnectionBeforeExposingServerInformation`, `TestClientPaginatesToolsAndPreservesProtocolToolDefinitions` (+2) | 2 |
| `mcp/src/transports/in-memory.ts` | `./mcp` | `TestInMemoryTransportPairDeliversCopiesInOrderAndClosesBothEnds`, `TestInMemoryTransportRejectsSendsBeforeStartAndToAnUnstartedOrMissingPeer` | 5 |
| `mcp/src/transports/stdio.ts` | `./mcp` | `TestStdioTransportConnectsToANewlineDelimitedMCPServerAndCapturesStderr`, `TestStdioTransportKillsAServerThatIgnoresShutdownIncludingItsChildren`, `TestStdioTransportTerminatesTheChildrenOfAServerThatExitsOnStdinClose` (+3) | 3 |
| `mcp/src/transports/streamable-http.ts` | `./mcp` | `TestStreamableHTTPTransportHandlesJSONAndSSEResponsesWithSessionAndProtocolHeaders`, `TestStreamableHTTPTransportClassifiesAuthenticationFailures`, `TestStreamableHTTPTransportFailsOnlyTheRequestWhoseSSEStreamBreaks` (+13) | 5 |
| `mcp/src/transports/transport.ts` | `./mcp` | `TestTransportEventsDeliverInRegistrationOrderUntilDisposed`, `TestTransportEventsEmitCloseOnceAndHonorDisposal`, `TestClientNotifiesCloseListenersOnceWhenTheTransportDrops` | 4 |
| `telemetry/src/index.ts` | `./telemetry` | `TestTelemetryUpstream`, `TestTelemetrySchemaKeepsEmptyMembers`, `TestTypedSpanStarterRecordsStartAttributesAndNestsByCallbackSpan` | 3 |
| `telemetry/src/memory.ts` | `./telemetry` | `TestConformanceUpstream` | 4 |
| `telemetry/src/noop.ts` | `./telemetry` | `TestTelemetryUpstream` | 2 |
| `telemetry/src/testing/conformance.ts` | `./telemetry` | `TestConformanceUpstream` | 3 |
| `telemetry/src/testing/types.ts` | `./telemetry` | `TestConformanceFixtureIsOwnedAndDisposedByEachCase`, `TestConformanceFixtureDisposesAfterAFailingCaseAndJoinsTheDisposeError` | 3 |

## Review (rev-gap-behavior-libs)

- Mutation re-run (`go test -overlay`): callback state consumption, empty state, in-memory unstarted peer, stdio forced kill, manifest version, `memory.go` explicit status and attribute copy, `auth-provider` `ServerURL`, codemode call status, evals `Tool failed`, telemetry dispose join. All killed. Three of them (callback `delete`, in-memory `peerOK`, stdio forced kill) kill by a hang or test timeout; their records had an empty `( FAIL)` test list and now name the test.
- `chord/src/services/errors.ts`: the eight REMOTE_SERVICE_ERROR_CODES literals survived every internal/chord test (they travel on the session-worker wire). New `TestRemoteServiceErrorCodesAreTheStringsErrorsTSDeclares` pins the codes, the message-only `Error()` and the exact-code match.
- `chord/src/node/manifest.ts`: the record named one unrelated test; `FACET_BUNDLE_MANIFEST_FILE` survived. New `TestFacetBundleManifestConstantsAndPluginAreTheOnesManifestTSDeclares` pins the five constants and the optional `version` encoding; the record also names `TestFacetBundles` and `TestReadFacetBundleArtifact`, which kill the format literals.
- `evals/src/harness.ts` promptAgent: `assistant.errorMessage ?? fallback` (harness.ts:249) keeps an empty message, but `settledResponse` treated `""` as absent and the lane's test pinned that. Red: `TestSettledResponseRejectsRunsWithoutAUsableAssistantMessage/empty_message`. Fixed at `internal/evals/harness_run.go` (presence check); the test now covers absent, null and empty messages.
- Duplicates removed: 89 records and two duplicate durable tests (see Durable round).

## Review (rev-gap-behavior-libs-r, eef48fc40b)

Scope: the 54 records of this file, and the batch eef48fc40b (the lg-help-7 rebase: T6r rule, `TestOrderedObjectIsARecord`, the radius renames and the regenerated ledger). The recorded mutations re-run red (streamable-http BOM, report `>= 1`, memory explicit status, jsonrpc `hasError`, codemode timeout message, flow S256). Every recorded test exists in its package. Hand-chosen semantic mutations of oauth/flow.go (RFC 9207 `iss`, step-up scope order and dedupe, `withScope`, client authentication policy), telemetry/memory.go (settled guards, detached snapshots, status copy, error name, undefined attributes, parent id) and evals/report.go (pair order, blocked outcome, flaky key, paired metrics, saturation) are all killed.

- `mcp/src/transports/streamable-http.ts`: the reconnect policy survived every test (server `retry` delay, the 408 transient status, the resume-budget reset on progress, the `>= maxRetries` bounds of both loops, the backoff doubling). Pi's streamable-http.test.ts does not pin it either. New `streamable_http_reconnect_test.go` (5 tests) kills each of these mutants.
- `mcp/src/transports/stdio.ts` and `streamable-http.ts`: the blank checks used `strings.TrimSpace`, but Pi uses `String.prototype.trim`, which strips U+FEFF and keeps U+0085. A BOM-only stdout line or SSE `data` was reported as a JSON error, and a NEL-only line or `data` was skipped silently. `describeHttpFailure` also trimmed NEL and kept BOM. The lane fixed the same sites independently in abf52e2a35. The merged `mcp/js_trim_test.go` keeps the lane's `describeHttpFailure` test and adds the review's tests: the stdio close remainder (stdio.ts:119, which no lane test covers) and the JSON.parse error of a NEL-only stdout line and SSE `data`.
- `codemode/src/runtime/host.ts`: `finish` sets `durationMs` on a cancelled pending call (host.ts:303-306), and that line survived. New `TestACallCancelledByTheEndOfTheExecutionRecordsHowLongItRan`. Pi arms the deadline only for a finite `timeoutMs` (host.ts:147, `Number.isFinite`), but Go excluded only +Inf. A NaN or -Inf timeout therefore expired at once instead of running without a deadline. Red: `TestRunsWithoutADeadlineWhenTimeoutIsNotFinite`. Fixed in `codemode/execution.go` startDeadline.
- `chord/src/services/consumer.ts`: the `was used as two different kinds` check (consumer.ts:104-110) survived. New `TestRemoteServiceMemberUsedAsTwoKindsIsRejected`.
- Batch: `TestOrderedObjectIsARecord` let two T6r mutants survive (value type not compared, key type not checked). It now carries integrate-042's stricter cases. The batch's renames pointed both `radiusProvider` rows at `NewRadiusProvider`, which at this tree returns `*RadiusProvider`, not the Provider. That reopened 4 rows, and the regenerated baseline hid the increase (25 to 28). At this tree they would point at `RadiusModelsProvider`, and the ledger would report 24 gaps. integrate-042 already contains eef48fc40b and the radius.go change that makes `NewRadiusProvider` the Provider (2bf103965b), so the renames keep `NewRadiusProvider` and close after the merge. Reverting them here would regress integrate.

## Gates

Isolation note: the first test runs of this lane inherited the launcher's agent-directory variables (`PIG_CODING_AGENT_DIR` pointed at a worker directory, `HOME` was real). They ran only the telemetry, mcp, codemode and evals tests, which write no credentials; the eval harness (before the `PIG_HOME` unset was in the base) materialized pig's embedded documentation and caches under `~/.pig/docs` and `~/.pig/cache`. No `auth.json` or settings file changed (mtimes checked). All later runs, including every mutation run, used scratch directories for `PIG_CODING_AGENT_DIR`, `PI_CODING_AGENT_DIR`, `PIG_HOME`, `PI_HOME` and `HOME`.

`go vet` (linux, windows, darwin) and `golangci-lint` (0 issues) on `./telemetry/... ./mcp/... ./codemode/... ./internal/evals/... ./internal/chord/...`; `go test -race` on telemetry, mcp, mcp/oauth, codemode and internal/evals (also internal/chord, its chordjson, chordctx and delta packages, and chord/delta); `make divergence-guard` (50 baselined hits, unchanged); `make source-hygiene` (clean); `go run ./test/parity/cmd/coverage` loads every record. `internal/experimental` has pre-existing failures in this worktree unrelated to the rows (the Node oracle needs `extensions/sdk-ts/node_modules`); the chord Node-facet rows were measured with the `-run` set recorded in each mutation.
