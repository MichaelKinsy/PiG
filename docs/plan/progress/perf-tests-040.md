# perf-tests-040: faster test suite without losing coverage

Lane perf-tests-040, base `porter/pi-0.99.1` (41b538cae). Owner ask: public CI's `cmd/pig` package took 495 s against `go test`'s 10-minute default; make the suite faster at the root, keeping every assertion.

## Method

- The CI grouped runner, unchanged: `automation/ci/test-grouped.sh {fast,cli,subprocess,conformance}`, one invocation per shard, each pinned with `taskset` to its own 4 cores, `GOMAXPROCS=4`, `CI=1`, hermetic `PIG_CODING_AGENT_DIR`. A `go` wrapper on `PATH` adds `-json -timeout=40m` to `go test` only so the baseline can finish and report per-test times; no timeout was raised in the repository.
- The 196-core host runs other lanes (load average 40-80), so absolute times carry about ±25% noise. Every before/after pair below ran at the same time on equal core sets (the baseline from a clean worktree of the base commit); the first baseline of `test-fast` ran in the lane worktree.
- Environment findings while measuring (not repository changes): mise shims for `node`, `uv` and `tmux` fail inside a worktree when several installs race, so the runs put the real `node 24.19.0`, `uv` and `tmux` binaries first on `PATH`.
- Failing tests are identical before and after (the sets were compared): upstream 0.99.1 oracle pins against the 0.99.2 mirror, `Xvfb` missing, `TestNodeVendoredTuiUpstreamTests`, `TestGoVersionPolicy`, the correspondence known-gaps tests. None is caused by or hidden by this lane.

## What was slow, and the root fix

| root cause | fix | effect |
|---|---|---|
| A test replaced `HOME` with a temp directory and then ran a real `go build`; the go command derives `GOCACHE`/`GOMODCACHE` from `HOME`, so PiG and its whole dependency graph compiled in an empty cache for every build. | `internal/testenv.ScopeTempDir` (called through `ScopeTempDir` or `RunScoped` by the `TestMain` of each package that scopes its temporary directory, before any test runs) pins both caches (`KeepGoBuildCaches`); unit test `TestKeepGoBuildCachesSurvivesAHomeChange` (red when the pin is removed, green with it). | `TestNativePigletBinaryBuildLeavesSourceTreeUntouched` 107 s to 7 s solo; `coding/pigletbuild` 301 s to 92 s in the shard; `runtimecell` 62 s to 30 s. |
| Every `pi.events` bridge rig baked its configuration into the fixture's source, so each rig compiled a new Rust/Go cell (4-8 s per Rust row, about 60 rows). | A native fixture reads `<name>.bus.json` from its Host's working directory; every rig of one language and name uses one source tree and one build cache shared the way concurrent sessions share `~/.pig` (`busNativeSource`, `busBuildRoot`). The rows run in parallel. | Bus tests without the stress test 249 s to 96 s solo. |
| `t.Setenv("PIG_SDK_{GO,PY,RS}_ROOT", ...)` repeated, in about 20 tests, the values `TestMain` already exports for the same module root, and made those tests serial. | Removed the redundant calls (`subprocess`, `extension-conformance`). | Packed-SDK tests became parallel-capable. |
| Hundreds of tests that own their process, pseudo-terminal, `HOME` and temp directories ran one after another although a serial test finishes before any parallel test resumes. | `t.Parallel()` on the independent ones. Parallel top-level tests per package, before to after: `cmd/pig` 4 to 63, `subprocess` 26 to 82, `extension-conformance` 0 to 58, `ai` +2. A transitive scan of each test's helpers excluded every test that reaches `t.Setenv`/`t.Chdir`/`os.Setenv`, swaps `os.Stderr` (`captureStderr`), measures process-wide allocation or goroutines, or sets a package hook (`shortSockDir`, `setTerminationShutdownHook`, the `MarshalEnvelope` allocation tests). | cmd/pig, subprocess, conformance rows above. |

## Tried and rejected (kept out of the change)

