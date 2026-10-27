#!/usr/bin/env bash
# Temporary: runs the tests matching $1 and keeps the full log in .gt.log of the tree. Usage: gt.sh <regex> [go test flags]
set -uo pipefail
re=$1; shift
go test ./test/extension-conformance -count=1 -timeout 55m -run "$re" "$@" > .gt.log 2>&1
echo "exit: $?; $(wc -l < .gt.log) lines"
