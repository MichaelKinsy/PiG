#!/usr/bin/env bash
# Fail when a test run left files or directories in the scratch temporary directory it ran under. Usage: assert-clean-tmp.sh <scratch-dir>
# Node shares these compile caches across processes on purpose, and they carry no PiG prefix. A go-build* directory is one go command's work directory (GOTMPDIR), which go removes on exit, so a remaining one is a killed build's leak.
set -euo pipefail

scratch=${1:?usage: assert-clean-tmp.sh <scratch-dir>}
leftover=()
while IFS= read -r -d '' entry; do
  case "${entry##*/}" in
    node-compile-cache | v8-compile-cache-*) ;;
    *) leftover+=("$entry") ;;
  esac
done < <(find "$scratch" -mindepth 1 -maxdepth 1 -print0)

if [[ ${#leftover[@]} -gt 0 ]]; then
  echo "[assert-clean-tmp] ${#leftover[@]} entries leaked into $scratch:" >&2
  printf '  %s\n' "${leftover[@]:0:40}" >&2
  exit 1
fi
