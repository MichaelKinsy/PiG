#!/bin/sh
# The Durable 0.5.0 bar's long-run memory proof: record TURNS (default 300) turns of the bench workload on the native stack,
# replay them through the native core and fail unless the live set stops growing (countgate flat). Five warm turns cannot show a
# leak that a conversation of hundreds of turns keeps: the live set of a short window can fall while a long run climbs.
# Usage: [TURNS=300] sh durable/core/contracttest/flat.sh   (about 3.5 min for 300 turns)
set -eu
root=$(cd "$(dirname "$0")/../../.." && pwd)
cd "$root"
# The programs build through the tree's go.work with the pinned toolchain, as gate.sh and lib/instr.mjs do: a caller's GOWORK=off
# builds bench against the published extensions/sdk, which lacks the workspace's API.
export GOTOOLCHAIN=${GOTOOLCHAIN:-go1.27.2} GOWORK=
work=$(mktemp -d "${TMPDIR:-/tmp}/dcore-flat.XXXXXX")
trap 'rm -rf "$work"' EXIT
go build -buildvcs=false -o "$work/bench" ./durable/core/sqlhost/cmd/bench
go build -buildvcs=false -o "$work/countgate" ./durable/core/contracttest/cmd/countgate
env HOME="$work/home" PIG_CODING_AGENT_DIR="$work/agent" PI_CODING_AGENT_DIR="$work/agent" PIG_HOME="$work/pig" PI_HOME="$work/pig" \
	TZ=UTC DETENV_EPOCH=1800000000000 "$work/bench" --db "$work/s.sqlite" --trace "$work/t.trace" --record "$work/r.rec" \
	--seed 3 --turns "${TURNS:-300}" --tools 8 --variant incr >/dev/null
"$work/countgate" flat --rec "$work/r.rec"
