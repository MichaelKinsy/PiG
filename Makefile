# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
#
# PiG's command interface. Developers, CI, and coding agents run make targets
# instead of ad hoc shell commands. `make help` lists the targets by group.
#
# Every script behind these targets lives in automation/ (see its README):
# dev/ for setup and local tools, ci/ for gates and test runners, gen/ for
# generators and the upstream mirror, release/ for release checks, porter/ for
# Pig Porter, images/ for CI images, and make/ for the parity targets included
# at the end of this file.
#
# Reward function: `make coverage` should print three numbers going up
# (parity_score, runtime_score, sdk_score) and zero going down (untested,
# orphans, divergences).

.DEFAULT_GOAL := help
# Caches follow XDG_CACHE_HOME; temporary files follow TMPDIR. Sandboxes,
# devcontainers, and hosted runners often forbid writes elsewhere.
PIG_CACHE_HOME ?= $(or $(XDG_CACHE_HOME),$(HOME)/.cache)
# golangci-lint caches absolute package paths. Isolate each checkout so parallel worktrees retain warm results without serving findings from another tree.
GOLANGCI_LINT_CACHE ?= $(PIG_CACHE_HOME)/golangci-lint/$(notdir $(CURDIR))
export GOLANGCI_LINT_CACHE
PIG_TMP ?= $(patsubst %/,%,$(or $(TMPDIR),/tmp))
# macOS mktemp ignores TMPDIR without a template, so recipes always pass one.
MKTEMP = mktemp "$(PIG_TMP)/pig.XXXXXXXX"
MKTEMP_DIR = mktemp -d "$(PIG_TMP)/pig.XXXXXXXX"
PIG_DEV_HOME ?= $(PIG_CACHE_HOME)/pig-dev
# Machine-local toolchain and module-proxy settings written by `make setup`.
# Absent on machines that need neither.
-include $(PIG_DEV_HOME)/env.mk
# An env.mk written by an older `make setup` (module-proxy fallback) exported
# GOWORK=off. The root module resolves extensions/sdk only through go.work, so
# never let that setting reach the gates; nested-module recipes set GOWORK=off
# on their own commands.
ifeq ($(GOWORK),off)
unexport GOWORK
endif
# Workspace mode accepts only -mod=readonly or -mod=vendor.
ifneq ($(filter -mod=mod,$(GOFLAGS)),)
export GOFLAGS := $(strip $(filter-out -mod=mod,$(GOFLAGS)))
endif

PIG_BIN ?= $(HOME)/.local/bin/pig
# Gate-only pig binary. It is built through go.work (the root module requires
# the nested SDK at its release version, which exists only after the release is
# tagged) and then runs with GOWORK=off so the extensions it validates build as
# standalone modules.
CHECK_PIG_BIN := $(CURDIR)/tmp/check-bin/pig

# Default parity to a freshly built in-tree binary. If callers explicitly set
# PIG_BIN in the environment/command line, honor it for compatibility with the
# older parity workflow.
PARITY_PIG_BIN ?= $(if $(filter command line environment,$(origin PIG_BIN)),$(PIG_BIN),$(CURDIR)/bin/pig-parity)

RESULTS  ?= $(PIG_TMP)/parity-results.json

EXTENSION_CORPUS_RESULTS ?= $(PIG_TMP)/pig-typescript-extension-corpus.json

PARITY_TIMEOUT ?= 20m

PARITY_STRESS_TIMEOUT ?= 30m

PARITY_DURABLE_TIMEOUT ?= 45m

# Probed on first use only, so targets that never schedule parity skip the probe.
PARITY_RESOURCE_LIMITS = $(eval PARITY_RESOURCE_LIMITS := $$(shell ./automation/ci/parity-resource-limits.sh))$(PARITY_RESOURCE_LIMITS)

PARITY_PARALLEL ?= $(word 1,$(PARITY_RESOURCE_LIMITS))

PARITY_GROUP_LIMITS ?= $(word 2,$(PARITY_RESOURCE_LIMITS))

UPSTREAM_VERSION := $(shell awk -F'"' '/^const UpstreamVersion = "/ { print $$2; exit }' coding/pigversion/pigversion.go)

UPSTREAM_REVIEWED_VERSION := $(shell awk -F'"' '/^const UpstreamReviewedVersion = "/ { print $$2; exit }' coding/upstream.go)

