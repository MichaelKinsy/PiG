#!/usr/bin/env bash
cd "$(dirname "${BASH_SOURCE[0]}")/../.."
echo "== lisp tests"; sbcl --noinform --non-interactive --no-sysinit --no-userinit --disable-debugger --load extensions/sdk-lisp/tests/run.lisp 2>&1 | tail -15
echo "== sdksurface"; go run ./test/parity/cmd/sdksurface -check 2>&1 | grep -i "lisp\|registry\|model-stream\|event-stream\|ctx-complete" | head -40
echo "== lint"; git fetch -q origin batch/0.6.0-followup 2>&1 | tail -1; make lint-changed LINT_BASE=origin/batch/0.6.0-followup 2>&1 | tail -30
