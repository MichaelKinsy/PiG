#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
set -euo pipefail
here=$(CDPATH="" cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=automation/layout/arguments.sh
source "$here/arguments.sh"
exec python3 -B "$here/docs.py" "${options[@]}" "$root"
