#!/usr/bin/env bash
set -euo pipefail
go test ./internal/codingagent -run '^TestMermaidFallbackParityTrace$' -count=1 -v |
  awk '/^MERMAID_FALLBACK / { sub(/^MERMAID_FALLBACK /, ""); print }'
