# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT

# Hosted Linux shards partition make check. tests/ci-images checks this against the current prerequisite graph so adding a gate cannot silently omit it from CI.
ci-build: build vet lint go-fix-clean
ci-test-fast: test-fast
ci-test-cli: test-cli
ci-test-subprocess: test-subprocess
ci-test-conformance: test-conformance
ci-sdk: test-go-modules test-sdk-rs test-sdk-ts
ci-extensions: typescript-extension-corpus examples-check standard-check
ci-race: test-race
ci-integration: test-integration
ci-parity: parity-fast
ci-drift: lint-scenarios port-map-drift coverage-drift divergence-consistency divergence-quality divergence-guard source-hygiene docs-drift
ci-contracts: correspondence-check porter-check interface-inventory interface-inventory-test interface-go-drift interface-recommendations-drift interface-mapping-quality interface-delta behavior-contracts test-inventory-drift test-inventory format-version-inventory custom-factory-ledger-drift
ci-closure: closure-check

test-fast: test-prereqs interface-deps parity-deps
	@./automation/ci/test-grouped.sh fast

test-cli: test-prereqs interface-deps parity-deps
	@./automation/ci/test-grouped.sh cli

test-subprocess: test-prereqs interface-deps parity-deps
	@./automation/ci/test-grouped.sh subprocess

test-conformance: test-prereqs interface-deps parity-deps
	@./automation/ci/test-grouped.sh conformance

.PHONY: ci-build ci-test-fast ci-test-cli ci-test-subprocess ci-test-conformance ci-sdk ci-extensions ci-race ci-integration ci-parity ci-drift ci-contracts ci-closure test-fast test-cli test-subprocess test-conformance
