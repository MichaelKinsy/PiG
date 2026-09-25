# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
#
# Parity machinery: the gates, inventories, and scenario runners that keep PiG
# faithful to the pinned Pi release. The root Makefile includes this file, so
# every target runs as `make <target>` from the repository root, and
# `make help-parity` lists them. parity/README.md explains the workflow.

SOURCE_HYGIENE_BASE ?= $(shell git rev-parse --verify HEAD^ >/dev/null 2>&1 && echo HEAD^ || echo HEAD)

# Generate a subprocess coverage profile from a successful parity run. The
# target fails when parity fails; a partial profile is never reported as proof.
PARITY_FLOW_COVERAGE ?= $(PIG_TMP)/pig-parity-cover.out

PARITY_FLOW_RUN ?= .

##@ Parity: faithfulness to the pinned Pi release

typescript-extension-corpus: parity-bin ## Validate every pinned upstream TypeScript extension example
	@go run ./parity/cmd/extensioncorpus \
		-root "$(CURDIR)" \
		-pig-bin "$(PARITY_PIG_BIN)" \
		-out "$(EXTENSION_CORPUS_RESULTS)"

schedule-report: ## Print Go-test and parity scheduler buckets
	@./automation/ci/report-schedule.sh

behavior-input-inventory-drift:
	@tmp=$$($(MKTEMP)); trap 'rm -f "$$tmp"' EXIT; \
		cd parity/interface-extractor && \
		node src/extract-behavior-inputs.mjs \
			--source-root ../../.upstream/current \
			--upstream-version "$(UPSTREAM_VERSION)" \
			--out "$$tmp" && \
		cmp "$$tmp" ../interfaces/behavior-inputs-v$(UPSTREAM_VERSION).json
	@echo "behavior-input-inventory-drift: clean"

behavior-input-mapping-proposal:
	@go run ./parity/cmd/behaviorcheck \
		-generate-input-mapping \
		-input-inventory parity/interfaces/behavior-inputs-v$(UPSTREAM_VERSION).json \
		-previous-input-mapping parity/interfaces/behavior-input-mapping-v$(UPSTREAM_REVIEWED_VERSION).json \
		-owner-overrides parity/interfaces/behavior-owner-overrides-v$(UPSTREAM_VERSION).json \
		-families parity/families.toml \
		-output-input-mapping parity/interfaces/behavior-input-mapping-v$(UPSTREAM_VERSION).json

# test-inventory-drift regenerates the upstream-test denominator from the pinned
# source tree and fails if it differs from the committed inventory, so an upstream
# leap that adds, removes, or edits a test file forces the inventory (and, in
# turn, the reviewed disposition mapping) to be brought current.
test-inventory-drift: ## Test-inventory-drift regenerates the upstream-test denominator from the pinned
	@tmp=$$($(MKTEMP)); trap 'rm -f "$$tmp"' EXIT; \
		cd parity/interface-extractor && \
		node src/extract-test-inventory.mjs \
			--source-root ../../.upstream/current \
			--upstream-version "$(UPSTREAM_VERSION)" \
			--out "$$tmp" && \
		cmp "$$tmp" ../interfaces/upstream-tests-v$(UPSTREAM_VERSION).json
	@echo "test-inventory-drift: clean"

# test-inventory validates the reviewed disposition mapping over the upstream-test
# inventory: every test file has a disposition, hashes are current, and every
# ported/scenario-covered/divergence claim still resolves to real evidence.
test-inventory: ## Test-inventory validates the reviewed disposition mapping over the upstream-test
	@go run ./parity/cmd/testinventorycheck

# test-inventory-strict additionally rejects pending and partial dispositions.
# It joins foundation-check only once the pending frontier reaches zero.
test-inventory-strict: ## Test-inventory-strict additionally rejects pending and partial dispositions
	@go run ./parity/cmd/testinventorycheck -strict

behavior-contracts: behavior-input-inventory-drift
	@go run ./parity/cmd/behaviorcheck

behavior-contracts-strict: behavior-input-inventory-drift
	@go run ./parity/cmd/behaviorcheck -strict

source-hygiene: check-scratch-paths
	@go run ./parity/cmd/sourcehygiene -diff-base '$(SOURCE_HYGIENE_BASE)'

source-hygiene-full: check-scratch-paths
	@go run ./parity/cmd/sourcehygiene -full

# check-scratch-paths rejects a tracked file outside delivery/ that leaks an
# internal scratch-cache path or this operator's absolute machine path into
# the public tree (see RELEASE-DRYRUN.md blocker 4).
check-scratch-paths:
	@python3 -B -m unittest discover -s automation/ci -p test_public_hygiene.py
	@./automation/ci/check-scratch-paths.sh