- Parallel RPC shutdown-ordering rows (`rpc_shutdown*`, `rpc_abort_turn_end`): they end stdin right after a prompt and compare output order with Pi. With the group parallel, `TestRPCInputEndAfterExtensionCommandComparedWithPi/pig/microimmediate` failed; with only two rows at once (a slot limiter), `.../pig-with-sibling/sync` failed 1 in 6 package runs: pig wrote the `session_shutdown` notification before the prompt response (Pi writes the response first). These rows stay serial (about 250 s of the `cmd/pig` run). The order flip under 2-way load looks like a real timing dependence in pig's microtask/macrotask emulation. It is an open PiG-versus-Pi ordering finding for the RPC shutdown path, recorded here and not fixed by this lane.
- Running the three languages of `TestNativeEventBusStressKeepsEveryDeliveryAndBoundedState` at once: 615 s against 381 s one after another when the cores are contended (the bursts are latency-bound cross-process round trips, so more runnable processes lengthen every round trip). Reverted to the original sequential test; it is about 360 s of CPU-bound work (1M deliveries per language) and the largest remaining item of `test-conformance`.
- `t.Parallel()` on in-process tests that call `t.Setenv("PIG_HOME", ...)` or read process-wide state: converting them needs production-code env plumbing and is out of scope.

## Flakiness checks (4 cores, other shards running)

- `cmd/pig` `-count=5` full package: only the two pre-existing upstream 0.99.1 oracle pin failures, 5 of 5.
- `coding/extension/host/subprocess` `-count=5`: only `TestNodeVendoredTuiUpstreamTests` (pre-existing), 5 of 5.
- `test/extension-conformance` `-count=5` without the stress test: all pass. Stress test: unchanged code.
- `ai` changed tests `-count=5`: pass.
- Final-state rerun at the head before the last two edits: `cmd/pig` `-count=3` (only the two pre-existing upstream 0.99.1 oracle pins fail), `subprocess` `-count=3` (only `TestNodeVendoredTuiUpstreamTests`), `extension-conformance` `-count=3` without stress (all pass), stress `-count=2` (pass).
- `TestCLISelectsSessionBeforeNameValidation` (rows now parallel inside a `rows` group so the source-Session comparison still runs after every row) `-count=5`: pass, 43 s to 14 s.
- `ai` `TestAbortGoldenMatchesPi` and `TestOpenAIHTTPErrorMatchesPi` `-count=5`: pass.

## Remaining time, for the next pass

- `cmd/pig`: about 250 s of the serial phase is the RPC shutdown-ordering family (see above); the parallel phase is CPU-bound by the 200-run Bedrock/Azure/Mistral observation tests. Under a quiet 4-core machine the package is about 380-580 s here against 734-872 s before; the lane did not reach the 5-minute target on this loaded host.
- `test-conformance`: the stress test, about 360 s of CPU-bound cross-process round trips.
- `test-fast`: `ai` (serial tick-order tests) is the new critical path.
- A further sizeable cut needs either a deterministic ordering in pig's RPC shutdown path (so the shutdown rows can overlap) or cheaper per-delivery cost in the event bus.


## Shard `go test` phase, first package start to last package result (4 cores, GOMAXPROCS=4, CI=1)

| shard | before (s) | after (s) |
|---|---:|---:|
| test-fast | 317 | 224 |
| test-cli | 772 | 578 |
| test-subprocess | 428 | 319 |
| test-conformance | 924 | 602 |

### test-fast: package wall time (s)

| package | before | after |
|---|---:|---:|
| coding/pigletbuild | 301 | 92 |
| ai | 222 | 204 |
| internal/experimental | 181 | 131 |
| internal/codingagent | 177 | 124 |
| test/parity/closure | 155 | 113 |
| coding | 136 | 124 |
| tui | 121 | 82 |
| test/parity/cmd/closure | 107 | 77 |
| tui/widthx | 101 | 69 |
| test/parity/cmd/correspondence | 81 | 65 |
| test/parity/correspondence | 77 | 52 |
| coding/extension/host/runtimecell | 62 | 30 |
| test/ci-images | 48 | 46 |
| test/parity/cmd/sdksurface | 46 | 36 |

### test-cli: package wall time (s)

| package | before | after |
|---|---:|---:|
| cmd/pig | 772 | 578 |

### test-subprocess: package wall time (s)

| package | before | after |
|---|---:|---:|
| coding/extension/host/subprocess | 428 | 319 |

