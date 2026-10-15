# SPDX-License-Identifier: MIT

# Hosted Linux shards partition make check. test/ci-images checks this against the current prerequisite graph so adding a gate cannot silently omit it from CI.
ci-startup: startup-proxies
ci-build: module-publication build vet lint go-fix-clean
ci-test-fast: test-fast
ci-test-cli: test-cli
ci-test-subprocess: test-subprocess
ci-test-conformance: test-conformance
ci-sdk: test-go-modules test-sdk-rs test-sdk-py test-sdk-ts
ci-extensions: typescript-extension-corpus examples-check standard-check
ci-race: test-race
ci-integration: test-integration
ci-parity: parity-fast
# CI splits the hermetic parity scenarios in two by name hash; each half runs under the same 20-minute limit.
ci-parity-1: parity-fast-1
ci-parity-2: parity-fast-2
parity-fast-1:
	@$(MAKE) -s parity-fast PARITY_SHARD=1/2
parity-fast-2:
	@$(MAKE) -s parity-fast PARITY_SHARD=2/2
ci-drift: lint-scenarios port-map-drift coverage-drift divergence-consistency divergence-quality divergence-guard port-lint source-hygiene test-provenance docs-drift bug-ledger-check any-trap perf-lint perf-scoreboard-check
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

# A golden or snapshot file declares where its expected values come from (test/testdata-provenance.toml).
test-provenance: ## Fail on a golden or snapshot file without declared Pi provenance
	@go run ./test/parity/cmd/testprune lint -provenance

# An expectation edited in the same commit as the code it tests needs the Pi behavior change that justifies it.
test-expectation-lint: ## Fail on test expectations edited with their code and no Pi-Behavior-Change trailer (TEST_EXPECT_BASE, default main)
	@base=$$(git merge-base HEAD "$(TEST_EXPECT_BASE)") || { echo "test-expectation-lint: cannot find merge base with $(TEST_EXPECT_BASE)" >&2; exit 2; }; \
	go run ./test/parity/cmd/testprune lint -base "$$base"

TEST_EXPECT_BASE ?= main

.PHONY: test-provenance test-expectation-lint ci-startup ci-build ci-test-fast ci-test-cli ci-test-subprocess ci-test-conformance ci-sdk ci-extensions ci-race ci-integration ci-parity ci-parity-1 ci-parity-2 parity-fast-1 parity-fast-2 ci-drift ci-contracts ci-closure test-fast test-cli test-subprocess test-conformance
