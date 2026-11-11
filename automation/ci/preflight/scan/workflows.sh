#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Scan step: actionlint and zizmor on the changed workflow files. Usage: workflows.sh <work dir> <main tree>
# The configurations are main's; only findings on lines the PR adds count. Writes <work>/workflows.txt.
set -uo pipefail
work=$1 root=$2
mapfile -t wf < <(grep -E '^\.github/workflows/.*\.ya?ml$' "$work/files.txt")
: > "$work/workflows.txt"
[ "${#wf[@]}" -gt 0 ] || exit 0
alcfg="$root/.github/actionlint.yaml"
[ -f "$alcfg" ] || alcfg=/dev/null
# shellcheck disable=SC2016 # the single quotes keep actionlint's Go template from the shell.
(cd "$work/head" && actionlint -config-file "$alcfg" -format '{{range $e := .}}{{$e.Filepath}}:{{$e.Line}}: {{$e.Message}} [{{$e.Kind}}]{{"\n"}}{{end}}' "${wf[@]}") > "$work/workflows-all.txt" 2>&1
(cd "$work/head" && zizmor --offline --no-config --format plain "${wf[@]}") >> "$work/workflows-all.txt" 2>&1
grep -F -f <(sed 's/$/:/' "$work/added-lines.txt") "$work/workflows-all.txt" > "$work/workflows.txt"
[ ! -s "$work/workflows.txt" ]
