#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

# Runs the Lisp extension SDK's own tests with the pinned SBCL (PIG_SBCL, else sbcl on PATH). A missing SBCL is an environment failure.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
sbcl=${PIG_SBCL:-sbcl}
command -v "$sbcl" >/dev/null 2>&1 || { echo "test-sdk-lisp: $sbcl not found; run automation/ci/install-sbcl.sh and set PIG_SBCL" >&2; exit 1; }
exec "$sbcl" --noinform --non-interactive --no-sysinit --no-userinit --disable-debugger --load "$root/extensions/sdk-lisp/tests/run.lisp"