# Default gate. If this is green, the change is safe to commit.
format-version-inventory: ## Validate reviewed ownership of every production version-like field
	@go run ./parity/cmd/formatversions -marker-roots ".,piglets"

format-version-policy: ## Reject remaining Pig-owned format discriminators (strict)
	@go run ./parity/cmd/formatversions -strict -marker-roots ".,piglets"

closure-check: interface-deps ## Validate deterministic closure schema/store/derivation/query contracts
	go test ./parity/closure ./parity/cmd/closure

correspondence-check: interface-deps
	go test ./parity/correspondence ./parity/cmd/correspondence
	@go run ./parity/cmd/correspondence compare \
		-root . \
		-upstream-version "$(UPSTREAM_VERSION)" \
		-target-worktree >/dev/null
	@echo "correspondence-check: direct facts agree except listed known gaps; packet paths run in go test"

porter: ## Start the interactive Pig Porter workbench
	@$(MAKE) -s parity-bin
	@./automation/porter/run-pig-porter.sh $(PORTER_ARGS)

porter-task: ## Run one bounded headless Porter task
	@test -n "$(TASK)" || { echo "TASK is required (for example: make porter-task TASK='verify rpc')" >&2; exit 2; }
	@$(MAKE) -s parity-bin
	@./automation/porter/run-pig-porter.sh --no-session $(PORTER_ARGS) -p "/skill:pig-porter $(TASK)"

porter-campaign: ## Run read-only workers
	@test "$(MODE)" = "inventory" -o "$(MODE)" = "verify" || { echo "MODE must be inventory or verify" >&2; exit 2; }
	@test -n "$(FAMILIES)" || { echo "FAMILIES is required" >&2; exit 2; }
	@$(MAKE) -s parity-bin
	@PIG_BIN="$(PARITY_PIG_BIN)" ./automation/porter/run-pig-porter-campaign.sh "$(MODE)" $(FAMILIES)

porter-check: ## Verify the direct Porter contract and Piglet extension adapter
	go test ./parity/porter ./parity/cmd/porter
	@go build -buildvcs=false -o $(CHECK_PIG_BIN) ./cmd/pig
	@GOWORK=off $(CHECK_PIG_BIN) install --validate-only --json piglets/porter/extensions/pig-porter >/dev/null
	@GOWORK=off $(CHECK_PIG_BIN) piglet validate piglets/porter/pig-porter.yaml >/dev/null

porter-smoke: ## Verify the Porter Piglet in TUI and headless modes
	@./automation/porter/verify-pig-porter-local.sh

custom-factory-ledger: ## Regenerate the internal pinned custom-factory draft
	@go run ./parity/cmd/customfactoryledger

custom-factory-ledger-drift: ## Validate the checked-in internal draft
	@go test ./parity/cmd/customfactoryledger -count=1
	@go run ./parity/cmd/customfactoryledger -check

check-contracts-fast: closure-check correspondence-check porter-check interface-inventory interface-inventory-test interface-go-drift interface-recommendations-drift interface-mapping-quality interface-delta behavior-contracts test-inventory-drift test-inventory format-version-inventory custom-factory-ledger-drift ## Local generated contract validation and drift gates

check-contracts: check-contracts-fast interface-inventory-drift ## Fast contracts + exact published-package inventory regeneration

# Foundation gate used before advanced Piglet work or a completed upstream
# leap. Unlike the normal development gate, it rejects every partial, broken,
# not-started, weak-only, untested, or family-unassigned intended port.
foundation-check: check-core check-contracts parity source-hygiene-full divergence-quality interface-recommendations interface-mapping-strict interface-delta-strict upstream-delta async-contracts behavior-contracts-strict format-version-policy coverage-strict ## Check + strict semantic interface and behavioral completeness gates
	@$(MAKE) -s family-gaps STRICT=1
	@echo "make foundation-check: complete intended-portable behavior is assigned and verified."

parity-bin: parity-deps
	@mkdir -p $(dir $(PARITY_PIG_BIN))
	CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "-s -w -X main.Build=$$(git rev-parse --short HEAD 2>/dev/null || echo dev)" -o $(PARITY_PIG_BIN) ./cmd/pig
	@[ "$$(uname -s)" = "Darwin" ] && command -v codesign >/dev/null 2>&1 && codesign --force --sign - $(PARITY_PIG_BIN) >/dev/null 2>&1 || true
	@echo "parity pig: $$($(PARITY_PIG_BIN) --version) → $(PARITY_PIG_BIN)"

