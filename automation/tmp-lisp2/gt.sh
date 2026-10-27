#!/usr/bin/env bash
# Temporary: runs the named tests (arguments) and keeps the full log in .gt.log of the tree.
set -uo pipefail
re="^($(echo "$*" | tr ' ' '|'))\$"
go test ./test/extension-conformance -count=1 -timeout 55m -run "$re" > .gt.log 2>&1
echo "exit: $?; $(wc -l < .gt.log) lines"
