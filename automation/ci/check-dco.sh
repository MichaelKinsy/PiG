#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT

set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <base-commit> <head-commit>" >&2
  exit 2
fi

base=$1
head=$2

git rev-parse --verify "${base}^{commit}" >/dev/null
git rev-parse --verify "${head}^{commit}" >/dev/null

commits=()
while IFS= read -r commit; do
  commits+=("$commit")
done < <(git rev-list --reverse --no-merges "${base}..${head}")
missing=()

for commit in "${commits[@]}"; do
  message=$(git show -s --format=%B "$commit")
  author=$(git show -s --format='%an <%ae>' "$commit")
  committer=$(git show -s --format='%cn <%ce>' "$commit")

  if grep -Fxiq "Signed-off-by: ${author}" <<<"$message"; then
    continue
  fi
  if grep -Fxiq "Signed-off-by: ${committer}" <<<"$message"; then
    continue
  fi
  missing+=("$commit")
done

if (( ${#missing[@]} > 0 )); then
  echo "The following commits lack a DCO sign-off matching the author or committer:" >&2
  for commit in "${missing[@]}"; do
    git show -s --format='  %h %s' "$commit" >&2
  done
  echo "Create commits with: git commit --signoff" >&2
  exit 1
fi

printf 'DCO: %d non-merge commit(s) signed off\n' "${#commits[@]}"