# require-parity-ran is the loud-fail counterpart to TestParity's quiet
# skips. ResolvePigBin/ResolveUpstreamPiBin (parity/runner/binaries.go) call
# t.Skip with an explicit reason when PIG_PARITY_PIG_BIN/PIG_BIN is unset, or
# when tmux or the pinned Pi 0.87.1 binary is missing - and that skip happens
# before TestParity runs a single scenario or writes RESULTS, so `go test`
# still exits 0. A results file with zero outcomes (or no results file at
# all - the two are equivalent here) means nothing was actually compared
# against real Pi, which must never look the same as "everything passed".
#
# Set PIG_PARITY_ALLOW_ZERO=1 to accept a zero-scenario run on purpose (for
# example, a sandbox that intentionally has no Pi install).
require-parity-ran:
	@if [ -n "$$PIG_PARITY_ALLOW_ZERO" ]; then \
	    echo "require-parity-ran: PIG_PARITY_ALLOW_ZERO set, not checking $(RESULTS)"; \
	    exit 0; \
	  fi; \
	  n=$$(go run ./parity/cmd/checkparityran -results "$(RESULTS)") || exit 1; \
	  if [ "$$n" -eq 0 ]; then \
	    echo "require-parity-ran: 0 parity scenarios ran (results file: $(RESULTS))." >&2; \
	    echo "This means the parity suite skipped before comparing anything against real Pi -" >&2; \
	    echo "check PIG_PARITY_PIG_BIN/PIG_BIN, tmux, and the pinned Pi 0.87.1 install." >&2; \
	    echo "A green 'go test' exit code here is a skip, not a pass." >&2; \
	    echo "Set PIG_PARITY_ALLOW_ZERO=1 to accept this on purpose." >&2; \
	    exit 1; \
	  fi; \
	  echo "require-parity-ran: $$n parity scenario(s) actually ran"

# Hermetic scenarios (no real LLM, no real network).
# Runs in parallel by default; scenarios with runtime_ratio_max or the
# "serial" tag automatically opt out. To force serial execution (e.g. when
# diagnosing flakes), pass -pig-parity.serial=true.
#
# Cleanup is layered so each mechanism catches what the prior misses:
#   1. defer killSession(session) : happy path in each test goroutine
#   2. TestMain signal handler    : SIGINT/SIGTERM/SIGHUP on the process
#   3. Makefile trap on EXIT      : catches SIGKILL and kills only sessions
#      owned by that Make invocation (the in-process handler never fires,
#      but the shell survives and runs the trap)
parity: parity-bin ## Scenario-driven comparison with resource-sensitive concurrency groups
	@rm -f "$(RESULTS)"
	@run_id=$$$$; \
	  cleanup_tmux() { tmux -L "pig-parity-$$run_id" list-sessions -F '#{session_name}' 2>/dev/null | xargs -I{} tmux -L "pig-parity-$$run_id" kill-session -t {} 2>/dev/null || true; }; \
	  trap cleanup_tmux EXIT; \
	  PIG_PARITY_RUN_ID="$$run_id" PIG_PARITY_PIG_BIN="$(PARITY_PIG_BIN)" go test -tags=parity -count=1 -timeout $(PARITY_TIMEOUT) -parallel $(PARITY_PARALLEL) ./parity/runner \
	    -args -pig-parity.tags=hermetic -pig-parity.group-limits=$(PARITY_GROUP_LIMITS) -pig-parity.results=$(RESULTS)
	@$(MAKE) -s require-parity-ran RESULTS=$(RESULTS)
	@$(MAKE) -s coverage

# Fast pre-commit gate: identical scenarios, assertions, isolation, and
# fail-fast behavior as `make parity`, but one Pig/Pi pair per scenario.
# CI and explicit full verification keep each scenario's declared durability.
parity-fast: parity-bin ## One strict Pig/Pi pair per hermetic scenario for the pre-commit loop
	@rm -f "$(RESULTS)"
	@run_id=$$$$; \
	  cleanup_tmux() { tmux -L "pig-parity-$$run_id" list-sessions -F '#{session_name}' 2>/dev/null | xargs -I{} tmux -L "pig-parity-$$run_id" kill-session -t {} 2>/dev/null || true; }; \
	  trap cleanup_tmux EXIT; \
	  PIG_PARITY_RUN_ID="$$run_id" PIG_PARITY_PIG_BIN="$(PARITY_PIG_BIN)" go test -tags=parity -count=1 -timeout $(PARITY_TIMEOUT) -parallel $(PARITY_PARALLEL) ./parity/runner \
	    -args -pig-parity.tags=hermetic -pig-parity.runs=1 -pig-parity.group-limits=$(PARITY_GROUP_LIMITS) -pig-parity.results=$(RESULTS)
	@$(MAKE) -s require-parity-ran RESULTS=$(RESULTS)

