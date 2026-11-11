#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Scan step: every copy of the toolchain pins matches its source, at the PR's head. Usage: pins.sh <work dir> <main tree>
# main's check runs: its scripts are copied over the PR's copies, so the PR's code never runs. Writes <work>/pins.txt.
set -uo pipefail
work=$1 root=$2
cp "$root/automation/ci/check-compliance.py" "$work/head/automation/ci/check-compliance.py"
mkdir -p "$work/head/automation/pin" && cp "$root/automation/pin/pinlib.py" "$work/head/automation/pin/pinlib.py"
python3 "$work/head/automation/ci/check-compliance.py" --skip files --skip citation --skip actions --skip permissions --skip reuse --skip govulncheck > "$work/pins-all.txt" 2>&1
status=$?
grep -vE '^compliance: .*(SKIPPED|ok|passed)$' "$work/pins-all.txt" > "$work/pins.txt"
exit "$status"
