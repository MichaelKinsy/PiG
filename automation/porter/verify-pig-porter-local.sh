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
cleanup() {
  tmux kill-session -t "$session" 2>/dev/null || true
  rm -rf "$home" "$pane"
}
trap cleanup EXIT

mkdir -p "$home/agent"
printf '%s\n' '{"defaultProjectTrust":"always"}' >"$home/agent/settings.json"

tmux new-session -d -s "$session" -x 140 -y 50 \
  "cd '$ROOT' && env -u HTTPS_PROXY -u HTTP_PROXY -u https_proxy -u http_proxy PIG_HOME='$home' PIG_BIN='$PIG_BIN' '$ROOT/automation/porter/run-pig-porter.sh' --verbose"

ready=false
for _ in $(seq 1 240); do
  tmux capture-pane -pt "$session" -S - >"$pane" 2>/dev/null || true
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
  if grep -q 'Ready. Type a message' "$pane"; then
    ready=true
    break
  fi
  sleep 0.25
done
[ "$ready" = true ] || { cat "$pane" >&2; echo "pig-porter: did not reach the TUI" >&2; exit 1; }

grep -qE '^\[Extensions\].*\bpig-porter\b' "$pane" || {
  cat "$pane" >&2
  echo "pig-porter: selected extension is not active" >&2
  exit 1
}
grep -qE '^- pig-porter: .*/piglets/porter/skills/pig-porter/SKILL.md$' "$pane" || {
  cat "$pane" >&2
  echo "pig-porter: selected skill is not active" >&2
  exit 1
}

tmux send-keys -t "$session" -l -- '/pig-porter {"operation":"disposition-plan"}'
tmux send-keys -t "$session" Enter
operation_ok=false
for _ in $(seq 1 240); do
  tmux capture-pane -pt "$session" -S - >"$pane" 2>/dev/null || true
  if grep -q 'No model selected' "$pane"; then
    cat "$pane" >&2
    echo "pig-porter: extension command was handled as a model prompt" >&2
    exit 1
  fi
  if grep -q '"totalChanged"' "$pane" && grep -q '"materialOnPorted"' "$pane"; then
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
