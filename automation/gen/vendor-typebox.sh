#!/usr/bin/env bash
# Rebuild the TypeBox modules that the Node extension runtime serves for the
# typebox, typebox/value, typebox/compile, and @sinclair/typebox* specifiers.
# The version and integrity equal the TypeBox that the pinned Pi release ships.
set -euo pipefail

version="1.3.27"
integrity="sha512-zu+jc1pcy4UiNThxikUr36f0Rybk9PEeCg/NE6adeWr/SKsdNO4EzZHYRDlv2YCVAfj3Odq3dESSo/jNyoBXzA=="
esbuild="0.25.10"
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
shims="$root/coding/extension/host/subprocess/runtime-node/shims"
work=$(mktemp -d "${TMPDIR:-/tmp}/pig.XXXXXXXX")
trap 'rm -rf "$work"' EXIT

cd "$work"
npm pack --silent "typebox@$version" >/dev/null
tarball="typebox-$version.tgz"
actual="sha512-$(openssl dgst -sha512 -binary "$tarball" | base64 -w0)"
[[ "$actual" == "$integrity" ]] || { echo "typebox $version integrity mismatch: $actual" >&2; exit 1; }
mkdir -p node_modules/typebox entries out
tar -xzf "$tarball" -C node_modules/typebox --strip-components=1
printf 'export * from "typebox";\n' > entries/typebox.mjs
printf 'export * from "typebox/value";\n' > entries/typebox-value.mjs
printf 'export * from "typebox/compile";\n' > entries/typebox-compile.mjs
npx -y "esbuild@$esbuild" entries/typebox.mjs entries/typebox-value.mjs entries/typebox-compile.mjs \
  --bundle --format=esm --platform=node --splitting --outdir=out \
  --out-extension:.js=.mjs --entry-names='[name]' --chunk-names='typebox-shared-[hash]' \
  --legal-comments=inline --log-level=warning
rm -f "$shims"/typebox.mjs "$shims"/typebox-value.mjs "$shims"/typebox-compile.mjs "$shims"/typebox-shared-*.mjs
cp out/*.mjs "$shims/"
echo "vendored typebox $version into $shims"
