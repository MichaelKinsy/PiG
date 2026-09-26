# PORT_MAP coverage report

Generated from `616` PORT_MAP entries and `262` parity scenarios.

**Porting:** 446 / 542 intended-portable entries ✅ (82.3%); **Verification:** 428 behavioral (96.0%), 4 weak-only (no behavioral verification), 14 untested.
Raw PORT_MAP rows: 616. Breakdown: 74 n/a (designed out) · 24 🟡 partial · 72 ⬜ not started. See DIVERGENCES.md for the documented exceptions.

Behavioral evidence includes paired scenarios and reviewed mutation-proven Go unit tests. Unit tests are listed separately; last run refers only to paired scenarios, not unit execution or exhaustive parity.

| upstream | port | scenarios | behavioral | last run | unit tests |
|---|---|---|---|---|---|
| `packages/agent/src/agent.ts` | ✅ | 1 (01-print-mode-arithmetic) | 1 (01-print-mode-arithmetic) | not run |  |
| `packages/agent/src/agent-loop.ts` | ✅ | 2 (02-print-faux-tool-cycle, 07-print-faux-parallel-tools) | 2 (02-print-faux-tool-cycle, 07-print-faux-parallel-tools) | not run |  |
| `packages/agent/src/types.ts` | ✅ | 2 (02-print-faux-tool-cycle, 07-print-faux-parallel-tools) | 2 (02-print-faux-tool-cycle, 07-print-faux-parallel-tools) | not run |  |
| `packages/agent/src/proxy.ts` | 🟡 | 0 | 0 | not run |  |
| `packages/agent/src/index.ts` | n/a | 0 | 0 | not run |  |
| `packages/agent/src/node.ts` | n/a | 0 | 0 | not run |  |
| `packages/agent/src/harness/messages.ts` | ✅ | 1 (07-skill-command-system-prompt) | 1 (07-skill-command-system-prompt) | not run |  |
| `packages/agent/src/harness/prompt-templates.ts` | ✅ | 1 (07-skill-command-system-prompt) | 1 (07-skill-command-system-prompt) | not run |  |
| `packages/agent/src/harness/system-prompt.ts` | ✅ | 1 (07-skill-command-system-prompt) | 1 (07-skill-command-system-prompt) | not run |  |
| `packages/agent/src/harness/types.ts` | ✅ | 1 (05-session-lifecycle) | 1 (05-session-lifecycle) | not run |  |
| `packages/agent/src/harness/agent-harness.ts` | ✅ | 1 (05-session-lifecycle) | 1 (05-session-lifecycle) | not run |  |
| `packages/agent/src/harness/tools/bash.ts` | ✅ | 1 (01-print-bash-tool-exec) | 1 (01-print-bash-tool-exec) | not run |  |
| `packages/agent/src/harness/tools/edit-diff.ts` | ✅ | 1 (07-print-edit-tool-exec) | 1 (07-print-edit-tool-exec) | not run |  |
| `packages/agent/src/harness/tools/edit.ts` | ✅ | 1 (07-print-edit-tool-exec) | 1 (07-print-edit-tool-exec) | not run |  |
| `packages/agent/src/harness/tools/file-mutation-queue.ts` | ✅ | 0 | 1 (unit:./internal/codingagent/tools:TestMutationQueuePreservesFailureAndReleasesPath) | not run | ./internal/codingagent/tools:TestMutationQueuePreservesFailureAndReleasesPath |
| `packages/agent/src/harness/tools/image.ts` | ✅ | 0 | 2 (unit:./internal/codingagent/tools:TestHarnessImageBMPPlanesAndBitDepth, unit:./internal/codingagent/tools:TestHarnessImageMagicBoundaries) | not run | ./internal/codingagent/tools:TestHarnessImageBMPPlanesAndBitDepth, ./internal/codingagent/tools:TestHarnessImageMagicBoundaries |
| `packages/agent/src/harness/tools/index.ts` | n/a | 0 | 0 | not run |  |
| `packages/agent/src/harness/tools/path-utils.ts` | ✅ | 0 | 1 (unit:./internal/codingagent/tools:TestExpandPath_UnicodeSpaces) | not run | ./internal/codingagent/tools:TestExpandPath_UnicodeSpaces |
| `packages/agent/src/harness/tools/read.ts` | ✅ | 1 (02-print-read-tool-exec) | 1 (02-print-read-tool-exec) | not run |  |
| `packages/agent/src/harness/tools/tool-context.ts` | n/a | 0 | 0 | not run |  |
| `packages/agent/src/harness/tools/write.ts` | ✅ | 1 (03-print-write-tool-exec) | 1 (03-print-write-tool-exec) | not run |  |
| `packages/agent/src/harness/skills.ts` | ✅ | 1 (07-skill-command-system-prompt) | 1 (07-skill-command-system-prompt) | not run |  |
| `packages/agent/src/harness/compaction/compaction.ts` | ✅ | 1 (01-compact-command) | 1 (01-compact-command) | not run |  |
| `packages/agent/src/harness/compaction/utils.ts` | ✅ | 1 (01-compact-command) | 1 (01-compact-command) | not run |  |
| `packages/agent/src/harness/compaction/branch-summarization.ts` | ✅ | 1 (03-branch-summarize-via-tree) | 1 (03-branch-summarize-via-tree) | not run |  |
| `packages/agent/src/harness/utils/shell-output.ts` | ✅ | 1 (08-print-tool-truncation-pipeline) | 1 (08-print-tool-truncation-pipeline) | not run |  |
| `packages/agent/src/harness/utils/truncate.ts` | ✅ | 1 (08-print-tool-truncation-pipeline) | 1 (08-print-tool-truncation-pipeline) | not run |  |
| `packages/agent/src/harness/env/nodejs.ts` | ✅ | 0 | 1 (unit:./agent/harness/env:TestNodeEnvExpandsHomeRelativePathsAndFileURLs) | not run | ./agent/harness/env:TestNodeEnvExpandsHomeRelativePathsAndFileURLs |
| `packages/agent/src/harness/session/session.ts` | ✅ | 2 (02-session-memory-context, 05-session-lifecycle) | 2 (02-session-memory-context, 05-session-lifecycle) | not run |  |
| `packages/agent/src/harness/result.ts` | ✅ | 0 | 1 (unit:./agent/harness:TestTaggedErrorJSONMatchesUpstreamToJSON) | not run | ./agent/harness:TestTaggedErrorJSONMatchesUpstreamToJSON |
| `packages/agent/src/harness/session/context.ts` | ✅ | 0 | 1 (unit:./agent/harness/session:TestContextProjectorFailureStopsLaterEffects) | not run | ./agent/harness/session:TestContextProjectorFailureStopsLaterEffects |
| `packages/agent/src/harness/session/index.ts` | n/a | 0 | 0 | not run |  |
| `packages/agent/src/harness/session/jsonl/codec.ts` | ✅ | 0 | 1 (unit:./agent/harness/session:TestJsonlHeaderValidation) | not run | ./agent/harness/session:TestJsonlHeaderValidation |
| `packages/agent/src/harness/session/jsonl/repo.ts` | ✅ | 0 | 1 (unit:./agent/harness/session:TestJsonlRepositoryCwdDiscoveryAndEncodedIDs) | not run | ./agent/harness/session:TestJsonlRepositoryCwdDiscoveryAndEncodedIDs |
| `packages/agent/src/harness/session/jsonl/storage.ts` | ✅ | 0 | 1 (unit:./agent/harness/session:TestJsonlTornTailAndCompleteCorruption) | not run | ./agent/harness/session:TestJsonlTornTailAndCompleteCorruption |
| `packages/agent/src/harness/session/jsonl/types.ts` | ✅ | 0 | 2 (unit:./agent/harness/session:TestHarnessJSONLHeaderRepositoryRoundTrip, unit:./agent/harness/session:TestHarnessJSONLInvalidHeaderRepositoryOpen) | not run | ./agent/harness/session:TestHarnessJSONLHeaderRepositoryRoundTrip, ./agent/harness/session:TestHarnessJSONLInvalidHeaderRepositoryOpen |
| `packages/agent/src/harness/session/memory.ts` | ✅ | 0 | 1 (unit:./agent/harness/session:TestMemoryStorageConformance) | not run | ./agent/harness/session:TestMemoryStorageConformance |
| `packages/agent/src/harness/session/testing/index.ts` | n/a | 0 | 0 | not run |  |
| `packages/agent/src/harness/session/testing/types.ts` | ✅ | **0 (untested)** | **0** | not run |  |
| `packages/agent/src/harness/session/types.ts` | ✅ | 0 | 1 (unit:./agent/harness/session:TestPendingEntryAndEntryJSONKeepNullDistinctFromAbsent) | not run | ./agent/harness/session:TestPendingEntryAndEntryJSONKeepNullDistinctFromAbsent |
| `packages/agent/src/harness/telemetry.ts` | 🟡 | 0 | 0 | not run |  |
| `packages/agent/src/harness/config.ts` | ✅ | 0 | 1 (unit:./agent/harness:TestValidateToolNamesRejectsDuplicates) | not run | ./agent/harness:TestValidateToolNamesRejectsDuplicates |
| `packages/agent/src/harness/context.ts` | 🟡 | 0 | 0 | not run |  |
| `packages/agent/src/harness/events.ts` | 🟡 | 0 | 0 | not run |  |
| `packages/agent/src/harness/execution/assistant.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/agent/src/harness/execution/effect-gate.ts` | 🟡 | 0 | 0 | not run |  |
| `packages/agent/src/harness/execution/tools.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/agent/src/harness/hooks.ts` | 🟡 | 0 | 0 | not run |  |
| `packages/agent/src/harness/pico3/bash.ts` | ✅ | 0 | 1 (unit:./agent/harness/pico3:TestBashToolStreamsAndReportsExit) | not run | ./agent/harness/pico3:TestBashToolStreamsAndReportsExit |
| `packages/agent/src/harness/pico3/bounded.ts` | ✅ | 0 | 1 (unit:./agent/harness/pico3:TestBoundedSlicesOversizedChunksAndCapsLines) | not run | ./agent/harness/pico3:TestBoundedSlicesOversizedChunksAndCapsLines |
| `packages/agent/src/harness/pico3/chord.ts` | ✅ | 0 | 1 (unit:./agent/harness/pico3:TestChordRejectsInvalidCapacity) | not run | ./agent/harness/pico3:TestChordRejectsInvalidCapacity |
| `packages/agent/src/harness/pico3/context.ts` | ✅ | 0 | 1 (unit:./agent/harness/pico3:TestForkMissingToolResultPreservesCallIdentity) | not run | ./agent/harness/pico3:TestForkMissingToolResultPreservesCallIdentity |
| `packages/agent/src/harness/pico3/harness.ts` | ✅ | 0 | 1 (unit:./agent/harness/pico3:TestOneTurnWithTwoToolsOnBothBackends) | not run | ./agent/harness/pico3:TestOneTurnWithTwoToolsOnBothBackends |
| `packages/agent/src/harness/pico3/hooks.ts` | ✅ | 0 | 1 (unit:./agent/harness/pico3:TestBeforeCollapseDeclineSummaryAndInstructions) | not run | ./agent/harness/pico3:TestBeforeCollapseDeclineSummaryAndInstructions |
| `packages/agent/src/harness/pico3/index.ts` | ✅ | **0 (untested)** | **0** | not run |  |
| `packages/agent/src/harness/pico3/jsonl.ts` | ✅ | 0 | 2 (unit:./agent/harness/pico3:TestHarnessPicoJSONLPublicationRecovery, unit:./agent/harness/pico3:TestHarnessPicoJSONLRetirementAndHighWater) | not run | ./agent/harness/pico3:TestHarnessPicoJSONLPublicationRecovery, ./agent/harness/pico3:TestHarnessPicoJSONLRetirementAndHighWater |
| `packages/agent/src/harness/pico3/kinds/collapse.ts` | ✅ | 0 | 1 (unit:./agent/harness/pico3:TestBeforeCollapseDeclineSummaryAndInstructions) | not run | ./agent/harness/pico3:TestBeforeCollapseDeclineSummaryAndInstructions |
| `packages/agent/src/harness/pico3/kinds/entries.ts` | ✅ | 0 | 1 (unit:./agent/harness/pico3:TestOneTurnWithTwoToolsOnBothBackends) | not run | ./agent/harness/pico3:TestOneTurnWithTwoToolsOnBothBackends |
| `packages/agent/src/harness/pico3/kinds/frames.ts` | ✅ | 0 | 1 (unit:./agent/harness/pico3:TestFrameMetadataUsesSharedAIValues) | not run | ./agent/harness/pico3:TestFrameMetadataUsesSharedAIValues |
| `packages/agent/src/harness/pico3/kinds/generation.ts` | ✅ | 0 | 1 (unit:./agent/harness/pico3:TestOneTurnWithTwoToolsOnBothBackends) | not run | ./agent/harness/pico3:TestOneTurnWithTwoToolsOnBothBackends |
| `packages/agent/src/harness/pico3/kinds/job.ts` | ✅ | 0 | 1 (unit:./agent/harness/pico3:TestSchedulerJobSpawnFailure) | not run | ./agent/harness/pico3:TestSchedulerJobSpawnFailure |
| `packages/agent/src/harness/pico3/kinds/plugin.ts` | ✅ | 0 | 1 (unit:./agent/harness/pico3:TestPluginHandlerResultAndMissingHandler) | not run | ./agent/harness/pico3:TestPluginHandlerResultAndMissingHandler |
| `packages/agent/src/harness/pico3/kinds/post-tools.ts` | ✅ | 0 | 1 (unit:./agent/harness/pico3:TestOneTurnWithTwoToolsOnBothBackends) | not run | ./agent/harness/pico3:TestOneTurnWithTwoToolsOnBothBackends |
| `packages/agent/src/harness/pico3/kinds/task-api.ts` | ✅ | 0 | 1 (unit:./agent/harness/pico3:TestPluginTaskAPIRejectsToolOnlyActions) | not run | ./agent/harness/pico3:TestPluginTaskAPIRejectsToolOnlyActions |
| `packages/agent/src/harness/pico3/kinds/tool.ts` | ✅ | 0 | 1 (unit:./agent/harness/pico3:TestOneTurnWithTwoToolsOnBothBackends) | not run | ./agent/harness/pico3:TestOneTurnWithTwoToolsOnBothBackends |
| `packages/agent/src/harness/pico3/membrane.ts` | ✅ | 0 | 1 (unit:./agent/harness/pico3:TestMembraneIdentityRevocationAndPlainInputs) | not run | ./agent/harness/pico3:TestMembraneIdentityRevocationAndPlainInputs |
| `packages/agent/src/harness/pico3/memory.ts` | ✅ | 0 | 1 (unit:./agent/harness/pico3:TestOneTurnWithTwoToolsOnBothBackends) | not run | ./agent/harness/pico3:TestOneTurnWithTwoToolsOnBothBackends |
| `packages/agent/src/harness/pico3/scheduler.ts` | ✅ | 0 | 1 (unit:./agent/harness/pico3:TestOneTurnWithTwoToolsOnBothBackends) | not run | ./agent/harness/pico3:TestOneTurnWithTwoToolsOnBothBackends |
| `packages/agent/src/harness/pico3/session.ts` | ✅ | 0 | 1 (unit:./agent/harness/pico3:TestOneTurnWithTwoToolsOnBothBackends) | not run | ./agent/harness/pico3:TestOneTurnWithTwoToolsOnBothBackends |
| `packages/agent/src/harness/pico3/system.ts` | ✅ | 0 | 1 (unit:./agent/harness/pico3:TestSystemSectionsBaselineDeltaAndHeadOmissions) | not run | ./agent/harness/pico3:TestSystemSectionsBaselineDeltaAndHeadOmissions |
| `packages/agent/src/harness/pico3/types.ts` | ✅ | 0 | 1 (unit:./agent/harness/pico3:TestOneTurnWithTwoToolsOnBothBackends) | not run | ./agent/harness/pico3:TestOneTurnWithTwoToolsOnBothBackends |
| `packages/agent/src/harness/pico3/view.ts` | ✅ | 0 | 1 (unit:./agent/harness/pico3:TestWatchConvergesAndStops) | not run | ./agent/harness/pico3:TestWatchConvergesAndStops |
| `packages/agent/src/harness/runtime/drive.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/agent/src/harness/runtime/drive/boundary.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/agent/src/harness/runtime/drive/checkpoint.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/agent/src/harness/runtime/drive/deferred.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/agent/src/harness/runtime/drive/generation.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/agent/src/harness/runtime/drive/reconcile.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/agent/src/harness/runtime/drive/recovery.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/agent/src/harness/runtime/drive/response.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/agent/src/harness/runtime/drive/retry.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/agent/src/harness/runtime/drive/structural.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/agent/src/harness/runtime/drive/terminal.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/agent/src/harness/runtime/drive/tool-placement.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/agent/src/harness/runtime/drive/tools.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/agent/src/harness/runtime/harness.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/agent/src/harness/runtime/index.ts` | n/a | 0 | 0 | not run |  |
| `packages/agent/src/harness/runtime/lane.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/agent/src/harness/runtime/progress.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/agent/src/harness/runtime/reducer.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/agent/src/harness/runtime/restore.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/agent/src/harness/runtime/transcript.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/agent/src/harness/runtime/types.ts` | 🟡 | 0 | 0 | not run |  |
| `packages/agent/src/harness/session/commit.ts` | ✅ | 0 | 1 (unit:./agent/harness/session:TestMemoryStorageConformance) | not run | ./agent/harness/session:TestMemoryStorageConformance |
| `packages/agent/src/harness/session/fork-policy.ts` | ✅ | 0 | 1 (unit:./agent/harness/session:TestCreateForkSnapshotMatchesUpstream) | not run | ./agent/harness/session:TestCreateForkSnapshotMatchesUpstream |
| `packages/agent/src/harness/session/fork.ts` | ✅ | 0 | 1 (unit:./agent/harness/session:TestCreateForkSnapshotMatchesUpstream) | not run | ./agent/harness/session:TestCreateForkSnapshotMatchesUpstream |
| `packages/agent/src/harness/session/in-memory-storage-state.ts` | ✅ | 0 | 1 (unit:./agent/harness/session:TestMemoryStorageConformance) | not run | ./agent/harness/session:TestMemoryStorageConformance |
| `packages/agent/src/harness/session/jsonl/fork.ts` | ✅ | 0 | 1 (unit:./agent/harness/session:TestJsonlSessionRepoConformance) | not run | ./agent/harness/session:TestJsonlSessionRepoConformance |
| `packages/agent/src/harness/session/jsonl/index.ts` | n/a | 0 | 0 | not run |  |
| `packages/agent/src/harness/session/jsonl/io.ts` | ✅ | 0 | 1 (unit:./agent/harness/session:TestJsonlAtomicPublicationFailuresAndRetry) | not run | ./agent/harness/session:TestJsonlAtomicPublicationFailuresAndRetry |
| `packages/agent/src/harness/session/jsonl/legacy-v3.ts` | ✅ | 0 | 1 (unit:./agent/harness/session:TestJsonlLegacyConfigurationLabelsAndDiscardedParents) | not run | ./agent/harness/session:TestJsonlLegacyConfigurationLabelsAndDiscardedParents |
| `packages/agent/src/harness/session/mutation-line.ts` | ✅ | 0 | 1 (unit:./agent/harness/session:TestMutationLineContinuesAfterFailedJobPreservingTheFailure) | not run | ./agent/harness/session:TestMutationLineContinuesAfterFailedJobPreservingTheFailure |
| `packages/agent/src/harness/session/testing/benchmark/datasets.ts` | ✅ | 0 | 1 (unit:./agent/harness/session:TestMemoryBenchmarkScenariosProduceTheirExpectedResults) | not run | ./agent/harness/session:TestMemoryBenchmarkScenariosProduceTheirExpectedResults |
| `packages/agent/src/harness/session/testing/benchmark/session-repo.ts` | ✅ | 0 | 1 (unit:./agent/harness/session:TestMemoryBenchmarkScenariosProduceTheirExpectedResults) | not run | ./agent/harness/session:TestMemoryBenchmarkScenariosProduceTheirExpectedResults |
| `packages/agent/src/harness/session/testing/benchmark/storage.ts` | ✅ | 0 | 1 (unit:./agent/harness/session:TestMemoryBenchmarkScenariosProduceTheirExpectedResults) | not run | ./agent/harness/session:TestMemoryBenchmarkScenariosProduceTheirExpectedResults |
| `packages/agent/src/harness/session/testing/conformance/session-repo.ts` | ✅ | 0 | 1 (unit:./agent/harness/session:TestJsonlSessionRepoConformance) | not run | ./agent/harness/session:TestJsonlSessionRepoConformance |
| `packages/agent/src/harness/session/testing/conformance/storage.ts` | ✅ | 0 | 1 (unit:./agent/harness/session:TestMemoryStorageConformance) | not run | ./agent/harness/session:TestMemoryStorageConformance |
| `packages/agent/src/harness/session/testing/gating-storage.ts` | ✅ | 0 | 1 (unit:./agent/harness/session/testing:TestGatingStorageRejectsNonPositiveCounts) | not run | ./agent/harness/session/testing:TestGatingStorageRejectsNonPositiveCounts |
| `packages/agent/src/harness/session/testing/instrumented-storage.ts` | ✅ | 0 | 1 (unit:./agent/harness/session/testing:TestInstrumentedStorageClearsAttemptsWithoutAffectingTheDelegate) | not run | ./agent/harness/session/testing:TestInstrumentedStorageClearsAttemptsWithoutAffectingTheDelegate |
| `packages/agent/src/harness/session/testing/storage-decorator.ts` | ✅ | 0 | 1 (unit:./agent/harness/session/testing:TestInstrumentedStorageDelegatesEveryReadWithoutRecordingSyntheticWrites) | not run | ./agent/harness/session/testing:TestInstrumentedStorageDelegatesEveryReadWithoutRecordingSyntheticWrites |
| `packages/agent/src/harness/session/values.ts` | ✅ | 0 | 1 (unit:./agent/harness/session:TestBoundAddressesValidateComponents) | not run | ./agent/harness/session:TestBoundAddressesValidateComponents |
| `packages/agent/src/harness/utils/adaptive-publisher.ts` | ✅ | 0 | 1 (unit:./agent/harness/utils:TestAdaptivePublisherCommitsItsBaselineBeforeAConsumerThrows) | not run | ./agent/harness/utils:TestAdaptivePublisherCommitsItsBaselineBeforeAConsumerThrows |
| `packages/agent/src/harness/utils/output-capture.ts` | ✅ | 0 | 1 (unit:./agent/harness/utils:TestOutputCaptureSlideDropCountsUTF16CodeUnits) | not run | ./agent/harness/utils:TestOutputCaptureSlideDropCountsUTF16CodeUnits |
| `packages/agent/src/harness/utils/usage.ts` | ✅ | 0 | 1 (unit:./agent/harness/utils:TestUsageOptionalPresenceAndOwnership) | not run | ./agent/harness/utils:TestUsageOptionalPresenceAndOwnership |
| `packages/agent/src/search/index.ts` | ✅ | **0 (untested)** | **0** | not run |  |
| `packages/ai/src/types.ts` | ✅ | 2 (01-print-faux-text-streaming, 03-print-faux-error-stop) | 2 (01-print-faux-text-streaming, 03-print-faux-error-stop) | not run |  |
| `packages/ai/src/models.generated.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/image-models.generated.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/models.ts` | ✅ | 2 (01-ai-sdk-helpers, 12-expired-credential-is-not-refresh-failure) | 2 (01-ai-sdk-helpers, 12-expired-credential-is-not-refresh-failure) | not run |  |
| `packages/ai/src/models-store.ts` | ✅ | 0 | 1 (unit:./ai:TestInMemoryModelsStoreClonesAndDeletes) | not run | ./ai:TestInMemoryModelsStoreClonesAndDeletes |
| `packages/ai/src/model-catalog.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/providers/all.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/api/anthropic-messages.ts` | 🟡 | 1 (04-provider-wire-payloads) | 1 (04-provider-wire-payloads) | not run |  |
| `packages/ai/src/api/constrained-sampling.ts` | ✅ | 0 | 1 (unit:./ai:TestResolveJSONSchemaStrictSampling) | not run | ./ai:TestResolveJSONSchemaStrictSampling |
| `packages/ai/src/api/azure-openai-responses.ts` | ✅ | 1 (04-provider-wire-payloads) | 1 (04-provider-wire-payloads) | not run |  |
| `packages/ai/src/api/bedrock-converse-stream.ts` | ✅ | 1 (04-provider-wire-payloads) | 1 (04-provider-wire-payloads) | not run |  |
| `packages/ai/src/api/google-generative-ai.ts` | ✅ | 1 (04-provider-wire-payloads) | 1 (04-provider-wire-payloads) | not run |  |
| `packages/ai/src/api/google-vertex.ts` | ✅ | 1 (04-provider-wire-payloads) | 1 (04-provider-wire-payloads) | not run |  |
| `packages/ai/src/api/mistral-conversations.ts` | ✅ | 1 (04-provider-wire-payloads) | 1 (04-provider-wire-payloads) | not run |  |
| `packages/ai/src/api/anthropic-messages.lazy.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/api/azure-openai-responses.lazy.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/api/bedrock-converse-stream.lazy.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/api/google-generative-ai.lazy.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/api/google-vertex.lazy.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/api/mistral-conversations.lazy.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/api/openai-codex-responses.lazy.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/api/openai-completions.lazy.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/api/openai-responses.lazy.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/api/openrouter-images.lazy.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/api/pi-messages.lazy.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/auth/context.ts` | ✅ | 1 (01-list-models-env-builtins) | **0 (weak-only)** | not run |  |
| `packages/ai/src/auth/credential-store.ts` | ✅ | 1 (02-list-models-oauth-builtins) | **0 (weak-only)** | not run |  |
| `packages/ai/src/auth/helpers.ts` | ✅ | 2 (01-ai-sdk-helpers, 01-list-models-env-builtins) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/auth/resolve.ts` | ✅ | 2 (01-list-models-env-builtins, 02-list-models-oauth-builtins) | **0 (weak-only)** | not run |  |
| `packages/ai/src/auth/types.ts` | ✅ | 4 (01-ai-sdk-helpers, 01-list-models-env-builtins, 01-oauth-shared, 02-list-models-oauth-builtins) | 2 (01-ai-sdk-helpers, 01-oauth-shared) | not run |  |
| `packages/ai/src/auth/oauth/anthropic.ts` | ✅ | 1 (02-oauth-anthropic) | 1 (02-oauth-anthropic) | not run |  |
| `packages/ai/src/auth/oauth/device-code.ts` | ✅ | 1 (03-oauth-github-copilot) | 1 (03-oauth-github-copilot) | not run |  |
| `packages/ai/src/auth/oauth/github-copilot.ts` | ✅ | 3 (03-oauth-github-copilot, 07-login-dialog, 07-oauth-github-copilot-env) | 3 (03-oauth-github-copilot, 07-login-dialog, 07-oauth-github-copilot-env) | not run |  |
| `packages/ai/src/auth/oauth/load.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/auth/oauth/kimi-coding.ts` | ✅ | 0 | 1 (unit:./ai:TestKimiRefreshRejectsInvalidGrantWithoutRetry) | not run | ./ai:TestKimiRefreshRejectsInvalidGrantWithoutRetry |
| `packages/ai/src/auth/oauth/openrouter.ts` | ✅ | 1 (08-oauth-openrouter) | 1 (08-oauth-openrouter) | not run |  |
| `packages/ai/src/auth/oauth/oauth-page.ts` | ✅ | 1 (06-oauth-callback-page) | 1 (06-oauth-callback-page) | not run |  |
| `packages/ai/src/auth/oauth/openai-codex.ts` | ✅ | 1 (05-oauth-openai-codex) | 1 (05-oauth-openai-codex) | not run |  |
| `packages/ai/src/auth/oauth/pkce.ts` | ✅ | 1 (01-oauth-shared) | 1 (01-oauth-shared) | not run |  |
| `packages/ai/src/auth/oauth/radius.ts` | ✅ | 0 | 1 (unit:./ai:TestRadiusOAuthDeviceLoginUsesGatewayEndpoints) | not run | ./ai:TestRadiusOAuthDeviceLoginUsesGatewayEndpoints |
| `packages/ai/src/auth/oauth/xai.ts` | ✅ | 1 (09-oauth-xai) | 1 (09-oauth-xai) | not run |  |
| `packages/ai/src/bun-oauth.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/compat.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/compat/extension-oauth-types.ts` | ✅ | **0 (untested)** | **0** | not run |  |
| `packages/ai/src/images-models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/legacy-api-aliases.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/providers/openai-codex.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/openai.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/openrouter-images.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/utils/error-body.ts` | ✅ | 1 (04-provider-wire-payloads) | 1 (04-provider-wire-payloads) | not run |  |
| `packages/ai/src/utils/estimate.ts` | ✅ | 1 (04-overflow-retry) | 1 (04-overflow-retry) | not run |  |
| `packages/ai/src/utils/retry.ts` | ✅ | 1 (08-print-faux-auto-retry) | 1 (08-print-faux-auto-retry) | not run |  |
| `packages/ai/src/utils/provider-retry.ts` | ✅ | 0 | 1 (unit:./ai:TestIsRetryableProviderResponse) | not run | ./ai:TestIsRetryableProviderResponse |
| `packages/ai/src/providers/amazon-bedrock.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/ant-ling.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/anthropic.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/azure-openai-responses.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/cerebras.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/cloudflare-ai-gateway.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/cloudflare-workers-ai.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/deepseek.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/fireworks.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/github-copilot.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/google-vertex.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/google.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/groq.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/huggingface.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/kimi-coding.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/minimax-cn.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/minimax.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/mistral.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/moonshotai-cn.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/moonshotai.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/nvidia.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/openai-codex.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/openai.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/opencode-go.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/opencode.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/openrouter.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/qwen-token-plan.models.ts` | ✅ | 0 | 1 (unit:./ai:TestQwenTokenPlanExposesTextModelsAndOmitsImageModels) | not run | ./ai:TestQwenTokenPlanExposesTextModelsAndOmitsImageModels |
| `packages/ai/src/providers/qwen-token-plan-cn.models.ts` | ✅ | 0 | 1 (unit:./ai:TestQwenTokenPlanExposesTextModelsAndOmitsImageModels) | not run | ./ai:TestQwenTokenPlanExposesTextModelsAndOmitsImageModels |
| `packages/ai/src/providers/together.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/vercel-ai-gateway.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/xai.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/xiaomi-token-plan-ams.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/xiaomi-token-plan-cn.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/xiaomi-token-plan-sgp.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/xiaomi.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/zai-coding-cn.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/zai.models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/ant-ling.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/cerebras.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/cloudflare-ai-gateway.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/cloudflare-auth.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/cloudflare-stream.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/cloudflare-workers-ai.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/data-json.d.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/providers/deepseek.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/fireworks.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/github-copilot.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/groq.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/huggingface.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/kimi-coding.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/minimax-cn.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/minimax.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/moonshotai-cn.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/moonshotai.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/nvidia.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/opencode-go.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/opencode.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/openrouter.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/qwen-token-plan.ts` | ✅ | 0 | 1 (unit:./ai:TestQwenTokenPlanCatalogRoutes) | not run | ./ai:TestQwenTokenPlanCatalogRoutes |
| `packages/ai/src/providers/qwen-token-plan-cn.ts` | ✅ | 0 | 1 (unit:./ai:TestQwenTokenPlanCatalogRoutes) | not run | ./ai:TestQwenTokenPlanCatalogRoutes |
| `packages/ai/src/providers/radius-config.ts` | ✅ | 1 (11-radius-metadata-import) | 1 (11-radius-metadata-import) | not run |  |
| `packages/ai/src/providers/radius.ts` | ✅ | 1 (11-radius-metadata-import) | 1 (11-radius-metadata-import) | not run |  |
| `packages/ai/src/providers/together.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/vercel-ai-gateway.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/xai.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/xiaomi-token-plan-ams.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/xiaomi-token-plan-cn.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/xiaomi-token-plan-sgp.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/xiaomi.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/zai-coding-cn.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/zai.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/status-indicator.ts` | 🟡 | 2 (10-loader-countdown-bordered-behavior, 21-status-lifecycle) | 2 (10-loader-countdown-bordered-behavior, 21-status-lifecycle) | not run |  |
| `packages/coding-agent/src/rpc-entry.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/utils/image-process.ts` | ✅ | 1 (06-bmp-file-args) | 1 (06-bmp-file-args) | not run |  |
| `packages/ai/src/api/lazy.ts` | n/a | 0 | 0 | not run |  |
| `packages/agent/src/stream-fn.ts` | 🟡 | 0 | 0 | not run |  |
| `packages/ai/src/env-api-keys.ts` | ✅ | 3 (01-list-models-env-builtins, 02-list-models-oauth-builtins, 07-oauth-github-copilot-env) | 3 (01-list-models-env-builtins, 02-list-models-oauth-builtins, 07-oauth-github-copilot-env) | not run |  |
| `packages/ai/src/session-resources.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/oauth.ts` | ✅ | 1 (01-oauth-shared) | 1 (01-oauth-shared) | not run |  |
| `packages/ai/src/index.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/cli.ts` | ✅ | 1 (07-login-github-copilot) | 1 (07-login-github-copilot) | not run |  |
| `packages/ai/src/bedrock-provider.ts` | ✅ | 1 (04-provider-wire-payloads) | 1 (04-provider-wire-payloads) | not run |  |
| `packages/ai/src/api/openai-completions.ts` | ✅ | 3 (01-print-faux-text-streaming, 04-provider-wire-payloads, 09-orphaned-tool-result-wire) | 3 (01-print-faux-text-streaming, 04-provider-wire-payloads, 09-orphaned-tool-result-wire) | not run |  |
| `packages/ai/src/api/openai-responses.ts` | ✅ | 1 (04-provider-wire-payloads) | 1 (04-provider-wire-payloads) | not run |  |
| `packages/ai/src/api/openai-responses-shared.ts` | ✅ | 2 (04-provider-wire-payloads, 10-responses-tool-identity) | 2 (04-provider-wire-payloads, 10-responses-tool-identity) | not run |  |
| `packages/ai/src/api/openai-codex-responses.ts` | 🟡 | 1 (04-provider-wire-payloads) | 1 (04-provider-wire-payloads) | not run |  |
| `packages/ai/src/api/openai-prompt-cache.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/api/pi-messages.ts` | ✅ | 0 | 1 (unit:./ai:TestPiMessagesInMemoryPrematureEnd) | not run | ./ai:TestPiMessagesInMemoryPrematureEnd |
| `packages/ai/src/providers/anthropic.ts` | ✅ | 2 (01-ai-sdk-helpers, 04-provider-wire-payloads) | 2 (01-ai-sdk-helpers, 04-provider-wire-payloads) | not run |  |
| `packages/ai/src/providers/google.ts` | ✅ | 2 (01-ai-sdk-helpers, 04-provider-wire-payloads) | 2 (01-ai-sdk-helpers, 04-provider-wire-payloads) | not run |  |
| `packages/ai/src/api/google-shared.ts` | ✅ | 1 (04-provider-wire-payloads) | 1 (04-provider-wire-payloads) | not run |  |
| `packages/ai/src/providers/google-vertex.ts` | ✅ | 2 (01-ai-sdk-helpers, 04-provider-wire-payloads) | 2 (01-ai-sdk-helpers, 04-provider-wire-payloads) | not run |  |
| `packages/ai/src/providers/amazon-bedrock.ts` | ✅ | 2 (01-ai-sdk-helpers, 04-provider-wire-payloads) | 2 (01-ai-sdk-helpers, 04-provider-wire-payloads) | not run |  |
| `packages/ai/src/api/cloudflare.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/providers/azure-openai-responses.ts` | ✅ | 2 (01-ai-sdk-helpers, 04-provider-wire-payloads) | 2 (01-ai-sdk-helpers, 04-provider-wire-payloads) | not run |  |
| `packages/ai/src/providers/mistral.ts` | ✅ | 2 (01-ai-sdk-helpers, 04-provider-wire-payloads) | 2 (01-ai-sdk-helpers, 04-provider-wire-payloads) | not run |  |
| `packages/ai/src/api/github-copilot-headers.ts` | ✅ | 2 (01-print-mode-arithmetic, 03-copilot-headers) | 2 (01-print-mode-arithmetic, 03-copilot-headers) | not run |  |
| `packages/ai/src/providers/faux.ts` | ✅ | 2 (01-print-faux-text-streaming, 03-print-faux-error-stop) | 2 (01-print-faux-text-streaming, 03-print-faux-error-stop) | not run |  |
| `packages/ai/src/api/transform-messages.ts` | ✅ | 3 (02-print-faux-tool-cycle, 07-print-faux-parallel-tools, 09-orphaned-tool-result-wire) | 3 (02-print-faux-tool-cycle, 07-print-faux-parallel-tools, 09-orphaned-tool-result-wire) | not run |  |
| `packages/ai/src/providers/images/register-builtins.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/api/openrouter-images.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/api/simple-options.ts` | ✅ | 1 (02-print-faux-tool-cycle) | 1 (02-print-faux-tool-cycle) | not run |  |
| `packages/ai/src/utils/event-stream.ts` | ✅ | 1 (01-print-faux-text-streaming) | 1 (01-print-faux-text-streaming) | not run |  |
| `packages/ai/src/utils/provider-env.ts` | ✅ | 1 (04-provider-wire-payloads) | 1 (04-provider-wire-payloads) | not run |  |
| `packages/ai/src/images.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/image-models.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/images-api-registry.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/utils/hash.ts` | ✅ | 0 | 1 (unit:./ai:TestResponsesConvertMessages_ForeignCopilotToolCallIDHashedToFcShape) | not run | ./ai:TestResponsesConvertMessages_ForeignCopilotToolCallIDHashedToFcShape |
| `packages/ai/src/utils/headers.ts` | ✅ | 1 (03-copilot-headers) | 1 (03-copilot-headers) | not run |  |
| `packages/ai/src/utils/json-parse.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/utils/diagnostics.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/utils/node-http-proxy.ts` | ✅ | 1 (01-ai-sdk-helpers) | 1 (01-ai-sdk-helpers) | not run |  |
| `packages/ai/src/utils/overflow.ts` | ✅ | 1 (04-print-faux-overflow-error) | **0 (weak-only)** | not run |  |
| `packages/ai/src/utils/text.ts` | ✅ | **0 (untested)** | **0** | not run |  |
| `packages/ai/src/utils/uuid.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/utils/abort-signals.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/utils/sanitize-unicode.ts` | ✅ | 1 (06-print-faux-sanitize-control) | 1 (06-print-faux-sanitize-control) | not run |  |
| `packages/ai/src/utils/typebox-helpers.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/utils/validation.ts` | ✅ | 1 (05-print-faux-validation-reject) | 1 (05-print-faux-validation-reject) | not run |  |
| `packages/ai/src/providers/baseten.models.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/providers/baseten.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/utils/abort.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/api/cloudflare-ai-binding.ts` | n/a | 0 | 0 | not run |  |
| `packages/ai/src/auth/oauth/meta.ts` | ✅ | 0 | 1 (unit:./ai:TestMetaOAuthRefreshReMintsKeyFromStoredIdentityToken) | not run | ./ai:TestMetaOAuthRefreshReMintsKeyFromStoredIdentityToken |
| `packages/ai/src/providers/meta.models.ts` | ✅ | 0 | 1 (unit:./ai:TestMetaCatalogMatchesPinnedProviderDefinition) | not run | ./ai:TestMetaCatalogMatchesPinnedProviderDefinition |
| `packages/ai/src/providers/meta.ts` | ✅ | 0 | 3 (unit:./ai:TestMetaCatalogMatchesPinnedProviderDefinition, unit:./ai:TestMetaResponsesClampsUnsupportedMaxThinking, unit:./ai:TestMetaResponsesRequestShape) | not run | ./ai:TestMetaCatalogMatchesPinnedProviderDefinition, ./ai:TestMetaResponsesClampsUnsupportedMaxThinking, ./ai:TestMetaResponsesRequestShape |
| `packages/ai/src/providers/opencode-headers.ts` | ✅ | 0 | 1 (unit:./coding:TestOpenCodeSessionHeaderMapsSessionIDWithoutCacheRetention) | not run | ./coding:TestOpenCodeSessionHeaderMapsSessionIDWithoutCacheRetention |
| `packages/ai/src/providers/qwen-token-plan-individual.models.ts` | ✅ | 0 | 1 (unit:./ai:TestQwenTokenPlanIndividualExposesExactlyDocumentedTextModels) | not run | ./ai:TestQwenTokenPlanIndividualExposesExactlyDocumentedTextModels |
| `packages/ai/src/providers/qwen-token-plan-individual.ts` | ✅ | 0 | 3 (unit:./ai:TestEnvAPIKeysQwenIndividualReusesInternationalTokenPlanVariable, unit:./ai:TestQwenTokenPlanIndividualExposesExactlyDocumentedTextModels, unit:./ai:TestQwenTokenPlanSendsQwenThinkingFields) | not run | ./ai:TestEnvAPIKeysQwenIndividualReusesInternationalTokenPlanVariable, ./ai:TestQwenTokenPlanIndividualExposesExactlyDocumentedTextModels, ./ai:TestQwenTokenPlanSendsQwenThinkingFields |
| `packages/ai/src/providers/radius.models.ts` | ✅ | 0 | 1 (unit:./ai:TestRadiusProviderShipsPublicCatalogForDefaultGateway) | not run | ./ai:TestRadiusProviderShipsPublicCatalogForDefaultGateway |
| `packages/ai/src/utils/assistant-message-frame.ts` | ✅ | 0 | 1 (unit:./ai:TestAssistantMessageFrameUsesAuthoritativeTextEndContentAndSignature) | not run | ./ai:TestAssistantMessageFrameUsesAuthoritativeTextEndContentAndSignature |
| `packages/ai/src/utils/pi-user-agent.ts` | ✅ | 0 | 3 (unit:./ai:TestPiUserAgentFormat, unit:./ai:TestProviderDefaultUserAgent, unit:./ai:TestProviderUserAgentOverridePrecedence) | not run | ./ai:TestPiUserAgentFormat, ./ai:TestProviderDefaultUserAgent, ./ai:TestProviderUserAgentOverridePrecedence |
| `packages/ai/src/utils/sleep.ts` | ✅ | 0 | 1 (unit:./ai:TestAbortableSleepRejectsWhenAlreadyAborted) | not run | ./ai:TestAbortableSleepRejectsWhenAlreadyAborted |
| `packages/ai/src/utils/transcript.ts` | ✅ | 0 | 1 (unit:./ai:TestSystemMessageReplayReplaysContentSectionsAndToolsIntoOneLeadingMessage) | not run | ./ai:TestSystemMessageReplayReplaysContentSectionsAndToolsIntoOneLeadingMessage |
| `packages/coding-agent/src/cli.ts` | ✅ | 1 (01-version-flag) | 1 (01-version-flag) | not run |  |
| `packages/coding-agent/src/main.ts` | ✅ | 18 (00-startup-banner, 01-noninteractive-undecided-denies-project-extension, 01-session-selector, 02-no-approve-denies-project-extension, 03-approve-loads-project-extension, 03-model-invalid, 03-session-continue-resume, 04-missing-session-cwd-startup-selector, 04-project-cannot-set-its-own-trust-default, 05-global-always-default-loads-project-extension, 05-list-models-extension-provider, 05-missing-session-cwd-headless-error, 06-missing-session-cwd-continue, 06-session-cwd-precedes-project-resource-loading, 07-global-session-prefix-confirm-fork, 07-resume-picker-selects-cwd-before-resources, 08-global-extension-decides-project-trust, 10-startup-resume-picker-cancel) | 17 (01-noninteractive-undecided-denies-project-extension, 01-session-selector, 02-no-approve-denies-project-extension, 03-approve-loads-project-extension, 03-model-invalid, 03-session-continue-resume, 04-missing-session-cwd-startup-selector, 04-project-cannot-set-its-own-trust-default, 05-global-always-default-loads-project-extension, 05-list-models-extension-provider, 05-missing-session-cwd-headless-error, 06-missing-session-cwd-continue, 06-session-cwd-precedes-project-resource-loading, 07-global-session-prefix-confirm-fork, 07-resume-picker-selects-cwd-before-resources, 08-global-extension-decides-project-trust, 10-startup-resume-picker-cancel) | not run |  |
| `packages/coding-agent/src/index.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/config.ts` | ✅ | 1 (02-config-resources-list) | 1 (02-config-resources-list) | not run |  |
| `packages/coding-agent/src/migrations.ts` | ✅ | 1 (06-prompt-template-migration-expand) | 1 (06-prompt-template-migration-expand) | not run |  |
| `packages/coding-agent/src/package-manager-cli.ts` | ✅ | 4 (04-package-install-local, 05-package-list-empty, 06-package-list-git-source, 09-bare-list-package-parity) | 4 (04-package-install-local, 05-package-list-empty, 06-package-list-git-source, 09-bare-list-package-parity) | not run |  |
| `packages/coding-agent/src/cli/args.ts` | ✅ | 8 (01-list-models-table, 01-print-initial-message-file-args, 02-list-models-search, 02-no-approve-denies-project-extension, 03-approve-loads-project-extension, 03-startup-resume-picker, 06-model-scope-toggle, 07-model-spec-thinking-suffix) | 8 (01-list-models-table, 01-print-initial-message-file-args, 02-list-models-search, 02-no-approve-denies-project-extension, 03-approve-loads-project-extension, 03-startup-resume-picker, 06-model-scope-toggle, 07-model-spec-thinking-suffix) | not run |  |
| `packages/coding-agent/src/cli/initial-message.ts` | ✅ | 1 (01-print-initial-message-file-args) | 1 (01-print-initial-message-file-args) | not run |  |
| `packages/coding-agent/src/cli/file-processor.ts` | ✅ | 1 (01-print-initial-message-file-args) | 1 (01-print-initial-message-file-args) | not run |  |
| `packages/coding-agent/src/cli/list-models.ts` | ✅ | 2 (01-list-models-table, 02-list-models-search) | 2 (01-list-models-table, 02-list-models-search) | not run |  |
| `packages/coding-agent/src/cli/config-selector.ts` | ✅ | 2 (01-config-empty, 02-config-resources-list) | 2 (01-config-empty, 02-config-resources-list) | not run |  |
| `packages/coding-agent/src/cli/credential-print.ts` | ✅ | 0 | 3 (unit:./cmd/pig:TestCredentialPrintPrintsBearerTokenFromAuthorizationHeader, unit:./cmd/pig:TestCredentialPrintPrintsResolvedAPIKey, unit:./cmd/pig:TestCredentialPrintRefreshesExpiredOAuthTokenBeforePrinting) | not run | ./cmd/pig:TestCredentialPrintPrintsBearerTokenFromAuthorizationHeader, ./cmd/pig:TestCredentialPrintPrintsResolvedAPIKey, ./cmd/pig:TestCredentialPrintRefreshesExpiredOAuthTokenBeforePrinting |
| `packages/coding-agent/src/cli/session-picker.ts` | ✅ | 3 (03-startup-resume-picker, 07-resume-picker-selects-cwd-before-resources, 10-startup-resume-picker-cancel) | 3 (03-startup-resume-picker, 07-resume-picker-selects-cwd-before-resources, 10-startup-resume-picker-cancel) | not run |  |
| `packages/coding-agent/src/cli/project-trust.ts` | ✅ | 1 (08-global-extension-decides-project-trust) | 1 (08-global-extension-decides-project-trust) | not run |  |
| `packages/coding-agent/src/cli/startup-ui.ts` | ✅ | 5 (03-startup-resume-picker, 04-missing-session-cwd-startup-selector, 06-missing-session-cwd-continue, 07-resume-picker-selects-cwd-before-resources, 10-startup-resume-picker-cancel) | 5 (03-startup-resume-picker, 04-missing-session-cwd-startup-selector, 06-missing-session-cwd-continue, 07-resume-picker-selects-cwd-before-resources, 10-startup-resume-picker-cancel) | not run |  |
| `packages/coding-agent/src/core/sdk.ts` | ✅ | 3 (04-slash-command, 05-session-lifecycle, 08-overlay-animation) | 3 (04-slash-command, 05-session-lifecycle, 08-overlay-animation) | not run |  |
| `packages/coding-agent/src/core/agent-session.ts` | ✅ | 28 (02-json-mode-extension-command, 02-print-extension-command, 02-session-memory-context, 04-overflow-retry, 05-nothing-to-compact-too-small, 05-session-info-after-tool-cycle, 06-overflow-retry-flushes-queued-message, 07-escape-cancels-compaction, 08-manual-compaction-flushes-entered-message, 08-print-faux-auto-retry, 08-session-info-populated-all-entries, 13-rpc-steering-queue, 14-rpc-bash-streaming, 15-rpc-compaction-result, 16-model-picker-enter-session-only, 17-at-file-literal-user-message, 17-model-picker-save-default, 17-rpc-abort-retry, 18-model-picker-save-default-scope-order, 18-rpc-session-replacement-cancel, 20-rpc-runtime-boundary-values, 21-extension-tool-and-command-info, 21-rpc-extension-session-events, 22-rpc-unknown-model-preflight, 23-rpc-multi-model-cycle-order, 24-rpc-prompt-during-compaction, 25-rpc-clear-queue, 27-rpc-extension-discovered-resources) | 28 (02-json-mode-extension-command, 02-print-extension-command, 02-session-memory-context, 04-overflow-retry, 05-nothing-to-compact-too-small, 05-session-info-after-tool-cycle, 06-overflow-retry-flushes-queued-message, 07-escape-cancels-compaction, 08-manual-compaction-flushes-entered-message, 08-print-faux-auto-retry, 08-session-info-populated-all-entries, 13-rpc-steering-queue, 14-rpc-bash-streaming, 15-rpc-compaction-result, 16-model-picker-enter-session-only, 17-at-file-literal-user-message, 17-model-picker-save-default, 17-rpc-abort-retry, 18-model-picker-save-default-scope-order, 18-rpc-session-replacement-cancel, 20-rpc-runtime-boundary-values, 21-extension-tool-and-command-info, 21-rpc-extension-session-events, 22-rpc-unknown-model-preflight, 23-rpc-multi-model-cycle-order, 24-rpc-prompt-during-compaction, 25-rpc-clear-queue, 27-rpc-extension-discovered-resources) | not run |  |
| `packages/coding-agent/src/core/agent-session-runtime.ts` | 🟡 | 5 (05-session-lifecycle, 06-missing-session-cwd-continue, 06-session-cwd-precedes-project-resource-loading, 07-resume-picker-selects-cwd-before-resources, 18-rpc-session-replacement-cancel) | 5 (05-session-lifecycle, 06-missing-session-cwd-continue, 06-session-cwd-precedes-project-resource-loading, 07-resume-picker-selects-cwd-before-resources, 18-rpc-session-replacement-cancel) | not run |  |
| `packages/coding-agent/src/core/agent-session-services.ts` | ✅ | 1 (05-session-lifecycle) | 1 (05-session-lifecycle) | not run |  |
| `packages/coding-agent/src/core/auth-storage.ts` | ✅ | 2 (02-list-models-oauth-builtins, 07-oauth-github-copilot-env) | 2 (02-list-models-oauth-builtins, 07-oauth-github-copilot-env) | not run |  |
| `packages/coding-agent/src/core/auth-guidance.ts` | ✅ | 1 (07-login-dialog) | 1 (07-login-dialog) | not run |  |
| `packages/coding-agent/src/core/bash-executor.ts` | ✅ | 1 (01-print-bash-tool-exec) | 1 (01-print-bash-tool-exec) | not run |  |
| `packages/coding-agent/src/core/cache-stats.ts` | ✅ | 0 | 1 (unit:./internal/codingagent:TestCodexCacheWasteBoundaries) | not run | ./internal/codingagent:TestCodexCacheWasteBoundaries |
| `packages/coding-agent/src/core/defaults.ts` | ✅ | 1 (02-footer-thinking-indicator) | 1 (02-footer-thinking-indicator) | not run |  |
| `packages/coding-agent/src/core/diagnostics.ts` | ✅ | 1 (02-config-resources-list) | 1 (02-config-resources-list) | not run |  |
| `packages/coding-agent/src/core/trust-manager.ts` | ✅ | 1 (01-noninteractive-undecided-denies-project-extension) | 1 (01-noninteractive-undecided-denies-project-extension) | not run |  |
| `packages/coding-agent/src/core/project-trust.ts` | ✅ | 4 (01-noninteractive-undecided-denies-project-extension, 04-project-cannot-set-its-own-trust-default, 05-global-always-default-loads-project-extension, 08-global-extension-decides-project-trust) | 4 (01-noninteractive-undecided-denies-project-extension, 04-project-cannot-set-its-own-trust-default, 05-global-always-default-loads-project-extension, 08-global-extension-decides-project-trust) | not run |  |
| `packages/coding-agent/src/core/experimental.ts` | ✅ | 1 (05-footer-experimental-indicator) | 1 (05-footer-experimental-indicator) | not run |  |
| `packages/coding-agent/src/core/event-bus.ts` | ✅ | 1 (06-eventbus-pubsub) | 1 (06-eventbus-pubsub) | not run |  |
| `packages/coding-agent/src/core/http-dispatcher.ts` | ✅ | 1 (01-print-bash-tool-exec) | 1 (01-print-bash-tool-exec) | not run |  |
| `packages/coding-agent/src/core/exec.ts` | ✅ | 1 (07-extension-exec) | 1 (07-extension-exec) | not run |  |
| `packages/coding-agent/src/core/footer-data-provider.ts` | ✅ | 2 (01-footer-default, 12-model-refresh-radius-footer) | 2 (01-footer-default, 12-model-refresh-radius-footer) | not run |  |
| `packages/coding-agent/src/core/keybindings.ts` | ✅ | 2 (09-tree-filter-user-only, 19-generic-tool-details) | 2 (09-tree-filter-user-only, 19-generic-tool-details) | not run |  |
| `packages/coding-agent/src/core/messages.ts` | ✅ | 1 (02-print-faux-tool-cycle) | 1 (02-print-faux-tool-cycle) | not run |  |
| `packages/coding-agent/src/core/model-config.ts` | ✅ | 0 | 1 (unit:./internal/codingagent:TestModelRegistryInputLimitsValidation) | not run | ./internal/codingagent:TestModelRegistryInputLimitsValidation |
| `packages/coding-agent/src/core/model-registry.ts` | ✅ | 0 | 1 (unit:./internal/codingagent:TestModelRegistry_RefreshReloadsModelsJSON) | not run | ./internal/codingagent:TestModelRegistry_RefreshReloadsModelsJSON |
| `packages/coding-agent/src/core/model-runtime.ts` | ✅ | 5 (08-scoped-models-selector, 10-login-subscription-providers, 10-model-picker-refresh-status, 12-expired-credential-is-not-refresh-failure, 13-scoped-models-fuzzy-filter) | 5 (08-scoped-models-selector, 10-login-subscription-providers, 10-model-picker-refresh-status, 12-expired-credential-is-not-refresh-failure, 13-scoped-models-fuzzy-filter) | not run |  |
| `packages/coding-agent/src/core/models-store.ts` | ✅ | 0 | 1 (unit:./ai:TestFileModelsStoreCreatesFilePreservesOrderAndRoundTrips) | not run | ./ai:TestFileModelsStoreCreatesFilePreservesOrderAndRoundTrips |
| `packages/coding-agent/src/core/provider-composer.ts` | ✅ | 1 (07-footer-extension-subscription) | 2 (07-footer-extension-subscription, unit:./internal/codingagent:TestModelRegistrySamplingParamsMergePerKey) | not run | ./internal/codingagent:TestModelRegistrySamplingParamsMergePerKey |
| `packages/coding-agent/src/core/radius.ts` | ✅ | 0 | 1 (unit:./internal/codingagent:TestGetRadiusGatewayURLHonorsOverride) | not run | ./internal/codingagent:TestGetRadiusGatewayURLHonorsOverride |
| `packages/coding-agent/src/core/remote-catalog-provider.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/core/runtime-credentials.ts` | ✅ | **0 (untested)** | **0** | not run |  |
| `packages/coding-agent/src/core/usage-totals.ts` | ✅ | 2 (06-footer-resumed-stored-cost, 08-session-info-populated-all-entries) | 2 (06-footer-resumed-stored-cost, 08-session-info-populated-all-entries) | not run |  |
| `packages/coding-agent/src/core/model-resolver.ts` | ✅ | 4 (03-model-invalid, 05-model-direct-switch, 06-model-scope-toggle, 07-model-spec-thinking-suffix) | 4 (03-model-invalid, 05-model-direct-switch, 06-model-scope-toggle, 07-model-spec-thinking-suffix) | not run |  |
| `packages/coding-agent/src/core/output-guard.ts` | ✅ | 1 (01-print-bash-tool-exec) | 1 (01-print-bash-tool-exec) | not run |  |
| `packages/coding-agent/src/core/package-manager.ts` | ✅ | 7 (01-noninteractive-undecided-denies-project-extension, 02-no-approve-denies-project-extension, 03-approve-loads-project-extension, 05-package-list-empty, 06-package-list-git-source, 23-extension-directory-entries, 26-extension-load-order) | 7 (01-noninteractive-undecided-denies-project-extension, 02-no-approve-denies-project-extension, 03-approve-loads-project-extension, 05-package-list-empty, 06-package-list-git-source, 23-extension-directory-entries, 26-extension-load-order) | not run |  |
| `packages/coding-agent/src/core/prompt-templates.ts` | 🟡 | 1 (06-prompt-template-migration-expand) | 1 (06-prompt-template-migration-expand) | not run |  |
| `packages/coding-agent/src/core/resolve-config-value.ts` | ✅ | 1 (01-list-models-env-builtins) | 1 (01-list-models-env-builtins) | not run |  |
| `packages/coding-agent/src/core/resource-loader.ts` | ✅ | 12 (01-noninteractive-undecided-denies-project-extension, 02-no-approve-denies-project-extension, 03-approve-loads-project-extension, 04-project-cannot-set-its-own-trust-default, 05-global-always-default-loads-project-extension, 06-prompt-template-migration-expand, 06-session-cwd-precedes-project-resource-loading, 07-resume-picker-selects-cwd-before-resources, 07-skill-command-system-prompt, 08-global-extension-decides-project-trust, 26-extension-load-order, 27-rpc-extension-discovered-resources) | 12 (01-noninteractive-undecided-denies-project-extension, 02-no-approve-denies-project-extension, 03-approve-loads-project-extension, 04-project-cannot-set-its-own-trust-default, 05-global-always-default-loads-project-extension, 06-prompt-template-migration-expand, 06-session-cwd-precedes-project-resource-loading, 07-resume-picker-selects-cwd-before-resources, 07-skill-command-system-prompt, 08-global-extension-decides-project-trust, 26-extension-load-order, 27-rpc-extension-discovered-resources) | not run |  |
| `packages/coding-agent/src/core/session-cwd.ts` | ✅ | 8 (01-session-selector, 03-session-continue-resume, 04-missing-session-cwd-startup-selector, 05-missing-session-cwd-headless-error, 06-missing-session-cwd-continue, 06-session-cwd-precedes-project-resource-loading, 07-global-session-prefix-confirm-fork, 07-resume-picker-selects-cwd-before-resources) | 8 (01-session-selector, 03-session-continue-resume, 04-missing-session-cwd-startup-selector, 05-missing-session-cwd-headless-error, 06-missing-session-cwd-continue, 06-session-cwd-precedes-project-resource-loading, 07-global-session-prefix-confirm-fork, 07-resume-picker-selects-cwd-before-resources) | not run |  |
| `packages/coding-agent/src/core/session-manager.ts` | ✅ | 7 (01-session-selector, 03-session-continue-resume, 07-global-session-prefix-confirm-fork, 08-generated-session-identity, 08-session-info-populated-all-entries, 16-resumed-session-identity, 21-rpc-extension-session-events) | 7 (01-session-selector, 03-session-continue-resume, 07-global-session-prefix-confirm-fork, 08-generated-session-identity, 08-session-info-populated-all-entries, 16-resumed-session-identity, 21-rpc-extension-session-events) | not run |  |
| `packages/coding-agent/src/core/settings-manager.ts` | ✅ | 6 (01-noninteractive-undecided-denies-project-extension, 02-no-approve-denies-project-extension, 02-settings-selector, 03-approve-loads-project-extension, 04-project-cannot-set-its-own-trust-default, 05-global-always-default-loads-project-extension) | 6 (01-noninteractive-undecided-denies-project-extension, 02-no-approve-denies-project-extension, 02-settings-selector, 03-approve-loads-project-extension, 04-project-cannot-set-its-own-trust-default, 05-global-always-default-loads-project-extension) | not run |  |
| `packages/coding-agent/src/core/skills.ts` | ✅ | 1 (07-skill-command-system-prompt) | 1 (07-skill-command-system-prompt) | not run |  |
| `packages/coding-agent/src/core/slash-commands.ts` | ✅ | 5 (01-session-info-empty, 02-name-set-get, 03-new-session, 04-changelog-structure, 21-extension-tool-and-command-info) | 4 (01-session-info-empty, 02-name-set-get, 03-new-session, 21-extension-tool-and-command-info) | not run |  |
| `packages/coding-agent/src/core/source-info.ts` | ✅ | 3 (02-config-resources-list, 21-extension-tool-and-command-info, 23-extension-directory-entries) | 3 (02-config-resources-list, 21-extension-tool-and-command-info, 23-extension-directory-entries) | not run |  |
| `packages/coding-agent/src/core/system-prompt.ts` | ✅ | 1 (07-skill-command-system-prompt) | 1 (07-skill-command-system-prompt) | not run |  |
| `packages/coding-agent/src/core/telemetry.ts` | ✅ | **0 (untested)** | **0** | not run |  |
| `packages/coding-agent/src/core/timings.ts` | ✅ | 1 (03-tool-execution-bash) | 1 (03-tool-execution-bash) | not run |  |
| `packages/coding-agent/src/core/index.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/core/provider-attribution.ts` | ✅ | **0 (untested)** | **0** | not run |  |
| `packages/coding-agent/src/core/compaction/compaction.ts` | ✅ | 7 (01-compact-command, 02-recompact-update-summary, 04-overflow-retry, 05-nothing-to-compact-too-small, 07-escape-cancels-compaction, 15-rpc-compaction-result, 26-rpc-session-stats-context-estimate) | 7 (01-compact-command, 02-recompact-update-summary, 04-overflow-retry, 05-nothing-to-compact-too-small, 07-escape-cancels-compaction, 15-rpc-compaction-result, 26-rpc-session-stats-context-estimate) | not run |  |
| `packages/coding-agent/src/core/compaction/branch-summarization.ts` | ✅ | 1 (03-branch-summarize-via-tree) | 1 (03-branch-summarize-via-tree) | not run |  |
| `packages/coding-agent/src/core/compaction/utils.ts` | ✅ | 3 (01-compact-command, 02-recompact-update-summary, 03-branch-summarize-via-tree) | 3 (01-compact-command, 02-recompact-update-summary, 03-branch-summarize-via-tree) | not run |  |
| `packages/coding-agent/src/core/compaction/index.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/core/extensions/types.ts` | 🟡 | 8 (01-print-extension-tool-bridge, 01-register-handshake, 03-tool-call-blocker, 16-rpc-extension-ui-events, 19-rpc-extension-ui-select, 21-extension-tool-and-command-info, 24-tool-renderers-collapsed, 25-tool-renderers-expanded) | 8 (01-print-extension-tool-bridge, 01-register-handshake, 03-tool-call-blocker, 16-rpc-extension-ui-events, 19-rpc-extension-ui-select, 21-extension-tool-and-command-info, 24-tool-renderers-collapsed, 25-tool-renderers-expanded) | not run |  |
| `packages/coding-agent/src/core/extensions/runner.ts` | ✅ | 13 (02-print-tool-result-modifier, 03-tool-call-blocker, 08-global-extension-decides-project-trust, 10-shortcut-reload-authority, 11-subprocess-tool-ui-dialogs, 12-subprocess-tool-ui-cancel, 13-subprocess-tool-ui-headless-fallback, 14-subprocess-message-renderer, 16-resumed-session-identity, 17-subprocess-widget-factory, 18-subprocess-entry-renderer, 21-rpc-extension-session-events, 24-rpc-prompt-during-compaction) | 13 (02-print-tool-result-modifier, 03-tool-call-blocker, 08-global-extension-decides-project-trust, 10-shortcut-reload-authority, 11-subprocess-tool-ui-dialogs, 12-subprocess-tool-ui-cancel, 13-subprocess-tool-ui-headless-fallback, 14-subprocess-message-renderer, 16-resumed-session-identity, 17-subprocess-widget-factory, 18-subprocess-entry-renderer, 21-rpc-extension-session-events, 24-rpc-prompt-during-compaction) | not run |  |
| `packages/coding-agent/src/core/extensions/loader.ts` | ✅ | 2 (01-register-handshake, 04-slash-command) | 2 (01-register-handshake, 04-slash-command) | not run |  |
| `packages/coding-agent/src/core/extensions/wrapper.ts` | ✅ | 1 (01-print-extension-tool-bridge) | 1 (01-print-extension-tool-bridge) | not run |  |
| `packages/coding-agent/src/core/extensions/index.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/extensions/index.ts` | ✅ | 0 | 1 (unit:./cmd/pig:TestStartBuiltInLlamaRestoresTheStoredCatalog) | not run | ./cmd/pig:TestStartBuiltInLlamaRestoresTheStoredCatalog |
| `packages/coding-agent/src/extensions/llama/client.ts` | ✅ | 0 | 1 (unit:./internal/codingagent/llama:TestNormalizeLlamaServerURL) | not run | ./internal/codingagent/llama:TestNormalizeLlamaServerURL |
| `packages/coding-agent/src/extensions/llama/huggingface.ts` | ✅ | 0 | 1 (unit:./internal/codingagent/llama:TestFindHuggingFaceTokenReadsTokenFiles) | not run | ./internal/codingagent/llama:TestFindHuggingFaceTokenReadsTokenFiles |
| `packages/coding-agent/src/extensions/llama/index.ts` | ✅ | 0 | 1 (unit:./internal/codingagent/llama:TestParseHuggingFaceModel) | not run | ./internal/codingagent/llama:TestParseHuggingFaceModel |
| `packages/coding-agent/src/extensions/llama/provider.ts` | ✅ | 0 | 1 (unit:./internal/codingagent/llama:TestSetCatalogExposesLoadedAndSleepingModelsWithRouterMetadata) | not run | ./internal/codingagent/llama:TestSetCatalogExposesLoadedAndSleepingModelsWithRouterMetadata |
| `packages/coding-agent/src/extensions/llama/ui.ts` | ✅ | 0 | 1 (unit:./internal/codingagent/llama:TestCompactCountAndContextLabels) | not run | ./internal/codingagent/llama:TestCompactCountAndContextLabels |
| `packages/coding-agent/src/core/tools/bash.ts` | ✅ | 2 (01-print-bash-tool-exec, 07-visual-truncate-bash-collapse) | 2 (01-print-bash-tool-exec, 07-visual-truncate-bash-collapse) | not run |  |
| `packages/coding-agent/src/core/tools/output-accumulator.ts` | ✅ | 1 (08-print-tool-truncation-pipeline) | 1 (08-print-tool-truncation-pipeline) | not run |  |
| `packages/coding-agent/src/core/tools/read.ts` | ✅ | 1 (02-print-read-tool-exec) | 1 (02-print-read-tool-exec) | not run |  |
| `packages/coding-agent/src/core/tools/write.ts` | ✅ | 1 (03-print-write-tool-exec) | 1 (03-print-write-tool-exec) | not run |  |
| `packages/coding-agent/src/core/tools/edit.ts` | ✅ | 1 (07-print-edit-tool-exec) | 1 (07-print-edit-tool-exec) | not run |  |
| `packages/coding-agent/src/core/tools/grep.ts` | ✅ | 1 (04-print-grep-tool-exec) | 1 (04-print-grep-tool-exec) | not run |  |
| `packages/coding-agent/src/core/tools/find.ts` | ✅ | 1 (05-print-find-tool-exec) | 1 (05-print-find-tool-exec) | not run |  |
| `packages/coding-agent/src/core/tools/ls.ts` | ✅ | 1 (06-print-ls-tool-exec) | 1 (06-print-ls-tool-exec) | not run |  |
| `packages/coding-agent/src/core/tools/edit-diff.ts` | ✅ | 2 (07-print-edit-tool-exec, 10-edit-tool-diff) | 2 (07-print-edit-tool-exec, 10-edit-tool-diff) | not run |  |
| `packages/coding-agent/src/core/tools/file-mutation-queue.ts` | ✅ | 1 (10-print-batched-file-mutation) | 1 (10-print-batched-file-mutation) | not run |  |
| `packages/coding-agent/src/core/tools/render-utils.ts` | ✅ | 1 (08-print-tool-truncation-pipeline) | 1 (08-print-tool-truncation-pipeline) | not run |  |
| `packages/coding-agent/src/core/tools/truncate.ts` | ✅ | 1 (08-print-tool-truncation-pipeline) | 1 (08-print-tool-truncation-pipeline) | not run |  |
| `packages/coding-agent/src/core/tools/tool-definition-wrapper.ts` | ✅ | 4 (11-print-extension-tool-bridge, 11-subprocess-tool-ui-dialogs, 12-subprocess-tool-ui-cancel, 13-subprocess-tool-ui-headless-fallback) | 4 (11-print-extension-tool-bridge, 11-subprocess-tool-ui-dialogs, 12-subprocess-tool-ui-cancel, 13-subprocess-tool-ui-headless-fallback) | not run |  |
| `packages/coding-agent/src/core/tools/path-utils.ts` | ✅ | 1 (09-print-path-resolution) | 1 (09-print-path-resolution) | not run |  |
| `packages/coding-agent/src/core/tools/index.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/core/export-html/ansi-to-html.ts` | ✅ | 1 (02-export-cli-ansi) | 1 (02-export-cli-ansi) | not run |  |
| `packages/coding-agent/src/core/export-html/tool-renderer.ts` | ✅ | 1 (05-export-interactive-slash) | 1 (05-export-interactive-slash) | not run |  |
| `packages/coding-agent/src/core/export-html/index.ts` | ✅ | 4 (01-export-cli-plain, 02-export-cli-ansi, 03-export-cli-tools, 04-export-cli-full-mix) | 4 (01-export-cli-plain, 02-export-cli-ansi, 03-export-cli-tools, 04-export-cli-full-mix) | not run |  |
| `packages/coding-agent/src/modes/interactive/interactive-mode.ts` | 🟡 | 58 (01-session-info-empty, 01-slash-popup, 02-name-set-get, 02-settings-selector, 02-slash-filter-model, 02-startup-compact-help, 02-tree-empty-overlay, 03-footer-multi-provider-prefix, 03-model-invalid, 03-new-session, 03-slash-accept-hotkeys, 03-startup-expanded-help, 04-model-picker-overlay, 04-slash-dismiss, 04-startup-verbose-collapse, 05-model-direct-switch, 05-path-force-tab-popup, 05-startup-loaded-resources, 05-tree-nonempty-overlay, 06-model-scope-toggle, 06-overflow-retry-flushes-queued-message, 06-path-accept-partial, 06-settings-editor-padding-live, 06-startup-loaded-resources-expanded, 07-escape-cancels-compaction, 07-path-dismiss, 08-manual-compaction-flushes-entered-message, 08-scoped-models-selector, 08-session-info-populated-all-entries, 09-model-filter-resets-selection, 10-login-subscription-providers, 10-model-picker-refresh-status, 10-shortcut-reload-authority, 11-subprocess-tool-ui-dialogs, 12-managed-tool-status, 12-model-refresh-radius-footer, 12-subprocess-tool-ui-cancel, 14-subprocess-message-renderer, 15-footer-status-reload-composition, 15-model-picker-google-and-hint, 16-model-picker-enter-session-only, 16-resumed-session-identity, 17-at-file-literal-user-message, 17-model-picker-save-default, 17-subprocess-widget-factory, 18-model-picker-save-default-scope-order, 18-subprocess-entry-renderer, 19-generic-tool-details, 20-differential-render-overflow-terminates, 21-extension-notify-style, 21-resume-thinking-blocks, 21-status-lifecycle, 22-resume-hidden-thinking, 23-resume-assistant-terminal-state, 24-resume-file-tool-previews, 25-review-thinking-tool-boundary, 26-review-thinking-markdown, 27-command-argument-completions) | 58 (01-session-info-empty, 01-slash-popup, 02-name-set-get, 02-settings-selector, 02-slash-filter-model, 02-startup-compact-help, 02-tree-empty-overlay, 03-footer-multi-provider-prefix, 03-model-invalid, 03-new-session, 03-slash-accept-hotkeys, 03-startup-expanded-help, 04-model-picker-overlay, 04-slash-dismiss, 04-startup-verbose-collapse, 05-model-direct-switch, 05-path-force-tab-popup, 05-startup-loaded-resources, 05-tree-nonempty-overlay, 06-model-scope-toggle, 06-overflow-retry-flushes-queued-message, 06-path-accept-partial, 06-settings-editor-padding-live, 06-startup-loaded-resources-expanded, 07-escape-cancels-compaction, 07-path-dismiss, 08-manual-compaction-flushes-entered-message, 08-scoped-models-selector, 08-session-info-populated-all-entries, 09-model-filter-resets-selection, 10-login-subscription-providers, 10-model-picker-refresh-status, 10-shortcut-reload-authority, 11-subprocess-tool-ui-dialogs, 12-managed-tool-status, 12-model-refresh-radius-footer, 12-subprocess-tool-ui-cancel, 14-subprocess-message-renderer, 15-footer-status-reload-composition, 15-model-picker-google-and-hint, 16-model-picker-enter-session-only, 16-resumed-session-identity, 17-at-file-literal-user-message, 17-model-picker-save-default, 17-subprocess-widget-factory, 18-model-picker-save-default-scope-order, 18-subprocess-entry-renderer, 19-generic-tool-details, 20-differential-render-overflow-terminates, 21-extension-notify-style, 21-resume-thinking-blocks, 21-status-lifecycle, 22-resume-hidden-thinking, 23-resume-assistant-terminal-state, 24-resume-file-tool-previews, 25-review-thinking-tool-boundary, 26-review-thinking-markdown, 27-command-argument-completions) | not run |  |
| `packages/coding-agent/src/modes/interactive/external-editor.ts` | ✅ | 0 | 1 (unit:./internal/codingagent:TestOpenExternalEditorStripsOneTrailingNewline) | not run | ./internal/codingagent:TestOpenExternalEditorStripsOneTrailingNewline |
| `packages/coding-agent/src/modes/interactive/components/trust-selector.ts` | ✅ | 0 | 1 (unit:./internal/codingagent:TestTrustHandler_SavesSelectedDecision) | not run | ./internal/codingagent:TestTrustHandler_SavesSelectedDecision |
| `packages/coding-agent/src/modes/interactive/components/first-time-setup.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/modes/interactive/theme/theme.ts` | ✅ | 1 (04-settings-theme-submenu) | 1 (04-settings-theme-submenu) | not run |  |
| `packages/coding-agent/src/modes/interactive/theme/theme-controller.ts` | ✅ | 0 | 1 (unit:./tui:TestResolveThemeSetting) | not run | ./tui:TestResolveThemeSetting |
| `packages/coding-agent/src/modes/interactive/model-search.ts` | ✅ | 2 (13-scoped-models-fuzzy-filter, 14-model-picker-fuzzy-filter) | 2 (13-scoped-models-fuzzy-filter, 14-model-picker-fuzzy-filter) | not run |  |
| `packages/coding-agent/src/modes/print-mode.ts` | ✅ | 4 (01-json-mode-streams-events, 01-print-mode-arithmetic, 02-json-mode-extension-command, 02-print-extension-command) | 4 (01-json-mode-streams-events, 01-print-mode-arithmetic, 02-json-mode-extension-command, 02-print-extension-command) | not run |  |
| `packages/coding-agent/src/modes/rpc/rpc-mode.ts` | ✅ | 25 (01-rpc-malformed-input, 02-rpc-unknown-command, 03-rpc-state-setters, 05-rpc-query-commands, 06-rpc-chat-faux, 07-rpc-model-thinking-commands, 08-rpc-new-session, 09-rpc-clone-session, 10-rpc-fork-session, 11-rpc-switch-session, 12-rpc-export-html, 13-rpc-steering-queue, 14-rpc-bash-streaming, 15-rpc-compaction-result, 16-rpc-extension-ui-events, 17-rpc-abort-retry, 18-rpc-session-replacement-cancel, 19-rpc-extension-ui-select, 20-rpc-runtime-boundary-values, 21-rpc-extension-session-events, 22-rpc-unknown-model-preflight, 23-rpc-multi-model-cycle-order, 24-rpc-prompt-during-compaction, 25-rpc-clear-queue, 26-rpc-session-stats-context-estimate) | 25 (01-rpc-malformed-input, 02-rpc-unknown-command, 03-rpc-state-setters, 05-rpc-query-commands, 06-rpc-chat-faux, 07-rpc-model-thinking-commands, 08-rpc-new-session, 09-rpc-clone-session, 10-rpc-fork-session, 11-rpc-switch-session, 12-rpc-export-html, 13-rpc-steering-queue, 14-rpc-bash-streaming, 15-rpc-compaction-result, 16-rpc-extension-ui-events, 17-rpc-abort-retry, 18-rpc-session-replacement-cancel, 19-rpc-extension-ui-select, 20-rpc-runtime-boundary-values, 21-rpc-extension-session-events, 22-rpc-unknown-model-preflight, 23-rpc-multi-model-cycle-order, 24-rpc-prompt-during-compaction, 25-rpc-clear-queue, 26-rpc-session-stats-context-estimate) | not run |  |
| `packages/coding-agent/src/modes/rpc/rpc-types.ts` | ✅ | 12 (02-rpc-unknown-command, 03-rpc-state-setters, 05-rpc-query-commands, 07-rpc-model-thinking-commands, 08-rpc-new-session, 09-rpc-clone-session, 10-rpc-fork-session, 11-rpc-switch-session, 12-rpc-export-html, 16-rpc-extension-ui-events, 19-rpc-extension-ui-select, 25-rpc-clear-queue) | 12 (02-rpc-unknown-command, 03-rpc-state-setters, 05-rpc-query-commands, 07-rpc-model-thinking-commands, 08-rpc-new-session, 09-rpc-clone-session, 10-rpc-fork-session, 11-rpc-switch-session, 12-rpc-export-html, 16-rpc-extension-ui-events, 19-rpc-extension-ui-select, 25-rpc-clear-queue) | not run |  |
| `packages/coding-agent/src/modes/rpc/rpc-client.ts` | ✅ | 0 | 1 (unit:./coding/rpcclient:TestRPCCommandWirePreservesOptionalValues) | not run | ./coding/rpcclient:TestRPCCommandWirePreservesOptionalValues |
| `packages/coding-agent/src/modes/rpc/jsonl.ts` | ✅ | 2 (01-rpc-malformed-input, 04-rpc-jsonl-framing) | 2 (01-rpc-malformed-input, 04-rpc-jsonl-framing) | not run |  |
| `packages/coding-agent/src/modes/index.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/utils/changelog.ts` | ✅ | 1 (05-changelog-ignores-collapse-setting) | 1 (05-changelog-ignores-collapse-setting) | not run |  |
| `packages/coding-agent/src/utils/ansi.ts` | ✅ | 1 (02-export-cli-ansi) | 1 (02-export-cli-ansi) | not run |  |
| `packages/coding-agent/src/utils/child-process.ts` | ✅ | **0 (untested)** | **0** | not run |  |
| `packages/coding-agent/src/utils/clipboard.ts` | 🟡 | 1 (01-clipboard-read-deferred) | 0 | not run |  |
| `packages/coding-agent/src/utils/clipboard-image.ts` | 🟡 | 1 (01-clipboard-read-deferred) | 0 | not run |  |
| `packages/coding-agent/src/utils/exif-orientation.ts` | ✅ | 1 (04-image-processing-file-args) | 1 (04-image-processing-file-args) | not run |  |
| `packages/coding-agent/src/utils/frontmatter.ts` | ✅ | 1 (07-skill-command-system-prompt) | 1 (07-skill-command-system-prompt) | not run |  |
| `packages/coding-agent/src/utils/fs-watch.ts` | ✅ | 1 (01-footer-default) | 1 (01-footer-default) | not run |  |
| `packages/coding-agent/src/utils/git.ts` | ✅ | 1 (06-package-list-git-source) | 1 (06-package-list-git-source) | not run |  |
| `packages/coding-agent/src/utils/html.ts` | ✅ | 1 (02-export-cli-ansi) | 1 (02-export-cli-ansi) | not run |  |
| `packages/coding-agent/src/utils/image-convert.ts` | ✅ | 1 (02-image-convert-deferred) | 3 (unit:./internal/codingagent:TestConvertToPngReturnsNilWhenConversionFails, unit:./internal/codingagent:TestConvertToPngReturnsOriginalDataForPNG, unit:./internal/codingagent:TestImageConvertDecodedPixels) | not run | ./internal/codingagent:TestConvertToPngReturnsNilWhenConversionFails, ./internal/codingagent:TestConvertToPngReturnsOriginalDataForPNG, ./internal/codingagent:TestImageConvertDecodedPixels |
| `packages/coding-agent/src/utils/image-resize.ts` | ✅ | 2 (04-image-processing-file-args, 06-bmp-file-args) | 2 (04-image-processing-file-args, 06-bmp-file-args) | not run |  |
| `packages/coding-agent/src/utils/mime.ts` | ✅ | 1 (08-mime-detection-image-file-args) | 1 (08-mime-detection-image-file-args) | not run |  |
| `packages/coding-agent/src/utils/paths.ts` | ✅ | 1 (04-package-install-local) | 1 (04-package-install-local) | not run |  |
| `packages/coding-agent/src/utils/pi-user-agent.ts` | ✅ | 0 | 1 (unit:./internal/codingagent:TestBugReportEnvironmentUsesPiGUserAgent) | not run | ./internal/codingagent:TestBugReportEnvironmentUsesPiGUserAgent |
| `packages/coding-agent/src/utils/photon.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/utils/shell.ts` | ✅ | 1 (01-print-bash-tool-exec) | 1 (01-print-bash-tool-exec) | not run |  |
| `packages/coding-agent/src/utils/syntax-highlight.ts` | ✅ | 1 (01-text-wrap-long-response) | 1 (01-text-wrap-long-response) | not run |  |
| `packages/coding-agent/src/utils/sleep.ts` | ✅ | 0 | 1 (unit:./ai:TestRetryAssistantCallAbortsBackoffSleepViaContext) | not run | ./ai:TestRetryAssistantCallAbortsBackoffSleepViaContext |
| `packages/coding-agent/src/utils/tools-manager.ts` | 🟡 | 2 (05-print-find-tool-exec, 12-managed-tool-status) | 2 (05-print-find-tool-exec, 12-managed-tool-status) | not run |  |
| `packages/coding-agent/src/utils/version-check.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/utils/deprecation.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/utils/image-resize-core.ts` | ✅ | 1 (04-image-processing-file-args) | 1 (04-image-processing-file-args) | not run |  |
| `packages/coding-agent/src/utils/image-resize-worker.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/utils/json.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/utils/open-browser.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/utils/windows-self-update.ts` | ✅ | 0 | 1 (unit:./internal/codingagent:TestQuarantineNativeDependenciesMovesLoadedImagesAndCopiesThemBack) | not run | ./internal/codingagent:TestQuarantineNativeDependenciesMovesLoadedImagesAndCopiesThemBack |
| `packages/coding-agent/src/bun/cli.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/bun/restore-sandbox-env.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/modes/interactive/components/assistant-message.ts` | ✅ | 10 (01-assistant-message-text, 04-conversation-layout, 05-multi-turn-spacing, 18-latex-math-rendering, 19-mermaid-diagram-rendering, 21-resume-thinking-blocks, 22-resume-hidden-thinking, 23-resume-assistant-terminal-state, 25-review-thinking-tool-boundary, 26-review-thinking-markdown) | 10 (01-assistant-message-text, 04-conversation-layout, 05-multi-turn-spacing, 18-latex-math-rendering, 19-mermaid-diagram-rendering, 21-resume-thinking-blocks, 22-resume-hidden-thinking, 23-resume-assistant-terminal-state, 25-review-thinking-tool-boundary, 26-review-thinking-markdown) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/bash-execution.ts` | ✅ | 2 (06-bash-execution-success, 20-bash-execution-long-line-wrap) | 2 (06-bash-execution-success, 20-bash-execution-long-line-wrap) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/bordered-loader.ts` | ✅ | 2 (10-loader-countdown-bordered-behavior, 15-bordered-loader) | 1 (10-loader-countdown-bordered-behavior) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/branch-summary-message.ts` | ✅ | 1 (13-branch-summary-message) | 1 (13-branch-summary-message) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/compaction-summary-message.ts` | ✅ | 1 (14-compaction-summary-message) | 1 (14-compaction-summary-message) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/config-selector.ts` | ✅ | 3 (01-config-empty, 02-config-resources-list, 03-config-filter-narrow) | 3 (01-config-empty, 02-config-resources-list, 03-config-filter-narrow) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/countdown-timer.ts` | ✅ | 2 (09-countdown-timer, 10-loader-countdown-bordered-behavior) | 1 (10-loader-countdown-bordered-behavior) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/custom-editor.ts` | ✅ | 1 (custom-editor-precedence) | 1 (custom-editor-precedence) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/custom-entry.ts` | ✅ | 1 (18-subprocess-entry-renderer) | 1 (18-subprocess-entry-renderer) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/custom-message.ts` | ✅ | 4 (12-custom-message, 20-message-output-padding, 22-custom-message-markdown-wrap, 22-exit-final-layout) | 4 (12-custom-message, 20-message-output-padding, 22-custom-message-markdown-wrap, 22-exit-final-layout) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/diff.ts` | ✅ | 1 (10-edit-tool-diff) | 1 (10-edit-tool-diff) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/dynamic-border.ts` | ✅ | 1 (08-dynamic-border-width80) | 1 (08-dynamic-border-width80) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/extension-editor.ts` | ✅ | 1 (10-extension-editor) | 1 (10-extension-editor) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/extension-input.ts` | ✅ | 1 (11-extension-input) | 1 (11-extension-input) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/extension-selector.ts` | ✅ | 1 (09-extension-selector-summarize-branch) | 1 (09-extension-selector-summarize-branch) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/footer.ts` | ✅ | 8 (01-footer-default, 02-footer-thinking-indicator, 03-footer-multi-provider-prefix, 04-footer-nonzero-context, 05-footer-experimental-indicator, 06-footer-resumed-stored-cost, 07-footer-extension-subscription, 15-footer-status-reload-composition) | 8 (01-footer-default, 02-footer-thinking-indicator, 03-footer-multi-provider-prefix, 04-footer-nonzero-context, 05-footer-experimental-indicator, 06-footer-resumed-stored-cost, 07-footer-extension-subscription, 15-footer-status-reload-composition) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/index.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/modes/interactive/components/keybinding-hints.ts` | ✅ | 1 (07-visual-truncate-bash-collapse) | 1 (07-visual-truncate-bash-collapse) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/login-dialog.ts` | ✅ | 1 (07-login-dialog) | 1 (07-login-dialog) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/model-selector.ts` | ✅ | 13 (03-model-invalid, 04-model-picker-overlay, 05-model-direct-switch, 06-model-scope-toggle, 09-model-filter-resets-selection, 10-model-picker-refresh-status, 11-model-picker-arrow-wrap, 12-expired-credential-is-not-refresh-failure, 14-model-picker-fuzzy-filter, 15-model-picker-google-and-hint, 16-model-picker-enter-session-only, 17-model-picker-save-default, 18-model-picker-save-default-scope-order) | 13 (03-model-invalid, 04-model-picker-overlay, 05-model-direct-switch, 06-model-scope-toggle, 09-model-filter-resets-selection, 10-model-picker-refresh-status, 11-model-picker-arrow-wrap, 12-expired-credential-is-not-refresh-failure, 14-model-picker-fuzzy-filter, 15-model-picker-google-and-hint, 16-model-picker-enter-session-only, 17-model-picker-save-default, 18-model-picker-save-default-scope-order) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/oauth-selector.ts` | ✅ | 2 (06-oauth-selector, 10-login-subscription-providers) | 2 (06-oauth-selector, 10-login-subscription-providers) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/scoped-models-selector.ts` | ✅ | 2 (08-scoped-models-selector, 13-scoped-models-fuzzy-filter) | 2 (08-scoped-models-selector, 13-scoped-models-fuzzy-filter) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/session-selector.ts` | ✅ | 1 (01-session-selector) | 1 (01-session-selector) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/session-selector-search.ts` | ✅ | 1 (01-session-selector) | 1 (01-session-selector) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/settings-selector.ts` | ✅ | 7 (02-settings-selector, 03-settings-filter-theme, 04-settings-theme-submenu, 05-kitty-settings-visible, 05-settings-thinking-submenu, 06-settings-editor-padding-live, 07-settings-model-thinking-model-step) | 7 (02-settings-selector, 03-settings-filter-theme, 04-settings-theme-submenu, 05-kitty-settings-visible, 05-settings-thinking-submenu, 06-settings-editor-padding-live, 07-settings-model-thinking-model-step) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/show-images-selector.ts` | 🟡 | 1 (08-show-images-selector) | 0 | not run |  |
| `packages/coding-agent/src/modes/interactive/components/skill-invocation-message.ts` | ✅ | 1 (11-skill-invocation-message) | 1 (11-skill-invocation-message) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/theme-selector.ts` | ✅ | 1 (04-settings-theme-submenu) | 1 (04-settings-theme-submenu) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/thinking-selector.ts` | ✅ | **0 (untested)** | **0** | not run |  |
| `packages/coding-agent/src/modes/interactive/components/tool-execution.ts` | ✅ | 5 (03-tool-execution-bash, 04-conversation-layout, 19-generic-tool-details, 24-tool-renderers-collapsed, 25-tool-renderers-expanded) | 5 (03-tool-execution-bash, 04-conversation-layout, 19-generic-tool-details, 24-tool-renderers-collapsed, 25-tool-renderers-expanded) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/tree-selector.ts` | ✅ | 5 (02-tree-empty-overlay, 05-tree-nonempty-overlay, 07-tree-fold-root, 09-tree-filter-user-only, 10-tree-scroll-cursor-visible) | 4 (02-tree-empty-overlay, 05-tree-nonempty-overlay, 07-tree-fold-root, 09-tree-filter-user-only) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/user-message.ts` | ✅ | 3 (02-user-message-box, 04-conversation-layout, 05-multi-turn-spacing) | 3 (02-user-message-box, 04-conversation-layout, 05-multi-turn-spacing) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/user-message-selector.ts` | ✅ | 1 (16-user-message-selector) | 1 (16-user-message-selector) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/visual-truncate.ts` | ✅ | 2 (07-visual-truncate-bash-collapse, 20-bash-execution-long-line-wrap) | 2 (07-visual-truncate-bash-collapse, 20-bash-execution-long-line-wrap) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/armin.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/modes/interactive/components/daxnuts.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/modes/interactive/components/earendil-announcement.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/cli/experimental/cli.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/cli/experimental/command-options.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/cli/experimental/command.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/cli/experimental/commands/client.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/cli/experimental/commands/server.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/client/index.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/core/pi-manifest.ts` | ✅ | 0 | 1 (unit:./coding/packagecontent:TestReadPiManifest) | not run | ./coding/packagecontent:TestReadPiManifest |
| `packages/coding-agent/src/modes/interactive/components/markdown-transform.ts` | ✅ | 1 (19-mermaid-diagram-rendering) | 1 (19-mermaid-diagram-rendering) | not run |  |
| `packages/coding-agent/src/modes/interactive/components/mermaid.ts` | ✅ | 1 (19-mermaid-diagram-rendering) | 1 (19-mermaid-diagram-rendering) | not run |  |
| `packages/coding-agent/src/modes/json-event.ts` | ✅ | 2 (01-json-mode-streams-events, 06-rpc-chat-faux) | 2 (01-json-mode-streams-events, 06-rpc-chat-faux) | not run |  |
| `packages/coding-agent/src/utils/abort.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/utils/management-http.ts` | ✅ | 0 | 1 (unit:./internal/managementhttp:TestFetchWithRetryRetriesTransientTransportFailure) | not run | ./internal/managementhttp:TestFetchWithRetryRetriesTransientTransportFailure |
| `packages/coding-agent/src/utils/tool-result-images.ts` | ✅ | 0 | 1 (unit:./internal/imageprocessing:TestNormalizeToolResultImagesUsesProfileAndRetainsFailures) | not run | ./internal/imageprocessing:TestNormalizeToolResultImagesUsesProfileAndRetainsFailures |
| `packages/coding-agent/src/bun/runtime-setup.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/bun/sandbox-env-setup.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/cli/auth-check.ts` | ✅ | 0 | 5 (unit:./cmd/pig:TestAuthCheckDoesNotCreateAuthFileOrParentDirectory, unit:./cmd/pig:TestAuthCheckReadsCredentialsWithoutRefreshingOAuthWhenRequested, unit:./cmd/pig:TestAuthCheckReportsConfiguredProviderAsReady, unit:./cmd/pig:TestAuthCheckReportsMalformedAuthStateAsInvalid, unit:./cmd/pig:TestAuthCheckReportsUnknownProviderAsNotReady) | not run | ./cmd/pig:TestAuthCheckDoesNotCreateAuthFileOrParentDirectory, ./cmd/pig:TestAuthCheckReadsCredentialsWithoutRefreshingOAuthWhenRequested, ./cmd/pig:TestAuthCheckReportsConfiguredProviderAsReady, ./cmd/pig:TestAuthCheckReportsMalformedAuthStateAsInvalid, ./cmd/pig:TestAuthCheckReportsUnknownProviderAsNotReady |
| `packages/coding-agent/src/cli/auth-command.ts` | ✅ | 0 | 3 (unit:./cmd/pig:TestAuthCommandExitCodesAndErrors, unit:./cmd/pig:TestAuthCommandPrintsSecretsOnlyInTheRequestedForm, unit:./cmd/pig:TestParseAuthCommandLargeMinimumExpiry) | not run | ./cmd/pig:TestAuthCommandExitCodesAndErrors, ./cmd/pig:TestAuthCommandPrintsSecretsOnlyInTheRequestedForm, ./cmd/pig:TestParseAuthCommandLargeMinimumExpiry |
| `packages/coding-agent/src/cli/setup.ts` | ✅ | 0 | 1 (unit:./cmd/pig:TestSetupCliSetsInheritedProcessMarkers) | not run | ./cmd/pig:TestSetupCliSetsInheritedProcessMarkers |
| `packages/coding-agent/src/core/bug-report-upload.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/core/bug-report.ts` | ✅ | 0 | 1 (unit:./internal/codingagent:TestRedactBugReportJSONKeepsTokenCounts) | not run | ./internal/codingagent:TestRedactBugReportJSONKeepsTokenCounts |
| `packages/coding-agent/src/core/cache-warmer.ts` | ✅ | 2 (05-session-info-after-tool-cycle, 08-session-info-populated-all-entries) | 2 (05-session-info-after-tool-cycle, 08-session-info-populated-all-entries) | not run |  |
| `packages/coding-agent/src/core/crash-log.ts` | 🟡 | 0 | 0 | not run |  |
| `packages/coding-agent/src/core/extensions/jiti-loader.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/core/extensions/jiti-static-loader.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/core/extensions/virtual-modules.ts` | 🟡 | 0 | 0 | not run |  |
| `packages/coding-agent/src/core/session-export.ts` | ✅ | 0 | 1 (unit:./internal/codingagent:TestSerializeSessionBranchChainsTheCurrentBranch) | not run | ./internal/codingagent:TestSerializeSessionBranchChainsTheCurrentBranch |
| `packages/coding-agent/src/core/settings-diagnostics.ts` | ✅ | 0 | 1 (unit:./internal/codingagent:TestDeduplicateDiagnosticsByTypeAndMessage) | not run | ./internal/codingagent:TestDeduplicateDiagnosticsByTypeAndMessage |
| `packages/coding-agent/src/core/tools/powershell.ts` | ✅ | **0 (untested)** | **0** | not run |  |
| `packages/coding-agent/src/core/tools/renderers/bash.ts` | ✅ | 0 | 1 (unit:./internal/codingagent:TestShellBodyRendererCollapsedPreview) | not run | ./internal/codingagent:TestShellBodyRendererCollapsedPreview |
| `packages/coding-agent/src/core/tools/renderers/edit.ts` | 🟡 | 0 | 0 | not run |  |
| `packages/coding-agent/src/core/tools/renderers/find.ts` | ✅ | 0 | 1 (unit:./internal/codingagent:TestFindAndLsBodyRenderers) | not run | ./internal/codingagent:TestFindAndLsBodyRenderers |
| `packages/coding-agent/src/core/tools/renderers/grep.ts` | ✅ | 0 | 1 (unit:./internal/codingagent:TestGrepBodyRenderer) | not run | ./internal/codingagent:TestGrepBodyRenderer |
| `packages/coding-agent/src/core/tools/renderers/index.ts` | ✅ | 0 | 1 (unit:./tui:TestBuiltinToolHeaderUsesShellPrompts) | not run | ./tui:TestBuiltinToolHeaderUsesShellPrompts |
| `packages/coding-agent/src/core/tools/renderers/ls.ts` | ✅ | 0 | 1 (unit:./internal/codingagent:TestFindAndLsBodyRenderers) | not run | ./internal/codingagent:TestFindAndLsBodyRenderers |
| `packages/coding-agent/src/core/tools/renderers/read.ts` | 🟡 | 0 | 0 | not run |  |
| `packages/coding-agent/src/core/tools/renderers/write.ts` | 🟡 | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/cli.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/client-runtime.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/client-tui-chat.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/client-tui.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/client.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/commands.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/coordinator-entry.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/coordinator.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/micro/api.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/micro/main.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/micro/models.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/micro/runtime.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/micro/sessions.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/micro/tools.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/micro/tui.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/mini/main.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/mini/server/entry.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/mini/server/run.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/mini/shared/protocol.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/mini/shared/rpc.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/mini/shared/transport.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/mini/tui/run.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/mini/tui/session.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/mini/tui/view.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/mini/worker/entry.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/mini/worker/lane-service.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/mini/worker/models-service.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/mini/worker/run.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/plugin.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/plugins/bundled.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/plugins/package.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/process.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/radius-auth.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/radius-relay.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/server.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/services/agent-controller-provider.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/services/agent-controller.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/services/connection.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/services/models-provider.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/services/models.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/services/plugins.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/services/presentation-ui.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/services/server.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/services/sessions.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/services/slash-commands-provider.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/services/slash-commands.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/services/transcript-provider.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/services/transcript.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/services/worker.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/session-worker-manager.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/session-worker.ts` | ⬜ | 0 | 0 | not run |  |
| `packages/coding-agent/src/experimental/source-resolver.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/modes/interactive/bug-report.ts` | ✅ | 0 | 1 (unit:./internal/codingagent:TestBugPromptPreservesDescriptionLinesAndCancels) | not run | ./internal/codingagent:TestBugPromptPreservesDescriptionLinesAndCancels |
| `packages/coding-agent/src/modes/interactive/chat-viewport.ts` | ✅ | 0 | 1 (unit:./internal/codingagent:TestCreateChatViewportPinsDockUnderTranscript) | not run | ./internal/codingagent:TestCreateChatViewportPinsDockUnderTranscript |
| `packages/coding-agent/src/modes/interactive/components/settings-submenu.ts` | ⬜ | 3 (05-settings-thinking-submenu, 07-settings-model-thinking-model-step, 08-settings-model-thinking-empty-search) | 3 (05-settings-thinking-submenu, 07-settings-model-thinking-model-step, 08-settings-model-thinking-empty-search) | not run |  |
| `packages/coding-agent/src/modes/interactive/model-catalog-refresh.ts` | ⬜ | 1 (12-model-refresh-radius-footer) | 1 (12-model-refresh-radius-footer) | not run |  |
| `packages/coding-agent/src/modes/interactive/session-share.ts` | ✅ | **0 (untested)** | **0** | not run |  |
| `packages/coding-agent/src/modes/interactive/theme/theme-json.ts` | ✅ | 0 | 1 (unit:./tui:TestLoadThemeFileRejectsSlashInName) | not run | ./tui:TestLoadThemeFileRejectsSlashInName |
| `packages/coding-agent/src/modes/interactive/tui-renderer.ts` | ✅ | 0 | 1 (unit:./internal/codingagent:TestCreateInteractiveTuiRoutesOsc8ClickToInjectedOpener) | not run | ./internal/codingagent:TestCreateInteractiveTuiRoutesOsc8ClickToInjectedOpener |
| `packages/coding-agent/src/utils/clipboard-command.ts` | ✅ | **0 (untested)** | **0** | not run |  |
| `packages/coding-agent/src/utils/highlight-js.d.ts` | n/a | 0 | 0 | not run |  |
| `packages/coding-agent/src/utils/text.ts` | ✅ | 0 | 1 (unit:./internal/text:TestSplitBom) | not run | ./internal/text:TestSplitBom |
| `packages/coding-agent/src/utils/wsl.ts` | ✅ | 0 | 1 (unit:./internal/codingagent:TestIsWSL) | not run | ./internal/codingagent:TestIsWSL |
| `packages/coding-agent/src/utils/zip.ts` | n/a | 0 | 0 | not run |  |
| `packages/tui/src/tui.ts` | ✅ | 1 (02-settings-selector) | 1 (02-settings-selector) | not run |  |
| `packages/tui/src/editor-component.ts` | ✅ | 1 (02-slash-filter-model) | 1 (02-slash-filter-model) | not run |  |
| `packages/tui/src/autocomplete.ts` | ✅ | 8 (01-slash-popup, 02-slash-filter-model, 03-slash-accept-hotkeys, 04-slash-dismiss, 05-path-force-tab-popup, 06-path-accept-partial, 07-path-dismiss, 27-command-argument-completions) | 8 (01-slash-popup, 02-slash-filter-model, 03-slash-accept-hotkeys, 04-slash-dismiss, 05-path-force-tab-popup, 06-path-accept-partial, 07-path-dismiss, 27-command-argument-completions) | not run |  |
| `packages/tui/src/fuzzy.ts` | ✅ | 1 (03-settings-filter-theme) | 1 (03-settings-filter-theme) | not run |  |
| `packages/tui/src/keybindings.ts` | ✅ | 2 (04-settings-theme-submenu, 11-select-list-input-state) | 2 (04-settings-theme-submenu, 11-select-list-input-state) | not run |  |
| `packages/tui/src/keys.ts` | ✅ | 1 (09-tree-filter-user-only) | 1 (09-tree-filter-user-only) | not run |  |
| `packages/tui/src/kill-ring.ts` | ✅ | 1 (07-kill-ring-kill-yank) | 1 (07-kill-ring-kill-yank) | not run |  |
| `packages/tui/src/stdin-buffer.ts` | ✅ | 1 (09-tree-filter-user-only) | 1 (09-tree-filter-user-only) | not run |  |
| `packages/tui/src/terminal.ts` | ✅ | 1 (02-settings-selector) | 1 (02-settings-selector) | not run |  |
| `packages/tui/src/terminal-colors.ts` | ✅ | 0 | 1 (unit:./tui:TestParseOsc11BackgroundColor) | not run | ./tui:TestParseOsc11BackgroundColor |
| `packages/tui/src/terminal-image.ts` | ✅ | 1 (03-image-rendering-deferred) | 1 (03-image-rendering-deferred) | not run |  |
| `packages/tui/src/undo-stack.ts` | ✅ | 1 (08-undo-stack-kill-restore) | 1 (08-undo-stack-kill-restore) | not run |  |
| `packages/tui/src/native-modifiers.ts` | ✅ | **0 (untested)** | **0** | not run |  |
| `packages/tui/src/word-navigation.ts` | ✅ | 0 | 1 (unit:./tui:TestEditor_WordBoundaryHelpers) | not run | ./tui:TestEditor_WordBoundaryHelpers |
| `packages/tui/src/utils.ts` | ✅ | 6 (01-text-wrap-long-response, 02-box-tool-result-paint, 05-markdown-text-fits, 06-widthx-cjk-double-width, 13-scoped-models-fuzzy-filter, 14-model-picker-fuzzy-filter) | 6 (01-text-wrap-long-response, 02-box-tool-result-paint, 05-markdown-text-fits, 06-widthx-cjk-double-width, 13-scoped-models-fuzzy-filter, 14-model-picker-fuzzy-filter) | not run |  |
| `packages/tui/src/index.ts` | n/a | 0 | 0 | not run |  |
| `packages/tui/src/components/box.ts` | ✅ | 1 (02-box-tool-result-paint) | 1 (02-box-tool-result-paint) | not run |  |
| `packages/tui/src/components/cancellable-loader.ts` | ✅ | 1 (09-cancellable-loader) | 1 (09-cancellable-loader) | not run |  |
| `packages/tui/src/components/editor.ts` | ✅ | 4 (02-slash-filter-model, 06-settings-editor-padding-live, 12-editor-wrapped-history-navigation, 27-command-argument-completions) | 4 (02-slash-filter-model, 06-settings-editor-padding-live, 12-editor-wrapped-history-navigation, 27-command-argument-completions) | not run |  |
| `packages/tui/src/components/image.ts` | ✅ | 1 (03-image-rendering-deferred) | 1 (03-image-rendering-deferred) | not run |  |
| `packages/tui/src/components/input.ts` | ✅ | 8 (01-slash-popup, 02-slash-filter-model, 03-slash-accept-hotkeys, 04-slash-dismiss, 05-path-force-tab-popup, 06-path-accept-partial, 07-path-dismiss, 14-model-picker-fuzzy-filter) | 8 (01-slash-popup, 02-slash-filter-model, 03-slash-accept-hotkeys, 04-slash-dismiss, 05-path-force-tab-popup, 06-path-accept-partial, 07-path-dismiss, 14-model-picker-fuzzy-filter) | not run |  |
| `packages/tui/src/components/loader.ts` | ✅ | 1 (10-loader-countdown-bordered-behavior) | 1 (10-loader-countdown-bordered-behavior) | not run |  |
| `packages/tui/src/components/markdown.ts` | ✅ | 4 (01-assistant-message-text, 05-markdown-text-fits, 18-latex-math-rendering, 26-review-thinking-markdown) | 4 (01-assistant-message-text, 05-markdown-text-fits, 18-latex-math-rendering, 26-review-thinking-markdown) | not run |  |
| `packages/tui/src/components/select-list.ts` | ✅ | 3 (04-settings-theme-submenu, 08-settings-model-thinking-empty-search, 11-select-list-input-state) | 3 (04-settings-theme-submenu, 08-settings-model-thinking-empty-search, 11-select-list-input-state) | not run |  |
| `packages/tui/src/components/settings-list.ts` | ✅ | 2 (02-settings-selector, 03-settings-filter-theme) | 2 (02-settings-selector, 03-settings-filter-theme) | not run |  |
| `packages/tui/src/components/spacer.ts` | ✅ | 1 (04-spacer-message-separation) | 1 (04-spacer-message-separation) | not run |  |
| `packages/tui/src/components/text.ts` | ✅ | 2 (01-text-wrap-long-response, 20-bash-execution-long-line-wrap) | 2 (01-text-wrap-long-response, 20-bash-execution-long-line-wrap) | not run |  |
| `packages/tui/src/components/truncated-text.ts` | ✅ | 0 | 1 (unit:./tui:TestTruncatedText_PreservesANSI) | not run | ./tui:TestTruncatedText_PreservesANSI |
| `packages/tui/src/components/alt-screen-flash.ts` | ✅ | 0 | 3 (unit:./tui:TestFlashExactRender, unit:./tui:TestFlashNonPositiveExpiryRequestsRender, unit:./tui:TestFlashOrderedExpiryAndDispose) | not run | ./tui:TestFlashExactRender, ./tui:TestFlashNonPositiveExpiryRequestsRender, ./tui:TestFlashOrderedExpiryAndDispose |
| `packages/tui/src/components/h-stack.ts` | ✅ | 0 | 1 (unit:./tui:TestHStackRenderAlignEndOffsetsShorterChild) | not run | ./tui:TestHStackRenderAlignEndOffsetsShorterChild |
| `packages/tui/src/components/scroll-view.ts` | ✅ | 2 (01-conversation-fullscreen, 02-keyboard-navigation-fullscreen) | 2 (01-conversation-fullscreen, 02-keyboard-navigation-fullscreen) | not run |  |
| `packages/tui/src/components/stack.ts` | ✅ | 1 (01-conversation-fullscreen) | 1 (01-conversation-fullscreen) | not run |  |
| `packages/tui/src/components/v-stack.ts` | ✅ | 1 (01-conversation-fullscreen) | 1 (01-conversation-fullscreen) | not run |  |
| `packages/tui/src/latex.ts` | ✅ | 1 (18-latex-math-rendering) | 1 (18-latex-math-rendering) | not run |  |
| `packages/tui/src/layout-node.ts` | ✅ | 1 (01-conversation-fullscreen) | 1 (01-conversation-fullscreen) | not run |  |
| `packages/tui/src/layout.ts` | ✅ | 1 (01-conversation-fullscreen) | 1 (01-conversation-fullscreen) | not run |  |
| `packages/tui/src/tui-alt-screen.ts` | ✅ | 2 (01-conversation-fullscreen, 02-keyboard-navigation-fullscreen) | 2 (01-conversation-fullscreen, 02-keyboard-navigation-fullscreen) | not run |  |
| `packages/tui/src/tui-main-screen.ts` | ✅ | 11 (03-main-screen-assistant-settled, 04-main-screen-assistant-streaming, 05-main-screen-tool-streaming, 05-multi-turn-spacing, 06-main-screen-tool-settled, 07-main-screen-editor-growth, 08-main-screen-editor-shrink, 09-main-screen-compaction-in-progress, 10-main-screen-compaction-settled, 20-differential-render-overflow-terminates, 22-exit-final-layout) | 11 (03-main-screen-assistant-settled, 04-main-screen-assistant-streaming, 05-main-screen-tool-streaming, 05-multi-turn-spacing, 06-main-screen-tool-settled, 07-main-screen-editor-growth, 08-main-screen-editor-shrink, 09-main-screen-compaction-in-progress, 10-main-screen-compaction-settled, 20-differential-render-overflow-terminates, 22-exit-final-layout) | not run |  |
| `packages/tui/src/alt-screen-search.ts` | ✅ | 0 | 1 (unit:./tui:TestAltScreenSearchNormalizesAcrossRows) | not run | ./tui:TestAltScreenSearchNormalizesAcrossRows |
| `packages/tui/src/components/mouse-region.ts` | ✅ | 0 | 1 (unit:./tui:TestMouseRegionPrefersNestedHandlers) | not run | ./tui:TestMouseRegionPrefersNestedHandlers |
| `packages/tui/src/native-module-path.ts` | n/a | 0 | 0 | not run |  |
| `packages/tui/src/native-platform.ts` | n/a | 0 | 0 | not run |  |
