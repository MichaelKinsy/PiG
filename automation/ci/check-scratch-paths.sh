#!/usr/bin/env bash
# Source hygiene uses the same private-path policy as publication review.
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$root"
exec python3 automation/ci/check-public-hygiene.py "$@"
