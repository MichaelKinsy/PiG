#!/bin/sh
# SPDX-License-Identifier: MIT
# go vet over every package of the module. The vendored modernc.org/sqlite driver layer is vetted with every analyzer but
# unsafeptr: its uintptr to unsafe.Pointer conversions are the calling convention of the transpiled SQLite library, and
# upstream's own package fails that one analyzer too (third_party/sqlite/FORK.md). Arguments are go vet flags (for example
# -tags ...); GOOS, GOARCH and CGO_ENABLED are inherited.
set -eu
vendored=github.com/MichaelKinsy/PiG/third_party/sqlite
# shellcheck disable=SC2046
go vet "$@" $(go list ./... | grep -v "^$vendored\$")
go vet "$@" -unsafeptr=false "$vendored"
