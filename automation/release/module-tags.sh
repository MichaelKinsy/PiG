#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
#
# module-tags.sh VERSION [GO_MOD]
#
# Prints the Go module tags a PiG release VERSION (no v prefix) must create on
# its one release commit, one per line: the root tag vVERSION first, then
# <dir>/vVERSION for every nested PiG module the root go.mod requires (for
# example extensions/sdk/v0.2.0). `go install
# github.com/MichaelKinsy/PiG/cmd/pig@vVERSION` resolves those nested modules
# through their own tags, so every one must exist on the same commit.
#
# Fails closed when the root go.mod (default: ./go.mod) carries a replace or
# exclude directive (go install pkg@version refuses both), or requires a
# nested PiG module at any version other than vVERSION, or names a nested
# module whose directory has no go.mod declaring it.
set -euo pipefail

version=${1:?usage: module-tags.sh VERSION [GO_MOD]}
gomod=${2:-go.mod}
root_dir=$(dirname "$gomod")
root_module=github.com/MichaelKinsy/PiG

[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]] ||
	{ echo "module-tags.sh: $version is not a semantic version without a v prefix" >&2; exit 2; }
[[ -f $gomod ]] || { echo "module-tags.sh: $gomod not found" >&2; exit 2; }
grep -Eq "^module ${root_module}[[:space:]]*$" "$gomod" ||
	{ echo "module-tags.sh: $gomod is not module $root_module" >&2; exit 1; }

if grep -Eq '^[[:space:]]*(replace|exclude)([[:space:]]|\()' "$gomod"; then
	echo "module-tags.sh: $gomod has replace or exclude directives; go install $root_module/cmd/pig@v$version would refuse it:" >&2
	grep -En '^[[:space:]]*(replace|exclude)([[:space:]]|\()' "$gomod" >&2
	exit 1
fi

echo "v$version"
failures=0
# Requirements appear as `require path version` or as `path version` lines
# inside a require block; both put the module path right before the version.
while read -r path req_version; do
	dir=${path#"$root_module"/}
	if [[ $req_version != "v$version" ]]; then
		echo "module-tags.sh: $gomod requires $path $req_version; the release commit must require v$version" >&2
		failures=$((failures + 1))
		continue
	fi
	if ! grep -Eq "^module ${path}[[:space:]]*$" "$root_dir/$dir/go.mod" 2>/dev/null; then
		echo "module-tags.sh: $root_dir/$dir/go.mod does not declare module $path" >&2
		failures=$((failures + 1))
		continue
	fi
	echo "$dir/v$version"
done < <(sed -nE "s#^[[:space:]]*(require[[:space:]]+)?(${root_module}/[^[:space:]]+)[[:space:]]+(v[^[:space:]]+).*#\2 \3#p" "$gomod" | sort -u)
((failures == 0)) || exit 1
