# fix-106-win-publish progress

Public issue #106: intermittent Windows `publish cell entry: rename ...: Access is denied` stops Node extensions loading.

Pi has no cell cache, so there is no upstream behavior to port. The reference practice is npm's graceful-fs, which retries EPERM/EACCES/EBUSY renames on Windows with backoff.

## Red (stub commit)

`go test ./coding/extension/host/runtimecell` on Linux, stub `renameRetry.publish` (one rename, no retry):

- FAIL TestRenameRetryRetriesTransientErrorsUntilRenameSucceeds
- FAIL TestRenameRetryStopsAtTheBudgetAndReportsTheLastError
- FAIL TestRenameRetryAdoptsAPeerEntryPublishedWhileWaiting
- FAIL TestRenameRetryCancellationDuringWaitKeepsBothCauses
- FAIL TestWaitRenameRetryHonoursCancellationPromptly
- FAIL TestPublishArtifactRetriesATransientPublishRename
- FAIL TestPublishArtifactReportsAPersistentPublishRenameFailureUnchanged
- FAIL TestPublishArtifactAdoptsAPeerEntryWhenRenameKeepsFailing
- FAIL TestPublishArtifactWithFailureCacheRetriesATransientFailurePublish

The Windows-only tests (`rename_windows_test.go`, `node_runtime_windows_test.go`) compile under `GOOS=windows go vet` and run only on Windows.
