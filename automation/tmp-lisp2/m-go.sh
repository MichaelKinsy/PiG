#!/usr/bin/env bash
cd "$(dirname "${BASH_SOURCE[0]}")/../.."
go test ./test/extension-conformance -v -count=1 -timeout 1500s -run '^(TestModelStreamingSDKsMatch|TestModelStreamCallbacksSDKsMatch|TestModelStreamFetchSDKsMatch|TestSDKModelCallsCarryPiGIdentityNeverPis|TestModelTypes.*|TestLateProvider.*)$/lisp' 2>&1 | grep -E '^(=== RUN|--- |\s+--- |FAIL|ok|PASS|\s+[a-z_]+\.go)' | grep -v '=== RUN' | tail -${TAILN:-120}
go test ./test/extension-conformance -count=1 -run 'TestEverySDKCanReachEveryWireCapability|TestCapabilityExemptionsAreCurrent|TestModelAuthUsesOneWireShape' 2>&1 | tail -30
