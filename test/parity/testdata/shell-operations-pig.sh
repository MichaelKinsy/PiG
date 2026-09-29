#!/usr/bin/env bash
set -euo pipefail
if ! output=$(go test -count=1 -run '^TestToolsBashPort/should_(handle_process_spawn_errors|pass_shellPath_through_to_shell_resolution)$' -v ./internal/codingagent/tools); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep '^BASH_'
