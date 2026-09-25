#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PIG_ROOT="$ROOT"
PIG_BIN="${PIG_BIN:-$PIG_ROOT/bin/pig-parity}"
MAX_JOBS="${PIG_PORTER_MAX_JOBS:-3}"

usage() {
  cat <<'EOF'
Usage: automation/porter/run-pig-porter-campaign.sh <inventory|verify> <family> [family...]

Runs bounded pig-porter jobs concurrently against disposable snapshots of the
current commit. Workers cannot modify the integration workspace. Their only
output is one report and status file per family under tmp/porter-campaigns.

Environment:
  PIG_PORTER_MAX_JOBS   Maximum concurrent workers (default: 3)
  PIG_PORTER_MODEL      Explicit model passed to every worker
  PIG_PORTER_REPORT_DIR Override report directory
  PIG_BIN               Pig executable (default: bin/pig-parity)
EOF
}

if [ "$#" -lt 2 ]; then
  usage >&2
  exit 2
fi
mode="$1"
shift
case "$mode" in
  inventory|verify) ;;
  *) echo "pig-porter campaign: mode must be inventory or verify" >&2; exit 2 ;;
esac
case "$MAX_JOBS" in
  ''|*[!0-9]*) echo "pig-porter campaign: PIG_PORTER_MAX_JOBS must be a positive integer" >&2; exit 2 ;;
esac
[ "$MAX_JOBS" -gt 0 ] || { echo "pig-porter campaign: PIG_PORTER_MAX_JOBS must be positive" >&2; exit 2; }
[ -x "$PIG_BIN" ] || { echo "pig-porter campaign: Pig binary is not executable: $PIG_BIN" >&2; exit 1; }

cd "$ROOT"
if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  echo "pig-porter campaign: tracked workspace changes exist; commit them before creating an immutable campaign" >&2
  exit 1
fi
commit="$(git rev-parse HEAD)"
short_commit="$(git rev-parse --short HEAD)"
version="$(awk -F'"' '/^const UpstreamVersion = "/ { print $2; exit }' "$PIG_ROOT/coding/pigversion/pigversion.go")"
[ -n "$version" ] || { echo "pig-porter campaign: cannot resolve coding.UpstreamVersion" >&2; exit 1; }
upstream="$PIG_ROOT/.upstream/v$version"
[ -d "$upstream" ] || { echo "pig-porter campaign: exact upstream mirror is missing: $upstream" >&2; exit 1; }

families=()
declare -A seen=()
for family in "$@"; do
  [ -z "${seen[$family]:-}" ] || { echo "pig-porter campaign: duplicate family $family" >&2; exit 2; }
  seen[$family]=1
  (cd "$PIG_ROOT" && go run ./parity/cmd/familygaps -family "$family" >/dev/null)
  families+=("$family")
done

stamp="$(date -u +%Y%m%dT%H%M%SZ)"
report_dir="${PIG_PORTER_REPORT_DIR:-$PIG_ROOT/tmp/porter-campaigns/$stamp-$short_commit}"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/pig-porter-campaign.XXXXXX")"
mkdir -p "$report_dir"
chmod 700 "$work_dir" "$report_dir"
cleanup() {
  chmod -R u+rwX "$work_dir" 2>/dev/null || true
  rm -rf "$work_dir"
}
trap cleanup EXIT

prepare_snapshot() {
  local family="$1"
  local snapshot="$work_dir/$family/source"
  mkdir -p "$snapshot"
  git archive "$commit" | tar -x -C "$snapshot"
  mkdir -p "$snapshot/.upstream"
  cp -R "$upstream" "$snapshot/.upstream/v$version"
  ln -s "v$version" "$snapshot/.upstream/current"
  # Campaign workers are read-only at the filesystem boundary. Keep only
  # disposable build/test output writable; source, tests, evidence, ledgers,
  # and Piglet inputs cannot be changed even if a worker ignores its prompt.
  mkdir -p "$snapshot/tmp"
  chmod -R a-w "$snapshot"
  chmod -R u+rwX "$snapshot/tmp"
}

prepare_home() {
  local family="$1"
  local home="$work_dir/$family/home"
  local source_home="${PIG_HOME:-$HOME/.pig}"
  mkdir -p "$home/agent"
  chmod 700 "$home" "$home/agent"
  for name in auth.json settings.json models.json; do
    if [ -f "$source_home/agent/$name" ]; then
      cp "$source_home/agent/$name" "$home/agent/$name"
      chmod 600 "$home/agent/$name"
    fi
  done
}

run_worker() {
  local family="$1"
  local snapshot="$work_dir/$family/source"
  local home="$work_dir/$family/home"
  local report="$report_dir/$family.log"
  local status="$report_dir/$family.status"
  local profile="$snapshot/piglets/porter/pig-porter.yaml"
  local campaign_families
  campaign_families="$(IFS=,; printf '%s' "${families[*]}")"
  local model_args=()
  if [ -n "${PIG_PORTER_MODEL:-}" ]; then
    model_args=(--model "$PIG_PORTER_MODEL")
  fi
  {
    printf 'campaign_commit=%s\nupstream_version=%s\nmode=%s\nfamily=%s\n\n' "$commit" "$version" "$mode" "$family"
    cd "$snapshot"
    PIG_HOME="$home" "$PIG_BIN" --piglet "$profile" --no-session "${model_args[@]}" -p "$(cat <<EOF
You are the read-only $family worker in a parallel Pig Porter campaign.
Campaign commit: $commit
Exact upstream: $version
Mode: $mode
All sibling assignments: $campaign_families
Maximum concurrent workers: $MAX_JOBS

Other workers are active in isolated snapshots. Do not edit repository source,
tests, evidence, generated inventories, or git state. Do not kill or signal any
process or tmux session, remove temporary state, use broad process matching, or
run destructive git commands. Build tools may create their normal ephemeral
outputs inside this disposable snapshot.

Be patient with every build, test, extraction, and oracle probe. Wait for its
declared timeout. Do not duplicate, prematurely terminate, or repeatedly retry
a quiet or slow command. If an operation times out or is externally blocked,
record the exact command and blocker once, then continue only with independent
read-only analysis.

/skill:pig-porter $mode $family
EOF
)"
  } >"$report" 2>&1
  printf 'pass\n' >"$status"
}

pids=()
pid_families=()
failures=0
wait_worker() {
  local pid="$1"
  local family="$2"
  if wait "$pid"; then
    printf 'pig-porter campaign: PASS %s\n' "$family"
  else
    printf 'fail\n' >"$report_dir/$family.status"
    printf 'pig-porter campaign: FAIL %s (see %s)\n' "$family" "$report_dir/$family.log" >&2
    failures=$((failures + 1))
  fi
}

for family in "${families[@]}"; do
  prepare_snapshot "$family"
  prepare_home "$family"
  run_worker "$family" &
  pids+=("$!")
  pid_families+=("$family")
  if [ "${#pids[@]}" -ge "$MAX_JOBS" ]; then
    wait_worker "${pids[0]}" "${pid_families[0]}"
    pids=("${pids[@]:1}")
    pid_families=("${pid_families[@]:1}")
  fi
done
for index in "${!pids[@]}"; do
  wait_worker "${pids[$index]}" "${pid_families[$index]}"
done

printf 'pig-porter campaign: reports=%s commit=%s workers=%d failures=%d\n' "$report_dir" "$short_commit" "${#families[@]}" "$failures"
[ "$failures" -eq 0 ]
