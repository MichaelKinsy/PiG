#!/usr/bin/env bash
# Create a fresh local rehearsal with a loopback-only model and no inherited credentials.
set -euo pipefail
exec python3 -B "$(dirname "$0")/stage.py" "$@"
