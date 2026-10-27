#!/usr/bin/env bash
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."
bash automation/tmp-lisp2/run.sh go test ./test/extension-conformance -count=1 -v -timeout 40m -run 'TestConformance_HostPathsAgreeAcrossSDKs' > /tmp/s2-go.log 2>&1
echo "=== go"; grep -E "^\s*--- |^(ok|FAIL|panic)|_test.go:" /tmp/s2-go.log | cut -c1-600 | tail -30
