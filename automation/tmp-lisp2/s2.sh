#!/usr/bin/env bash
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."
bash automation/tmp-lisp2/run.sh make test-sdk-lisp 2>&1 | tail -40
bash automation/tmp-lisp2/run.sh go test ./test/extension-conformance -count=1 -timeout 30m -run 'TestSessionGraph(SessionManager|SettingsManager|ProjectTrustStore|DefaultResourceLoader|DefaultPackageManager)AcrossSDKs/lisp$|TestSessionGraphLispPackedAcrossSDKs' 2>&1 | tail -150
