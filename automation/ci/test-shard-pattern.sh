#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
#
# Usage: test-shard-pattern.sh <shard> <shards> <package>
#
# Print the go test -run pattern that selects shard <shard> (1-based) of
# <shards> of the package's top-level tests, examples and fuzz targets. A test
# belongs to the shard given by the FNV-1a hash of its name, so the shards
# partition the package exactly once and a new test lands in one shard without
# editing a list. Each shard runs as its own go test process, under its own
# package timeout.
set -euo pipefail
export LC_ALL=C

if [[ $# -ne 3 || ! $1 =~ ^[1-9][0-9]*$ || ! $2 =~ ^[1-9][0-9]*$ || $1 -gt $2 ]]; then
  echo "usage: $0 <shard> <shards> <package>" >&2
  exit 2
fi
shard=$1 shards=$2 package=$3

# Capture the listing in the shell itself: a go test failure inside a pipeline
# or process substitution would not stop the script and would read as no tests.
listing=$(go test -list . "$package")
names=()
while IFS= read -r line; do
  if [[ $line =~ ^(Test|Example|Fuzz)[A-Za-z0-9_]*$ ]]; then
    names+=("$line")
  elif [[ $line =~ ^(Test|Example|Fuzz) ]]; then
    # A Go identifier may hold non-ASCII letters. Fail rather than leave such a
    # test out of every shard.
    echo "test-shard-pattern: $package lists $line, which is not an ASCII test name" >&2
    exit 1
  fi
done <<<"$listing"
if [[ ${#names[@]} -eq 0 ]]; then
  echo "test-shard-pattern: $package lists no tests" >&2
  exit 1
fi

hash=0
fnv1a32() {
  local name=$1 i code
  hash=2166136261
  for ((i = 0; i < ${#name}; i++)); do
    printf -v code '%d' "'${name:i:1}"
    hash=$(((hash ^ code) * 16777619 & 0xFFFFFFFF))
  done
}

selected=()
for name in "${names[@]}"; do
  fnv1a32 "$name"
  if ((hash % shards + 1 == shard)); then
    selected+=("$name")
  fi
done
if [[ ${#selected[@]} -eq 0 ]]; then
  echo "test-shard-pattern: shard $shard of $shards of $package selects no tests" >&2
  exit 1
fi
pattern=$(IFS='|'; printf '^(%s)$' "${selected[*]}")
# Windows limits a command line to 32767 characters, and go test passes the
# pattern on to the test binary.
if [[ ${#pattern} -gt 24000 ]]; then
  echo "test-shard-pattern: shard $shard of $shards of $package needs a ${#pattern}-character pattern; raise the shard count" >&2
  exit 1
fi
printf '%s\n' "$pattern"