### test-conformance: package wall time (s)

| package | before | after |
|---|---:|---:|
| test/extension-conformance | 924 | 602 |

### test-fast: 40 slowest tests before (s) and the same test after (s)

| before | after | status | package | test |
|---:|---:|---|---|---|
| 159.1 | 18.9 | pass | coding/pigletbuild | TestNativePigletBinaryBuildLeavesSourceTreeUntouched |
| 106.4 | 76.8 | fail | test/parity/cmd/closure | TestImportDenominatorUsesCompiledCorrespondenceDenominator |
| 100.6 | 69.2 | pass | tui/widthx | TestPiWidthDifferential |
| 89.3 | 27.2 | pass | coding/pigletbuild | TestSignedPigletBinaryRefusesAlteration |
| 84.5 | 54.6 | pass | tui | TestUpstreamAutocompleteFD |
| 76.5 | 77.4 | pass | ai | TestAbortGoldenMatchesPi |
| 58.6 | 34.3 | pass | test/parity/closure | TestAddProviderWireBehaviorsGeneratesPinnedMappingHypotheses |
| 53.5 | 31.5 | pass | test/parity/closure | TestProviderWireMappingsRequireReviewedDecisions |
| 44.5 | 25.4 | pass | test/parity/closure | TestImportCurrentDenominatorsAccountsEveryLedgerRow |
| 44.2 | 29.9 | pass | test/parity/closure | TestStructuredReportsPreserveCurrentAuthorityRows |
| 36.2 | 22.8 | pass | ai | TestOpenAIHTTPErrorMatchesPi |
| 35.1 | 33.5 | pass | test/ci-images | TestPorterValidationIgnoresCallerConfiguration |
| 34.9 | 33.3 | fail | coding | TestUpstreamCodemodeOptionsAndStore |
| 33.4 | 22.7 | pass | test/parity/closure | TestAddCompactionSettingsVerticalKeepsMappingsUnderReview |
| 31.5 | 1.6 | pass | coding/extension/host/runtimecell | TestBuildGoPackedCellBuildsLegacySDKFactoryAgainstCurrentSDK |
| 30.5 | 29.9 | pass | ai | TestMistralDirectMatchesPiTickOrder |
| 26.3 | 18.1 | pass | coding/pigletbuild | TestPublishGitHubRealSignedBuildRoundTripsThroughPull |
| 25.9 | 15.8 | pass | test/parity/closure | TestFoundationDashboardAccountsCurrentDenominatorsWithoutCredit |
| 24.3 | 16.9 | fail | test/parity/cmd/correspondence | TestSubmitBundleStoresCanonicalProposal |
| 23.7 | 20.7 | pass | internal/experimental | TestFacetBundles |
| 23.4 | 12.2 | fail | test/parity/closure | TestCorrespondenceAssertionsRequestTypedCompactionEvidence |
| 22.6 | 15.3 | pass | test/parity/closure | TestReadRecordsBeyondImportLimit |
| 22.5 | 20.3 | pass | test/parity/closure | TestExecuteMutationRequestVerdictIgnoresStderrNoise |
| 22.0 | 15.1 | fail | test/parity/cmd/correspondence | TestWorkPacketCurrentPinKeepsAgentReadOnly |
| 21.8 | 21.7 | pass | ai | TestPiMessagesDirectMatchesPiTickOrder |
| 19.1 | 11.6 | pass | internal/experimental | TestExperimentalDurableServerCompositionA |
| 18.7 | 16.8 | fail | test/parity/cmd/correspondence | TestPacketCurrentPin |
| 17.8 | 15.7 | fail | test/parity/correspondence | TestCompactionSettingsCorrespondenceCurrentPin |
| 17.2 | 17.2 | pass | codemode | TestExecuteReturnsOnlyAfterTheVMIsClosedForEveryTerminalKind |
| 17.0 | 19.3 | pass | coding/piglet/release | TestVerifyAssetAndDownloadShareCompleteSizeBoundary |
| 16.6 | 15.7 | pass | test/parity/cmd/coverage | TestReportOutputFlagAndImplicitStdoutNotice |
| 16.6 | 8.4 | pass | internal/experimental | TestServerSelectedPresentationFacetsUpstream |
| 16.4 | 16.2 | fail | test/parity/cmd/correspondence | TestCompareCurrentPin |
| 15.5 | 9.4 | pass | test/parity/closure | TestMarkdownReportsPreserveCurrentAuthorityRows |
| 15.3 | 11.2 | pass | test/parity/closure | TestStoreReadsCanonicalRecordsPastImportLimit |
| 15.2 | 16.5 | pass | coding | TestUpstreamAgentSessionCodemodeTool |
| 14.1 | 9.3 | pass | test/parity/cmd/sdksurface | TestProbeFindsVendoredPackageAndSymbolMembers |
| 13.9 | 10.1 | pass | internal/experimental | TestNodeFacetObserverReportsAssimilatedFailures |
| 13.5 | 10.1 | pass | coding/rpcclient | TestRPCSpawnPipeFailureClosesEarlierDescriptors |
| 13.1 | 11.1 | pass | test/docs-drift | TestPublicClaimsMatchTheEvidence |

