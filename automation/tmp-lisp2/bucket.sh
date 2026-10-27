#!/usr/bin/env bash
# Temporary: runs bucket K of N of the extension-conformance tests (alphabetical by index modulo N). Usage: bucket.sh N K [go test flags]
set -uo pipefail
n=$1; k=$2; shift 2
names=$(go test ./test/extension-conformance -list '^Test' | grep '^Test' | awk -v n="$n" -v k="$k" '(NR-1)%n==k')
re="^($(echo "$names" | paste -sd'|' -))\$"
echo "bucket $k/$n: $(echo "$names" | wc -l) tests"
log=/tmp/bucket-$k.log
go test ./test/extension-conformance -count=1 -timeout 55m -run "$re" "$@" > "$log" 2>&1
echo "exit: $?; $(wc -l < "$log") lines"
grep -E '^(ok|FAIL|panic)' "$log" | head -5
grep -B14 -E '^\s*--- FAIL' "$log" | cut -c1-400 | tail -${TAILN:-250}
