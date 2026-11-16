#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# ShellCheck over every tracked shell script under automation/ and docs/site/public, as the Security workflow's
# "Analyze shell scripts" step runs it (make lint-shell; make pre-push calls it too). The version is the one pinned in
# automation/ci/install-shellcheck.sh: a different installed version fails here rather than report findings CI will not.
# The tracked files, as CI's fresh checkout has them: untracked trees (an installed npm toolchain) hold scripts CI never sees.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
pinned=$(./automation/ci/install-shellcheck.sh --print-version)
if ! command -v shellcheck >/dev/null; then
  echo "lint-shell: shellcheck not found; install ShellCheck $pinned (brew install shellcheck, or automation/ci/install-shellcheck.sh on Linux)" >&2
  exit 1
fi
have=$(shellcheck --version | awk '/^version:/ {print $2}')
if [ "$have" != "$pinned" ]; then
  echo "lint-shell: shellcheck $have is installed, CI pins $pinned (automation/ci/install-shellcheck.sh); install that version so the findings match" >&2
  exit 1
fi
git ls-files -z 'automation/*.sh' 'docs/site/public/*.sh' | xargs -0 shellcheck
