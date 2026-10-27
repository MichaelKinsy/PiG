#!/usr/bin/env bash
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."
bash automation/tmp-lisp2/run.sh go test ./test/extension-conformance -count=1 -v -timeout 30m -run 'TestConformance_HostConfigurationAgreesAcrossSDKs/subprocess-lisp' > /tmp/s2-host.log 2>&1
echo "=== host test"; tail -30 /tmp/s2-host.log | cut -c1-1500
echo "=== sdksurface"
bash automation/tmp-lisp2/run.sh go run ./test/parity/cmd/sdksurface -check > /tmp/s2-surface.log 2>&1
grep -i "S2\|SessionManager\|SettingsManager\|lisp" /tmp/s2-surface.log | head -40; tail -5 /tmp/s2-surface.log
