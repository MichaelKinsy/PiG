#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
#
# Usage: windows-native-shard.sh <shard>
#
# Print the packages that Windows native job <shard> (1 to 3) tests: the
# packages of windows-test-packages.sh without the ones the extension and cli
# jobs test. Shards 1 and 2 hold named slow packages, balanced by measured
# duration; shard 3 holds every other package, so the shards partition the list
# and a new package lands in shard 3 without editing this script.
set -euo pipefail
export LC_ALL=C

if [[ $# -ne 1 || ! $1 =~ ^[1-3]$ ]]; then
  echo "usage: $0 <shard 1-3>" >&2
  exit 2
fi
ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
module=github.com/MichaelKinsy/PiG
all=$("$ROOT/automation/ci/windows-test-packages.sh")
# grep -v exits 1 when nothing remains; an empty list is caught below.
all=$(printf '%s\n' "$all" | grep -v -x -e "$module/coding/extension/host/runtimecell" -e "$module/coding/extension/host/subprocess" -e "$module/cmd/pig" || true)
shard1=("$module/coding" "$module/tui")
shard2=("$module/internal/experimental" "$module/ai")
case $1 in
  1) printf '%s\n' "${shard1[@]}" ;;
  2) printf '%s\n' "${shard2[@]}" ;;
  3) printf '%s\n' "$all" | grep -v -x -F -e "${shard1[0]}" -e "${shard1[1]}" -e "${shard2[0]}" -e "${shard2[1]}" || true ;;
esac | while IFS= read -r pkg; do
  # Every named package must still be in the list, so a rename fails loudly. The list comes from a here-string, not a
  # pipe: under pipefail, `grep -q` exiting on its match would leave a piping printf to die of SIGPIPE.
  if ! grep -qx -F -- "$pkg" <<<"$all"; then
    echo "windows-native-shard: $pkg is not a Windows test package" >&2
    exit 1
  fi
  printf '%s\n' "$pkg"
done
