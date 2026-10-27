#!/usr/bin/env bash
# Temporary: runs bucket K of N of the extension-conformance tests (alphabetical by index modulo N). Usage: bucket.sh N K [go test flags]
set -uo pipefail
n=$1; k=$2; shift 2
names=$(go test ./test/extension-conformance -list '^Test' | grep '^Test' | awk -v n="$n" -v k="$k" '(NR-1)%n==k')
re="^($(echo "$names" | paste -sd'|' -))\$"
echo "bucket $k/$n: $(echo "$names" | wc -l) tests"
go test ./test/extension-conformance -count=1 -run "$re" "$@" 2>&1 | tail -${TAILN:-120}
