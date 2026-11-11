#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Scan step: every golden or snapshot file has its provenance entry, at the PR's head. Usage: provenance.sh <work dir> <main tree>
# main's testprune ($PREFLIGHT_BIN_DIR/testprune) runs over the PR's head tree. Writes <work>/provenance.txt.
set -uo pipefail
work=$1
(cd "$work/head" && "$PREFLIGHT_BIN_DIR/testprune" lint -provenance) > "$work/provenance-all.txt" 2>&1
status=$?
grep -v '^note:' "$work/provenance-all.txt" > "$work/provenance.txt"
exit "$status"
