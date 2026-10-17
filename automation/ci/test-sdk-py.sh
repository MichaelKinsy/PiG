#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

# Runs the Python extension SDK's pytest suite in a venv under PIG_CACHE_HOME that holds only the hash-locked dev
# dependencies of extensions/sdk-py/requirements-dev.txt. The venv's name hashes the lock and the interpreter, so a lock
# or interpreter change builds a new venv instead of reusing a stale one. The venv is built in a temporary directory
# and renamed into place, so an interrupted install or a concurrent run in another worktree never leaves a half-built
# venv behind. pytest runs as `python -m pytest` from the SDK root, which puts pig_sdk on sys.path without installing it.
# uv builds the venv when it is on PATH, because a system Python (Debian's, for one) can lack ensurepip and pip;
# otherwise the interpreter's own venv and pip do. Both install with --require-hashes and wheels only.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
sdk="$root/extensions/sdk-py"
lock="$sdk/requirements-dev.txt"
cache="${PIG_CACHE_HOME:-${XDG_CACHE_HOME:-$HOME/.cache}}/pig-sdk-py"

python=$(python3 -c 'import sys; print(sys.executable)')
key=$("$python" -c '
import hashlib, sys
digest = hashlib.sha256(open(sys.argv[1], "rb").read())
digest.update(f"{sys.version}\0{sys.executable}".encode())
print(digest.hexdigest()[:16])
' "$lock")
venv="$cache/venv-$key"

if [[ ! -f "$venv/.installed" ]]; then
  mkdir -p "$cache"
  tmp=$(mktemp -d "$cache/build.XXXXXXXX")
  trap 'rm -rf "$tmp"' EXIT
  if command -v uv >/dev/null 2>&1; then
    uv venv --quiet --python "$python" "$tmp/venv"
    uv pip install --quiet --python "$tmp/venv/bin/python" --require-hashes --only-binary :all: -r "$lock"
  else
    "$python" -m venv "$tmp/venv"
    "$tmp/venv/bin/python" -m pip install --quiet --disable-pip-version-check --no-input \
      --require-hashes --only-binary :all: -r "$lock"
  fi
  : >"$tmp/venv/.installed"
  # rename(2) fails when another run already renamed its equivalent venv into place; that venv is complete, so this
  # run uses it and discards its own. mv cannot express this portably: given an existing directory it moves into it.
  python3 -c 'import os, sys
try:
    os.rename(sys.argv[1], sys.argv[2])
except OSError:
    if not os.path.isfile(os.path.join(sys.argv[2], ".installed")):
        raise' "$tmp/venv" "$venv"
  rm -rf "$tmp"
  trap - EXIT
fi

cd "$sdk"
exec "$venv/bin/python" -m pytest -p no:cacheprovider "$@"
