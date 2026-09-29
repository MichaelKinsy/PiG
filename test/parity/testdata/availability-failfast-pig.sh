#!/usr/bin/env bash
set -euo pipefail
go test ./coding -run '^TestAvailabilityFailureSettlesBeforeCredentialList$' -count=1 -v |
  awk '/^AVAILABILITY_FAILFAST / { sub(/^AVAILABILITY_FAILFAST /, ""); print }'
