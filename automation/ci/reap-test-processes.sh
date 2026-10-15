#!/usr/bin/env bash
# Kill and report the processes a test run left behind. Usage: reap-test-processes.sh <scratch-dir>
# test-grouped.sh runs each group with TMPDIR=<scratch-dir>, and every process a test starts inherits it, or a directory beneath it when a TestMain scoped its own. A process of this user whose environment still names such a directory after go test returned outlived the test that started it: a tmux server, a Node oracle, a helper re-executing the test binary. It is killed so it cannot hold CPU, memory or a terminal after the run, and the group fails so the leak is fixed at its source.
# Hosts without /proc (macOS) read each process's environment from `ps -E`, which appends it to the command line, so a process is matched by a ` TMPDIR=<scratch>` word instead of its environ file. REAP_USE_PS=1 selects that path on any host, which is how it is tested on Linux.
set -euo pipefail

scratch=${1:?usage: reap-test-processes.sh <scratch-dir>}
use_ps=${REAP_USE_PS:-}
[[ -d /proc/self ]] || use_ps=1

if [[ -n "$use_ps" ]]; then
  # Exclude this script and every ancestor: their environment may name the scratch directory too.
  declare -A keep=()
  pid=$$
  while [[ -n "$pid" && "$pid" != 0 && -z "${keep[$pid]:-}" ]]; do
    keep[$pid]=1
    pid=$(ps -o ppid= -p "$pid" 2>/dev/null | tr -d ' ' || true)
  done
  leaked=()
  # ps -E appends the environment to the command; -ww lifts the width limit so TMPDIR is not cut off.
  while read -r pid args; do
    [[ -n "${keep[$pid]:-}" ]] && continue
    [[ " $args " == *" TMPDIR=$scratch "* || " $args" == *" TMPDIR=$scratch/"* ]] || continue
    [[ "$args" == *"** telemetry **"* ]] && continue
    leaked+=("$pid ${args:0:200}")
    kill -KILL "$pid" 2>/dev/null || true
  done < <(ps -Eww -x -o pid= -o command= 2>/dev/null || true)
  if [[ ${#leaked[@]} -gt 0 ]]; then
    echo "[reap-test-processes] ${#leaked[@]} processes outlived the tests that started them under $scratch (killed):" >&2
    printf '  %s\n' "${leaked[@]:0:40}" >&2
    exit 1
  fi
  exit 0
fi

# Exclude this script and every ancestor: they run with the caller's environment, which may name the scratch directory.
declare -A keep=()
pid=$$
while [[ -n "$pid" && "$pid" != 0 ]]; do
  keep[$pid]=1
  pid=$(awk '/^PPid:/ {print $2}' "/proc/$pid/status" 2>/dev/null || true)
done

leaked=()
# One grep over every readable environment finds the candidates; the loop then checks each exactly.
mapfile -t candidates < <(grep -lsz -F "TMPDIR=$scratch" /proc/[0-9]*/environ 2>/dev/null || true)
for file in "${candidates[@]}"; do
  dir=${file%/environ}
  pid=${dir#/proc/}
  [[ -n "${keep[$pid]:-}" ]] && continue
  [[ -O "$dir" ]] || continue
  env=$( { tr '\0' '\n' <"$dir/environ"; } 2>/dev/null) || continue
  tmpdir=$(sed -n 's/^TMPDIR=//p' <<<"$env" | head -1)
  [[ "$tmpdir" == "$scratch" || "$tmpdir" == "$scratch"/* ]] || continue
  args=$( { tr '\0' ' ' <"$dir/cmdline"; } 2>/dev/null) || continue
  # The go command's telemetry child is detached by design and exits on its own; it is not a test's process.
  [[ "$args" == *"** telemetry **"* ]] && continue
  leaked+=("$pid $(cat "$dir/comm" 2>/dev/null): ${args:0:200}")
  kill -KILL "$pid" 2>/dev/null || true
done

if [[ ${#leaked[@]} -gt 0 ]]; then
  echo "[reap-test-processes] ${#leaked[@]} processes outlived the tests that started them under $scratch (killed):" >&2
  printf '  %s\n' "${leaked[@]:0:40}" >&2
  exit 1
fi
