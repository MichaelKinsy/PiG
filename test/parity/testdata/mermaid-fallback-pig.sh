#!/usr/bin/env bash
set -euo pipefail
./test/parity/testdata/prebuilt-go-test.sh ./internal/codingagent -run '^TestMermaidFallbackParityTrace$' -count=1 -v |
  awk '/^MERMAID_FALLBACK / { sub(/^MERMAID_FALLBACK /, ""); print }'
