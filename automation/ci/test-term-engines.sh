#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

# The terminal-engine A/B test of D205 (docs/testing/term-engines.md). Generates the corpus from deterministic Go, builds or installs
# the pinned engines (xterm.js, alacritty_terminal, wezterm-term, libvterm, pyte) from their locks, then runs test/term-engines, which
# replays scroll-region frames (variant A) and Pi's bytes (variant B) through each engine and requires equal screens.
# Everything built lives under PIG_CACHE_HOME/pig-term-engines, keyed by the lock or pin that produced it. A missing toolchain or a
# digest mismatch fails: it is an environment failure, never a skip.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"
engines=test/term-engines
cache="${PIG_CACHE_HOME:-${XDG_CACHE_HOME:-$HOME/.cache}}/pig-term-engines"
mkdir -p "$cache"

sha256() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }
# short_key FILE...: a digest of the files' contents, the name of the cache directory they key.
short_key() { cat "$@" | { if command -v sha256sum >/dev/null 2>&1; then sha256sum; else shasum -a 256; fi; } | cut -c1-16; }

# Build into a temporary directory and rename into place, so an interrupted run or a concurrent one in another worktree never leaves a
# half-built directory behind (the pattern of test-sdk-py.sh).
publish() { # tmp dest
  if [[ ! -e "$2" ]]; then mv "$1" "$2" 2>/dev/null || rm -rf "$1"; else rm -rf "$1"; fi
}

# xterm.js: the locked npm tree.
python3 automation/ci/npm-locked.py "$engines/xterm"

# pyte: a venv holding the hash-locked wheels.
pyte_lock="$engines/pyte/requirements.txt"
python=$(python3 -c 'import sys; print(sys.executable)')
pyte_key=$(short_key "$pyte_lock" <(echo "$("$python" -c 'import sys; print(sys.version)')$python"))
venv="$cache/pyte-$pyte_key"
if [[ ! -f "$venv/.installed" ]]; then
  tmp=$(mktemp -d "$cache/build.XXXXXXXX")
  trap 'rm -rf "$tmp"' EXIT
  if command -v uv >/dev/null 2>&1; then
    uv venv --quiet --python "$python" "$tmp/venv"
    uv pip install --quiet --python "$tmp/venv/bin/python" --require-hashes --only-binary :all: -r "$pyte_lock"
  else
    "$python" -m venv "$tmp/venv"
    "$tmp/venv/bin/python" -m pip install --quiet --disable-pip-version-check --no-input --require-hashes --only-binary :all: -r "$pyte_lock"
  fi
  : >"$tmp/venv/.installed"
  rm -rf "$venv"
  mv "$tmp/venv" "$venv"
  rm -rf "$tmp"
  trap - EXIT
fi

# libvterm: the pinned release source, checked against its digest, compiled to one executable.
lock=automation/dev/toolchains.lock
row=$(awk -F'\t' '!/^#/ && $1=="libvterm" {print; exit}' "$lock")
[[ -n "$row" ]] || { echo "test-term-engines: $lock has no libvterm row" >&2; exit 1; }
url=$(cut -f6 <<<"$row") digest=$(cut -f7 <<<"$row")
vterm_key=$(short_key <(echo "$row") "$engines/libvterm/run.c" <(cc --version | head -1))
vterm="$cache/libvterm-$vterm_key"
if [[ ! -x "$vterm/run" ]]; then
  tmp=$(mktemp -d "$cache/build.XXXXXXXX")
  trap 'rm -rf "$tmp"' EXIT
  curl -fsSL --retry 3 -o "$tmp/libvterm.tar.gz" "$url"
  [[ "$(sha256 "$tmp/libvterm.tar.gz")" == "$digest" ]] || { echo "test-term-engines: $url does not match its pinned sha256 $digest" >&2; exit 1; }
  mkdir "$tmp/src"
  tar -xzf "$tmp/libvterm.tar.gz" -C "$tmp/src" --strip-components=1
  mkdir "$tmp/out"
  cc -std=c99 -O1 -w -I"$tmp/src/include" -o "$tmp/out/run" "$engines/libvterm/run.c" "$tmp"/src/src/*.c
  rm -rf "$vterm"
  mv "$tmp/out" "$vterm"
  rm -rf "$tmp"
  trap - EXIT
fi

# alacritty_terminal and wezterm-term: Rust runners built from their locks into one cache target directory.
export CARGO_TARGET_DIR="${PIG_TERM_ENGINES_CARGO_TARGET:-$cache/cargo-target}"
cargo build --quiet --locked --release --manifest-path "$engines/alacritty/Cargo.toml"
cargo build --quiet --locked --release --manifest-path "$engines/wezterm/Cargo.toml"

corpus=$(mktemp -d "${TMPDIR:-/tmp}/term-corpus.XXXXXXXX")
trap 'rm -rf "$corpus"' EXIT
PIG_TERM_ENGINE_CORPUS_DIR="$corpus" go test ./tui -run '^TestAltScreenTermEngineCorpus$' -count=1

export PIG_TERM_ENGINE_CORPUS_DIR="$corpus"
export PIG_TERM_ENGINE_XTERM="node $root/$engines/xterm/run.mjs"
export PIG_TERM_ENGINE_PYTE="$venv/bin/python $root/$engines/pyte/run.py"
export PIG_TERM_ENGINE_LIBVTERM="$vterm/run"
export PIG_TERM_ENGINE_ALACRITTY="$CARGO_TARGET_DIR/release/pig-term-engine-alacritty"
export PIG_TERM_ENGINE_WEZTERM="$CARGO_TARGET_DIR/release/pig-term-engine-wezterm"
go test -tags termengines ./test/term-engines -count=1 -v "$@"
