# SPDX-License-Identifier: MIT

# Hosted Linux shards partition make check. test/ci-images checks this against the current prerequisite graph so adding a gate cannot silently omit it from CI.
ci-startup: startup-proxies
ci-build: module-publication build vet lint go-fix-clean
ci-test-fast: test-fast
ci-test-cli: test-cli
ci-test-subprocess: test-subprocess
ci-test-conformance: test-conformance
ci-sdk: test-go-modules test-sdk-rs test-sdk-ts
ci-extensions: typescript-extension-corpus examples-check standard-check node-facets-chord-check
ci-race: test-race
ci-integration: test-integration
ci-parity: parity-fast
ci-drift: lint-scenarios port-map-drift coverage-drift divergence-consistency divergence-quality divergence-guard port-lint source-hygiene docs-drift
ci-contracts: correspondence-check porter-check interface-inventory interface-inventory-test interface-go-drift interface-recommendations-drift interface-mapping-quality interface-delta interface-gaps portmap-check behavior-contracts test-inventory-drift test-inventory test-porting-release known-gaps-drift format-version-inventory custom-factory-ledger-drift sdk-surface-drift sdk-apidiff piglet-strip-ids-drift piglet-strip-build
ci-closure: closure-check

# Run after upstream-mirror under each selected Node runtime. Do not reinstall
# npm dependencies here: the qualified npm requires a newer Node than Pi does.
# The matrix selects extension hosting/runtime tests. Go URL-conversion oracle
# tests stay in the full suite on the qualified Node pin.
ci-node-runtime:
	go test -tags=parity -count=1 ./coding/extension/host/subprocess -run '^Test(Node|EmbeddedNode|ResolveNode|EnsureNode|Host.*Node|Builder.*Node|BuildExtCommand_Node|PiDiff|PiScrollView|PiTui|VendoredCrossSpawn|TerminalInputNode)'
	go test -tags=parity -count=1 ./coding/cli -run '^TestRPCStartupMalformedPackageManifests$$'
	@mkdir -p $(dir $(PARITY_PIG_BIN))
	go build -o $(PARITY_PIG_BIN) ./cmd/pig
	@results=$$($(MKTEMP)); trap 'rm -f "$$results"' EXIT; \
		PIG_PARITY_PIG_BIN="$(PARITY_PIG_BIN)" go test -tags=parity -count=1 ./test/parity/runner \
		-run 'TestParity/(23-extension-directory-entries|26-extension-load-order|31-extension-runtime-surface|32-extension-load-failure-message|34-extension-(module-loading|process-identity)|55-(package-manifest-startup|node-package-entry-exports))$$' \
		-args -pig-parity.results="$$results" && \
		$(MAKE) -s require-parity-ran RESULTS="$$results"

.PHONY: ci-node-runtime

test-fast: test-prereqs interface-deps parity-deps durable-interop-deps
	@./automation/ci/test-grouped.sh fast

test-cli: test-prereqs interface-deps parity-deps durable-interop-deps
	@./automation/ci/test-grouped.sh cli

test-subprocess: test-prereqs interface-deps parity-deps durable-interop-deps
	@./automation/ci/test-grouped.sh subprocess

test-conformance: test-prereqs interface-deps parity-deps durable-interop-deps
	@./automation/ci/test-grouped.sh conformance

.PHONY: ci-startup ci-build ci-test-fast ci-test-cli ci-test-subprocess ci-test-conformance ci-sdk ci-extensions ci-race ci-integration ci-parity ci-drift ci-contracts ci-closure test-fast test-cli test-subprocess test-conformance
