#!/usr/bin/env bash
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."
bash automation/tmp-lisp2/run.sh go test ./test/extension-conformance -count=1 -v -timeout 30m -run 'TestConformance_Host(Configuration|Paths)AgreesAcrossSDKs' 2>&1 | grep -E "^\s*(=== RUN|--- |ok|FAIL|PASS|panic)|_test.go" | cut -c1-500 | grep -i "lisp\|FAIL\|^ok\|panic" | tail -40
bash automation/tmp-lisp2/run.sh go run ./test/parity/cmd/sdksurface -check 2>&1 | tail -30
