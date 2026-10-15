#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PIG_BIN="${PIG_PORTER_SMOKE_BIN:-$ROOT/bin/pig-porter-smoke}"
cd "$ROOT"

mkdir -p "$(dirname "$PIG_BIN")"
CGO_ENABLED=0 go build -buildvcs=false -trimpath -o "$PIG_BIN" ./cmd/pig
command -v tmux >/dev/null || { echo "pig-porter: smoke test requires tmux" >&2; exit 1; }

session="pig-porter-smoke-$$"
home="$(mktemp -d "${TMPDIR:-/tmp}/pig.XXXXXXXX")"
pane="$(mktemp "${TMPDIR:-/tmp}/pig.XXXXXXXX")"
# A tmux server of its own, so the run never touches the caller's sessions.
tm() { tmux -L "$session" -f /dev/null "$@"; }
pig_pid=""
cleanup() {
  tm kill-session -t "$session" 2>/dev/null || true
  # Killing the session ends pig through its terminal-loss exit, which still writes exit.log and the session under $home: wait for pig first.
  if [ -n "$pig_pid" ]; then
    for _ in $(seq 1 100); do
      kill -0 "$pig_pid" 2>/dev/null || break
      sleep 0.1
    done
  fi
  rm -rf "$home" "$pane" || echo "pig-porter: could not remove $home" >&2
}
trap cleanup EXIT

mkdir -p "$home/agent"
printf '%s\n' '{"defaultProjectTrust":"always"}' >"$home/agent/settings.json"

# The isolated HOME must not move the caller's Go caches and settings: the Piglet's Go extension is built with them, and a
# module cache under $home is read-only, so the cleanup could not remove it.
go_env="GOCACHE='$(go env GOCACHE)' GOMODCACHE='$(go env GOMODCACHE)' GOPATH='$(go env GOPATH)' GOENV='$(go env GOENV)'"
# exec at each step makes the pane process pig itself (run-pig-porter.sh execs it), so the cleanup can wait for it.
tm new-session -d -s "$session" -x 140 -y 50 \
  "cd '$ROOT' && exec env -u HTTPS_PROXY -u HTTP_PROXY -u https_proxy -u http_proxy $go_env HOME='$home' PIG_HOME='$home' PIG_CODING_AGENT_DIR='$home/agent' PI_CODING_AGENT_DIR='$home/agent' PIG_BIN='$PIG_BIN' '$ROOT/automation/porter/run-pig-porter.sh' --verbose"
pig_pid="$(tm display-message -p -t "$session" '#{pane_pid}' 2>/dev/null || true)"

ready=false
for _ in $(seq 1 240); do
  tm capture-pane -pt "$session" -S - >"$pane" 2>/dev/null || true
  if grep -q 'Trust project folder?' "$pane"; then
    cat "$pane" >&2
    echo "pig-porter: isolated smoke unexpectedly requested project trust" >&2
    exit 1
  fi
  if grep -q 'warning: extension "pig-porter"' "$pane"; then
    cat "$pane" >&2
    echo "pig-porter: extension failed to load" >&2
    exit 1
  fi
  # The startup header lists each loaded resource under [Extensions] and [Skills] once the TUI is up.
  if grep -q '^\[Extensions\]' "$pane" && grep -q 'piglets/porter/extensions/pig-porter$' "$pane"; then
    ready=true
    break
  fi
  sleep 0.25
done
[ "$ready" = true ] || { cat "$pane" >&2; echo "pig-porter: did not reach the TUI" >&2; exit 1; }

grep -q 'piglets/porter/extensions/pig-porter$' "$pane" || {
  cat "$pane" >&2
  echo "pig-porter: selected extension is not active" >&2
  exit 1
}
grep -q 'piglets/porter/skills/pig-porter/SKILL.md$' "$pane" || {
  cat "$pane" >&2
  echo "pig-porter: selected skill is not active" >&2
  exit 1
}

tm send-keys -t "$session" -l -- '/pig-porter {"operation":"disposition-plan"}'
tm send-keys -t "$session" Enter
operation_ok=false
for _ in $(seq 1 240); do
  tm capture-pane -pt "$session" -S - >"$pane" 2>/dev/null || true
  if grep -q 'No model selected' "$pane"; then
    cat "$pane" >&2
    echo "pig-porter: extension command was handled as a model prompt" >&2
    exit 1
  fi
  # The plan is thousands of lines and the TUI clears its scrollback when it redraws, so only the tail of the JSON is on the
  # pane: every group ends with these three members, which no error message prints.
  if grep -q '"portMapStatus"' "$pane" && grep -q '"recommendation"' "$pane" && grep -q '"decision"' "$pane"; then
    operation_ok=true
    break
  fi
  sleep 0.25
done
[ "$operation_ok" = true ] || {
  cat "$pane" >&2
  echo "pig-porter: disposition-plan did not return its deterministic report" >&2
  exit 1
}

echo "pig-porter smoke: Piglet extension, skill, and read-only disposition plan verified"
