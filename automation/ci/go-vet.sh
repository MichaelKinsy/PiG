#!/bin/sh
# SPDX-License-Identifier: MIT
# go vet over every package of the module except the vendored modernc.org/sqlite driver layer. Its uintptr to unsafe.Pointer
# conversions are the calling convention of the transpiled SQLite library and fail vet's unsafeptr check; the directory keeps
# upstream's code (internal/third_party/sqlite/FORK.md), as golangci-lint's and staticcheck's exclusions of third_party do.
# Arguments are go vet flags (for example -tags ...); GOOS, GOARCH and CGO_ENABLED are inherited.
set -eu
packages=$(go list ./... | grep -v '/internal/third_party/sqlite$')
# shellcheck disable=SC2086
exec go vet "$@" $packages
