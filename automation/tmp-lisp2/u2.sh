#!/usr/bin/env bash
cd "$(dirname "${BASH_SOURCE[0]}")/../.."
export PIG_SBCL="$(bash automation/ci/install-sbcl.sh | tail -1)/bin/sbcl"
export PATH="$(dirname "$PIG_SBCL"):$PATH"
(cd extensions/sdk-lisp && go generate ./...)
echo "=== lisp unit tests"
make test-sdk-lisp 2>&1 | tail -80 || true
echo "=== conformance rows"
go test ./test/extension-conformance -count=1 -run 'TestAutocompleteFactoriesAcrossSDKs|TestEditorComponent|TestConformance_EditorComponentGetter|TestConformance_TerminalInput|TestEverySDKCanReachEveryWireCapability|TestEverySDKReceivesEveryHostNotifyCapability' 2>&1 | grep -v "^ok\|^=== \|PASS: " | tail -150 || true
