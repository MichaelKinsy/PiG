#!/usr/bin/env bash
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."
bash automation/tmp-lisp2/run.sh go run ./test/parity/cmd/sdksurface 2>&1 | tail -5
make generate > .gen.log 2>&1; tail -3 .gen.log
git status --short | grep -v -e "automation/tmp-lisp2" -e "^?? \.[a-z0-9-]*\(\.\|$\)" | awk '{print $2}' | tar czf .lisp2-gen.tgz -T - 2>/dev/null
git status --short | head -30
{
 echo "== sdk-lisp"; make test-sdk-lisp 2>&1 | tail -6
 echo "== sdksurface"; go run ./test/parity/cmd/sdksurface -check -release 2>&1 | tail -8
 echo "== build"; go build ./... 2>&1 | tail -5
 echo "== module-size"; make module-size 2>&1 | tail -3
 echo "== changelog"; make changelog-check 2>&1 | tail -3
 echo "== lint"; make lint-changed LINT_BASE=origin/batch/0.6.0-followup 2>&1 | tail -8
 echo "== size"; go test ./test/perfgate -run 'TestReleaseBinarySizeGate|TestHeadlessEmbedBinarySizeGate' -count=1 2>&1 | tail -4
} > .final.out 2>&1
cat .final.out
