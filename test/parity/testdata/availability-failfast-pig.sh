#!/usr/bin/env bash
set -euo pipefail
./test/parity/testdata/prebuilt-go-test.sh ./coding -run '^TestAvailabilityFailureSettlesBeforeCredentialList$' -count=1 -v |
  awk '/^AVAILABILITY_FAILFAST / { sub(/^AVAILABILITY_FAILFAST /, ""); print }'
