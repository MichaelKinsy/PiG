#!/usr/bin/env bash
# Materialize a read-only Pi source tree for the exact version pinned in
# coding/pigversion/pigversion.go. The mirror provides local source, tests,
# and documentation for parity work without adding upstream files to this
# repository.
#
# The script:
#   1. reads UpstreamVersion and UpstreamCommit from
#      coding/pigversion/pigversion.go;
#   2. resolves the release tag to its exact peeled commit;
#   3. reuses the public tarball cache under
#      ${XDG_CACHE_HOME:-~/.cache}/checkouts/github.com/earendil-works/pi/tarballs/;
#   4. extracts and validates the complete upstream tree;
#   5. records source provenance; and
#   6. points .upstream/current at the pinned version.
#
# It does not change the PiG source pin, commit, or push.
#
# Usage:
#   ./automation/gen/mirror-upstream.sh
#   ./automation/gen/mirror-upstream.sh --force
#   ./automation/gen/mirror-upstream.sh --version v0.70.5
#   ./automation/gen/mirror-upstream.sh --prune
#   ./automation/gen/mirror-upstream.sh --help
#
# --version materializes an additional release for comparison. It does not
# change coding/pigversion/pigversion.go.
#
# Requires: curl, tar, git
# Optional: GH_PUBLIC_TOKEN

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
UPSTREAM_REPO="earendil-works/pi"
CACHE_DIR="${XDG_CACHE_HOME:-$HOME/.cache}/checkouts/github.com/${UPSTREAM_REPO}"
TARBALL_DIR="${CACHE_DIR}/tarballs"
MIRROR_ROOT="${REPO_ROOT}/.upstream"

valid_commit() {
  [[ "$1" =~ ^[0-9a-f]{40}$ ]]
}

resolve_tag_commit() {
  local tag="$1" output peeled direct
  if [[ ! "$tag" =~ ^[A-Za-z0-9._-]+$ ]]; then
    echo "error: invalid upstream tag ${tag}" >&2
    return 1
  fi
  output=$(git ls-remote "https://github.com/${UPSTREAM_REPO}.git" \
    "refs/tags/${tag}" "refs/tags/${tag}^{}") || {
    echo "error: cannot resolve ${UPSTREAM_REPO} tag ${tag}" >&2
    return 1
  }
  peeled=$(awk -v ref="refs/tags/${tag}^{}" '$2 == ref { print $1; exit }' <<<"$output")
  direct=$(awk -v ref="refs/tags/${tag}" '$2 == ref { print $1; exit }' <<<"$output")
  local commit="${peeled:-$direct}"
  if ! valid_commit "$commit"; then
    echo "error: ${UPSTREAM_REPO} tag ${tag} did not resolve to a commit" >&2
    return 1
  fi
  printf '%s' "$commit"
}

read_pinned_commit() {
  # Capture then slice rather than piping into head: under pipefail, head
  # closing early makes the writer fail on SIGPIPE.
  local matches
  matches=$(sed -nE 's/^const UpstreamCommit = "([0-9a-f]{40})"$/\1/p' "$REPO_ROOT/coding/pigversion/pigversion.go")
  printf '%s' "${matches%%$'\n'*}"
}

write_provenance() {
  local path="$1" version="$2" tag="$3" commit="$4"
  local temporary="${path}.tmp.$$"
  printf '{\n  "version": "%s",\n  "tag": "%s",\n  "commit": "%s"\n}\n' "$version" "$tag" "$commit" >"$temporary"
  # The values are constrained to release/tag and hexadecimal commit
  # identities above, so this generated object is deterministic JSON.
  mv -f "$temporary" "$path"
}

verify_provenance() {
  local directory="$1" version="$2" tag="$3" commit="$4"
  local marker="$directory/.pig-upstream-source.json"
  [[ -f "$marker" ]] || return 1
  grep -Fq "\"version\": \"${version}\"" "$marker" || return 1
  grep -Fq "\"tag\": \"${tag}\"" "$marker" || return 1
  grep -Fq "\"commit\": \"${commit}\"" "$marker" || return 1
}

verify_mirror_tree() {
  local directory="$1"
  local source_count
  [[ -f "$directory/packages/coding-agent/src/core/extensions/types.ts" ]] || return 1
  [[ -f "$directory/packages/ai/src/providers/data/amazon-bedrock.json" ]] || return 1
  [[ -f "$directory/packages/ai/src/image-models.generated.js" ]] || return 1
  source_count=$(find "$directory/packages" -path '*/src/*' -name '*.ts' 2>/dev/null | wc -l | tr -d ' ')
  (( source_count >= 200 ))
}

