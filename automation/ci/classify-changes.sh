#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Prints os-specific=true|false as well: true when a changed path matches a line of automation/ci/os-specific-paths.txt, a
# change a Linux run cannot prove, so the pull request runs the full run (Windows and macOS included).
#
# Prints portmap=true|false as well: true on every non-pull_request event, and on a pull_request when any changed path
# matches a `trigger:` rule of automation/ci/portmap-paths.txt, fails to match any `skip:` rule (fail-closed: an unknown
# path runs portmap-1..12), deletes or renames a Go file (or a Go/Rust/Python file under extensions/sdk*), removes a
# `func Test/Benchmark/Fuzz/Example` declaration, or modifies a `// pi:` or `// Ports ` marker in a Go file.
#
# Prints product=true|false for a pull request or a merge group: false only when
# every changed path is project prose or repository metadata that no product test
# reads. Scheduled, manual and push runs always verify the product. A merge
# group's base is the commit its entry was queued on, so the diff is that one
# entry's change, the same change its pull request was classified on.
#
# Usage: classify-changes.sh <event> <base-sha> <head-sha>
set -euo pipefail

event=${1:?event}
base=${2:-}
head=${3:-}

script_dir=$(dirname "$0")
os_paths=$script_dir/os-specific-paths.txt
portmap_paths=$script_dir/portmap-paths.txt
os_specific=false
portmap=true
product=true
if [ "$event" = pull_request ] || [ "$event" = merge_group ]; then
  : "${base:?base commit}" "${head:?head commit}"
  # A failing diff must fail the job, not read as an empty change that skips the product suites.
  # --no-renames: a rename is listed as the path that disappears and the path that appears, whatever diff.renames says.
  changed=$(git diff --name-only --no-renames "$base...$head")
  product=false
  if [ "$event" = pull_request ]; then
    portmap=false
  fi
  patterns=()
  while IFS= read -r line; do
    case "$line" in '' | '#'*) ;; *) patterns+=("$line") ;; esac
  done <"$os_paths"
  pm_triggers=()
  pm_skips=()
  while IFS= read -r line; do
    case "$line" in
      '' | '#'*) ;;
      trigger:*) pm_triggers+=("${line#trigger:}") ;;
      skip:*) pm_skips+=("${line#skip:}") ;;
      *)
        echo "portmap-paths.txt: invalid rule (want trigger:<glob> or skip:<glob>): $line" >&2
        exit 1
        ;;
    esac
  done <"$portmap_paths"
  go_changed=()
  while IFS= read -r path; do
    [ -n "$path" ] || continue
    for pattern in "${patterns[@]}"; do
      # shellcheck disable=SC2254 # the pattern is a glob on purpose
      case "$path" in
        $pattern)
          os_specific=true
          echo "os-specific change: $path" >&2
          break
          ;;
      esac
    done
    if [ "$event" = pull_request ] && [ "$portmap" = false ]; then
      matched_trigger=false
      for pattern in "${pm_triggers[@]}"; do
        # shellcheck disable=SC2254 # the pattern is a glob on purpose
        case "$path" in
          $pattern)
            matched_trigger=true
            portmap=true
            echo "portmap change (trigger): $path" >&2
            break
            ;;
        esac
      done
      if [ "$matched_trigger" = false ]; then
        matched_skip=false
        for pattern in "${pm_skips[@]}"; do
          # shellcheck disable=SC2254 # the pattern is a glob on purpose
          case "$path" in
            $pattern)
              matched_skip=true
              break
              ;;
          esac
        done
        if [ "$matched_skip" = false ]; then
          portmap=true
          echo "portmap change (unknown path, fail-closed): $path" >&2
        fi
      fi
      case "$path" in
        *.go) go_changed+=("$path") ;;
      esac
    fi
    case "$path" in
      README.md | CHANGELOG.md | .github/CONTRIBUTING.md | .github/CODE_OF_CONDUCT.md | docs/project/GOVERNANCE.md | docs/project/MAINTAINERS.md | .github/SECURITY.md | .github/SUPPORT.md | CITATION.cff) ;;
      docs/media/* | .github/ISSUE_TEMPLATE/* | .github/pull_request_template.md | .github/CODEOWNERS) ;;
      .github/dependabot.yml | .github/security-insights.yml | .coderabbit.yaml) ;;
      *)
        product=true
        echo "product change: $path" >&2
        ;;
    esac
  done <<<"$changed"
  # Fail closed on deleted/renamed Go files (and Go/Rust/Python files under extensions/sdk*):
  # with --no-renames, a rename appears as `D old` + `A new`, so checking status D catches both
  # deletions and renames of files that PORT_MAP.md rows may cite.
  if [ "$event" = pull_request ] && [ "$portmap" = false ]; then
    deleted_status=$(git diff --name-status --no-renames "$base...$head")
    while IFS=$'\t' read -r status del_path _rest; do
      [ "$status" = "D" ] || continue
      case "$del_path" in
        *.go | extensions/sdk-rs/*.rs | extensions/sdk-py/*.py)
          portmap=true
          echo "portmap change (deleted or renamed cited-language file): $del_path" >&2
          break
          ;;
      esac
    done <<<"$deleted_status"
  fi
  # Fail closed on Go marker edits and removed test/bench/fuzz/example declarations:
  # if any changed Go file adds/removes/alters a `// pi:` or `// Ports ` marker that portmapcheck
  # reads, or removes a `func Test/Benchmark/Fuzz/Example` declaration, run portmap-1..12.
  if [ "$event" = pull_request ] && [ "$portmap" = false ] && [ "${#go_changed[@]}" -gt 0 ]; then
    diff_out=$(git diff -U0 --no-renames "$base...$head" -- "${go_changed[@]}")
    if printf '%s\n' "$diff_out" | grep -Eq '^[+-][[:space:]]*//[[:space:]]*(pi:|Ports[[:space:]])|^-func[[:space:]]+((Test|Benchmark|Fuzz)[A-Z0-9_]|Example([A-Z0-9_]|\())'; then
      portmap=true
      echo "portmap change (Go marker or removed Test/Benchmark/Fuzz/Example diff)" >&2
    fi
  fi
fi
echo "product=$product"
echo "os-specific=$os_specific"
echo "portmap=$portmap"
