#!/usr/bin/env bash
set -euo pipefail
output=$(go test ./agent ./agent/harness/session ./coding ./cmd/pig -count=1 -v -run '^(TestUpstreamLaxExtensionContent|TestUpstreamLaxSessionEntryContent|TestUserContentUnionPreservesWireAndProviderShape|TestOmittedGoUserContentRemainsAnEmptyProviderArray|TestResumedSessionPreservesUserStringThroughNextPrompt|TestRPCUserContentRetainsStringAndArrayVariants|TestWritesMatchUpstreamJSONMembers)$' 2>&1) || { printf '%s\n' "$output"; exit 1; }
for prefix in SESSION_LAX_EXTENSIONS SESSION_LAX_ENTRIES SESSION_USER_STRING_CONTEXT; do
  printf '%s\n' "$output" | grep "^$prefix "
done
