#!/bin/sh
# SPDX-License-Identifier: MIT
# Run one measurement job under the shared xd670 lock on the reserved CPUs.
#   PIG_BIN=/path/to/pig [PHI_BIN=...] run.sh first-frame --subject pig --mode warm --runs 30 --out out.jsonl
#   run.sh summarize out.jsonl
# The lock is held for the whole invocation, so call it once per (subject, mode) job and never run two at once.
here=$(cd "$(dirname "$0")" && pwd)
if [ "$1" = summarize ]; then exec python3 "$here/ptyprobe.py" "$@"; fi
exec flock /tmp/perf-xd670.lock taskset -c "${PROBE_CPUS:-0-63}" python3 "$here/ptyprobe.py" "$@"
