#!/usr/bin/env bash
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."
bash automation/tmp-lisp2/run.sh make test-sdk-lisp 2>&1 | tail -40
bash automation/tmp-lisp2/run.sh go test ./test/extension-conformance -count=1 -v -timeout 30m -run 'TestSessionGraph(SessionManager|SettingsManager|ProjectTrustStore|DefaultResourceLoader|DefaultPackageManager)AcrossSDKs/lisp$|TestSessionGraphLispPackedAcrossSDKs' 2>&1 | grep -E "^(=== RUN|--- |ok|FAIL|PASS|panic)|session_graph" | cut -c1-400 | tail -120
