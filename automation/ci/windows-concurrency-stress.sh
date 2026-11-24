#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Nightly Windows concurrency stress suite: runs high-repetition (-count=N, default 20)
# tests over Windows-sensitive concurrency, file-locking, atomic-write, pipe-lifecycle,
# and process-tree cleanup surfaces that ordinary single-run PR shards can miss.
#
# Usage:
#   automation/ci/windows-concurrency-stress.sh [--dry-run]
#
# Environment:
#   PIG_WIN_STRESS_COUNT    repetitions per test (default: 20)
#   PIG_WIN_STRESS_TIMEOUT  per-package go test timeout (default: 15m)
set -euo pipefail

DRY_RUN=0
if [[ "${1:-}" == "--dry-run" ]]; then
  DRY_RUN=1
  shift
fi
if [[ $# -ne 0 ]]; then
  echo "usage: $0 [--dry-run]" >&2
  exit 2
fi

COUNT="${PIG_WIN_STRESS_COUNT:-20}"
TIMEOUT="${PIG_WIN_STRESS_TIMEOUT:-15m}"
if ! [[ "$COUNT" =~ ^[1-9][0-9]*$ ]]; then
  echo "windows-concurrency-stress: PIG_WIN_STRESS_COUNT must be a positive integer, got: $COUNT" >&2
  exit 2
fi

ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"

run_stress() {
  local label="$1" pkg="$2" pattern="$3"
  local cmd=(go test -count="$COUNT" -timeout="$TIMEOUT" -run "$pattern" "$pkg")
  echo "[windows-concurrency-stress] $label: ${cmd[*]}"
  if [[ "$DRY_RUN" -eq 1 ]]; then
    return 0
  fi
  "${cmd[@]}"
}

# 1. Atomic file replacement, Windows sharing contention, and security descriptor carry.
run_stress "atomicfile" ./internal/atomicfile \
  "^(TestWindowsWriteFile|TestWriteFileNeverLeavesPathAbsentAcrossProcessAndGoroutineObservers|TestReplaceWithMove)"

# 2. Pi lockfile Windows EPERM retries, delete-pending races, heartbeat, and concurrent store writes.
run_stress "pilock" ./internal/pilock \
  "^(TestConcurrentStoreWritesWithPiBackend|TestAuthLockWindowsEPERMUpstream|TestAcquireRetries|TestAcquireWithOptions|TestHeartbeat|TestLegacy)"

# 3. Auth storage credential locks and cancellation races.
run_stress "auth-storage-locks" ./ai \
  "^(TestAuthStorageFileLockWaitCancellation|TestAuthStorageHeldFileLockCancellationUpstream|TestAuthStorageReleasesLockAcquiredConcurrentlyWithCancellationUpstream|TestAuthStorageRetriesBrieflyContendedFileLockUpstream|TestAuthStorageLock|TestSharedAuthDirectoryLockWaitIsCancelled)"

# 4. Windows overlapped pipe lifecycle and childwait drain ordering.
run_stress "childwait-pipes" ./internal/childwait \
  "^(TestNewPipeReadEndIsOverlappedAndWriteEndIsNot|TestPipesDeliver|TestPipesWait|TestPollPipe|TestPollReadable|TestWaitIdle)"

# 5. Windows named pipe listener close/accept concurrency.
run_stress "winpipe" ./internal/chord/winpipe \
  "^(TestCloseEndsAWaitingAcceptAndFreesTheName|TestCloseReturnsWhileAClientDialsAndGivesUp)$"

# 6. Subprocess extension-host Windows pipe accept/close, process-tree cleanup, and RPC call/reply ordering.
run_stress "subprocess-host" ./coding/extension/host/subprocess \
  "^(TestPipeListener|TestAwaitConnectionHonorsAClientBeforeItsConnectEventIsSet|TestWindowsProcessTree|TestHarnessEntry.*Windows|TestHarnessEntryNormalExitLeavesNoProcess|TestHarnessEntryTerminateEndsItsBinary|TestCallLanesStartTheCallsOfConcurrentRequestsInReadOrder|TestCallsOfParallelToolRequestsApplyInSendOrder|TestDialogRepliesFollowTheOrderTheirAnswersArrived|TestCloseAfterThePeerClosedSucceeds|TestAHostCloseAnswersPendingRequestsWithTheMark|TestShutdown)"

# 7. CLI Windows RPC socket/pipe lifecycle, concurrent listen, and bash-tree kill on input end.
run_stress "cli-rpc-windows" ./coding/cli \
  "^(TestListenBindsAnAFUnixSocketOnWindows|TestListenServesASocketInAnOwnerOnlyDirectory|TestListenConcurrentSessionBuildsReportNoDataRace|TestListenExtensionShutdownEndsOnlyThatSession|TestIsClosedPipeWriteError|TestWin_RPCInputEndKillsRunningBashTree)$"

echo "[windows-concurrency-stress] all 7 groups completed (count=$COUNT)."
