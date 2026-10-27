#!/usr/bin/env bash
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."
bash automation/tmp-lisp2/run.sh make test-sdk-lisp > /tmp/s2-sdk.log 2>&1; echo "=== sdk"; tail -6 /tmp/s2-sdk.log | cut -c1-600
bash automation/tmp-lisp2/run.sh go test ./test/extension-conformance -count=1 -v -timeout 40m -run 'TestSessionGraph(SessionManager|SettingsManager|ProjectTrustStore|DefaultResourceLoader|DefaultPackageManager)AcrossSDKs|TestSessionGraphLispPackedAcrossSDKs|TestConformance_Host(Configuration|Paths)AgreesAcrossSDKs' > /tmp/s2-go.log 2>&1
echo "=== go"; grep -E "^\s*--- |^(ok|FAIL|panic)|_test.go:" /tmp/s2-go.log | cut -c1-600 | grep -i "lisp\|FAIL\|^ok\|panic" | tail -60