PI_PACKAGE_ROOT := $(CURDIR)/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent

PIG_PARITY_PI_BIN := $(CURDIR)/extensions/sdk-ts/node_modules/.bin/pi

CARGO_TARGET_DIR ?= $(CURDIR)/tmp/test-fixtures/rust-target

export PI_PACKAGE_ROOT PIG_PARITY_PI_BIN CARGO_TARGET_DIR

SETUP_ARGS ?=
COMPLIANCE_ARGS ?=
HARNESSES ?= pig,pi
RUNS ?= 10
MODEL ?=
EVAL_RESULTS ?= tmp/evals/overhead.json
LIVE_RUNS ?= 3
EVAL_TASKS_DIR ?=
MUTATIONS ?= 30
SEED ?= 1
PROFILE ?= cpu,heap
BENCH ?= .
BENCH_PKGS ?= ./agent/... ./tui/... ./internal/codingagent/...
BENCH_COUNT ?= 6
SLOP_MAX_VERBOSITY = $(shell sed -n 's/^max_verbosity *= *//p' evals/budgets.toml)
SLOP_MAX_EROSION = $(shell sed -n 's/^max_erosion *= *//p' evals/budgets.toml)
PIGEVAL := PYTHONPATH=$(CURDIR)/evals python3 -m pigeval
HELP_AWK := BEGIN {FS = ":.*\#\# "} /^\#\#@ / {printf "\n%s\n", substr($$0, 5); next} /^[a-zA-Z0-9_%-]+:.*\#\# / {printf "  %-24s %s\n", $$1, $$2}

##@ Start here

setup: ## Install pinned Go, a module-proxy fallback when needed, and bin/pig (SETUP_ARGS=--all adds Node and eval harnesses)
	@./automation/dev/setup.sh $(SETUP_ARGS)

doctor: ## Report every development prerequisite without changing anything
	@./automation/dev/setup.sh --check $(SETUP_ARGS)

help: ## List targets by group
	@awk '$(HELP_AWK)' Makefile
	@echo
	@echo "Parity and porting targets: make help-parity. Scripts: automation/README.md."

help-parity: ## List the parity and porting targets (automation/make/parity.mk)
	@awk '$(HELP_AWK)' automation/make/parity.mk

##@ Build

pig: ## Build bin/pig from this checkout, with symbols for profiling
	@mkdir -p bin
	CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "-X main.Build=$$(git rev-parse --short HEAD 2>/dev/null || echo dev)" -o bin/pig ./cmd/pig

clean: ## Remove bin/ and tmp/ (builds, eval results, profiles, benchmarks)
	rm -rf bin tmp

build: ## Compile every package (go build ./...)
	go build -buildvcs=false ./...

# install: build pig binary → ~/.local/bin/pig (or PIG_BIN override)
# with embedded git sha + macOS ad-hoc codesign.
install: ## Install a stripped pig to PIG_BIN (default ~/.local/bin/pig)
	@mkdir -p $(dir $(PIG_BIN))
	CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "-s -w -X main.Build=$$(git rev-parse --short HEAD 2>/dev/null || echo dev)" -o $(PIG_BIN) ./cmd/pig
	@[ "$$(uname -s)" = "Darwin" ] && command -v codesign >/dev/null 2>&1 && codesign --force --sign - $(PIG_BIN) >/dev/null 2>&1 || true
	@echo "installed: $$($(PIG_BIN) --version) → $(PIG_BIN)"

##@ Test and lint

vet: ## Run go vet on every package
	go vet ./...

LINT_BASE ?= main

lint: ## Run golangci-lint over the repository (part of make check)
	@echo "Running golangci-lint..."
	go tool golangci-lint run --allow-parallel-runners --build-tags=integration,live,parity ./...

