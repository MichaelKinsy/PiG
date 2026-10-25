#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
set -euo pipefail

# Installs the apt packages the Linux test runs need beyond the runner image: tmux, fd and ripgrep for the coding-agent
# fixtures, a C toolchain with pkg-config and libxcb headers for the native clipboard fixtures, and Xvfb with xclip for
# the tests that start a real X11 selection server and its reader (internal/nativeplatform, the vendored Pi
# native-clipboard-linux test). Every workflow job that runs those tests calls this script, so the list has one owner:
# a job with its own list lost xclip once and the clipboard tests failed there with "xclip: executable file not found".
# Extra arguments name further packages for one job.

exec "$(dirname "${BASH_SOURCE[0]}")/install-apt-packages.sh" tmux fd-find ripgrep build-essential xvfb xclip pkg-config libxcb1-dev "$@"
