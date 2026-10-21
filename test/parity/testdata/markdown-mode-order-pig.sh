#!/usr/bin/env bash
# The Mode's construction seam is private. Drive its real callback pipeline and fused SDK through the package tests, then expose only the complete callback
# traces to the Pi comparator. A failed assertion or test process remains a failure. The test binary is the one the runner builds before the timed runs
# (the scenario's pig_go_tests), so the run's timeout covers the probe, not the Go toolchain.
set -euo pipefail
log=$(mktemp)
trap 'rm -f "$log"' EXIT
if ! ./test/parity/testdata/prebuilt-go-test.sh ./internal/codingagent -run '^TestMarkdownMode(ReplacementWaitsForCallbackBody|CrossComponentAdmissionOrder)$' -count=1 -v >"$log" 2>&1; then
  tail -100 "$log" >&2
  exit 1
fi
for name in replacement components; do
  traces=$(grep -o "MARKDOWN_ORDER $name:.*" "$log" | sed 's/^MARKDOWN_ORDER //; s/[[:space:]]*$//' || true)
  case $(printf '%s' "$traces" | grep -c .) in
  1) printf '%s\n' "$traces" ;;
  0)
    echo "missing trace \"$name\"" >&2
    exit 1
    ;;
  *)
    echo "duplicate trace \"$name\"" >&2
    exit 1
    ;;
  esac
done
