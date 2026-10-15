#!/usr/bin/env bash
# Re-record the exact-Pi observation fixtures (coding/testdata/rpc33-observation, ai/testdata/google-tick-order) from the
# pinned Pi build. Each probe stamps the pin's display version (PIG_UPSTREAM_VERSION), so a fixture recorded by another
# pin fails its Go test until this runs. Review the diff against the Pi source before accepting it; never edit a fixture.
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$root"
eval "$(python3 automation/pin/pin.py env)"
export PI_PACKAGE_ROOT="$root/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent"
private=$(mktemp -d)
trap 'rm -rf "$private"' EXIT
mkdir -p "$private/home" "$private/pig" "$private/pi"
export HOME="$private/home" PIG_CODING_AGENT_DIR="$private/pig" PI_CODING_AGENT_DIR="$private/pi"
dir=coding/testdata/rpc33-observation
run() { # <probe> <args...>: runs in the probe's directory so relative outputs land beside it
  local probe=$1; shift
  echo "== $probe $*"
  (cd "$(dirname "$probe")" && timeout 900 node "$(basename "$probe")" "$@") || failed+=("$probe")
}
failed=()
run $dir/probe.mjs pi.json inputs.json
p=$dir/providers
run $p/anthropic-messages/probe.mjs pi.json inputs.json
run $p/azure-openai-responses/probe.mjs pi.json inputs.json
run $p/azure-openai-responses/rpc.mjs rpc.json
run $p/azure-openai-responses/ticks.mjs ticks.json
run $p/bedrock-converse-stream/probe.mjs pi.json inputs.json
run $p/bedrock-converse-stream/chain.mjs chain.json
run $p/faux/probe.mjs pi.json
run $p/google-generative-ai/probe.mjs pi.json inputs.json
run $p/mistral-conversations/probe.mjs pi.json
run $p/mistral-conversations/realticks.mjs realticks.json
run $p/mistral-conversations/ticks.mjs ticks.json
run $p/openai-codex-responses/probe.mjs pi.json
run $p/pi-messages/probe.mjs pi.json
run $p/pi-messages/ticks.mjs ticks.json
run $p/test-faux/probe.mjs pi.json
run ai/testdata/google-tick-order/probe.mjs pi.json
if ((${#failed[@]})); then printf 'FAILED: %s\n' "${failed[@]}" >&2; exit 1; fi
