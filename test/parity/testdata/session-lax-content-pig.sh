#!/usr/bin/env bash
set -euo pipefail
output=$(./test/parity/testdata/prebuilt-go-test.sh ./agent ./coding ./coding/cli -count=1 -v -run '^(TestUpstreamLaxExtensionContent|TestUpstreamLaxSessionEntryContent|TestUserContentUnionPreservesWireAndProviderShape|TestOmittedGoUserContentRemainsAnEmptyProviderArray|TestResumedSessionPreservesUserStringThroughNextPrompt|TestRPCUserContentRetainsStringAndArrayVariants)$' 2>&1) || { printf '%s\n' "$output"; exit 1; }
for prefix in SESSION_LAX_EXTENSIONS SESSION_LAX_ENTRIES SESSION_USER_STRING_CONTEXT; do
  printf '%s\n' "$output" | grep "^$prefix "
done