hydrate_provider_data() {
  local directory="$1" version="$2" candidate package_version matches
  local target="$directory/packages/ai/src/providers/data"
  [[ -f "$target/amazon-bedrock.json" && -f "$directory/packages/ai/src/image-models.generated.js" ]] && return 0
  for candidate in \
    "${PIG_PUBLISHED_AI_DATA:-}" \
    "${PI_PACKAGE_ROOT:-}/node_modules/@earendil-works/pi-ai/dist/providers/data" \
    "$REPO_ROOT/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/providers/data" \
    "$(dirname "${PI_PACKAGE_ROOT:-/missing/pi-coding-agent}")/pi-ai/dist/providers/data" \
    "$HOME/.local/share/mise/installs/npm-earendil-works-pi-coding-agent/$version/lib/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/providers/data"; do
    [[ -n "$candidate" && -f "$candidate/amazon-bedrock.json" ]] || continue
    matches=$(sed -nE 's/^[[:space:]]*"version":[[:space:]]*"([^"]+)".*/\1/p' "$(dirname "$(dirname "$(dirname "$candidate")")")/package.json")
    package_version=${matches%%$'\n'*}
    [[ "$package_version" == "$version" ]] || {
      echo "error: published pi-ai data version ${package_version:-unknown} does not match $version" >&2
      return 1
    }
    mkdir -p "$target"
    cp "$candidate"/*.json "$target/"
    cp "$(dirname "$(dirname "$candidate")")/image-models.generated.js" "$directory/packages/ai/src/image-models.generated.js"
    return 0
  done
  echo "error: upstream source tag omits generated provider data and no matching published pi-ai package was found" >&2
  echo "       set PI_PACKAGE_ROOT or PIG_PUBLISHED_AI_DATA for pi-ai $version" >&2
  return 1
}

FORCE=0
PRUNE=0
OVERRIDE_TAG=""
while (( $# )); do
  case "$1" in
    --force) FORCE=1 ;;
    --prune) PRUNE=1 ;;
    --version)
      shift
      [[ $# -gt 0 ]] || { echo "--version requires a tag (e.g. v0.70.5)" >&2; exit 2; }
      OVERRIDE_TAG="$1"
      ;;
    -h|--help) sed -n '2,42p' "$0"; exit 0 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
  shift
done

# ── preflight ────────────────────────────────────────────────────────────────
for cmd in curl tar git; do
  command -v "$cmd" >/dev/null || { echo "missing dep: $cmd" >&2; exit 1; }
done

# ── resolve target tag ───────────────────────────────────────────────────────
if [[ -n "$OVERRIDE_TAG" ]]; then
  TARGET_TAG="$OVERRIDE_TAG"
else
  CURRENT_VERSION=$(grep -E '^const UpstreamVersion = "' "$REPO_ROOT/coding/pigversion/pigversion.go" \
    | sed -E 's/.*"([^"]+)".*/\1/')
  if [[ -z "$CURRENT_VERSION" ]]; then
    echo "error: could not parse UpstreamVersion from coding/pigversion/pigversion.go" >&2
    exit 1
  fi
  TARGET_TAG="v${CURRENT_VERSION}"
fi
TARGET_DIR="${MIRROR_ROOT}/${TARGET_TAG}"

RESOLVED_COMMIT=$(resolve_tag_commit "$TARGET_TAG") || exit 1
if [[ -z "$OVERRIDE_TAG" ]]; then
  PINNED_COMMIT=$(read_pinned_commit)
  if [[ -z "$PINNED_COMMIT" ]]; then
    echo "error: coding/pigversion/pigversion.go has no exact UpstreamCommit pin" >&2
    exit 1
  fi
  if [[ "$RESOLVED_COMMIT" != "$PINNED_COMMIT" ]]; then
    echo "error: ${TARGET_TAG} resolves to ${RESOLVED_COMMIT}, pinned ${PINNED_COMMIT}" >&2
    exit 1
  fi
fi

mkdir -p "$MIRROR_ROOT" "$TARBALL_DIR"

# ── prune old mirrors ────────────────────────────────────────────────────────
if [[ "$PRUNE" -eq 1 ]]; then
  echo "→ pruning .upstream/ entries other than ${TARGET_TAG}"
  shopt -s nullglob
  for d in "${MIRROR_ROOT}"/v*; do
    name="$(basename "$d")"
    if [[ "$name" != "$TARGET_TAG" ]]; then
      chmod -R u+w "$d" 2>/dev/null || true
      rm -rf "$d"
      echo "  removed $name"
    fi
  done
  # Also drop a stale `current` symlink if it points nowhere.
  if [[ -L "${MIRROR_ROOT}/current" && ! -e "${MIRROR_ROOT}/current" ]]; then
    rm -f "${MIRROR_ROOT}/current"
  fi
fi

# ── short-circuit if already extracted ───────────────────────────────────────
if [[ -d "$TARGET_DIR" && "$FORCE" -eq 0 ]]; then
  if verify_provenance "$TARGET_DIR" "${TARGET_TAG#v}" "$TARGET_TAG" "$RESOLVED_COMMIT"; then
    chmod -R u+w "$TARGET_DIR/packages/ai/src" 2>/dev/null || true
    hydrate_provider_data "$TARGET_DIR" "${TARGET_TAG#v}"
  fi
  if ! verify_provenance "$TARGET_DIR" "${TARGET_TAG#v}" "$TARGET_TAG" "$RESOLVED_COMMIT" || ! verify_mirror_tree "$TARGET_DIR"; then
    echo "error: upstream mirror provenance is missing or stale: ${TARGET_DIR}" >&2
    echo "       re-run with --force" >&2
    exit 1
  fi
  echo "✓ already mirrored: ${TARGET_DIR}"
  if [[ -z "${CI:-}${GITHUB_ACTIONS:-}" ]]; then
    chmod -R u-w "$TARGET_DIR" 2>/dev/null || true
  fi
  ln -snf "$TARGET_TAG" "${MIRROR_ROOT}/current"
  echo "  current → ${TARGET_TAG}"
  exit 0
fi

# Download the exact public release tarball.
TARBALL="${TARBALL_DIR}/${TARGET_TAG}.tar.gz"
if [[ ! -f "$TARBALL" ]]; then
  echo "→ fetching tarball for ${TARGET_TAG}"
  url="https://codeload.github.com/${UPSTREAM_REPO}/tar.gz/refs/tags/${TARGET_TAG}"
  auth_args=()
  [[ -n "${GH_PUBLIC_TOKEN:-}" ]] && auth_args=(-H "Authorization: Bearer ${GH_PUBLIC_TOKEN}")
  if ! curl -sSfL "${auth_args[@]+"${auth_args[@]}"}" -o "${TARBALL}.tmp" "$url"; then
    rm -f "${TARBALL}.tmp"
    echo "error: failed to download tarball for ${TARGET_TAG}" >&2
    echo "       check that the tag exists: https://github.com/${UPSTREAM_REPO}/releases/tag/${TARGET_TAG}" >&2
    exit 1
  fi
  mv "${TARBALL}.tmp" "$TARBALL"
fi

# ── extract full tree ────────────────────────────────────────────────────────
echo "→ extracting full source tree → ${TARGET_DIR}"
if [[ -d "$TARGET_DIR" ]]; then
  chmod -R u+w "$TARGET_DIR" 2>/dev/null || true
fi
rm -rf "$TARGET_DIR"
mkdir -p "$TARGET_DIR"
# Extract one top-level entry at a time. Some endpoint-protection tools stop
# large streaming extractions. Per-entry extraction remains deterministic and
# avoids that failure mode. The complete-tree validation below rejects partial
# extraction.
# Read the listing once. Piping a `tar -tzf` listing into `head` makes GNU tar
# (the Linux CI fleet) exit non-zero with "stdout: write error" when head closes
# the pipe after one line (SIGPIPE), and set -o pipefail turns that into a hard
# failure. BSD tar tolerates it, so this only breaks on Linux. Reading the full
# listing into a variable lets tar finish cleanly, then slice it. The slice must
# also avoid `| head`: printf-ing the whole listing into head re-triggers the
# same SIGPIPE/pipefail failure, so use read + parameter expansion instead.
LISTING=$(tar -tzf "$TARBALL")
read -r first_line <<<"$LISTING"
inner_root=${first_line%%/*}
TOP_LEVEL=$(printf '%s\n' "$LISTING" | awk -F/ 'NF >= 2 && $2 != "" {print $2}' | sort -u)
if [[ -z "$TOP_LEVEL" ]]; then
  echo "error: tarball has no top-level entries (corrupt download?)" >&2
  exit 1
fi
for entry in $TOP_LEVEL; do
  echo "  → $entry"
  # Use an include pattern anchored at the archive wrapper directory.
  if ! tar -xzf "$TARBALL" -C "$TARGET_DIR" --strip-components=1 \
        --include="${inner_root}/${entry}" \
        --include="${inner_root}/${entry}/*" 2>/dev/null; then
    # GNU tar wants the literal path without --include.
    tar -xzf "$TARBALL" -C "$TARGET_DIR" --strip-components=1 \
      "${inner_root}/${entry}" 2>/dev/null || true
  fi
  sleep 0.3
done

# Drop upstream npm lockfiles before locking the tree down. pig never installs
# them (ensure_upstream_pi uses mise), they are off the parity surface
# (packages/*/src/**), and they carry transitive CVEs that would otherwise sit
# in the cached mirror. Excluding them keeps the mirror to what parity reads.
find "$TARGET_DIR" -name package-lock.json -type f -delete 2>/dev/null || true

# The upstream source tag omits generated provider JSON files that the checked-in
# provider sources import. Recover the exact-version files from the published
# pi-ai package before validating and locking the mirror.
hydrate_provider_data "$TARGET_DIR" "${TARGET_TAG#v}"

# Verify complete extraction because an interrupted per-entry extraction can
# otherwise leave a plausible but incomplete mirror.
sentinel="packages/coding-agent/src/core/extensions/types.ts"
src_ts=$(find "$TARGET_DIR/packages" -path '*/src/*' -name '*.ts' 2>/dev/null | wc -l | tr -d ' ')
if ! verify_mirror_tree "$TARGET_DIR"; then
  echo "error: upstream mirror looks truncated (sentinel: $([[ -f "$TARGET_DIR/$sentinel" ]] && echo present || echo MISSING), src .ts: $src_ts)." >&2
  echo "       EDR likely killed the extraction. Re-run with --force, or seed from the MSR cache (oras pull)." >&2
  exit 1
fi

write_provenance "$TARGET_DIR/.pig-upstream-source.json" "${TARGET_TAG#v}" "$TARGET_TAG" "$RESOLVED_COMMIT"

# Make the tree read-only so we don't accidentally edit upstream sources.
# `chmod -R u-w` is portable across BSD (macOS) and GNU tar systems. Skip it in
# CI: the runner reuses its workspace, and a read-only directory cannot be
# unlinked, so actions/checkout's `git clean -ffdx` would fail with EACCES on
# the next job. The edit-protection only matters for interactive developers.
if [[ -z "${CI:-}${GITHUB_ACTIONS:-}" ]]; then
  chmod -R u-w "$TARGET_DIR" 2>/dev/null || true
fi

# ── refresh stable symlink ───────────────────────────────────────────────────
ln -snf "$TARGET_TAG" "${MIRROR_ROOT}/current"

# ── README so future-you knows what this directory is ────────────────────────
READMEPATH="${MIRROR_ROOT}/README.md"
# Force-overwrite matters on re-runs: the file may be read-only from a
# previous invocation.
if [[ -f "$READMEPATH" ]]; then chmod u+w "$READMEPATH" 2>/dev/null || true; fi
cat > "$READMEPATH" <<'EOF'
# .upstream/: pi reference mirrors

This directory holds **read-only** copies of upstream pi source
trees, one per version, for side-by-side reference while porting.

## Contents

\`\`\`
.upstream/
├── README.md          this file
├── current → v<X.Y.Z>/  symlink to the version pig currently tracks
├── v<X.Y.Z>/          full pi tree at that release tag
└── v<X.Y.Z>/.pig-upstream-source.json  resolved tag/commit provenance
\`\`\`

The mirror also contains \`.pig-upstream-source.json\`, generated only after the
full extraction succeeds. It records the release version, tag, and exact peeled
Git commit resolved from the upstream tag. The default invocation checks that
commit against \`coding.UpstreamCommit\`; a missing or stale marker fails closed
and requires \`--force\`.

\`\`\`bash
# rg across upstream
rg "createBashTool" .upstream/current/packages/coding-agent/

# diff a Go port against the TS source
code -d internal/codingagent/tools/tools.go .upstream/current/packages/coding-agent/src/core/tools/bash.ts

# refresh after updating coding/pigversion/pigversion.go
make upstream-mirror

# clean up older mirrors
./automation/gen/mirror-upstream.sh --prune
\`\`\`

## How it gets here

Run `make upstream-mirror` from the PiG repository root. It reads
`UpstreamVersion` and `UpstreamCommit` from `coding/pigversion/pigversion.go`, downloads the
matching public release tarball into
`${XDG_CACHE_HOME:-~/.cache}/checkouts/github.com/earendil-works/pi/`, validates its provenance,
and extracts it here.

The tree is \`chmod -R u-w\` so you can't accidentally edit it; if you
ever need to (e.g. apply a local patch for testing), \`chmod -R u+w\`
first or just re-extract with \`--force\`.

## Why gitignored

This is reference material, not pig source. Vendoring it would balloon
the repo by ~50–100 MB per version and mix licensing concerns. The
script is the only thing in version control; anyone can re-materialise
the mirror by running it.
EOF
chmod -w "$READMEPATH" 2>/dev/null || true

# ── summary ──────────────────────────────────────────────────────────────────
echo ""
echo "✓ mirrored: ${TARGET_DIR}"
echo "  current → ${TARGET_TAG}"
echo ""
echo "  tip: rg/grep upstream sources via \`.upstream/current/...\`"
echo "  tip: re-run with --force after editing UpstreamVersion to refresh"
echo "  tip: --prune drops mirrors of older tracked versions"
