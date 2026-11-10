#!/usr/bin/env bash
# Re-record the exact-Pi observation fixtures (coding/testdata/rpc33-observation, ai/testdata/google-tick-order) from the
# pinned Pi build. Each probe stamps the pin's display version (PIG_UPSTREAM_VERSION), so a fixture recorded by another
# pin fails its Go test until this runs. Review the diff against the Pi source before accepting it; never edit a fixture.
#
# The fixtures record the microtask and macrotask order of Pi's pipeline, which depends on the Node release: from Node 24.22.0
# a body delivered in separate socket writes can reach Pi's consumer in one macrotask, where Node 24.14.1 through 24.21.0 deliver
# one macrotask per write. The Go models replay the oracle Node, ai/testdata/node-microtasks/oracle-node-version, not .node-version
# (CI's Node) and not Node current. The script refuses to record under any other Node.
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$root"
oracle=$(tr -d '\r\n' < ai/testdata/node-microtasks/oracle-node-version)
have=$(node --version 2>/dev/null || true)
if [ "$have" != "v$oracle" ]; then
  echo "regenerate-observation-oracles: Node on PATH is '${have:-none}'; the observation fixtures are recorded under the oracle Node v$oracle (ai/testdata/node-microtasks/oracle-node-version), not .node-version." >&2
  echo "Put Node v$oracle first on PATH (tern-ci: run.sh --node $oracle, with that release mounted at /opt/node/$oracle if the image lacks it) and rerun." >&2
  exit 1
fi
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
