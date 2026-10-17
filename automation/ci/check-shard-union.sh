#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Usage: check-shard-union.sh <package> <shards>
#
# Fail unless the -run patterns of test-shard-pattern.sh for shards 1..<shards> of
# <package> select every top-level test, example and fuzz target the package lists
# exactly once, so sharding a package never drops or repeats a test.
set -euo pipefail
export LC_ALL=C
if [[ $# -ne 2 ]]; then
  echo "usage: $0 <package> <shards>" >&2
  exit 2
fi
package=$1 shards=$2
here=$(dirname -- "${BASH_SOURCE[0]}")
listing=$(go test -list . "$package")
names=$(grep -E '^(Test|Example|Fuzz)' <<<"$listing" || true)
if [[ -z $names ]]; then
  echo "check-shard-union: $package lists no tests" >&2
  exit 1
fi
counts=$(mktemp)
trap 'rm -f "$counts"' EXIT
for ((shard = 1; shard <= shards; shard++)); do
  pattern=$("$here/test-shard-pattern.sh" "$shard" "$shards" "$package")
  grep -E -- "$pattern" <<<"$names" >>"$counts" || true
done
bad=$(sort "$counts" | uniq -c | awk '$1 != 1 {print $2 " x" $1}')
missing=$(comm -23 <(sort <<<"$names") <(sort -u "$counts"))
if [[ -n $bad || -n $missing ]]; then
  echo "check-shard-union: $package shards do not partition its tests" >&2
  [[ -z $bad ]] || echo "run more than once: $bad" >&2
  [[ -z $missing ]] || echo "never run: $missing" >&2
  exit 1
fi
