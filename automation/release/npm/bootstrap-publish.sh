#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
#
# ONE-TIME bootstrap publish of PiG's npm packages from a maintainer's machine.
#
# npm can require a package to exist before a trusted publisher (OIDC) can be
# configured for it. Run this once, after `npm login` as an owner of the
# @pi-in-go npm org, to publish the seven packages for an already PUBLISHED
# GitHub Release. npm prompts for your one-time password (2FA) as usual.
# Afterwards, configure the trusted publisher printed at the end on each
# package; every later release publishes from .github/workflows/npm-publish.yml.
#
# The binaries come only from the published release archives, verified against
# SHA256SUMS, exactly as in CI. Versions already on npm are skipped.
#
# Usage: automation/release/npm/bootstrap-publish.sh [TAG]   (default v<PigVersion>)
# Env:   PIG_REPO (default MichaelKinsy/PiG), DRY_RUN=1 to pack without publishing.
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/../../.." && pwd)
gh_repo=${PIG_REPO:-MichaelKinsy/PiG}
default_version=$(sed -nE 's/^const PigVersion = "([^"]+)"$/\1/p' "$repo_root/coding/pigversion/pigversion.go")
tag=${1:-v$default_version}
[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]] || { echo "bootstrap: bad tag $tag" >&2; exit 1; }
version=${tag#v}
npm_version=${version%%+*}

for tool in gh npm python3; do
  command -v "$tool" >/dev/null || { echo "bootstrap: $tool is required" >&2; exit 1; }
done
if command -v sha256sum >/dev/null; then sha256_check=(sha256sum -c --ignore-missing)
else sha256_check=(shasum -a 256 -c --ignore-missing); fi

if [ "${DRY_RUN:-0}" != 1 ]; then
  who=$(npm whoami 2>/dev/null) || { echo "bootstrap: run 'npm login' first" >&2; exit 1; }
  echo "bootstrap: publishing as npm user $who"
fi

draft=$(gh release view "$tag" --repo "$gh_repo" --json isDraft --jq .isDraft)
[ "$draft" = false ] || { echo "bootstrap: release $tag is still a draft; publish it first" >&2; exit 1; }

work=$(mktemp -d "${TMPDIR:-/tmp}/pig-npm-bootstrap.XXXXXX")
echo "bootstrap: workdir $work"
mkdir -p "$work/release"
gh release download "$tag" --repo "$gh_repo" --dir "$work/release" \
  --pattern SHA256SUMS --pattern 'pig-*.tar.gz' --pattern 'pig-*.zip'
(cd "$work/release" && "${sha256_check[@]}" SHA256SUMS)

python3 "$repo_root/automation/release/npm/pack_npm.py" \
  --archives "$work/release" --version "$version" --out "$work/npm"

names=()
while read -r name tarball; do
  names+=("$name")
  if npm view "${name}@${npm_version}" version >/dev/null 2>&1; then
    echo "bootstrap: ${name}@${npm_version} already on npm; skipping"
    continue
  fi
  if [ "${DRY_RUN:-0}" = 1 ]; then
    echo "bootstrap: DRY_RUN, would publish $work/npm/$tarball"
    continue
  fi
  npm publish "$work/npm/$tarball" --access public
  echo "bootstrap: published ${name}@${npm_version}"
done < "$work/npm/publish-order.txt"

owner=${gh_repo%%/*}
repository=${gh_repo#*/}
cat <<EOF

Next: configure npm trusted publishing on EACH package below.
On https://www.npmjs.com/package/<name>/access, under "Trusted Publisher",
choose GitHub Actions and enter:

  Organization or user:  $owner
  Repository:            $repository
  Workflow filename:     npm-publish.yml
  Environment name:      (leave empty)

Packages:
EOF
for name in "${names[@]}"; do
  echo "  https://www.npmjs.com/package/${name}/access"
done
cat <<'EOF'

Then, optionally, set each package's publishing access to
"Require two-factor authentication and disallow tokens".
EOF
