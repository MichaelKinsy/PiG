#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Build-tag and platform gate. automation/ci/vet-tags.txt names every build tag and every operating system the tree's //go:build lines
# declare and the gate that compiles it; `make vet` runs the passes the file declares.
#   vet-tags.sh tags       print the `vet` tags, comma-separated, for `go vet -tags` (make vet)
#   vet-tags.sh platforms  print the environment (GOOS=... GOARCH=... [CGO_ENABLED=0]) of each `platform` pass, one line each (make vet)
#   vet-tags.sh check      fail on a tag or an operating system in a //go:build line that the file does not list, and on a row no Go file names
# A tag or platform that no gate compiles is how test/integration (undefined: livePaneProcesses), the durablescaling bench, and the Windows build of
# tui's tests (swScreen) stopped building on the line without a red.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"
list=automation/ci/vet-tags.txt

case "${1:-}" in
  tags)
    awk '$1 == "vet" { printf "%s%s", sep, $2; sep = "," } END { print "" }' "$list"
    exit 0
    ;;
  platforms)
    awk '$1 == "platform" { line = "GOOS=" $2 " GOARCH=" $3; for (i = 4; i <= NF; i++) line = line " " $i; print line }' "$list"
    exit 0
    ;;
  check) ;;
  *) echo "usage: vet-tags.sh tags|platforms|check" >&2; exit 2 ;;
esac

# GOARCH values and the other tags the go command sets itself (go help buildconstraint).
toolchain='^(386|amd64|amd64p32|arm|armbe|arm64|arm64be|loong64|mips|mipsle|mips64|mips64le|mips64p32|mips64p32le|ppc|ppc64|ppc64le|riscv|riscv64|s390|s390x|sparc|sparc64|wasm|unix|cgo|race|msan|asan|ignore|gc|gccgo|boringcrypto|purego|compiler_bootstrap|go[0-9.]+|goexperiment\..+)$'
oses='^(aix|android|darwin|dragonfly|freebsd|hurd|illumos|ios|js|linux|nacl|netbsd|openbsd|plan9|solaris|wasip1|windows|zos)$'

used="$(mktemp)"
trap 'rm -f "$used"' EXIT
# Every identifier of every //go:build line, negated or not, outside the pinned Pi mirror, installed packages and test fixtures.
find . \( -name .git -o -name .upstream -o -name node_modules -o -name testdata \) -prune -o -name '*.go' -type f -print0 |
  xargs -0 grep -h '^//go:build ' |
  sed 's|^//go:build ||' | tr -c 'A-Za-z0-9_.\n' ' ' | tr -s ' ' '\n' | grep -E '^[A-Za-z]' | grep -Ev "$toolchain" | sort -u >"$used" || true

status=0
while IFS= read -r tag; do
  if grep -Eq "$oses" <<<"$tag"; then
    # shellcheck disable=SC2016 # awk program: $1 is awk's field, not a shell expansion
    covered='$1 == "native" || $1 == "platform" || $1 == "os-other"'
  else
    # shellcheck disable=SC2016 # awk program: $1 is awk's field, not a shell expansion
    covered='$1 == "vet" || $1 == "other"'
  fi
  if ! awk -v t="$tag" "($covered) { if (\$2 == t) found = 1 } \$1 == \"prefix\" { if (index(t, \$2) == 1) found = 1 } END { exit !found }" "$list"; then
    echo "vet-tags: \"$tag\" appears in a //go:build line and has no row in $list; add \`vet $tag\` (make vet compiles it), \`other $tag <the gate that compiles it>\`, or for an operating system \`platform\`, \`os-other\` or \`native\`" >&2
    status=1
  fi
done <"$used"
while read -r kind tag _; do
  case "$kind" in
    vet | other | native | platform | os-other) grep -qxF -- "$tag" "$used" || { echo "vet-tags: $list lists \"$tag\" and no Go file names it in a //go:build line; remove the row" >&2; status=1; } ;;
    prefix) grep -q -- "^$tag" "$used" || { echo "vet-tags: $list lists the prefix \"$tag\" and no Go file names a tag with it; remove the row" >&2; status=1; } ;;
  esac
done < <(grep -Ev '^[[:space:]]*(#|$)' "$list")
exit "$status"
