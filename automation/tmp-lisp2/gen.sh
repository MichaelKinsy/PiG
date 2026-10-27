#!/usr/bin/env bash
# Temporary: regenerate the generated files on the run tree and pack the changed ones to .lisp2-gen.tgz
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."
bash automation/tmp-lisp2/run.sh go run ./test/parity/cmd/sdksurface 2>&1 | tail -5
make generate 2>&1 | tail -15
git status --short | tee .lisp2-gen.status | tail -40
git status --short | awk '{print $2}' | grep -v -e "^automation/tmp-lisp2" -e "^\.lisp2-gen" | tar czf .lisp2-gen.tgz -T - 2>/dev/null
ls -la .lisp2-gen.tgz
