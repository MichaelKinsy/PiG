#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/../../.." && pwd)
kind=$1
if [[ $kind == pig ]]; then
  binary=${PIG_PARITY_PIG_BIN:-${PIG_BIN:?set PIG_PARITY_PIG_BIN or PIG_BIN}}
  args=(--no-extensions --model test-faux/faux-1)
else
  binary=${PIG_PARITY_PI_BIN:-$(command -v pi)}
  [[ $("$binary" --version) == "0.87.1" ]] || { printf 'expected pinned Pi 0.87.1\n' >&2; exit 1; }
  args=(--no-extensions -e "$root/test/parity/testdata/test-faux-provider.ts" --model test-faux/faux-1)
fi
# An explicit queued-int invocation retains the unsynchronized stopped-job stress probe. Its two legal outcomes are covered deterministically by suspend-signal-order.py.
queued_interrupt=${2:-}
args+=("reply with exactly: SUSPEND_READY")
work=$(mktemp -d)
name="parity-suspend-${kind}-$$"
cleanup() { tmux -L "$name" kill-session -t "$name" 2>/dev/null || true; rm -rf "$work"; }
trap cleanup EXIT
{
  printf 'set -m\n'
  printf 'trap '\''printf "\\nSUSPEND_JOB_EXIT:%%s\\n" "$?"; read -r _'\'' EXIT\n'
  printf 'export PIG_TEST_FAUX=1 PI_SKIP_VERSION_CHECK=1 COLORTERM=truecolor\n'
  for key in PIG_CODING_AGENT_DIR PI_CODING_AGENT_DIR; do
    if [[ -n ${!key:-} ]]; then printf 'export %s=%q\n' "$key" "${!key}"; fi
  done
  printf 'cd %q\n' "$work"
  printf '%q ' "$binary" "${args[@]}"; printf '\n'
  printf 'jobs -p > %q\n' "$work/pid"
  printf 'test -s %q || exit 1\n' "$work/pid"
  printf 'printf "\\nSUSPEND_SHELL_READY\\n"\n'
  printf 'read -r answer; test "$answer" = resume || exit 1\n'
  if [[ $queued_interrupt == queued-int ]]; then printf 'kill -INT %%1; '; fi
  printf 'fg %%1\n'
} > "$work/launch.sh"
printf -v launch 'bash --noprofile --norc -i %q' "$work/launch.sh"
tmux -L "$name" new-session -d -s "$name" -x 120 -y 40 "$launch"
wait_for() {
  local text=$1 end=$((SECONDS+30))
  while (( SECONDS < end )); do
    tmux -L "$name" capture-pane -p -t "$name" > "$work/pane"
    if grep -Eq "$text" "$work/pane"; then return 0; fi
    sleep 0.05
  done
  tail -80 "$work/pane" >&2
  return 1
}
wait_for '^ SUSPEND_READY *$'
tmux -L "$name" send-keys -t "$name" -l 'reply with exactly: RESUMED_SOURCE_PROOF'
tmux -L "$name" send-keys -t "$name" C-z
wait_for SUSPEND_SHELL_READY
read -r pid < "$work/pid"
state=$(ps -o stat= -p "$pid")
[[ $state == *T* ]] || { printf 'job was not stopped: %s\n' "$state" >&2; exit 1; }
tmux -L "$name" send-keys -t "$name" -l resume
tmux -L "$name" send-keys -t "$name" Enter
end=$((SECONDS+30))
while :; do
  tmux -L "$name" capture-pane -p -t "$name" > "$work/pane"
  if ! grep -Fq SUSPEND_SHELL_READY "$work/pane" && grep -Fq 'reply with exactly: RESUMED_SOURCE_PROOF' "$work/pane"; then break; fi
  if (( SECONDS >= end )); then tail -80 "$work/pane" >&2; exit 1; fi
  sleep 0.05
done
tmux -L "$name" send-keys -t "$name" Enter
end=$((SECONDS+30))
while :; do
  tmux -L "$name" capture-pane -p -t "$name" > "$work/pane"
  if grep -Eq '^ RESUMED_SOURCE_PROOF *$' "$work/pane"; then break; fi
  if (( SECONDS >= end )); then tail -80 "$work/pane" >&2; exit 1; fi
  sleep 0.05
done
kill -0 "$pid"
tmux -L "$name" capture-pane -p -e -t "$name" > "$work/escaped"
python3 - "$work/escaped" <<'PY'
import json,re,sys
rows=open(sys.argv[1]).read().splitlines()
for row in rows:
    plain=re.sub(r'\x1b\[[0-?]*[ -/]*[@-~]','',row)
    if plain.strip()=='RESUMED_SOURCE_PROOF':
        print('SUSPEND_NATIVE '+json.dumps({'stopped':True,'aliveAfterResume':True,'response':row},ensure_ascii=False,separators=(',',':')))
        break
else: raise SystemExit('missing resumed response row')
PY
