#!/usr/bin/env bash
set -euo pipefail
output=$(PIG_BOUNDARY_PROBE=1 go test -p 2 ./coding -run '^TestUpstreamSessionBoundaries' -count=1 -v 2>&1) || { printf '%s\n' "$output"; exit 1; }
printf '%s\n' "$output" | python3 -c 'import json,sys; lines=list(sys.stdin); rows=[line for line in lines if line.startswith("SESSION_BOUNDARY_CASE ")]; rows.sort(key=lambda line:json.loads(line.split(" ",1)[1])[0]); sys.stdout.writelines(rows); sys.stdout.writelines(line for line in lines if line.startswith("SESSION_BOUNDARY_BRIDGE "))'