lint-changed: ## Lint whole changed Go files in changed packages against LINT_BASE (default main)
	@base=$$(git merge-base HEAD "$(LINT_BASE)") || { echo "lint-changed: cannot find merge base with $(LINT_BASE)" >&2; exit 2; }; \
	changed_go=$$({ git diff --name-only "$$base"...HEAD; git diff --name-only; git diff --name-only --cached; git ls-files --others --exclude-standard; } | awk '/\.go$$/' | sort -u); \
	if [ -z "$$changed_go" ]; then echo "lint-changed: no changed Go files"; exit 0; fi; \
	packages=$$(printf '%s\n' "$$changed_go" | while IFS= read -r file; do dir=$${file%/*}; [ "$$dir" = "$$file" ] && dir=.; [ -d "$$dir" ] && printf './%s\n' "$$dir"; done | sort -u); \
	if [ -z "$$packages" ]; then echo "lint-changed: no changed Go packages"; exit 0; fi; \
	echo "Running golangci-lint on changed packages: $$(printf '%s' "$$packages" | tr '\n' ' ')"; \
	go tool golangci-lint run --allow-parallel-runners --build-tags=integration,live,parity --new-from-rev="$$base" --whole-files $$packages

# Fast inner-loop gate (~90s). Build + vet + focused unit tests + QC smoke.
# Use between edits. `make check` is the pre-commit gate.
dev: build vet qc ## Fast inner loop: build, vet, and the QC smoke (about 90 s)

test: test-prereqs interface-deps parity-deps ## Grouped Go tests with locked interface tooling, fixtures, and safe concurrency
	@./automation/ci/test-grouped.sh

# Every maintained nested Go module must pass without workspace assistance.
# Fixture modules are included because they exercise the same clean-clone
# dependency boundaries as independently distributed modules.
test-sdk-go: ## Test the Go extension SDK module on its own
	@cd extensions/sdk && GOWORK=off go test ./... -count=1

test-go-modules: test-sdk-go ## Test every maintained nested Go module with GOWORK=off
	@set -eu; \
	for module in \
		cmd/pig/testdata/login-preview \
		coding/extension/host/subprocess/testdata/sdk-fixture \
		examples/extensions/go-factory \
		piglets/porter/extensions/pig-porter \
		piglets/standard; do \
		echo "testing Go module: $$module"; \
		(cd "$$module" && GOWORK=off go test ./... -count=1); \
	done

# The Rust SDK's own unit tests. `test` builds the Rust conformance fixtures,
# which compiles the SDK library but not its test targets, so a test-only break
# such as a struct literal missing a newly added field compiles clean and ships.
# That is exactly how ReadyMsg.state reached main red.
test-sdk-rs: ## Run the Rust extension SDK's unit tests
	@cd extensions/sdk-rs && cargo test --quiet

test-sdk-ts: parity-deps ## Check the pinned TypeScript extension declarations and runtime helpers
	@cd extensions/sdk-ts && npm test

examples-check: test-prereqs parity-bin ## Build and validate every example extension
	@set -eu; tmp=$$($(MKTEMP_DIR)); trap 'rm -rf "$$tmp"' EXIT; \
		cd examples/extensions/rust-factory && cargo build --release --quiet; \
		cd "$(CURDIR)"; \
		for extension in \
			examples/extensions/go-factory \
			examples/extensions/rust-factory \
			examples/extensions/python-factory; do \
			name=$$(basename "$$extension"); \
			echo "validating example extension: $$extension"; \
			"$(PARITY_PIG_BIN)" install --validate-only --json "$$extension" >"$$tmp/$$name.json"; \
			python3 -c 'import json,sys; data=json.load(open(sys.argv[1])); assert data["valid"], data; assert not data.get("diagnostics"), data.get("diagnostics")' "$$tmp/$$name.json"; \
		done

test-prereqs: ## Require every toolchain the core suite exercises
	@missing=""; \
	for tool in go git node npm python3 cargo rustc tmux; do \
		command -v "$$tool" >/dev/null 2>&1 || missing="$$missing $$tool"; \
	done; \
	if [ -n "$$missing" ]; then \
		echo "missing required Pig test toolchains:$$missing" >&2; \
		exit 1; \
	fi
	@echo "test prerequisites: go, git, node, npm, python3, cargo, rustc, tmux"

test-fixtures: ## Build the Go and Rust extension fixtures that tests load
	@./automation/ci/test-fixtures.sh

test-stress: ## Stress the grouped Go test scheduler under higher contention
	@./automation/ci/test-grouped.sh stress

# Deterministic tmux-driven integration tier: real pig in a real pty, faux
# provider, no network. Carries the signal guards, which cover shutdown paths
# no unit test reaches (extension cleanup on SIGTERM, SIGINT aborting the
# operation rather than the session). Tests needing external extension sources
# skip themselves.
test-integration: ## Deterministic tmux tier: real pig, faux provider, no network
	@./automation/ci/integration-tests.sh fast

# Race + goroutineleak gate for the interactive TUI concurrency model. Locks in
# the single-main-loop ownership invariant: a future off-loop UI touch fails
# here instead of shipping a latent data race. See automation/ci/test-race.sh.
test-race: ## Race and goroutine-leak gate for the TUI concurrency model
	@./automation/ci/test-race.sh

# go-fix-clean enforces AGENTS.md rule #3 (Go 1.27 idiom compliance).
# `go fix -diff ./...` must produce zero output. If this target fails,
# run `go fix ./...`, manually validate any suggestions that would
# change observable behavior (e.g. type changes like
# `slices.Contains` returning bool vs a string flag), then commit.
go-fix-clean: ## Fail when go fix would change any file
	@# Judge the diff on stdout; stderr (tool warnings) passes through. go fix
	@# exits non-zero when it has a diff, so a failure without one is an error.
	@status=0; diff_output=$$(go fix -diff ./...) || status=$$?; \
	if [ -n "$$diff_output" ]; then \
		echo "FAIL: go fix -diff produced output (AGENTS.md rule #3)."; \
		echo "Run \`go fix ./...\` and validate suggestions before committing."; \
		echo "First 40 lines of diff:"; \
		echo "$$diff_output" | head -40; \
		exit 1; \
	fi; \
	if [ "$$status" -ne 0 ]; then echo "FAIL: go fix -diff exited $$status without a diff."; exit 1; fi
	@echo "go fix: clean (Go 1.27 idioms compliant)"

# compliance checks the evidence behind the README badges and the OpenSSF
# Best Practices criteria (docs/project/compliance.md): required files,
# CITATION.cff, SHA-pinned actions, read-only workflow tokens, one source for
# every version pin, reuse lint, and govulncheck. It fails on any miss.
compliance: ## Check badge and OpenSSF evidence: files, pins, workflow hardening, REUSE, govulncheck
	@./automation/ci/check-compliance.py $(COMPLIANCE_ARGS)

# divergence-consistency enforces AGENTS.md rule #4. Every divergence
# numbered in DIVERGENCES.md must have a matching `// pig divergence (DN):`
# call-site comment in a .go file, and vice versa. Catches silent drift.
divergence-consistency: ## Match every DIVERGENCES.md entry to its call-site marker
	@./automation/ci/check-divergence-consistency.sh

divergence-quality: ## Validate divergence records as enforceable contracts
	@./automation/ci/check-divergence-quality.py

# divergence-guard fails on hidden divergences no DIVERGENCES.md entry
# records: invented limits and timeouts, dropped events, swallowed errors,
# success on an unknown stop reason. Current hits are ratcheted in
# automation/ci/divguard/baseline.toml, which may only shrink.
divergence-guard: ## Fail on unrecorded invented limits, dropped events and swallowed errors (ratchet)
	@go run ./automation/ci/divguard

# docs-drift gates the shipped docs bundle against the code it describes: every
# documented command must exist, every listable command must be documented, and
# every page must be reachable from the index.
docs-drift: ## Fail when generated documentation no longer matches its source
	@go test ./tests/docs-drift/ -count=1

npm-dist-test: ## Unit-test the npm package generator and launcher
	@python3 -m unittest automation/release/npm/test_pack_npm.py
	@node --test automation/release/npm/launcher.test.js

npm-dist-e2e: ## Cross-build, npm pack, install and run pig through the npm launcher
	@automation/release/npm/e2e-local.sh

standard-check: ## Verify PiG Standard requires and resolves only fused extensions
	@go test ./cmd/pig -run '^TestPiGStandardRequiresAndResolvesOnlyFusedExtensions$$' -count=1
	@go test ./coding/pigletbuild -run '^TestRunBuildRejectsRequiredFusedFallback$$' -count=1
	@go test ./coding/extension/host/subprocess -run '^TestHost_MixedFusedPackedAndIsolatedExtensions$$' -count=1
	@go build -buildvcs=false -o $(CHECK_PIG_BIN) ./cmd/pig
	@GOWORK=off $(CHECK_PIG_BIN) install --validate-only --json piglets/standard/extensions/piglogin >/dev/null
	@GOWORK=off $(CHECK_PIG_BIN) install --validate-only --json piglets/standard/extensions/pigrunner >/dev/null
	@GOWORK=off $(CHECK_PIG_BIN) install --validate-only --json piglets/standard/extensions/angrypigs >/dev/null
	@GOWORK=off $(CHECK_PIG_BIN) piglet validate piglets/standard/pig-standard.yaml >/dev/null

##@ Gates

check-core: build vet lint test test-go-modules test-sdk-rs test-sdk-ts typescript-extension-corpus examples-check test-race test-integration lint-scenarios port-map-drift coverage-drift standard-check go-fix-clean divergence-consistency divergence-quality divergence-guard check-contracts-fast source-hygiene docs-drift ## All deterministic build, lint, unit, race, and drift gates

check: check-core parity-fast ## Pre-commit gate: check-core plus parity-fast
	@echo
	@echo "make check: all gates green."
	@echo
	@echo "Reminder: run 'make coverage' before signaling success to refresh"
	@echo "the AGENTS.md dashboard and parity/coverage.md. Do not hand-edit."

# verify = deterministic gates + declared durability parity + coverage.
# The recommended one-shot for end-of-loop verification: `parity` proves every
# declared pair and regenerates the dashboard after the core gates pass.
verify: check-core check-contracts parity ## End-of-loop gate: check-core, contracts, and full parity
	@echo
	@echo "make verify: dashboard refreshed deterministically from"
	@echo "PORT_MAP.md + parity/scenarios. Commit the diff if any."

qc: parity-bin ## Build a fresh pig, then run the smoke and focused parity QC
	@PIG_BIN="$(PARITY_PIG_BIN)" ./automation/ci/qc-smoke.sh

# Release gate. Tighter than `check`: parity-live must pass, coverage
# must not regress, no new entries in DIVERGENCES.md without justification.
release-check: parity-live parity-perf ## Release gate: live and perf parity, no new divergences, no coverage loss
	@./automation/ci/check-divergence-delta.sh
	@./automation/ci/check-coverage-delta.sh

##@ Evals and performance

evals: pig ## Measure harness overhead against the mock model (HARNESSES=all for every installed harness)
	@$(PIGEVAL) overhead --harnesses $(HARNESSES) --runs $(RUNS) --pig bin/pig --out $(EVAL_RESULTS)
	@$(PIGEVAL) report $(EVAL_RESULTS) --out $(EVAL_RESULTS:.json=.md)

evals-requests: pig ## Save every request body and diff the first two HARNESSES (default: pig against pi)
	@$(PIGEVAL) requests --harnesses $(HARNESSES) --pig bin/pig --diff

evals-live: pig ## Run evals/tasks with a real model: make evals-live MODEL=provider/model
	@test -n "$(MODEL)" || { echo "evals-live: set MODEL, for example MODEL=anthropic/claude-sonnet-5" >&2; exit 2; }
	@$(PIGEVAL) live --harnesses $(HARNESSES) --pig bin/pig --model "$(MODEL)" --runs $(LIVE_RUNS) $(if $(EVAL_TASKS_DIR),--tasks-dir $(EVAL_TASKS_DIR)) --out tmp/evals/live.json
	@$(PIGEVAL) report tmp/evals/live.json --out tmp/evals/live.md

evals-mutate: ## Generate seeded bug-fix tasks from real Go files (MUTATIONS=30 SEED=1) into tmp/evals/mutation-tasks
	@$(PIGEVAL) mutate --count $(MUTATIONS) --seed $(SEED) --out tmp/evals/mutation-tasks

evals-publish: ## Copy a reviewed EVAL_RESULTS run to evals/results/latest.json and regenerate docs/site/docs/evals.md
	@test -f "$(EVAL_RESULTS)" || { echo "evals-publish: $(EVAL_RESULTS) not found; run make evals first" >&2; exit 2; }
	@cp "$(EVAL_RESULTS)" evals/results/latest.json
	@$(PIGEVAL) report evals/results/latest.json --out docs/site/docs/evals.md

evals-test: ## Unit-test pigeval, the eval harness
	@PYTHONPATH=$(CURDIR)/evals python3 -m unittest discover -s evals/tests -q

perf-check: ## Apply evals/budgets.toml (latency, memory, and request-size ceilings) to EVAL_RESULTS
	@$(PIGEVAL) check $(EVAL_RESULTS)

profile: pig ## Profile one prompt round trip (PROFILE=cpu,heap,allocs,block,mutex,trace) into tmp/profile
	@$(PIGEVAL) profile --pig bin/pig --kinds $(PROFILE) --out tmp/profile

pgo: pig ## Merge CPU profiles from repeated round trips into tmp/pgo/default.pgo for a profile-guided build
	@$(PIGEVAL) profile --pig bin/pig --kinds cpu --runs 20 --out tmp/pgo --merge tmp/pgo/default.pgo

slop: ## Measure erosion and clone verbosity (SlopCodeBench metrics) and list the heaviest functions
	@go run ./automation/ci/slopmetrics -top 15

slop-check: ## Fail when erosion or verbosity exceed the ratchet in evals/budgets.toml [slop]
	@go run ./automation/ci/slopmetrics -top 0 -max-verbosity $(SLOP_MAX_VERBOSITY) -max-erosion $(SLOP_MAX_EROSION) >/dev/null && echo "slop-check: within verbosity $(SLOP_MAX_VERBOSITY), erosion $(SLOP_MAX_EROSION)"

bench: ## Run Go benchmarks into tmp/bench/current.txt (BENCH=regex, BENCH_PKGS, BENCH_COUNT=6)
	@mkdir -p tmp/bench
	@go test -run '^$$' -bench '$(BENCH)' -benchmem -count $(BENCH_COUNT) $(BENCH_PKGS) > tmp/bench/current.txt; status=$$?; cat tmp/bench/current.txt; exit $$status

bench-base: ## Keep the last benchmark run as the base for bench-compare
	@cp tmp/bench/current.txt tmp/bench/base.txt

bench-compare: ## Compare the last benchmark run with the base by median; fails above a 5% regression
	@$(PIGEVAL) benchcmp tmp/bench/base.txt tmp/bench/current.txt --fail

##@ Docs

knowledge-graph: ## Regenerate the knowledge graph page, JSON-LD, and Mermaid from docs/knowledge-graph/pig.graph.json
	@python3 automation/gen/gen-knowledge-graph.py

##@ Upstream and catalogs

upstream-mirror: parity-deps ## Mirror the pinned Pi source into .upstream/current
	@./automation/gen/mirror-upstream.sh

model-catalogs: parity-deps ## Regenerate model catalogs from the pinned published Pi package
	@./automation/gen/generate-model-catalogs.sh

##@ Dependencies

interface-deps: ## Install the locked TypeScript compiler used by parity inventories
	@python3 automation/ci/npm-locked.py parity/interface-extractor

parity-deps: interface-deps ## Install the exact locked Pi comparator and the TypeScript compiler its scenarios import
	@python3 automation/ci/npm-locked.py extensions/sdk-ts
	@test -x "$(PIG_PARITY_PI_BIN)" || { echo "exact Pi comparator not found: $(PIG_PARITY_PI_BIN)" >&2; exit 1; }
	@test -d "$(PI_PACKAGE_ROOT)" || { echo "exact published Pi package not found: $(PI_PACKAGE_ROOT)" >&2; exit 1; }
	@test "$$(env -u HTTPS_PROXY -u HTTP_PROXY -u ALL_PROXY -u https_proxy -u http_proxy -u all_proxy "$(PIG_PARITY_PI_BIN)" --version)" = "$(UPSTREAM_VERSION)" || { echo "Pi comparator version does not match $(UPSTREAM_VERSION)" >&2; exit 1; }

.PHONY: npm-dist-test npm-dist-e2e compliance evals-mutate slop slop-check # Deterministic tmux-driven integration tier # Release gate. Tighter than `check` # The recommended one-shot for end-of-loop verification # docs-drift gates the shipped docs bundle against the code it describes # install # numbered in DIVERGENCES.md must have a matching `// pig divergence (DN) # the single-main-loop ownership invariant bench bench-base bench-compare build check check-core clean dev divergence-guard divergence-quality doctor evals evals-live evals-publish evals-requests evals-test examples-check go-fix-clean help help-parity interface-deps knowledge-graph lint lint-changed model-catalogs parity-deps perf-check pgo pig profile qc setup standard-check test test-fixtures test-go-modules test-prereqs test-sdk-go test-sdk-rs test-sdk-ts test-stress upstream-mirror vet

include automation/make/parity.mk
include automation/make/ci.mk