parity-family: parity-bin ## Run one hermetic family and stop on the first failed pair
	@test -n "$(FAMILY)" || (echo "FAMILY is required (for example: make parity-family FAMILY=compaction)" >&2; exit 2)
	@test -d "parity/scenarios/$(FAMILY)" || (echo "unknown parity family: $(FAMILY)" >&2; exit 2)
	@run_id=$$$$; \
	  cleanup_tmux() { tmux -L "pig-parity-$$run_id" list-sessions -F '#{session_name}' 2>/dev/null | xargs -I{} tmux -L "pig-parity-$$run_id" kill-session -t {} 2>/dev/null || true; }; \
	  trap cleanup_tmux EXIT; \
	  PIG_PARITY_RUN_ID="$$run_id" PIG_PARITY_PIG_BIN="$(PARITY_PIG_BIN)" go test -tags=parity -count=1 -timeout $(PARITY_TIMEOUT) -parallel $(PARITY_PARALLEL) ./parity/runner \
	    -args -pig-parity.dir="$(CURDIR)/parity/scenarios/$(FAMILY)" -pig-parity.tags=hermetic -pig-parity.group-limits=$(PARITY_GROUP_LIMITS)

parity-driver: parity-bin ## Run one pair for selected execution modes
	@test -n "$(DRIVER)" || (echo "DRIVER is required (for example: make parity-driver DRIVER=rpc-mode)" >&2; exit 2)
	@run_id=$$$$; \
	  cleanup_tmux() { tmux -L "pig-parity-$$run_id" list-sessions -F '#{session_name}' 2>/dev/null | xargs -I{} tmux -L "pig-parity-$$run_id" kill-session -t {} 2>/dev/null || true; }; \
	  trap cleanup_tmux EXIT; \
	  PIG_PARITY_RUN_ID="$$run_id" PIG_PARITY_PIG_BIN="$(PARITY_PIG_BIN)" go test -tags=parity -count=1 -timeout $(PARITY_TIMEOUT) -parallel $(PARITY_PARALLEL) ./parity/runner \
	    -args -pig-parity.tags=hermetic -pig-parity.drivers="$(DRIVER)" -pig-parity.runs=1 -pig-parity.group-limits=$(PARITY_GROUP_LIMITS)

# Scheduler stress repeats the complete suite twice with one pair per scenario.
# parity-durable owns each scenario's declared multi-run durability separately.
parity-stress: parity-bin ## Repeat parity under the default group scheduler
	@rm -f "$(RESULTS)"
	@run_id=$$$$; \
	  cleanup_tmux() { tmux -L "pig-parity-$$run_id" list-sessions -F '#{session_name}' 2>/dev/null | xargs -I{} tmux -L "pig-parity-$$run_id" kill-session -t {} 2>/dev/null || true; }; \
	  trap cleanup_tmux EXIT; \
	  PIG_PARITY_RUN_ID="$$run_id" PIG_PARITY_PIG_BIN="$(PARITY_PIG_BIN)" go test -tags=parity -count=2 -timeout $(PARITY_STRESS_TIMEOUT) -parallel $(PARITY_PARALLEL) ./parity/runner \
	    -args -pig-parity.tags=hermetic -pig-parity.runs=1 -pig-parity.group-limits=$(PARITY_GROUP_LIMITS) -pig-parity.results=$(RESULTS)
	@$(MAKE) -s require-parity-ran RESULTS=$(RESULTS)
	@$(MAKE) -s coverage

# Durability gate: require three paired runs inside each scenario. Keeping the
# repetitions in RunScenario makes the first failed pair stop that scenario;
# `go test -count=3` would restart the whole suite after a deterministic failure.
parity-durable: parity-bin ## Run every scenario 3x in one go test invocation (durability gate)
	@rm -f "$(RESULTS)"
	@run_id=$$$$; \
	  cleanup_tmux() { tmux -L "pig-parity-$$run_id" list-sessions -F '#{session_name}' 2>/dev/null | xargs -I{} tmux -L "pig-parity-$$run_id" kill-session -t {} 2>/dev/null || true; }; \
	  trap cleanup_tmux EXIT; \
	  PIG_PARITY_RUN_ID="$$run_id" PIG_PARITY_PIG_BIN="$(PARITY_PIG_BIN)" go test -tags=parity -count=1 -timeout $(PARITY_DURABLE_TIMEOUT) -parallel $(PARITY_PARALLEL) ./parity/runner \
	    -args -pig-parity.tags=hermetic -pig-parity.runs=3 -pig-parity.group-limits=$(PARITY_GROUP_LIMITS) -pig-parity.results=$(RESULTS)
	@$(MAKE) -s require-parity-ran RESULTS=$(RESULTS)
	@$(MAKE) -s coverage

