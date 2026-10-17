# Builds the Durable core candidates the conformance gate runs (durable/contract/impls.toml names these targets).
#   make -C durable/core build-go       the core with Go (GOOS=wasip1 reactor) as host/cf/dist/core-go.wasm
#   make -C durable/core build-tinygo   the same source with TinyGo (-scheduler=none) as host/cf/dist/core-tinygo.wasm
#   make -C durable/core build-native-bench   the bench-slice engine (store, rec, history on modernc SQLite) as contracttest/bin/benchhost,
#                                       and the storage-conformance provider (the write encoder as a Storage) as contracttest/bin/scprovider
#   make -C durable/core build-scprovider    only the storage-conformance provider (SC row of any candidate whose store is the core's)
# The JS facade (host/cf/src/facade) loads the .wasm named by DCORE_WASM; the gate sets it.
# The toolchain is the tree's build pin, .go-version at the repository root.
GO_VERSION_FILE := $(dir $(lastword $(MAKEFILE_LIST)))../../.go-version
GOTOOLCHAIN ?= go$(shell tr -d '\r\n' <"$(GO_VERSION_FILE)")
export GOTOOLCHAIN

.PHONY: build-go build-tinygo build-native-bench build-scprovider
build-go:
	sh budget/build-wasm.sh go cmd/corewasm host/cf/dist/core-go.wasm
build-tinygo:
	sh budget/build-wasm.sh tinygo cmd/corewasm host/cf/dist/core-tinygo.wasm
build-native-bench:
	GOWORK=off go build -o contracttest/bin/benchhost ./contracttest/cmd/benchhost
	GOWORK=off go build -o contracttest/bin/scprovider ./contracttest/cmd/scprovider
build-scprovider:
	GOWORK=off go build -o contracttest/bin/scprovider ./contracttest/cmd/scprovider
