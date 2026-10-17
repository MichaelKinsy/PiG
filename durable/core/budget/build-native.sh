#!/bin/sh
# Build the gate's native candidate (durable/contract/impls.toml `native`): the Go bench host for the W, X1 and H rows and
# cmd/corenative, the core the JS facade runs for the E, U and SC rows.
set -e
cd "$(dirname "$0")/../../.."
export GOTOOLCHAIN=${GOTOOLCHAIN:-go$(tr -d '\r\n' <.go-version)}
# -trimpath -buildvcs=false: the binaries are a function of the source alone, whatever GOFLAGS the caller exports.
go build -trimpath -buildvcs=false -o durable/core/sqlhost/bin/bench ./durable/core/sqlhost/cmd/bench
go build -trimpath -buildvcs=false -o durable/core/host/cf/dist/corenative ./durable/core/cmd/corenative