### test-cli: 40 slowest tests before (s) and the same test after (s)

| before | after | status | package | test |
|---:|---:|---|---|---|
| 80.9 | 78.6 | pass | cmd/pig | TestRPCInputEndAfterExtensionCommandComparedWithPi |
| 65.6 | 93.8 | pass | cmd/pig | TestRPCBedrockConverseStreamObservation |
| 42.9 | 43.6 | pass | cmd/pig | TestCLISelectsSessionBeforeNameValidation |
| 42.3 | 15.7 | pass | cmd/pig | TestExtensionToolContextIsWiredInEveryMode |
| 32.8 | 32.1 | pass | cmd/pig | TestRPCInputEndWindowClosesBeforeNextPollComparedWithPi |
| 30.6 | 30.6 | pass | cmd/pig | TestRPCInputEndQuarantinedNodeCommandComparedWithPi |
| 24.5 | 21.7 | pass | cmd/pig | TestExtensionInitScaffoldsBuildAndRegister |
| 20.7 | 40.1 | pass | cmd/pig | TestRPCAzureOpenAIResponsesObservation |
| 13.7 | 23.3 | pass | cmd/pig | TestRPCMistralMatchesPi |
| 13.1 | 32.7 | pass | cmd/pig | TestRPCToolFlagsSelectSameToolsAsPi |
| 11.2 | 45.6 | pass | cmd/pig | TestPigletExtensionToolsScopeInEveryMode |
| 11.1 | 10.8 | pass | cmd/pig | TestMissingSessionCLIComparedWithPi |
| 9.8 | 13.7 | pass | cmd/pig | TestRPCPiMessagesMatchesPi |
| 9.5 | 38.9 | pass | cmd/pig | TestRPCStartupMalformedPackageManifests |
| 9.2 | 9.2 | pass | cmd/pig | TestInteractiveCLIThinkingOverrideReachesSession |
| 9.1 | 9.1 | pass | cmd/pig | TestRPCInputEndDrainExitKeepsGoSDKCommandAwaitingExecComparedWithPi |
| 8.5 | 15.9 | pass | cmd/pig | TestInteractiveSignalsRetainHandlersThroughDisposalAndDrain |
| 7.5 | 22.3 | pass | cmd/pig | TestStartupReusesPreTrustExtensionsAcrossEntrypoints |
| 7.3 | 8.4 | pass | cmd/pig | TestInteractiveSessionCommandsReplaceThroughTheRuntimeFactory |
| 7.2 | 7.0 | pass | cmd/pig | TestUpstreamSDKSkillsSubprocess |
| 7.0 | 6.8 | pass | cmd/pig | TestResourceLoaderUpstreamSDKOptions |
| 6.1 | 9.3 | pass | cmd/pig | TestHeadlessExtensionSessionIdentity |
| 5.5 | 5.5 | pass | cmd/pig | TestNpmInstalledPigUpdatesThroughRealNpm |
| 5.5 | 5.5 | pass | cmd/pig | TestRPCWaitForIdleDoesNotHoldBackTurnEndHostCall |
| 5.0 | 6.9 | pass | cmd/pig | TestRPCPreflightAdmission |
| 4.9 | 5.0 | pass | cmd/pig | TestResourceLoaderUpstreamExtensionConflicts |
| 4.5 | 5.2 | pass | cmd/pig | TestInteractiveResumeWithAMissingCWDShowsPisStatuses |
| 4.0 | 4.1 | pass | cmd/pig | TestRPCInputEndCommandSettlesDuringSlowShutdownHandler |
| 3.9 | 3.9 | pass | cmd/pig | TestEmbeddedPackedAuthDiscoveryAndLoginByLanguage |
| 3.8 | 5.0 | pass | cmd/pig | TestModeWireLifecycle |
| 3.6 | 5.4 | pass | cmd/pig | TestSystemPromptOptionsThroughHeadlessStartup |
| 3.6 | 4.1 | pass | cmd/pig | TestStartupNativeProviderCallbacksWaitForEveryExtensionFactory |
| 3.4 | 5.0 | pass | cmd/pig | TestRPCPromptResponseSemanticsUpstream |
| 3.2 | 3.8 | pass | cmd/pig | TestInteractiveModelSelectionEmitsOneModelSelectEach |
| 3.0 | 3.1 | pass | cmd/pig | TestInteractiveSameSizeResizeKeepsScrollback |
| 2.8 | 3.6 | pass | cmd/pig | TestSubprocessVirtualModelReachesTheModelRuntime |
| 2.8 | 3.5 | pass | cmd/pig | TestForkMissingOrInvalidSessionPathComparedWithPi |
| 2.7 | 0.4 | pass | cmd/pig | TestRPCAncestorSkillTrustAndPrecedence |
| 2.7 | 3.2 | pass | cmd/pig | TestStartupModelFlagSelectsExtensionVirtualModel |
| 2.7 | 2.5 | pass | cmd/pig | TestCLIUpgradeFromV020 |