# Full suite including live LLM calls. Costs tokens, takes minutes.
# Live scenarios stay serial because billing concurrency is fragile.
parity-live: parity-bin ## Add scenarios tagged 'live' (real LLM calls)
	@rm -f "$(RESULTS)"
	@run_id=$$$$; \
	  cleanup_tmux() { tmux -L "pig-parity-$$run_id" list-sessions -F '#{session_name}' 2>/dev/null | xargs -I{} tmux -L "pig-parity-$$run_id" kill-session -t {} 2>/dev/null || true; }; \
	  trap cleanup_tmux EXIT; \
	  PIG_PARITY_RUN_ID="$$run_id" PIG_PARITY_PIG_BIN="$(PARITY_PIG_BIN)" go test -tags=parity -count=1 -v -timeout 30m -parallel 1 ./parity/runner \
	    -args -pig-parity.serial=true -pig-parity.results=$(RESULTS)
	@$(MAKE) -s require-parity-ran RESULTS=$(RESULTS)
	@$(MAKE) -s coverage

# Authoritative performance gate: runs only scenarios that enforce
# runtime_ratio_max, with one uncontended pair per scenario. parity-durable
# owns repeated behavior runs; scheduled jobs can repeat this complete gate.
# Intended for release-check and weekly runs, not for the dev loop.
parity-perf: parity-bin ## Authoritative performance gate: runs only scenarios that enforce
	@rm -f "$(RESULTS)"
	@run_id=$$$$; \
	  cleanup_tmux() { tmux -L "pig-parity-$$run_id" list-sessions -F '#{session_name}' 2>/dev/null | xargs -I{} tmux -L "pig-parity-$$run_id" kill-session -t {} 2>/dev/null || true; }; \
	  trap cleanup_tmux EXIT; \
	  PIG_PARITY_RUN_ID="$$run_id" PIG_PARITY_PIG_BIN="$(PARITY_PIG_BIN)" go test -tags=parity -count=1 -v -timeout 30m -parallel 1 ./parity/runner \
	    -args -pig-parity.tags=fast,hermetic -pig-parity.serial=true -pig-parity.runtime-ratio-only=true -pig-parity.runs=1 \
	    -pig-parity.results=$(RESULTS)
	@$(MAKE) -s require-parity-ran RESULTS=$(RESULTS)
	@$(MAKE) -s coverage

parity-flow-coverage: parity-deps
	@set -eu; covdir=$$($(MKTEMP_DIR)); trap 'rm -rf "$$covdir"' EXIT; \
		go build -cover -o "$$covdir/pig" ./cmd/pig; \
		GOCOVERDIR="$$covdir" \
		PIG_PARITY_PIG_BIN="$$covdir/pig" \
		PIG_PARITY_PI_BIN="$(PIG_PARITY_PI_BIN)" \
		go test -tags=parity ./parity/runner -run '$(PARITY_FLOW_RUN)' -count=1 -timeout 30m; \
		go tool covdata textfmt -i="$$covdir" -o="$(PARITY_FLOW_COVERAGE)"; \
		echo "$(PARITY_FLOW_COVERAGE)"

# Regenerate parity/coverage.md from the latest results, and patch the
# condensed coverage block in AGENTS.md and the porting block in README.md.
coverage: ## Regenerate coverage.md from PORT_MAP + scenarios
	@set -eu; mkdir -p tmp; tmp=$$(mktemp -d tmp/coverage.XXXXXXXX); trap 'rm -rf "$$tmp"' EXIT; \
	    cp AGENTS.md "$$tmp/AGENTS.md"; cp README.md "$$tmp/README.md"; \
	    go run ./parity/cmd/coverage \
	    $(if $(filter command line environment override,$(origin RESULTS)),$(if $(RESULTS),-results "$(RESULTS)",),$(if $(wildcard $(RESULTS)),-results "$(RESULTS)",)) \
	    -port-map PORT_MAP.md \
	    -scenarios parity/scenarios \
	    -agents-md "$$tmp/AGENTS.md" \
	    -readme "$$tmp/README.md" \
	    -badge "$$tmp/badge.svg" > "$$tmp/coverage.md"; \
	    mv "$$tmp/coverage.md" parity/coverage.md; \
	    mv "$$tmp/AGENTS.md" AGENTS.md; mv "$$tmp/README.md" README.md; \
	    mv "$$tmp/badge.svg" .github/badges/parity-coverage.svg
	@echo "wrote parity/coverage.md + AGENTS.md coverage block + README porting block + parity coverage badge"
	@head -6 parity/coverage.md | tail -2

