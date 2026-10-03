#!/usr/bin/env bash
# Print the import path of every package that has a test file built only for
# Windows (a _windows_test.go name or a windows build constraint). The
# toolchain decides, by comparing the test files each GOOS selects, so the
# native Windows job runs exactly the packages that carry Windows tests.
# Test files that also need a custom build tag (integration, parity) and
# Windows branches selected at run time by runtime.GOOS are not selected.
set -euo pipefail
export LC_ALL=C

ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"

# A quoted here-document keeps the template's $p a go list variable, not a shell expansion.
read -r -d '' list_template <<'TEMPLATE' || true
{{$p := .ImportPath}}{{range .TestGoFiles}}{{$p}} {{.}}{{"\n"}}{{end}}{{range .XTestGoFiles}}{{$p}} {{.}}{{"\n"}}{{end}}
TEMPLATE

test_files() {
  GOOS=$1 go list -e -f "$list_template" ./... | sort
}

# Capture each listing in the shell itself: a go list failure inside a process
# substitution would not stop the script and would read as no packages.
linux=$(test_files linux)
windows=$(test_files windows)
packages=$(comm -13 <(printf '%s\n' "$linux") <(printf '%s\n' "$windows") | cut -d' ' -f1 | sort -u)
if [ -z "$packages" ]; then
  echo "windows-test-packages: no package has a Windows-only test file" >&2
  exit 1
fi
printf '%s\n' "$packages"
