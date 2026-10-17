#!/usr/bin/env bash
# Replay a whole-repository gate's passing result when nothing it reads has changed.
# Usage: cached-gate.sh <name> [--out <dir>]... -- <command...>
#
# The key hashes the go toolchain version, the command, the gate's GOFLAGS/GOOS/GOARCH/CGO_ENABLED/GOWORK, the pinned upstream mirror version, every tracked file's index entry, the working tree's difference from the index, and every untracked, unignored file's content. A gate keyed this way reads nothing else: Go sources, ledgers, lockfiles and the upstream mirror are all tracked or pinned. On a hit the stored output is printed again, the stored --out directories are restored, and the script exits 0. Only passing runs are stored, so a failure always runs again and reports paths of the current checkout. The store is $PIG_CACHE_HOME/pig-gates (default ~/.cache), shared by every checkout on the host. CI (CI set) and PIG_GATE_CACHE=0 always run the command.
set -euo pipefail

name=${1:?usage: cached-gate.sh <name> [--out <dir>]... -- <command...>}
shift
outs=()
while [[ $# -gt 0 && "$1" != -- ]]; do
  case "$1" in
    --out) outs+=("$2"); shift 2 ;;
    *) echo "cached-gate: unknown option $1" >&2; exit 2 ;;
  esac
done
[[ "${1:-}" == -- ]] || { echo "usage: cached-gate.sh <name> [--out <dir>]... -- <command...>" >&2; exit 2; }
shift

if [[ -n "${CI:-}" || "${PIG_GATE_CACHE:-1}" == 0 ]]; then
  exec "$@"
fi

if command -v sha256sum >/dev/null 2>&1; then hasher=(sha256sum); else hasher=(shasum -a 256); fi
# The gate's own outputs are not its inputs.
excludes=()
for dir in "${outs[@]}"; do excludes+=(":(exclude)$dir"); done
key=$(
  {
    go version
    printf '%s\n' "$name" "$*" "${outs[*]:-}" "GOFLAGS=${GOFLAGS:-}" "GOOS=${GOOS:-}" "GOARCH=${GOARCH:-}" "CGO_ENABLED=${CGO_ENABLED:-}" "GOWORK=${GOWORK:-}"
    readlink .upstream/current 2>/dev/null || true
    git ls-files --stage
    git diff --no-ext-diff --binary
    git ls-files --others --exclude-standard -z -- . "${excludes[@]}" | xargs -0 -r "${hasher[@]}"
  } | "${hasher[@]}" | cut -c1-40
)
store="${PIG_CACHE_HOME:-${XDG_CACHE_HOME:-$HOME/.cache}}/pig-gates/$name"
entry="$store/$key"

if [[ -f "$entry/ok" ]]; then
  cat "$entry/log"
  for i in "${!outs[@]}"; do
    rm -rf "${outs[i]}"
    mkdir -p "${outs[i]}"
    tar -C "${outs[i]}" -xf "$entry/out-$i.tar"
  done
  touch "$entry"
  echo "[cached-gate] $name: unchanged inputs; replayed the passing run from $(date -r "$entry/log" -u +%FT%TZ) (PIG_GATE_CACHE=0 reruns)" >&2
  exit 0
fi

mkdir -p "$store"
tmp=$(mktemp -d "$store/.run.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
status=0
"$@" > >(tee "$tmp/log") 2>&1 || status=$?
wait
if [[ $status -eq 0 ]]; then
  for i in "${!outs[@]}"; do
    tar -C "${outs[i]}" -cf "$tmp/out-$i.tar" .
  done
  touch "$tmp/ok"
  rm -rf "$entry"
  mv "$tmp" "$entry" 2>/dev/null || true
  # Entries unused for a week go; touch on every hit keeps the live ones.
  find "$store" -mindepth 1 -maxdepth 1 -type d ! -name '.run.*' -mtime +7 -exec rm -rf {} + 2>/dev/null || true
fi
exit "$status"