interface-proposals: parity-deps interface-deps ## Regenerate unreviewed interface inventories and recommendations
	@set -eu; tmp=$$($(MKTEMP_DIR)); trap 'rm -rf "$$tmp"' EXIT; \
		cd parity/interface-extractor; \
		npm test >/dev/null; \
		node --max-old-space-size=3072 src/extract.mjs \
			--source-root ../../.upstream/current \
			--published-root "$(PI_PACKAGE_ROOT)" \
			--upstream-version "$(UPSTREAM_VERSION)" \
			--source-out "$$tmp/source.json" \
			--published-out "$$tmp/upstream.json"; \
		node src/extract-cli.mjs \
			--source-root ../../.upstream/current \
			--upstream-version "$(UPSTREAM_VERSION)" \
			--out "$$tmp/cli.json"; \
		node src/extract-behavior-inputs.mjs \
			--source-root ../../.upstream/current \
			--upstream-version "$(UPSTREAM_VERSION)" \
			--out "$$tmp/behavior-inputs.json"; \
		cd "$(CURDIR)"; \
		go run ./parity/cmd/gointerfaces -out "$$tmp/pig-go.json"; \
		go run ./parity/cmd/interfacerecommend \
			-inventory "$$tmp/upstream.json" \
			-observable "$$tmp/cli.json" \
			-go-inventory "$$tmp/pig-go.json" \
			-out "$$tmp/recommendations.json"; \
		go run ./parity/cmd/interfaceinventory \
			-inventory "$$tmp/upstream.json" \
			-observable "$$tmp/cli.json" \
			-go-inventory "$$tmp/pig-go.json" \
			-recommendations "$$tmp/recommendations.json"; \
		install -m 0644 "$$tmp/upstream.json" "parity/interfaces/upstream-v$(UPSTREAM_VERSION).json"; \
		install -m 0644 "$$tmp/cli.json" "parity/interfaces/cli-v$(UPSTREAM_VERSION).json"; \
		install -m 0644 "$$tmp/pig-go.json" parity/interfaces/pig-go.json; \
		install -m 0644 "$$tmp/behavior-inputs.json" "parity/interfaces/behavior-inputs-v$(UPSTREAM_VERSION).json"; \
		install -m 0644 "$$tmp/recommendations.json" "parity/interfaces/recommendations-v$(UPSTREAM_VERSION).json"
	@echo "interface proposals: generated $(UPSTREAM_VERSION) inventories and recommendations; reviewed mapping unchanged"

interface-proposal-check: ## Validate one recommendation file
	@test -n "$(PROPOSAL)" || { echo "PROPOSAL is required" >&2; exit 2; }
	@go run ./parity/cmd/interfaceinventory \
		-inventory "parity/interfaces/upstream-v$(UPSTREAM_VERSION).json" \
		-observable "parity/interfaces/cli-v$(UPSTREAM_VERSION).json" \
		-recommendations "$(PROPOSAL)"

interface-inventory: ## Validate the generated public package interface inventory
	@go run ./parity/cmd/interfaceinventory \
		-inventory parity/interfaces/upstream-v$(UPSTREAM_VERSION).json \
		-observable parity/interfaces/cli-v$(UPSTREAM_VERSION).json

interface-go-drift:
	@tmp=$$($(MKTEMP)); trap 'rm -f "$$tmp"' EXIT; \
		go run ./parity/cmd/gointerfaces -out "$$tmp" && \
		cmp "$$tmp" parity/interfaces/pig-go.json
	@echo "interface-go-drift: clean"

interface-recommendations-drift: interface-go-drift ## Regenerate Pig Go candidates and proposals
	@tmp=$$($(MKTEMP)); trap 'rm -f "$$tmp"' EXIT; \
		go run ./parity/cmd/interfacerecommend \
			-inventory parity/interfaces/upstream-v$(UPSTREAM_VERSION).json \
			-observable parity/interfaces/cli-v$(UPSTREAM_VERSION).json \
			-go-inventory parity/interfaces/pig-go.json \
			-out "$$tmp" && \
		cmp "$$tmp" parity/interfaces/recommendations-v$(UPSTREAM_VERSION).json
	@echo "interface-recommendations-drift: clean"

interface-recommendations: ## Validate non-authoritative agent porting proposals
	@go run ./parity/cmd/interfaceinventory \
		-inventory parity/interfaces/upstream-v$(UPSTREAM_VERSION).json \
		-observable parity/interfaces/cli-v$(UPSTREAM_VERSION).json \
		-recommendations parity/interfaces/recommendations-v$(UPSTREAM_VERSION).json

interface-mapping-quality: ## Validate mapping shapes, hierarchy, layers, references, and proposals without requiring closure
	@go run ./parity/cmd/interfaceinventory \
		-inventory parity/interfaces/upstream-v$(UPSTREAM_VERSION).json \
		-observable parity/interfaces/cli-v$(UPSTREAM_VERSION).json \
		-mapping parity/interfaces/mapping-v$(UPSTREAM_VERSION).json \
		-recommendations parity/interfaces/recommendations-v$(UPSTREAM_VERSION).json \
		-go-inventory parity/interfaces/pig-go.json

