#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail
here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=automation/layout/arguments.sh
source "$here/arguments.sh"
exec python3 -B "$here/build.py" "${options[@]}" "$root"
