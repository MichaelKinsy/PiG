#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Installs the pre-flight's scanners into <bin dir>: the archives of tools.txt through automation/ci/fetch-pinned.sh
# (accepted only at their pinned SHA-256), and reuse from automation/ci/security-tools.txt (hash-locked pip).
#   install-tools.sh <bin dir>
set -euo pipefail
bin=${1:?bin dir}
here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
root=$(cd -- "$here/../../.." && pwd)
mkdir -p "$bin"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
while IFS=$'\t' read -r name version sha url; do
	case $name in '#'* | '') continue ;; esac
	# These archives are not in the pig-ci-tools mirror, so fetch-pinned goes straight to the release asset.
	FETCH_OCI_REPO="" "$root/automation/ci/fetch-pinned.sh" "$sha" "$tmp/$name.tar.gz" "$url"
	tar -xzf "$tmp/$name.tar.gz" -C "$tmp" "$name"
	install -m 0755 "$tmp/$name" "$bin/$name"
	echo "installed $name $version"
done < "$here/tools.txt"
venv="$bin/../preflight-venv"
python3 -m venv "$venv"
"$venv/bin/pip" install --quiet --disable-pip-version-check --require-hashes -r "$root/automation/ci/security-tools.txt"
ln -sf "$venv/bin/reuse" "$bin/reuse"
echo "installed reuse $("$bin/reuse" --version | head -1)"