interface-mapping-strict: ## Require every semantic interface mapping to be fully closed
	@go run ./parity/cmd/interfaceinventory \
		-inventory parity/interfaces/upstream-v$(UPSTREAM_VERSION).json \
		-observable parity/interfaces/cli-v$(UPSTREAM_VERSION).json \
		-mapping parity/interfaces/mapping-v$(UPSTREAM_VERSION).json \
		-strict

interface-delta:
	@go run ./parity/cmd/interfacedelta \
		-from-inventory parity/interfaces/upstream-v$(UPSTREAM_REVIEWED_VERSION).json \
		-from-observable parity/interfaces/cli-v$(UPSTREAM_REVIEWED_VERSION).json \
		-to-inventory parity/interfaces/upstream-v$(UPSTREAM_VERSION).json \
		-to-observable parity/interfaces/cli-v$(UPSTREAM_VERSION).json \
		-manifest parity/interfaces/delta-v$(UPSTREAM_REVIEWED_VERSION)-v$(UPSTREAM_VERSION).json

interface-delta-strict:
	@go run ./parity/cmd/interfacedelta \
		-from-inventory parity/interfaces/upstream-v$(UPSTREAM_REVIEWED_VERSION).json \
		-from-observable parity/interfaces/cli-v$(UPSTREAM_REVIEWED_VERSION).json \
		-to-inventory parity/interfaces/upstream-v$(UPSTREAM_VERSION).json \
		-to-observable parity/interfaces/cli-v$(UPSTREAM_VERSION).json \
		-manifest parity/interfaces/delta-v$(UPSTREAM_REVIEWED_VERSION)-v$(UPSTREAM_VERSION).json \
		-strict

interface-inventory-test:
	@cd parity/interface-extractor && npm test

# The extraction (TypeScript compiler over the pinned upstream source tree and
# the exact published Pi package) is expensive and its inputs almost never
# change between candidates, so it is content-keyed cached by
# automation/ci/interface-extract-cache.sh under
# $(PIG_CACHE_HOME)/pig-interface-extract/<key>/. The cache only ever supplies
# the extractor's output bytes; the comparison against the committed
# inventories below always runs, cache hit or miss, so a stale committed file
# still fails this target. Set PIG_INTERFACE_EXTRACT_CACHE=0 to force a fresh
# extraction.
interface-inventory-drift: parity-deps interface-deps ## Regenerate from exact source/published package and compare
	@test -d "$(PI_PACKAGE_ROOT)" || { echo "exact published Pi package not found: $(PI_PACKAGE_ROOT)" >&2; exit 1; }
	@tmp_source=$$($(MKTEMP)); tmp_published=$$($(MKTEMP)); tmp_cli=$$($(MKTEMP)); \
		trap 'rm -f "$$tmp_source" "$$tmp_published" "$$tmp_cli"' EXIT; \
		./automation/ci/interface-extract-cache.sh \
			.upstream/current \
			"$(PI_PACKAGE_ROOT)" \
			"$(UPSTREAM_VERSION)" \
			parity/interface-extractor \
			"$$tmp_source" "$$tmp_published" "$$tmp_cli" && \
		cmp "$$tmp_published" parity/interfaces/upstream-v$(UPSTREAM_VERSION).json && \
		cmp "$$tmp_cli" parity/interfaces/cli-v$(UPSTREAM_VERSION).json
	@echo "interface-inventory-drift: clean ($(UPSTREAM_VERSION))"

coverage-strict:
	@go run ./parity/cmd/coverage \
	    -results $(RESULTS) \
	    -port-map PORT_MAP.md \
	    -scenarios parity/scenarios \
	    -strict >/dev/null

upstream-delta: ## Audit every changed source file in the pinned upstream leap
	@go run ./parity/cmd/upstreamdelta

# Honest gap ledger: reconcile the upstream interface inventory against PORT_MAP
# dispositions and the on-disk Go tree. Fails if any declaration is a genuine gap
# (mapped Go target absent), so CI asserts the missing-file count stays zero.
port-reconcile: ## Honest gap ledger: reconcile the upstream interface inventory against PORT_MAP
	@go run ./parity/cmd/portreconcile

# Per-group review bundles: top-level declarations aggregated by upstream source
# directory, each with its class breakdown and the parity scenarios that claim it.
# The unit a human approves once instead of proving each declaration. Pass
# COVERAGE=<profile> to split implemented into covered/uncovered, and
# PARITY_COVERAGE=<profile> to tier implemented as parity/unit-only/uncovered.
port-groups: ## Per-group review bundles: top-level declarations aggregated by upstream source
	@go run ./parity/cmd/portreconcile -groups \
	    $(if $(COVERAGE),-coverage $(COVERAGE),) \
	    $(if $(PARITY_COVERAGE),-parity-coverage $(PARITY_COVERAGE),)

