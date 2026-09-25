#!/usr/bin/env bash
# Print a resource-sensitive parity scheduler configuration.
# Output format: <parallel> <group-limits>
#
# The runner still owns correctness and per-driver grouping; this script chooses
# a safe default for the current machine instead of hard-coding one contention
# profile in the Makefile. Environment overrides:
#   PIG_PARITY_PROFILE=low|medium|high|auto
#   PARITY_PARALLEL / PARITY_GROUP_LIMITS still override Makefile defaults.
set -euo pipefail

cpu_count() {
  if command -v getconf >/dev/null 2>&1; then
    getconf _NPROCESSORS_ONLN 2>/dev/null && return
  fi
  if command -v sysctl >/dev/null 2>&1; then
    sysctl -n hw.ncpu 2>/dev/null && return
  fi
  echo 4
}

free_mem_mb() {
  if [[ -r /proc/meminfo ]]; then
    awk '/MemAvailable:/ {print int($2/1024); found=1} END {if (!found) print 0}' /proc/meminfo
    return
  fi
  if command -v vm_stat >/dev/null 2>&1; then
    local page_size free inactive speculative
    page_size=$(vm_stat | awk '/page size of/ {gsub(/\./, "", $8); print $8; exit}')
    free=$(vm_stat | awk '/Pages free:/ {gsub(/\./, "", $3); print $3; exit}')
    inactive=$(vm_stat | awk '/Pages inactive:/ {gsub(/\./, "", $3); print $3; exit}')
    speculative=$(vm_stat | awk '/Pages speculative:/ {gsub(/\./, "", $3); print $3; exit}')
    page_size=${page_size:-4096}
    free=${free:-0}
    inactive=${inactive:-0}
    speculative=${speculative:-0}
    echo $(( (free + inactive + speculative) * page_size / 1024 / 1024 ))
    return
  fi
  echo 0
}

load_ceil() {
  local raw
  raw=$( (uptime | sed -E 's/.*load averages?: ([0-9.]+).*/\1/' ) 2>/dev/null || echo 0 )
  python3 - <<PY 2>/dev/null || echo 0
import math
try:
    print(math.ceil(float("$raw")))
except Exception:
    print(0)
PY
}

profile=${PIG_PARITY_PROFILE:-auto}
cpus=$(cpu_count | head -1)
mem_mb=$(free_mem_mb | head -1)
load=$(load_ceil | head -1)
cpus=${cpus:-4}
mem_mb=${mem_mb:-0}
load=${load:-0}

if [[ "$profile" == "auto" ]]; then
  # Approximate currently-free CPU capacity. Keep at least 2 lanes when the
  # machine is busy so dev loops still make progress without stampeding tmux.
  available_cpu=$(( cpus - load ))
  if (( available_cpu < 2 )); then available_cpu=2; fi

  if (( cpus >= 12 && available_cpu >= 8 && (mem_mb == 0 || mem_mb >= 12000) )); then
    profile=high
  elif (( cpus >= 8 && available_cpu >= 5 && (mem_mb == 0 || mem_mb >= 7000) )); then
    profile=medium
  else
    profile=low
  fi
fi

case "$profile" in
  high)
    # One scenario lane normally starts Pig and Pi concurrently; extension
    # lanes can add another host process. Size lanes by pairs, not scenarios,
    # so Node/Pig startup bursts cannot starve a 10s extension handshake.
    echo "4 process=4,process-ext=1,tmux=4,tmux-ext=1,ht=2,rpc=3"
    ;;
  medium)
    echo "3 process=3,process-ext=1,tmux=3,tmux-ext=1,ht=2,rpc=2"
    ;;
  low)
    echo "2 process=2,process-ext=1,tmux=2,tmux-ext=1,ht=1,rpc=1"
    ;;
  *)
    echo "unknown PIG_PARITY_PROFILE=$profile (want auto|low|medium|high)" >&2
    exit 2
    ;;
esac
