# SPDX-License-Identifier: MIT

# Hosted Linux jobs (the preflight job and the Linux shards) partition make check. test/ci-images checks this against the current prerequisite graph so adding a gate cannot silently omit it from CI.
# ci-preflight is the compile gate the Windows, macOS and Node loader jobs wait for, so a change that does not build or vet fails before they spend their minutes.
ci-preflight: build vet go-fix-clean
ci-startup: startup-proxies
ci-build: module-publication lint
# The light pull-request run (automation/ci/ci-tiers.txt) lints the whole changed files (CI sets LINT_BASE=origin/main); the full run keeps the repository-wide lint.
ci-build-pr: module-publication lint-changed
# Short gates share one hosted job each, so their setup is paid once. CI runs them with make -k, so one failing gate does not hide the others.
ci-checks: ci-startup ci-drift ci-closure
ci-checks-pr: ci-startup ci-drift-pr ci-closure
ci-toolchains: ci-race ci-integration ci-sdk ci-extensions
# CI runs test-fast and test-cli in halves on separate jobs; each pair together is the whole target.
ci-test-fast: test-fast-parallel
ci-test-fast-sharded: test-fast-sharded
ci-test-cli-1: test-cli-1
ci-test-cli-2: test-cli-2
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
# module-size's install proof is the one drift gate the light pull-request run leaves to the full run.
ci-drift: ci-drift-pr module-size
ci-drift-pr: lint-scenarios port-map-drift coverage-drift divergence-consistency divergence-quality divergence-guard port-lint source-hygiene test-provenance docs-drift bug-ledger-check any-trap perf-lint race-coverage perf-scoreboard-check
ci-contracts: correspondence-check porter-check interface-inventory interface-inventory-test interface-go-drift interface-recommendations-drift interface-mapping-quality interface-delta interface-gaps behavior-contracts test-inventory-drift test-inventory test-porting-release known-gaps-drift format-version-inventory custom-factory-ledger-drift sdk-surface-drift sdk-apidiff piglet-strip-ids-drift piglet-strip-build
ci-closure: closure-check
# portmap-check runs the evidence tests of every proven PORT_MAP row under coverage. It builds each evidence package's test binary once and
# caches each green row in build/portmap-coverage, but a cold run is still about 18 CPU-minutes of 4-CPU runner time, so CI runs it as
# PORTMAP_SHARDS parallel jobs; every shard computes the same plan, judges its own rows and skips the rest, and the Linux verification
# job requires all of them.
PORTMAP_SHARDS := 3
ci-portmap-1: portmap-check-1
ci-portmap-2: portmap-check-2
ci-portmap-3: portmap-check-3
portmap-check-1:
	@$(MAKE) -s portmap-check PORTMAP_SHARD=1/$(PORTMAP_SHARDS)
portmap-check-2:
	@$(MAKE) -s portmap-check PORTMAP_SHARD=2/$(PORTMAP_SHARDS)
portmap-check-3:
	@$(MAKE) -s portmap-check PORTMAP_SHARD=3/$(PORTMAP_SHARDS)

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

test-fast-parallel: test-prereqs interface-deps parity-deps durable-interop-deps
	@./automation/ci/test-grouped.sh fast-parallel

test-fast-sharded: test-prereqs interface-deps parity-deps durable-interop-deps
	@./automation/ci/test-grouped.sh fast-sharded

test-cli-1: test-prereqs interface-deps parity-deps durable-interop-deps
	@./automation/ci/test-grouped.sh cli 1 4

test-cli-2: test-prereqs interface-deps parity-deps durable-interop-deps
	@./automation/ci/test-grouped.sh cli 5 8

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

.PHONY: test-provenance test-expectation-lint ci-preflight ci-startup ci-build ci-build-pr ci-checks ci-checks-pr ci-toolchains ci-test-fast ci-test-fast-sharded ci-test-cli-1 ci-test-cli-2 ci-test-subprocess ci-test-conformance ci-sdk ci-extensions ci-race ci-integration ci-parity ci-parity-1 ci-parity-2 parity-fast-1 parity-fast-2 ci-drift ci-drift-pr ci-contracts ci-portmap-1 ci-portmap-2 ci-portmap-3 portmap-check-1 portmap-check-2 portmap-check-3 ci-closure test-fast test-fast-parallel test-fast-sharded test-cli test-cli-1 test-cli-2 test-subprocess test-conformance
