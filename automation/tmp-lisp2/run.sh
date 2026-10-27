#!/usr/bin/env bash
# Temporary lane runner (removed before the lane merges): installs the pinned SBCL, regenerates the Lisp SDK pack, then runs "$@".
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."
export PIG_SBCL="$(bash automation/ci/install-sbcl.sh | tail -1)/bin/sbcl"
export PATH="$(dirname "$PIG_SBCL"):$PATH"
echo "sbcl: $PIG_SBCL"
(cd extensions/sdk-lisp && go generate ./...)
"$@"
