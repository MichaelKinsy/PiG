#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
set -euo pipefail

# Installs apt packages on a GitHub-hosted Ubuntu runner without letting one unreachable mirror hold the job.
# The runner image points apt at the Azure mirror, which has been unreachable for whole runs. The sources name it
# either directly (http://azure.archive.ubuntu.com/...) or through a mirror list (mirror+file:/etc/apt/apt-mirrors.txt)
# whose entries name it. When its release file does not answer, when apt cannot fetch every index, or when the install
# cannot download its packages from an Azure mirror that served the indexes, each Azure URI
# in the sources and in every mirror list they reference moves to the public archive once and the attempt repeats
# against it. Each apt call has its own deadline, and timeout kills a call that ignores
# SIGTERM. The whole script stays inside one budget (default 480s) that fits the 10-minute workflow step limit:
# every deadline is capped by the budget that remains, so retries cannot outlive the step.
# APT_SOURCES_DIR, APT_SOURCES_LIST, APT_BUDGET_SECONDS and SUDO exist so a test can run this script without root.

if [ "$#" -eq 0 ]; then
  echo "usage: automation/ci/install-apt-packages.sh <package>..." >&2
  exit 2
fi

sources_dir="${APT_SOURCES_DIR:-/etc/apt/sources.list.d}"
sources_list="${APT_SOURCES_LIST:-/etc/apt/sources.list}"
read -r -a sudo_cmd <<< "${SUDO-sudo}"
azure=azure.archive.ubuntu.com
public=archive.ubuntu.com
apt_options=(-o Acquire::Retries=3 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30)
budget="${APT_BUDGET_SECONDS:-480}"
kill_after=10
update_limit=90
install_limit=150
attempts=3
deadline=$((SECONDS + budget))

# run_apt <seconds> <apt-get args...> runs apt-get under the smaller of <seconds> and the remaining budget, plus the kill grace.
run_apt() {
  local limit="$1" left=$((deadline - SECONDS - kill_after))
  shift
  [ "$left" -lt "$limit" ] && limit="$left"
  if [ "$limit" -lt 1 ]; then
    echo "apt budget of ${budget}s is spent" >&2
    return 124
  fi
  "${sudo_cmd[@]}" timeout --kill-after="${kill_after}s" "${limit}s" apt-get "${apt_options[@]}" "$@"
}

# We install nothing from packages.microsoft.com; drop the runner image's source so its outages cannot fail this step.
"${sudo_cmd[@]}" rm -f "$sources_dir/microsoft-prod.list" "$sources_dir/microsoft-prod.sources"

# azure_files lists the files that name the Azure mirror: the sources themselves and the mirror lists they reference.
azure_files() {
  local list
  grep -rls -- "$azure" "$sources_list" "$sources_dir" 2>/dev/null || true
  while IFS= read -r list; do
    [ -f "$list" ] && grep -qs -- "$azure" "$list" && printf '%s\n' "$list"
  done < <(grep -rhoE -- 'mirror\+file:[^[:space:]]+' "$sources_list" "$sources_dir" 2>/dev/null | sed 's#^mirror+file:##' | sort -u)
}

mapfile -t source_files < <(azure_files | sort -u)

# use_public moves every Azure URI to the public archive. It succeeds only when it changed something.
use_public() {
  [ "${#source_files[@]}" -gt 0 ] || return 1
  echo "$azure failed; using $public" >&2
  "${sudo_cmd[@]}" sed -i "s#//$azure/#//$public/#g" "${source_files[@]}"
  source_files=()
}

if [ "${#source_files[@]}" -gt 0 ]; then
  codename="$(sed -n 's/^VERSION_CODENAME=//p' /etc/os-release | tr -d "\"'")"
  if ! curl --fail --silent --show-error --location --connect-timeout 10 --max-time 15 --output /dev/null \
    "http://$azure/ubuntu/dists/$codename/InRelease"; then
    use_public
  fi
fi

for attempt in $(seq 1 "$attempts"); do
  if run_apt "$update_limit" --error-on=any update; then
    if run_apt "$install_limit" install --yes "$@"; then
      exit 0
    fi
    # An install that cannot download from a mirror that served the indexes means this mirror is unusable for
    # packages; the retry's update and install run against the public archive. use_public changes the
    # sources once, so a later failure repeats on the public archive and never returns to Azure.
    use_public || true
  else
    # An update that cannot fetch every index means this mirror is unusable; the same mirror would fail again.
    use_public || true
  fi
  echo "apt attempt $attempt of $attempts failed" >&2
  if [ "$attempt" -lt "$attempts" ] && [ $((deadline - SECONDS)) -gt $((attempt * 10 + kill_after)) ]; then
    sleep $((attempt * 10))
  fi
done
exit 1
