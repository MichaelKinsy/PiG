#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Race-coverage drift gate. Every Go package that starts goroutines or timers in non-test code and has tests must be
# named in automation/ci/race-packages.txt, either in a nightly -race group or as an exempt entry with its reason, so a
# new concurrency owner cannot ship outside every -race run unnoticed (the class-5 audit found `coding`, `coding/cli`,
# `coding/extension` and the extension SDK module outside all of them). A nested Go module is one entry (its directory),
# run as its own module by test-race-nightly.sh. Packages that only share state behind a mutex may be listed too. The
# gate also fails on an entry whose directory is gone, on a group the nightly workflow's matrix does not run, on a
# duplicate entry, and on an exempt entry without a reason.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"
list=automation/ci/race-packages.txt
workflow=.github/workflows/nightly.yml

# Goroutine and timer starts: `go f(...)`, `go func(`, errgroup/WaitGroup `.Go(`, time.AfterFunc.
spawn='(^|[^[:alnum:]_.])go[[:space:]]+[[:alpha:]_(][[:alnum:]_.]*\(|\.Go\(func|time\.AfterFunc\('

# Directories that are not product code, plus Go modules nested under them.
skip='^\./(\.upstream|\.git|bench|docs|test|node_modules|third_party|automation|examples)(/|$)|/testdata(/|$)'

# Nested module roots (every go.mod below the repository root that is product code), longest first.
modules=()
while IFS= read -r mod; do
  dir=${mod%/go.mod}
  [[ "$dir" == . ]] && continue
  grep -Eq "$skip" <<<"$dir/" && continue
  modules+=("$dir")
done < <(find . -name go.mod -not -path './.upstream/*' -not -path '*/node_modules/*' | sort)

unit_of() { # directory -> its coverage unit (the nested module that contains it, or itself)
  local d=$1 m
  for m in "${modules[@]}"; do
    case "$d/" in "$m"/*) printf '%s\n' "$m"; return ;; esac
  done
  printf '%s\n' "$d"
}

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
: >"$work/needed"
while IFS= read -r file; do
  dir=${file%/*}
  grep -Eq "$skip" <<<"$dir/" && continue
  unit=$(unit_of "$dir")
  # A unit with no tests has nothing to run under -race.
  if [[ "$unit" == "$dir" ]]; then
    compgen -G "$dir/*_test.go" >/dev/null || continue
  else
    [[ -n $(find "$unit" -name '*_test.go' -print -quit) ]] || continue
  fi
  printf '%s\n' "$unit" >>"$work/needed"
done < <(grep -rlE "$spawn" --include='*.go' . 2>/dev/null | grep -v '_test\.go$' | sort)
sort -u -o "$work/needed" "$work/needed"

: >"$work/listed"
fail=0
while IFS= read -r line; do
  line=${line%%#*}
  [[ -z "${line//[[:space:]]/}" ]] && continue
  read -r group path reason <<<"$line"
  if [[ -z "${path:-}" ]]; then echo "race-coverage: malformed line in $list: $line" >&2; fail=1; continue; fi
  if [[ "$group" == exempt && -z "${reason:-}" ]]; then echo "race-coverage: exempt entry $path has no reason" >&2; fail=1; fi
  if [[ "$group" != exempt ]] && ! grep -Eq "group:.*[[:space:],[]${group}[],[:space:]]" "$workflow"; then
    echo "race-coverage: group $group of $path is not in the matrix of $workflow" >&2; fail=1
  fi
  if [[ ! -d "$path" ]]; then echo "race-coverage: $list names $path, which does not exist" >&2; fail=1; continue; fi
  printf '%s\n' "$path" >>"$work/listed"
done <"$list"
sort "$work/listed" >"$work/listed.sorted"
duplicate=$(uniq -d "$work/listed.sorted")
if [[ -n "$duplicate" ]]; then echo "race-coverage: listed twice in $list: $duplicate" >&2; fail=1; fi
sort -u -o "$work/listed.sorted" "$work/listed.sorted"

while IFS= read -r path; do
  echo "race-coverage: $path starts goroutines or timers and has tests but is in no -race group; add it to $list (a nightly group, or exempt with a reason)" >&2
  fail=1
done < <(comm -23 "$work/needed" "$work/listed.sorted")
if [[ $fail -ne 0 ]]; then exit 1; fi
echo "race-coverage: $(wc -l <"$work/needed" | tr -d ' ') concurrency packages, each in a nightly -race group or exempt with a reason."
