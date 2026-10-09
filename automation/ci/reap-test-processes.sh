#!/usr/bin/env bash
# Kill and report the processes a test run left behind. Usage: reap-test-processes.sh <scratch-dir>
# test-grouped.sh runs each group with TMPDIR=<scratch-dir>, and every process a test starts inherits it, or a directory beneath it when a TestMain scoped its own. A process of this user whose environment still names such a directory after go test returned outlived the test that started it: a tmux server, a Node oracle, a helper re-executing the test binary. It is killed so it cannot hold CPU, memory or a terminal after the run, and the group fails so the leak is fixed at its source.
# Hosts without /proc (macOS) have no portable way to read another process's environment; the script reports nothing there.
set -euo pipefail

scratch=${1:?usage: reap-test-processes.sh <scratch-dir>}
[[ -d /proc/self ]] || exit 0

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
