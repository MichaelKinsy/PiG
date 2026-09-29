#!/usr/bin/env bash
set -euo pipefail
# The probe's stdout is compared byte-for-byte with Pi's. A cold module cache makes `go test` write
# "go: downloading ..." progress to stderr, which the harness merges into the compared output, so
# keep the toolchain output out of the comparison and show it only when the probe fails.
out="$(go test ./cmd/pig -run '^TestSkillMetadataUsesFilesWithoutChangingConfigBundlePaths$' -count=1 -v 2>&1)" || {
	printf '%s\n' "$out" >&2
	exit 1
}
grep '^SKILL_METADATA ' <<<"$out"