async-contracts: ## Audit Promise/async semantics across the pinned upstream tree
	@go run ./parity/cmd/asynccheck

# Rewrite PORT_MAP.md in place with the coverage column merged. Use after
# parity runs so reviewers see "ported AND verified" in one column.
port-map: coverage ## Rewrite PORT_MAP.md with coverage column merged
	@echo "PORT_MAP.md = code mapping (human mental model)"
	@echo "parity/coverage.md = verification truth (machine-generated)"
	@echo "(use 'make coverage' to refresh the latter)"

# Scenario quality lint. Enforces rules from AGENTS.md that were
# previously prompt-only: equality comparators, upstream evidence,
# comparator-note comments, orphan covers, deferred descriptions,
# boot-only over-claims, divergence-citation honesty.
lint-scenarios: ## Scenario quality lint. Enforces rules from AGENTS.md that were
	@go run ./parity/cmd/lint \
	    -scenarios parity/scenarios \
	    -port-map PORT_MAP.md \
	    -divergences DIVERGENCES.md

# PORT_MAP completeness gate. Coverage uses PORT_MAP.md as its denominator;
# every current upstream source file in tracked packages must therefore be
# explicitly mapped, deferred, or designed out. Silent omission is a coverage bug.
port-map-drift: ## PORT_MAP completeness gate. Coverage uses PORT_MAP.md as its denominator;
	@./automation/ci/check-port-map-drift.py --upstream .upstream/current --port-map PORT_MAP.md

# Generated-report freshness gate. A scenario added without regenerating
# coverage.md leaves the report understating coverage. Only the PORT_MAP- and
# scenario-derived facts are compared; the "last run" column comes from a
# transient parity results file and is excluded.
coverage-drift: ## Generated-report freshness gate. A scenario added without regenerating
	@./automation/ci/check-coverage-drift.py --coverage parity/coverage.md

# Family scope report. Lists, for each behavior family declared in
# parity/families.toml, which PORT_MAP entries are covered by in-family
# scenarios, other-family scenarios, or untested. Run at the START of a
# family loop to see scope, and at the END to verify nothing was
# silently skipped.
#
# Examples:
#   make family-gaps                          # all families
#   make family-gaps FAMILY=slash-commands    # one family
#   make family-gaps STRICT=1                 # exit 1 if any untested
family-gaps: ## Family scope report. Lists, for each behavior family declared in
	@go run ./parity/cmd/familygaps \
	    $(if $(FAMILY),-family '$(FAMILY)',) \
	    $(if $(STRICT),-strict,) \
	    -families parity/families.toml \
	    -port-map PORT_MAP.md \
	    -scenarios parity/scenarios

# Scaffold a new scenario from a driver-aware template.
# Example:
#   make parity-new FAMILY=model NAME=03-bad-model-error DRIVER=print-mode \
#     COVERS='packages/coding-agent/src/modes/print-mode.ts'
parity-new: ## Scaffold a new parity scenario (optionally under FAMILY=...)
	@test -n "$(NAME)" || (echo "NAME is required" && exit 2)
	@test -n "$(COVERS)" || (echo "COVERS is required" && exit 2)
	@go run ./parity/cmd/newscenario \
	    -name '$(NAME)' \
	    $(if $(FAMILY),-family '$(FAMILY)',) \
	    -driver '$(or $(DRIVER),interactive-tmux)' \
	    -covers '$(COVERS)' \
	    $(if $(TAGS),-tags '$(TAGS)',) \
	    $(if $(MODEL),-model '$(MODEL)',)
	@echo "wrote parity/scenarios/$(if $(FAMILY),$(FAMILY)/,)$(NAME).toml"

.PHONY: async-contracts behavior-contracts behavior-contracts-strict behavior-input-inventory-drift behavior-input-mapping-proposal check-contracts check-contracts-fast check-scratch-paths closure-check correspondence-check coverage coverage-drift coverage-strict custom-factory-ledger custom-factory-ledger-drift family-gaps format-version-inventory format-version-policy foundation-check interface-delta interface-delta-strict interface-go-drift interface-inventory interface-inventory-drift interface-inventory-test interface-mapping-quality interface-mapping-strict interface-proposal-check interface-proposals interface-recommendations interface-recommendations-drift lint-scenarios parity parity-bin parity-driver parity-durable parity-family parity-fast parity-flow-coverage parity-live parity-new parity-perf parity-stress port-groups port-map port-map-drift port-reconcile porter porter-campaign porter-check porter-smoke porter-task require-parity-ran schedule-report source-hygiene source-hygiene-full test-inventory test-inventory-drift test-inventory-strict typescript-extension-corpus upstream-delta