### test-subprocess: 40 slowest tests before (s) and the same test after (s)

| before | after | status | package | test |
|---:|---:|---|---|---|
| 41.5 | 22.2 | pass | coding/extension/host/subprocess | TestContextResultShapesCrossSDKTransports |
| 13.7 | 13.6 | pass | coding/extension/host/subprocess | TestPackedStderrLogLifecycle |
| 13.0 | 12.9 | pass | coding/extension/host/subprocess | TestNodeRepeatedUnknownCrashBisectsAndRejoinsHealthyMembers |
| 12.6 | 12.6 | pass | coding/extension/host/subprocess | TestSlowNodeFactoryLoads |
| 11.9 | 18.6 | pass | coding/extension/host/subprocess | TestTurnBoundaryPackedFactoriesMatchIsolated |
| 11.0 | 24.9 | pass | coding/extension/host/subprocess | TestPackedPromptHandlerBodyFIFO |
| 10.9 | 34.4 | pass | coding/extension/host/subprocess | TestHost_ReloadPlansPackedRustAndFissionsQuarantinedCell |
| 10.8 | 10.6 | pass | coding/extension/host/subprocess | TestEmbeddedPackedOwnerActivationByLanguage |
| 10.4 | 14.2 | pass | coding/extension/host/subprocess | TestPackedSDKFocusedComponentAndSessionActionsMatch |
| 10.0 | 29.5 | pass | coding/extension/host/subprocess | TestAC59PackedSDKFocusedComponentsOwnInput |
| 9.8 | 11.6 | pass | coding/extension/host/subprocess | TestPackedUserMessageContentAcrossSDKs |
| 9.8 | 9.3 | pass | coding/extension/host/subprocess | TestPackedMemberFactoryFailureEndsTheLoad |
| 7.6 | 44.3 | pass | coding/extension/host/subprocess | TestRustFactoryReloadReinvokesFactoryInTheRetainedProcess |
| 7.6 | 7.5 | pass | coding/extension/host/subprocess | TestRustFactoryReloadWithControlCharactersInNameAndSocketDirectory |
| 7.5 | 28.3 | pass | coding/extension/host/subprocess | TestOAuthSubscriptionSDKPlacements |
| 7.1 | 7.5 | pass | coding/extension/host/subprocess | TestUpstreamExtensionFactoryCache |
| 7.0 | 7.0 | pass | coding/extension/host/subprocess | TestHostClosedConnectionKeepsItsProcessRetained |
| 6.4 | 15.0 | pass | coding/extension/host/subprocess | TestXrefEventBusInspectMatchesPi |
| 3.8 | 5.5 | pass | coding/extension/host/subprocess | TestGitMergeAndResolveExample |
| 3.6 | 4.4 | pass | coding/extension/host/subprocess | TestPackedProviderResponseAndToolPrompts |
| 3.5 | 3.5 | pass | coding/extension/host/subprocess | TestNodeCommandReturnDoesNotCancelItsHostCalls |
| 3.4 | 3.6 | pass | coding/extension/host/subprocess | TestMarshalEnvelope_OverflowingResultSizeIsOversized |
| 3.4 | 3.8 | pass | coding/extension/host/subprocess | TestPackedMessageOutputPadAcrossSDKs |
| 3.3 | 3.6 | pass | coding/extension/host/subprocess | TestNodeCrashAfterRetainedReloadRecoversWithoutReplay |
| 3.2 | 3.2 | pass | coding/extension/host/subprocess | TestNodeInlineExtensionNamingUpstream |
| 3.2 | 3.1 | pass | coding/extension/host/subprocess | TestPackedStderrLogUnreportedExitAndCancellation |
| 3.1 | 2.9 | pass | coding/extension/host/subprocess | TestNodeTimerCrashUsesRuntimeStackAttribution |
| 3.1 | 3.0 | pass | coding/extension/host/subprocess | TestMarshalEnvelope_OversizedResultRejectedWithoutCopy |
| 3.0 | 3.0 | pass | coding/extension/host/subprocess | TestNodeCrashQuarantinesOnlyCulpritAndDoesNotReplay |
| 3.0 | 3.3 | pass | coding/extension/host/subprocess | TestHostLoadAndReloadPreserveConfiguredGuardOrder |
| 2.9 | 2.9 | pass | coding/extension/host/subprocess | TestNodeJitiCachePersistsWithoutStaleSource |
| 2.8 | 2.8 | pass | coding/extension/host/subprocess | TestNodeFactoryCrashKeepsHealthyMembersTogether |
| 2.7 | 2.6 | pass | coding/extension/host/subprocess | TestSubagentProjectTrustUpstream |
| 2.7 | 2.7 | pass | coding/extension/host/subprocess | TestNodeSettingsManagerSharesPigSettings |
| 2.6 | 2.6 | pass | coding/extension/host/subprocess | TestNodeUnattributableCrashRestartsWholeGroup |
| 2.6 | 2.7 | pass | coding/extension/host/subprocess | TestNodeIsolatedCrashAfterRetainedReloadRestarts |
| 2.6 | 14.9 | pass | coding/extension/host/subprocess | TestToolRenderersAcrossSDKs |
| 2.6 | 2.7 | pass | coding/extension/host/subprocess | TestReloadAcrossAPlacementChangeStartsAFreshProcess |
| 2.6 | 2.6 | pass | coding/extension/host/subprocess | TestNodeRecoveryShutdownCancelsBlockedFactory |
| 2.6 | 3.0 | pass | coding/extension/host/subprocess | TestHostProviderQueueBindsToRunnerRegistry |

