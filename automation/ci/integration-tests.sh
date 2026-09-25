#!/usr/bin/env bash
# automation/ci/integration-tests.sh: split the integration suite by category so
# the long-running LLM-driven tests don't gate every commit.
#
# Categories (from AGENTS.md "Functional tests over unit tests"):
#
#   fast      : no LLM calls, deterministic. Runs in ~30s. Gate every commit.
#   parity    : side-by-side checks that still require the integration harness.
#               Canonical hermetic behavior belongs in parity/scenarios/.
#               Run before an upstream update or release.
#   live      : single-system live tests (TestLive_*, TestExt_Live_*,
#               TestPerf_*). Uses provider credentials. Run manually before a
#               release.
#   all       : every integration test (~25 min on a fast machine).
#
# Usage: ./automation/ci/integration-tests.sh [fast|parity|live|all] [-- extra go test flags...]
#
# Hard rules (per AGENTS.md):
#   - Never use `tmux kill-server`. Each test creates its own session by
#     PID-randomized name and kills only itself.
#   - Always use the cheap model literal: currently github-copilot/gpt-5-mini.
#     `grep -rE 'github-copilot/(gpt|claude)' tests/integration/` is the
#     audit query.

set -euo pipefail

cd "$(dirname "$0")/../.."

CATEGORY="${1:-fast}"
shift || true

# Patterns matched against `go test -run`. Categories are mutually exclusive
# except for "all".
case "$CATEGORY" in
    fast)
        # No LLM. Behavior_* + tui_* + structural ext tests,
        # plus the signal guards (faux provider, no network).
        # NOTE: TestExt_StartupWithSubagent etc. spawn pig but don't hit the
        # LLM, so they're "fast" for our purposes.
        PATTERN='^(TestBehavior_|TestSlash|TestShiftEnter|TestExitClean|TestSignalEmits|TestInteractiveSigint|TestExt_StartupWith|TestExt_Subagent|TestExt_NoExtensions|TestExt_StartupWithout|TestExt_Convention|TestExt_Cached)'
        TIMEOUT=300s
        ;;
    parity)
        # Side-by-side checks that need external extension or integration setup.
        # Hermetic product behavior belongs in parity/scenarios/*.toml.
        PATTERN='^TestParity_'
        TIMEOUT=900s
        ;;
    live)
        # Single-system live LLM tests. Slowest, most expensive in tokens.
        PATTERN='^(TestLive_|TestExt_Live_|TestPerf_)'
        TIMEOUT=1800s
        ;;
    all)
        PATTERN='.'
        TIMEOUT=2400s
        ;;
    *)
        echo "usage: $0 [fast|parity|live|all] [-- extra go test args]" >&2
        exit 2
        ;;
esac

echo "== integration tests: category=$CATEGORY pattern=$PATTERN timeout=$TIMEOUT"
echo

exec go test -tags integration -timeout "$TIMEOUT" -v \
    -run "$PATTERN" \
    ./tests/integration/... "$@"
