#!/usr/bin/env bash
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."
head=$(git rev-parse HEAD)
for rev in f6f56a668 945faa46a; do
  echo "== $rev"
  git checkout -q -f "$rev"
  (cd extensions/sdk-lisp && go generate ./...) >/dev/null 2>&1
  go test ./coding/extension/pigsdk -run 'Stage|Sync|Expand|Compressed' -count=5 -v 2>&1 | grep -E "^(--- |ok|FAIL|PASS)" | sort | uniq -c | head -12
  go test ./test/perfgate -run 'TestReleaseBinarySizeGate|TestHeadlessEmbedBinarySizeGate' -count=1 -v 2>&1 | grep -E "binary\.|^(--- |ok|FAIL)|ceiling|bytes" | head -40
done
