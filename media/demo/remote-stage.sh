#!/usr/bin/env bash
# Prepare another local HOME. This is a same-host handoff, not a remote-machine claim.
set -euo pipefail
: "${DEMO_ROOT:?Run stage.sh, then source the generated env.sh}"
mkdir "$HOME/clean"
mkdir -p "$HOME/clean/.pig/agent"
cp "$HOME/.pig/agent/models.json" "$HOME/clean/.pig/agent/models.json"
cp "$HOME/.pig/agent/settings.json" "$HOME/clean/.pig/agent/settings.json"
printf 'Clean HOME prepared with only local model settings; no SDK, compiler, or extension cache.\n'
