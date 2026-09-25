#!/usr/bin/env bash
# Fail if DIVERGENCES.md gained entries without a per-entry
# SCRUTINIZED:approved tag.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

if [[ ! -f DIVERGENCES.md ]]; then
  echo "release-check: DIVERGENCES.md missing"
  exit 1
fi

git_prefix=$(git rev-parse --show-prefix 2>/dev/null || true)
previous=$(mktemp "${TMPDIR:-/tmp}/pig.XXXXXXXX")
trap 'rm -f "$previous"' EXIT
git show "HEAD:${git_prefix}DIVERGENCES.md" >"$previous" 2>/dev/null || :

current_ids=$(grep -oE '^## D[0-9]+' DIVERGENCES.md | grep -oE 'D[0-9]+' | sort -u || true)
previous_ids=$(grep -oE '^## D[0-9]+' "$previous" | grep -oE 'D[0-9]+' | sort -u || true)
added=$(comm -13 <(printf '%s\n' "$previous_ids" | sed '/^$/d') <(printf '%s\n' "$current_ids" | sed '/^$/d'))

if [[ -n "$added" ]]; then
  count=$(printf '%s\n' "$added" | wc -l | tr -d ' ')
  echo "release-check: $count new divergence(s) since HEAD."
  failed=0
  while IFS= read -r id; do
    if ! awk -v id="$id" '
      $0 ~ "^## " id " " { section=1; next }
      section && /^## D[0-9]+ / { exit }
      section && /SCRUTINIZED:approved/ { approved=1 }
      END { exit approved ? 0 : 1 }
    ' DIVERGENCES.md; then
      echo "FAIL: $id is missing SCRUTINIZED:approved in its own section."
      failed=1
    fi
  done <<<"$added"
  if ((failed)); then
    exit 1
  fi
fi

active=$(printf '%s\n' "$current_ids" | sed '/^$/d' | wc -l | tr -d ' ')
echo "release-check: divergence delta OK ($active active)"
