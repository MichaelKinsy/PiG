#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Keeps `vendorHash` in nix/package.nix in step with the Go modules cmd/pig links.
#
#   nix/vendor-hash.sh check    exit 0 when vendorHash is current; otherwise print the expected hash and exit 1
#   nix/vendor-hash.sh update   rewrite vendorHash in nix/package.nix to the expected hash
#
# It builds only the fixed-output module cache (`.#pig.goModules`), so it takes seconds, not a full package build. The hash is
# the same on every system. It changes with go.mod, go.sum, go.work, go.work.sum, and with any import that pulls a different
# set of modules into cmd/pig. Nix is required; run it where Nix is installed, or let the "Nix vendor hash" workflow report the
# value.
set -euo pipefail

mode=${1:-}
case "$mode" in
check | update) ;;
*)
	echo "usage: $0 check|update" >&2
	exit 2
	;;
esac

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
package="$root/nix/package.nix"

current=$(sed -n 's/^[[:space:]]*vendorHash = "\(sha256-[^"]*\)";[[:space:]]*$/\1/p' "$package")
if [ "$(printf '%s\n' "$current" | grep -c .)" -ne 1 ]; then
	echo "vendor-hash: expected exactly one vendorHash line in $package" >&2
	exit 2
fi

log=$(mktemp)
trap 'rm -f "$log"' EXIT

if nix build --no-link --print-build-logs "$root#pig.goModules" >"$log" 2>&1; then
	echo "vendorHash is current: $current"
	exit 0
fi

expected=$(sed -n 's/^[[:space:]]*got:[[:space:]]*\(sha256-[A-Za-z0-9+\/]*=*\)[[:space:]]*$/\1/p' "$log" | sort -u)
if [ "$(printf '%s\n' "$expected" | grep -c .)" -ne 1 ]; then
	echo "vendor-hash: the module cache failed to build for a reason other than a stale hash:" >&2
	cat "$log" >&2
	exit 2
fi

if [ "$mode" = update ]; then
	tmp=$(mktemp)
	sed "s|vendorHash = \"$current\";|vendorHash = \"$expected\";|" "$package" >"$tmp"
	cat "$tmp" >"$package"
	rm -f "$tmp"
	echo "vendorHash updated: $current -> $expected"
	exit 0
fi

echo "vendorHash in nix/package.nix is stale." >&2
echo "  specified: $current" >&2
echo "  expected:  $expected" >&2
echo "Run nix/vendor-hash.sh update with Nix installed, or set vendorHash = \"$expected\"; in nix/package.nix." >&2
if [ -n "${GITHUB_ACTIONS:-}" ]; then
	echo "::error file=nix/package.nix,title=Stale vendorHash::set vendorHash = \"$expected\"; (specified $current)"
fi
exit 1