### test-conformance: 40 slowest tests before (s) and the same test after (s)

| before | after | status | package | test |
|---:|---:|---|---|---|
| 374.7 | 377.3 | pass | test/extension-conformance | TestNativeEventBusStressKeepsEveryDeliveryAndBoundedState |
| 43.6 | 16.2 | pass | test/extension-conformance | TestNativeEventBusListenerFailureDoesNotReachEmitterOrLaterListeners |
| 32.3 | 11.0 | pass | test/extension-conformance | TestNativeEventBusUnserializableNodePayloadReachesNoNativeListener |
| 26.0 | 0.0 | pass | test/extension-conformance | TestNativeEventBusNativeEmitterReachesNodeAndOtherCellListeners |
| 20.8 | 0.0 | pass | test/extension-conformance | TestNativeEventBusNativeViewSeesEarlierNodeListenersWrites |
| 20.1 | 0.0 | pass | test/extension-conformance | TestNativeEventBusReentrantEmitFromHandler |
| 20.1 | 0.0 | pass | test/extension-conformance | TestNativeEventBusRegistrationOrderAcrossRealms |
| 18.1 | 0.0 | pass | test/extension-conformance | TestNativeEventBusFailedFactorySubscriptionsAreDropped |
| 13.4 | 13.9 | pass | test/extension-conformance | TestConformance_TerminalInput |
| 13.2 | 1.9 | pass | test/extension-conformance | TestAutocompleteFactoriesAcrossSDKs |
| 12.2 | 0.0 | pass | test/extension-conformance | TestNativeEventBusUnhandledErrorChannel |
| 12.1 | 0.0 | pass | test/extension-conformance | TestNativeEventBusNodeEmitterReachesListenerAndItsEcho |
| 12.0 | 0.0 | pass | test/extension-conformance | TestNativeEventBusUnsubscribeDuringDispatchKeepsTheSnapshot |
| 12.0 | 0.0 | pass | test/extension-conformance | TestNativeEventBusNodePrimitivePayloads |
| 11.4 | 0.0 | pass | test/extension-conformance | TestNativeEventBusNewListenerAndRemoveListenerEvents |
| 11.1 | 0.0 | pass | test/extension-conformance | TestNativeEventBusNodePayloadIsTheEmittersJSONView |
| 11.0 | 0.0 | pass | test/extension-conformance | TestNativeEventBusHungNativeListenerBlocksOnlyItsEmitter |
| 10.9 | 0.0 | pass | test/extension-conformance | TestNativeEventBusCrossingEmittersDoNotDeadlock |
| 10.5 | 0.0 | pass | test/extension-conformance | TestNativeEventBusNativeEmitterSettlesNodeContinuationsBeforeEmitReturns |
| 10.4 | 0.0 | pass | test/extension-conformance | TestNativeEventBusFactoryTimeListenerIsLiveAtReady |
| 9.8 | 0.0 | pass | test/extension-conformance | TestNativeEventBusEmitterReentersThroughItsOwnListener |
| 9.4 | 9.4 | pass | test/extension-conformance | TestProviderObjectsAcrossSDKs |
| 8.7 | 10.1 | pass | test/extension-conformance | TestProviderProducersAcrossSDKs |
| 8.3 | 14.5 | pass | test/extension-conformance | TestPromptSelectedToolsMutationsAcrossSDKs |
| 7.5 | 8.8 | pass | test/extension-conformance | TestDynamicToolRegistrationAcrossSDKs |
| 7.3 | 5.9 | pass | test/extension-conformance | TestNativeEventBusRuntimeCrashMidDispatchReleasesTheEmitter |
| 6.4 | 3.1 | pass | test/extension-conformance | TestNativeEventBusFirstNativeCallWhileNodeRealmSpawns |
| 6.0 | 8.0 | pass | test/extension-conformance | TestPromptSectionMutationsAcrossSDKs |
| 5.9 | 9.7 | pass | test/extension-conformance | TestModelTypesReadsAcrossSDKs |
| 5.2 | 0.0 | pass | test/extension-conformance | TestRustSDKUnregisterVirtualModelBeforeBindFiltersTheRuntimeWidePendingList |
| 4.3 | 8.2 | pass | test/extension-conformance | TestModelTypesProviderOperationCancellationAcrossSDKs |
| 4.2 | 4.6 | pass | test/extension-conformance | TestScopedModelsAcrossSDKs |
| 3.6 | 4.7 | pass | test/extension-conformance | TestPromptHandlerBodyFIFOAcrossSDKs |
| 2.9 | 6.1 | pass | test/extension-conformance | TestModelTypesProviderObjectAcrossSDKs |
| 2.9 | 3.8 | pass | test/extension-conformance | TestModelTypesVirtualModelsAcrossSDKs |
| 2.8 | 4.1 | pass | test/extension-conformance | TestModelTypesClassifyAcrossSDKs |
| 2.8 | 4.9 | pass | test/extension-conformance | TestModelTypesProviderConfigImplementationsAcrossSDKs |
| 2.8 | 3.5 | pass | test/extension-conformance | TestConformance_TransportsMatch |
| 2.6 | 3.0 | pass | test/extension-conformance | TestSystemPromptOptionContentAcrossSDKs |
| 2.4 | 2.5 | pass | test/extension-conformance | TestNodeOAuthRefreshSignalFollowsItsCaller |

