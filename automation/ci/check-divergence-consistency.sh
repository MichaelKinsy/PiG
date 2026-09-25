#!/usr/bin/env bash
# Enforce unique ledger IDs and typed source markers.

set -euo pipefail

ROOT="${PIG_DIVERGENCE_ROOT:-$(cd "$(dirname "$0")/../.." && pwd)}"
cd "$ROOT"

if [[ ! -f DIVERGENCES.md ]]; then
  echo "FAIL: DIVERGENCES.md missing"
  exit 1
fi

headings() {
  local path=$1
  [[ -f "$path" ]] || return 0
  grep -oE '^## D[0-9]+' "$path" | grep -oE 'D[0-9]+' || true
}

markers() {
  local kind=$1
  grep -rohE "pig ${kind} \(D[0-9]+\)" \
    --include='*.go' --include='*.mjs' --include='*.py' --include='*.rs' \
    --exclude='*_test.go' --exclude-dir=.git --exclude-dir=.upstream \
    --exclude-dir=.next --exclude-dir=node_modules --exclude-dir=out . \
    | grep -oE 'D[0-9]+' | sort -u || true
}

core=$(headings DIVERGENCES.md | sort -u)
additive=$(headings docs/additive-features.md | sort -u)
all=$(printf '%s\n%s\n' "$core" "$additive" | sed '/^$/d' | sort)
divergence_refs=$(markers divergence)
additive_refs=$(markers additive)
untyped_markers=$(grep -RInE '^[[:space:]]*(//|#).*pig (divergence|additive)(:| [^(])' \
  --include='*.go' --include='*.mjs' --include='*.py' --include='*.rs' \
  --exclude-dir=.git --exclude-dir=.upstream --exclude-dir=.next \
  --exclude-dir=node_modules --exclude-dir=out . || true)

duplicates=$(printf '%s\n' "$all" | uniq -d)
missing_divergence=$(comm -23 <(printf '%s\n' "$core") <(printf '%s\n' "$divergence_refs"))
unknown_divergence=$(comm -13 <(printf '%s\n' "$core") <(printf '%s\n' "$divergence_refs"))
missing_additive=$(comm -23 <(printf '%s\n' "$additive") <(printf '%s\n' "$additive_refs"))
unknown_additive=$(comm -13 <(printf '%s\n' "$additive") <(printf '%s\n' "$additive_refs"))

# bullets prints each line of $1 as a "  - " list item.
bullets() {
  while IFS= read -r line; do
    printf '  - %s\n' "$line"
  done <<<"$1"
}

failed=0
if [[ -n "$untyped_markers" ]]; then
  echo "FAIL: untyped divergence/additive comments:"
  bullets "$untyped_markers"
  failed=1
fi
if [[ -n "$duplicates" ]]; then
  echo "FAIL: divergence/additive IDs must be globally unique across all ledgers:"
  bullets "$duplicates"
  failed=1
fi
if [[ -n "$missing_divergence" ]]; then
  echo "FAIL: core divergences without 'pig divergence' source markers:"
  bullets "$missing_divergence"
  failed=1
fi
if [[ -n "$unknown_divergence" ]]; then
  echo "FAIL: 'pig divergence' markers without core divergence records:"
  bullets "$unknown_divergence"
  failed=1
fi
if [[ -n "$missing_additive" ]]; then
  echo "FAIL: additive features without 'pig additive' source markers:"
  bullets "$missing_additive"
  failed=1
fi
if [[ -n "$unknown_additive" ]]; then
  echo "FAIL: 'pig additive' markers without additive feature records:"
  bullets "$unknown_additive"
  failed=1
fi
if (( failed )); then
  exit 1
fi

count=$(printf '%s\n' "$all" | sed '/^$/d' | wc -l | tr -d ' ')
echo "divergence consistency: OK ($count typed records with source markers)"
