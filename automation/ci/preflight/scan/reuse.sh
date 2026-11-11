#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Scan step: REUSE compliance of the changed files at the PR's head. Usage: reuse.sh <work dir> <main tree>
# Writes <work>/reuse.txt; exits 0 when compliant.
set -uo pipefail
work=$1
mapfile -t files < "$work/files.txt"
(cd "$work/head" && reuse lint-file "${files[@]}") > "$work/reuse.txt" 2>&1
