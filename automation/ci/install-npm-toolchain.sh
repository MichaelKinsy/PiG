#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
set -euo pipefail

# Installs the lockfile-pinned npm into automation/ci/npm-toolchain.
# npm's tarball bundles its own copies of its dependencies, and neither overrides nor the lockfile can replace a
# bundled copy. package.json therefore lists each dependency with an advisory fix as a direct dependency, and this
# script removes npm's bundled copy so npm resolves the patched one from the toolchain's top-level node_modules.
# The lockfile carries no entry for a removed bundled copy. The parity image does the same in
# automation/images/ci-parity/Dockerfile.
cd "$(dirname "${BASH_SOURCE[0]}")/npm-toolchain"

npm ci --ignore-scripts --no-audit --no-fund

while IFS= read -r dependency; do
  rm -rf "node_modules/npm/node_modules/$dependency"
  test -f "node_modules/$dependency/package.json"
done < <(node -e '
  const manifest = require("./package.json");
  for (const name of Object.keys(manifest.dependencies)) if (name !== "npm") console.log(name);
')

node node_modules/npm/bin/npm-cli.js --version >/dev/null
