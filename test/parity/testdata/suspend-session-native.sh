#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/../../.." && pwd)
kind=$1
if [[ $kind == pig ]]; then
  binary=${PIG_PARITY_PIG_BIN:-${PIG_BIN:?set PIG_PARITY_PIG_BIN or PIG_BIN}}
  args=(--no-extensions --model test-faux/faux-1)
else
  binary=${PIG_PARITY_PI_BIN:-$(command -v pi)}
  pinned=$(sed -n 's/^const UpstreamPackageVersion = "\([^"]*\)"$/\1/p' "$root/internal/coding/pigversion/pigversion.go")
  [[ -n $pinned && $("$binary" --version) == "$pinned" ]] || { printf 'expected pinned Pi %s\n' "$pinned" >&2; exit 1; }
  args=(--no-extensions -e "$root/test/parity/testdata/test-faux-provider.ts" --model test-faux/faux-1)
fi
# An explicit queued-int invocation retains the unsynchronized stopped-job stress probe. Its two legal outcomes are covered deterministically by suspend-signal-order.py.
queued_interrupt=${2:-}
args+=("reply with exactly: SUSPEND_READY")
work=$(mktemp -d)
name="parity-suspend-${kind}-$$"
# tmux leaves the socket of a server that kill-session ended; the cleanup removes it.
socket="${TMUX_TMPDIR:-/tmp}/tmux-$(id -u)/$name"
# The job is the program under test. It runs in its own process group (set -m), so a signal to the pane's shell does not reach it
# once the shell stops forwarding: end it explicitly, and then the session that held it.
cleanup() {
  local job=
  if [[ -s $work/pid ]]; then read -r job < "$work/pid" || true; fi
  if [[ $job =~ ^[0-9]+$ ]]; then kill -KILL -- "-$job" 2>/dev/null || true; kill -KILL "$job" 2>/dev/null || true; fi
  tmux -L "$name" kill-session -t "$name" 2>/dev/null || true
  rm -f -- "$socket"
  rm -rf "$work"
}
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
# The runner ends a scenario that overruns its budget with SIGKILL, which runs no trap: this watcher outlives the script, sees it
# gone and removes the tmux server, the pane's shell and the program it held. After a normal exit it finds the work directory
# already removed and ends the session a second time, which is harmless. It starts before the session, so a kill that comes after the
# session exists always finds it running.
script_pid=$$
(
  while kill -0 "$script_pid" 2>/dev/null; do sleep 0.2; done
  job=
  if [[ -s $work/pid ]]; then read -r job < "$work/pid" || true; fi
  if [[ $job =~ ^[0-9]+$ ]]; then kill -KILL -- "-$job" 2>/dev/null || true; kill -KILL "$job" 2>/dev/null || true; fi
  tmux -L "$name" kill-session -t "$name" 2>/dev/null || true
  rm -f -- "$socket"
  rm -rf "$work"
) </dev/null >/dev/null 2>&1 &
disown
tmux -L "$name" new-session -d -s "$name" -x 120 -y 40 "$launch"
wait_for() {
  local text=$1 end=$((SECONDS+${SUSPEND_PARITY_WAIT_SECONDS:-30}))
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
end=$((SECONDS+${SUSPEND_PARITY_WAIT_SECONDS:-30}))
while :; do
  tmux -L "$name" capture-pane -p -t "$name" > "$work/pane"
  if ! grep -Fq SUSPEND_SHELL_READY "$work/pane" && grep -Fq 'reply with exactly: RESUMED_SOURCE_PROOF' "$work/pane"; then break; fi
  if (( SECONDS >= end )); then tail -80 "$work/pane" >&2; exit 1; fi
  sleep 0.05
done
tmux -L "$name" send-keys -t "$name" Enter
end=$((SECONDS+${SUSPEND_PARITY_WAIT_SECONDS:-30}))
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
