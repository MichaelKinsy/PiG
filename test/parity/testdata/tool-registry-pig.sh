#!/usr/bin/env bash
set -euo pipefail
if ! output=$(go test -count=1 -run '^Test(SessionToolPromptUsesJavaScriptWhitespace|DefaultToolsInitialSelectionPort|ToolAllowlistExtensionPort|NoBuiltinToolsExtensionPort|ExcludeToolsExtensionPort|AgentSessionDynamicToolsPort)$' -v ./coding); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep '^REGISTRY '
