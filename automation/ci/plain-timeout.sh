# SPDX-License-Identifier: MIT
# Sourced by test-grouped.sh: the go test -timeout of a plain (non-race) whole-package run, in one place.
# In make test-fast at eight CPUs on xd670, ./chord/nextnode/app measures 520.368 s under load against 346.947 s in the rehearsal (go test's 10-minute default); three times the worst measure is 26 minutes, and 30m matches the plain whole-package ./ai timeout in .github/workflows/ci.yml.
# shellcheck shell=bash
PIG_PLAIN_TIMEOUT="${PIG_PLAIN_TIMEOUT:-30m}"
