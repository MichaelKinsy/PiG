#!/usr/bin/env bash
set -euo pipefail
# The probe's stdout is compared byte-for-byte with Pi's: keep the test log out of the comparison and
# show it only when the probe fails.
out="$(./test/parity/testdata/prebuilt-go-test.sh ./coding/cli -run '^TestSkillMetadataUsesFilesWithoutChangingConfigBundlePaths$' -count=1 -v 2>&1)" || {
	printf '%s\n' "$out" >&2
	exit 1
}
grep '^SKILL_METADATA ' <<<"$out"
