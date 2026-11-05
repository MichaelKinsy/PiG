#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
#
# Fails unless every non-merge commit in <base-commit>..<head-commit> carries a
# "Signed-off-by:" line matching its author or its committer.
#
# A commit that lacks one is also accepted when a LATER commit in the same range
# retroactively signs it off. That remediation commit must:
#   - have the same author as the unsigned commit: the same name and email, or two
#     identities that dco-identities.txt (next to this script) lists as one person;
#   - carry its own "Signed-off-by:" line matching that author;
#   - carry a remediation block naming the unsigned commit, which follows the DCO
#     bot convention: a lead line, then one "<sha> <subject>" row per commit, up
#     to the next blank line. A sha is a hex prefix of at least 10 characters.
#
#       I, Jane Doe, retroactively sign off on these commits:
#       0123456789abcdef0123456789abcdef01234567 fix the parser
#       89abcdef01 add the flag
#
#       Signed-off-by: Jane Doe <jane@example.com>
#
# The name in the lead line must be the remediation commit author's name. A
# remediation by anyone else, or one that precedes the commit it names, never
# counts. Create it with
#   git commit --allow-empty --signoff -S
#
# dco-identities.txt lists one person per line as identities joined by " = ":
#   Jane Doe <jane@users.noreply.example.com> = Jane Doe <jane@example.com>
# Blank lines and lines starting with "#" are ignored. The map is read from the
# checked-out tree, so changing it needs the review CODEOWNERS requires.

set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <base-commit> <head-commit>" >&2
  exit 2
fi

base=$1
head=$2

git rev-parse --verify "${base}^{commit}" >/dev/null
git rev-parse --verify "${head}^{commit}" >/dev/null

min_prefix=10

# Identity groups, one per line, each padded so " = <identity> = " matches whole identities.
groups=()
identities=$(dirname "${BASH_SOURCE[0]}")/dco-identities.txt
if [[ -f $identities ]]; then
  while IFS= read -r line || [[ -n $line ]]; do
    line=${line%%#*}
    if [[ $line == *" = "* ]]; then
      groups+=(" = ${line%"${line##*[![:space:]]}"} = ")
    fi
  done <"$identities"
fi

# has_signoff <identity> <message>: succeed when the message has a "Signed-off-by: <identity>" line,
# compared case-insensitively. Pure bash: it runs for every commit in the range.
has_signoff() {
  local nl=$'\n' found=1
  shopt -s nocasematch
  if [[ "${nl}$2${nl}" == *"${nl}Signed-off-by: $1${nl}"* ]]; then
    found=0
  fi
  shopt -u nocasematch
  return "$found"
}

# same_person <identity> <identity>: succeed when they are equal or share a group.
same_person() {
  local group g
  [[ $1 == "$2" ]] && return 0
  for (( g = 0; g < ${#groups[@]}; g++ )); do
    group=${groups[g]}
    [[ $group == *" = $1 = "* && $group == *" = $2 = "* ]] && return 0
  done
  return 1
}

# One git process for the whole range: records end in NUL, fields in US (0x1f).
# Per commit this keeps whether it is signed off (signed), whether it carries a
# sign-off for its own author (self_signed, what a remediation needs), and the
# message only when it holds a remediation lead line; the rest is discarded to
# keep the shell small.
lead_phrase=", retroactively sign off on these commits:"
commits=()
names=()
authors=()
signed=()
self_signed=()
remediations=()
while IFS= read -r -d '' record; do
  fields=()
  rest=$record
  for (( f = 0; f < 5; f++ )); do
    fields+=("${rest%%$'\037'*}")
    rest=${rest#*$'\037'}
  done
  author="${fields[1]} <${fields[2]}>"
  commits+=("${fields[0]}")
  names+=("${fields[1]}")
  authors+=("$author")
  if has_signoff "$author" "$rest"; then
    self_signed+=(1)
    signed+=(1)
  else
    self_signed+=(0)
    if has_signoff "${fields[3]} <${fields[4]}>" "$rest"; then
      signed+=(1)
    else
      signed+=(0)
    fi
  fi
  if [[ $rest == *"$lead_phrase"* ]]; then
    remediations+=("$rest")
  else
    remediations+=("")
  fi
done < <(git log --reverse --no-merges -z --format='%H%x1f%an%x1f%ae%x1f%cn%x1f%ce%x1f%B' "${base}..${head}")

# Print the sha prefixes a remediation block in $2 names for the author name $1.
remediated_prefixes() {
  REMEDIATION_LEAD="I, $1, retroactively sign off on these commits:" awk '
    $0 == ENVIRON["REMEDIATION_LEAD"] { inblock = 1; next }
    inblock && $0 ~ /^[[:space:]]*$/ { inblock = 0 }
    inblock { print $1 }
  ' <<<"$2"
}

# sha_has_prefix <sha> <hex prefix>: succeed when the prefix starts the sha, ignoring case.
sha_has_prefix() {
  local found=1
  shopt -s nocasematch
  if [[ $1 == "$2"* ]]; then
    found=0
  fi
  shopt -u nocasematch
  return "$found"
}

# remediated <index>: succeed when a later commit in the range remediates commits[index].
remediated() {
  local index=$1 later prefix
  local sha=${commits[index]} author=${authors[index]}
  for (( later = index + 1; later < ${#commits[@]}; later++ )); do
    same_person "$author" "${authors[later]}" || continue
    (( self_signed[later] )) || continue
    [[ -n ${remediations[later]} ]] || continue
    while IFS= read -r prefix; do
      if (( ${#prefix} >= min_prefix )) && [[ $prefix =~ ^[0-9a-fA-F]+$ ]] && sha_has_prefix "$sha" "$prefix"; then
        notes+=("  ${sha:0:10} signed off retroactively by ${commits[later]:0:10}")
        return 0
      fi
    done < <(remediated_prefixes "${names[later]}" "${remediations[later]}")
  done
  return 1
}

notes=()

missing=()
for (( i = 0; i < ${#commits[@]}; i++ )); do
  if (( signed[i] )) || remediated "$i"; then
    continue
  fi
  missing+=("${commits[i]}")
done

if (( ${#missing[@]} > 0 )); then
  echo "The following commits lack a DCO sign-off matching the author or committer:" >&2
  for commit in "${missing[@]}"; do
    git show -s --format='  %h %s' "$commit" >&2
  done
  echo "Create commits with: git commit --signoff" >&2
  exit 1
fi

for (( i = 0; i < ${#notes[@]}; i++ )); do
  echo "${notes[i]}" >&2
done
printf 'DCO: %d non-merge commit(s) signed off\n' "${#commits[@]}"
