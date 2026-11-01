#!/usr/bin/env bash
# Runs `mcp add` with the flag under test in a scratch agent directory and prints the command's output, its exit code and
# the mcp.json it wrote, so a flag that is accepted but not written cannot pass.
# usage: mcp-add-effect.sh pig|pi
set -uo pipefail
if [ "$1" = pig ]; then bin=${PIG_PARITY_PIG_BIN:-pig}; else bin=${PIG_PARITY_PI_BIN:-pi}; fi
dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
export PIG_CODING_AGENT_DIR=$dir PI_CODING_AGENT_DIR=$dir PIG_OFFLINE=1 PI_OFFLINE=1 PI_SKIP_VERSION_CHECK=1
"$bin" mcp add linear --url https://mcp.linear.app/mcp --oauth-client-registration dcr 2>&1
echo "exit $?"
cat "$dir/mcp.json"
