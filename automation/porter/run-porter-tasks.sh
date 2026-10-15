#!/usr/bin/env bash
# run-porter-tasks.sh <tasks.tsv> <log-dir> [worktree]
#
# Runs one headless Pig Porter task per tasks.tsv line (kind, scope, rows; the file `follow.py` writes), serially, in the worktree,
# and commits what each task left on the worktree's branch. The commit is the maintainer's step, never the Porter's.
#
# A task's commit is honest about its state: its subject says `complete` only when the task exited 0 and the tree builds; otherwise
# it says `PARTIAL` with the exit status or the failed build, so a reviewer and the router never take a timed-out or broken tree for
# finished output. Every file the task created is committed with the edits (git add -A, within .gitignore), so a clean checkout
# contains everything the task's tests need.
#
# Environment: PIG_PORTER_TASK_TIMEOUT (seconds, default 3600), PIG_PORTER_MODEL, PIG_PORTER_REF (PI_UPSTREAM_REF),
# PIG_PORTER_BUILD_CMD (default "go build ./..."), PIG_PORTER_RUNLOG (default <log-dir>/runlog.tsv).
set -u

[ "$#" -ge 2 ] || { echo "usage: run-porter-tasks.sh <tasks.tsv> <log-dir> [worktree]" >&2; exit 2; }
TASKS=$1
LOGS=$2
WT=${3:-$PWD}
TIMEOUT=${PIG_PORTER_TASK_TIMEOUT:-3600}
BUILD=${PIG_PORTER_BUILD_CMD:-go build ./...}
RUNLOG=${PIG_PORTER_RUNLOG:-$LOGS/runlog.tsv}
mkdir -p "$LOGS" "$(dirname "$RUNLOG")"
cd "$WT" || exit 1
[ -z "${PIG_PORTER_REF:-}" ] || export PI_UPSTREAM_REF=$PIG_PORTER_REF

can_sign() {
  [ -n "$(git config --get user.signingkey)" ] || gpg --list-secret-keys 2>/dev/null | grep -q '^sec'
}

# foreign_work: a task's new file that is byte-identical to the same path on another lane's branch that HEAD does not contain is that lane's
# unmerged work, copied (a durable delta task once landed seven such files in one PARTIAL commit). The recent unmerged branches are checked;
# PIG_PORTER_FOREIGN_REFS overrides the list and PIG_PORTER_FOREIGN_MIN_BYTES (default 64) skips files too small to be distinctive.
foreign_work() {
  local refs ref path size found=1 min=${PIG_PORTER_FOREIGN_MIN_BYTES:-64}
  refs=${PIG_PORTER_FOREIGN_REFS:-$(git for-each-ref --no-merged HEAD --sort=-committerdate --count=80 --format='%(refname)' refs/remotes/staging/team refs/heads)}
  [ -n "$refs" ] || return 1
  while IFS= read -r path; do
    [ -n "$path" ] || continue
    size=$(git cat-file -s ":$path")
    [ "$size" -ge "$min" ] || continue
    for ref in $refs; do
      if [ "$(git rev-parse -q --verify "$ref:$path" 2>/dev/null)" = "$(git rev-parse ":$path")" ]; then
        echo "run-porter-tasks: $path is byte-identical to $ref: another lane's unmerged work" >&2
        found=0
        break
      fi
    done
  done < <(git diff --cached --diff-filter=A --name-only)
  return $found
}

commit_task() { # commit_task <kind> <scope> <rc>
  local kind=$1 scope=$2 rc=$3 state build=ok
  git add -A
  git diff --cached --quiet && return 0
  if foreign_work; then
    echo "run-porter-tasks: not committing $kind $scope: it contains work copied from another lane's branch; the changes stay staged" >&2
    return 6
  fi
  if ! (eval "$BUILD") > "$LOGS/build.log" 2>&1 < /dev/null; then build=failed; fi
  if [ "$rc" -eq 0 ] && [ "$build" = ok ]; then
    state="complete"
  elif [ "$rc" -ne 0 ]; then
    state="PARTIAL (rc=$rc"; [ "$build" = ok ] || state="$state, build fails"; state="$state)"
  else
    state="PARTIAL (build fails)"
  fi
  # A failed commit fails the task: its changes must not wait in the tree for the next task's commit to take them under its own name.
  # -S is a `commit` option (before the subcommand git says "unknown option: -S"); turning signing off is a global -c.
  if can_sign; then
    git commit -S -s -q -m "porter: $kind $scope ($state, headless output, unreviewed)"
  else
    git -c commit.gpgsign=false commit -s -q -m "porter: $kind $scope ($state, headless output, unreviewed)"
  fi
}

require_clean_tree() {
  if [ -n "$(git status --porcelain)" ]; then
    echo "run-porter-tasks: the worktree is not clean; a task's commit must contain only that task's work:" >&2
    git status --short >&2
    exit 3
  fi
}

[ "${1:-}" = "--commit-only" ] && { commit_task "$4" "$5" "$6" || { echo "run-porter-tasks: commit failed" >&2; exit 4; }; exit 0; }

require_clean_tree
# Freeze every group's gap rows before any task runs: a closure task ends with make ledger-update, which shrinks the baseline, and the
# parts of a group must slice the rows as they were.
if [ -f automation/pin/follow.py ]; then
  python3 automation/pin/follow.py "$(python3 automation/pin/pin.py get commit)" --freeze-rows || { echo "run-porter-tasks: freezing the gap rows failed" >&2; exit 5; }
fi

while IFS=$'\t' read -r kind scope rows; do
  [ -n "$kind" ] || continue
  name=$(printf '%s-%s' "$kind" "$scope" | tr '/ ' '__')
  [ -f "$LOGS/$name.done" ] && continue
  require_clean_tree
  start=$(date +%s)
  echo "$(date -u +%FT%TZ) PORTER start [${PIG_PORTER_REF:-pin}] $kind $scope (${rows:-?})" >> "$RUNLOG"
  model_args=()
  [ -z "${PIG_PORTER_MODEL:-}" ] || model_args=("PORTER_ARGS=--model $PIG_PORTER_MODEL")
  # The task must not read this loop's task list through stdin: its pig did once, and the loop ended after one task.
  timeout "$TIMEOUT" make porter-task TASK="$kind $scope" "${model_args[@]}" > "$LOGS/$name.log" 2>&1 < /dev/null
  rc=$?
  git diff --stat > "$LOGS/$name.diffstat" 2>&1
  echo "$(date -u +%FT%TZ) PORTER end [${PIG_PORTER_REF:-pin}] rc=$rc secs=$(( $(date +%s) - start )) $kind $scope" >> "$RUNLOG"
  commit_task "$kind" "$scope" "$rc"
  status=$?
  [ "$status" -ne 6 ] || { echo "$(date -u +%FT%TZ) PORTER foreign work, not committed $kind $scope" >> "$RUNLOG"; exit 6; }
  if [ "$status" -ne 0 ]; then
    echo "run-porter-tasks: commit failed for $kind $scope; its changes stay in the tree and the task is not marked done" >&2
    echo "$(date -u +%FT%TZ) PORTER commit failed $kind $scope" >> "$RUNLOG"
    exit 4
  fi
  touch "$LOGS/$name.done"
done < "$TASKS"
echo "$(date -u +%FT%TZ) PORTER all tasks done [${PIG_PORTER_REF:-pin}] $WT" >> "$RUNLOG"
