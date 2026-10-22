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
# CI restores the Go build cache, test results included, from earlier commits; the cache key misses inputs child processes read, so CI reruns every test. Elsewhere unchanged packages replay their cached results.
GO_TEST_CI_COUNT := $(if $(CI),-count=1)
# Machine-local toolchain and module-proxy settings written by `make setup`.
# Absent on machines that need neither.
-include $(PIG_DEV_HOME)/env.mk
# Development gates select the local SDK through go.work, regardless of env.mk. Standalone-module and publication recipes set GOWORK=off on their own commands.
ifeq ($(GOWORK),off)
unexport GOWORK
endif
# Workspace mode accepts only -mod=readonly or -mod=vendor.
ifneq ($(filter -mod=mod,$(GOFLAGS)),)
export GOFLAGS := $(strip $(filter-out -mod=mod,$(GOFLAGS)))
endif

PIG_BIN ?= $(HOME)/.local/bin/pig
# Gate-only pig binary. It is built through go.work to exercise the developing SDK, then runs with GOWORK=off so the extensions it validates build as standalone modules. module-publication separately verifies the published dependency pins.
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

# parity-live runs every scenario under the default groups. Measured 2026-10-13 on the Linux lab host cores 160-175 (PARITY_PARALLEL=4,
# groups process=4 process-ext=1 tmux=4 tmux-ext=1 ht=2 rpc=3; host loaded by gate waves): 774 scenarios, 2522 s wall, scenario
# durations summing to 9278 s (mean 12.0 s, p50 5.9 s, p90 27.3 s, p99 88.1 s, longest 269 s). The timeout is about twice that wall.
PARITY_LIVE_TIMEOUT ?= 90m

# Probed on first use only, so targets that never schedule parity skip the probe.
PARITY_RESOURCE_LIMITS = $(eval PARITY_RESOURCE_LIMITS := $$(shell ./automation/ci/parity-resource-limits.sh))$(PARITY_RESOURCE_LIMITS)

PARITY_PARALLEL ?= $(word 1,$(PARITY_RESOURCE_LIMITS))

PARITY_GROUP_LIMITS ?= $(word 2,$(PARITY_RESOURCE_LIMITS))

# The upstream Pi pin is .upstream/pin (see automation/pin/pinlib.py). PI_UPSTREAM_REF=<tag|commit> overrides it for one run:
# the variables below follow it, and a run that overrides the pin builds Go with an overlay of pigversion.go.
PIN_TOOL := python3 $(CURDIR)/automation/pin/pin.py
PIN_WORDS := $(shell PI_UPSTREAM_REF='$(PI_UPSTREAM_REF)' $(PIN_TOOL) make-vars)
pin_word = $(patsubst $(1)=%,%,$(filter $(1)=%,$(PIN_WORDS)))
ifeq ($(PIN_WORDS),)
$(error automation/pin/pin.py make-vars failed; run it directly for the reason)
endif
UPSTREAM_VERSION := $(call pin_word,version)
UPSTREAM_PACKAGE_VERSION := $(call pin_word,package_version)
UPSTREAM_COMMIT := $(call pin_word,commit)
UPSTREAM_REF := $(call pin_word,ref)
UPSTREAM_KIND := $(call pin_word,kind)
UPSTREAM_OVERRIDE := $(call pin_word,override)
ifneq ($(call pin_word,goflags),)
export GOFLAGS := $(strip $(GOFLAGS) $(call pin_word,goflags))
endif
PIG_UPSTREAM_REF := $(UPSTREAM_REF)
PIG_UPSTREAM_COMMIT := $(UPSTREAM_COMMIT)
PIG_UPSTREAM_VERSION := $(UPSTREAM_VERSION)
PIG_UPSTREAM_PACKAGE_VERSION := $(UPSTREAM_PACKAGE_VERSION)
PIG_UPSTREAM_KIND := $(UPSTREAM_KIND)
PIG_UPSTREAM_MIRROR_DIR := $(call pin_word,mirror_dir)
PIG_UPSTREAM_OVERRIDE := $(UPSTREAM_OVERRIDE)
export PIG_UPSTREAM_REF PIG_UPSTREAM_COMMIT PIG_UPSTREAM_VERSION PIG_UPSTREAM_PACKAGE_VERSION PIG_UPSTREAM_KIND PIG_UPSTREAM_MIRROR_DIR PIG_UPSTREAM_OVERRIDE
export PI_UPSTREAM_REF

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
BENCH_PKGS ?= ./agent/... ./ai/... ./tui/... ./internal/codingagent/...
BENCH_COUNT ?= 6
SLOP_MAX_VERBOSITY = $(shell sed -n 's/^max_verbosity *= *//p' test/evals/budgets.toml)
SLOP_MAX_EROSION = $(shell sed -n 's/^max_erosion *= *//p' test/evals/budgets.toml)
PIGEVAL := PYTHONPATH=$(CURDIR)/test/evals python3 -m pigeval
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

##@ Release maintenance

.PHONY: set-version module-publication module-size
set-version: ## Update the release identity; preserve dependencies unless SET_VERSION_ARGS=--release-modules
	@./automation/ci/with-isolated-pig-home.sh go run ./automation/release/set-version -version "$(VERSION)" $(SET_VERSION_ARGS)

module-publication: ## Verify nested Go dependency tags and checksums against their public source
	@./automation/ci/with-isolated-pig-home.sh python3 -B -m unittest automation/ci/test_module_publication.py
	@python3 automation/ci/check-module-publication.py

module-size: ## Fail when the root module zip exceeds the committed byte budget; prove go install of cmd/pig from that zip through a file:// GOPROXY
	@./automation/ci/with-isolated-pig-home.sh go run ./automation/release/modulesize -install

##@ Build

pig: node-runtime ## Build bin/pig from this checkout, with symbols for profiling
	@mkdir -p bin
	CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "-X github.com/MichaelKinsy/PiG/coding/cli.Build=$$(git rev-parse --short HEAD 2>/dev/null || echo dev)" -o bin/pig ./cmd/pig

clean: ## Remove bin/ and tmp/ (builds, eval results, profiles, benchmarks)
	rm -rf bin tmp

node-runtime: ## Regenerate the embedded Node runtime archive and content digest
	go generate ./coding/extension/host/subprocess

build: node-runtime ## Compile every package (go build ./...)
	go build -buildvcs=false ./...

# install: build pig binary → ~/.local/bin/pig (or PIG_BIN override)
# with embedded git sha + macOS ad-hoc codesign.
install: node-runtime ## Install a stripped pig to PIG_BIN (default ~/.local/bin/pig)
	@mkdir -p $(dir $(PIG_BIN))
	CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "-s -w -X github.com/MichaelKinsy/PiG/coding/cli.Build=$$(git rev-parse --short HEAD 2>/dev/null || echo dev)" -o $(PIG_BIN) ./cmd/pig
	@[ "$$(uname -s)" = "Darwin" ] && command -v codesign >/dev/null 2>&1 && codesign --force --sign - $(PIG_BIN) >/dev/null 2>&1 || true
	@echo "installed: $$($(PIG_BIN) --version) → $(PIG_BIN)"

# build-env-daemons: one static pi-env daemon per remote system the SSH bootstrap supports, laid out as the bootstrap
# looks for them: $(ENV_DAEMON_DIR)/pi-env-<platform>-<arch>/pi-env[.exe]. Go links GOOS=android only with cgo, except on
# arm64, so Android on x86-64 runs the static linux/amd64 build with the daemon's target system set to android.
ENV_DAEMON_DIR ?= dist/pi-env
ENV_DAEMON_TARGETS ?= linux/amd64/linux-x64 linux/arm64/linux-arm64 darwin/amd64/darwin-x64 darwin/arm64/darwin-arm64 linux/amd64/android-x64 android/arm64/android-arm64 windows/amd64/windows-x64 windows/arm64/windows-arm64
build-env-daemons: ## Cross-build the pi-env daemon for every remote system SshConnection supports into ENV_DAEMON_DIR
	@set -eu; for target in $(ENV_DAEMON_TARGETS); do \
		goos=$${target%%/*}; rest=$${target#*/}; goarch=$${rest%%/*}; name=$${rest#*/}; \
		exe=pi-env; [ "$$goos" = windows ] && exe=pi-env.exe; \
		target_os=""; case "$$name" in android-*) target_os="-X github.com/MichaelKinsy/PiG/env/daemon.targetOS=android";; esac; \
		mkdir -p "$(ENV_DAEMON_DIR)/pi-env-$$name"; \
		echo "pi-env-$$name"; \
		CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch go build -buildvcs=false -trimpath -ldflags "-s -w $$target_os" -o "$(ENV_DAEMON_DIR)/pi-env-$$name/$$exe" ./env/cmd/pi-env; \
	done

##@ Generated files

# Keep producer/consumer order even under make -j. Reviewed mappings and evidence ledgers are inputs, not generated dispositions. Coverage deliberately ignores machine-local parity results so every checkout writes the same bytes.
generate: export LC_ALL := C
generate help-text: export FORCE_COLOR := 0

generate: ## Regenerate committed inventories, coverage, catalogs, help, and docs mirrors
	@$(MAKE) divergences
	@$(MAKE) piglet-strip-ids
	@$(MAKE) model-catalogs
	@$(MAKE) node-runtime
	@$(MAKE) highlight-grammars
	@$(MAKE) help-text
	@$(MAKE) knowledge-graph
	@$(MAKE) interface-proposals
	@$(MAKE) test-inventory-generate
	@$(MAKE) known-gaps
	@$(MAKE) custom-factory-ledger
	@$(MAKE) bug-ledger
	@$(MAKE) coverage RESULTS=
	@echo "Generated files refreshed. Review and commit the diff; CI compares these files."

help-text: parity-deps ## Regenerate the pinned upstream CLI help with PiG's identity
	@./automation/gen/gen-help.sh

highlight-grammars: parity-deps ## Snapshot Pi's highlight.js grammars into tui/internal/hljs and re-record Pi's highlighting oracle
	@node automation/gen/generate-highlight-grammars.mjs "$(PI_PACKAGE_ROOT)" tui/internal/hljs
	@node automation/gen/generate-highlight-oracle.mjs "$(PI_PACKAGE_ROOT)" tui/internal/hljs/testdata/corpus.json tui/internal/hljs/testdata/oracle.json.gz

zone-names: ## Regenerate the long time-zone names Node's ICU prints for date mapping keys (needs Node; output depends on Node's ICU, so it is not part of `make generate`)
	@node automation/gen/generate-zone-names.mjs > internal/yaml12/zonenames_gen.go

windows-zones: ## Regenerate CLDR's Windows-to-IANA zone mapping that Node's ICU uses to name the host zone on Windows (downloads windowsZones.xml for Node's CLDR release)
	@node automation/gen/generate-windows-zones.mjs > internal/yaml12/windowszones_gen.go

divergences: ## Regenerate docs/parity/DIVERGENCES.md from the one-file-per-record sources in docs/parity/divergences (edit D<N>.md, never the ledger)
	@go run ./automation/ci/divsplit

divergences-drift: ## Fail when docs/parity/DIVERGENCES.md differs from the records in docs/parity/divergences
	@go run ./automation/ci/divsplit -check

changelog-unreleased: ## Print the Unreleased changelog assembled from the fragments in changelog.d (nothing is written)
	@go run ./automation/ci/changelogfrag

changelog-check: ## Fail on a changelog fragment that is not named <lane>-<sha>.md after a commit reachable from HEAD, is not sections of bullets, or has a "### Fixed" bullet without a reachable <!-- fix: <sha> --> marker; needs the full history (fragments listed in automation/ci/changelogfrag/legacy.txt are exempt from the name and reachability rules, not from the marker rule)
	@go run ./automation/ci/changelogfrag -check

changelog-fold: ## Fold the changelog.d fragments into the VERSION section of CHANGELOG.md and delete them (integrator; rerun at release for later fragments)
	@test -n "$(VERSION)" || { echo "usage: make changelog-fold VERSION=x.y.z" >&2; exit 2; }
	@go run ./automation/ci/changelogfrag -fold=$(VERSION)

piglet-strip-ids: ## Regenerate the Piglet strip ID table from the tool, command, extension and feature registrations
	@go run ./coding/piglet/internal/genstripids

piglet-strip-ids-drift: ## Fail when the Piglet strip ID table differs from the registrations
	@go run ./coding/piglet/internal/genstripids -check

piglet-strip-build: ## Vet every package and build cmd/pig with every Piglet strip build tag (D92)
	@tags=$$(go run ./coding/piglet/internal/genstripids -print-tags) && go vet -tags "$$tags" ./... && CGO_ENABLED=0 go build -tags "$$tags" -o /dev/null ./cmd/pig

.PHONY: generate divergences divergences-drift help-text highlight-grammars zone-names windows-zones piglet-strip-ids piglet-strip-ids-drift piglet-strip-build changelog-unreleased changelog-check changelog-fold

##@ Test and lint

vet: ## Run go vet on every package, natively and for every shipped Windows architecture and for Android (Termux)
	go vet ./...
	GOOS=windows GOARCH=amd64 go vet ./...
	GOOS=windows GOARCH=arm64 go vet ./...
	CGO_ENABLED=0 GOOS=android GOARCH=arm64 go vet ./...

LINT_BASE ?= main

lint: ## Run golangci-lint over the repository (part of make check)
	@echo "Running golangci-lint..."
	go tool golangci-lint run --allow-parallel-runners --build-tags=integration,live,parity ./...
	@echo "Checking the Durable core import boundary (no encoding/json, reflect or template in the Wasm closure)..."
	go test -count=1 ./durable/core/contracttest/boundary/

# Like go list ./..., exclude testdata directories, including nested fixture modules, and packages whose Go files all carry a
# build constraint for another platform (for example the wasm-only durable core), which golangci-lint cannot type-check natively.
lint-changed: ## Lint whole changed Go files in changed packages against LINT_BASE (default main)
	@base=$$(git merge-base HEAD "$(LINT_BASE)") || { echo "lint-changed: cannot find merge base with $(LINT_BASE)" >&2; exit 2; }; \
	changed_go=$$({ git diff --name-only "$$base"...HEAD; git diff --name-only; git diff --name-only --cached; git ls-files --others --exclude-standard; } | awk '/\.go$$/ && !/(^|\/)testdata\//' | sort -u); \
	if [ -z "$$changed_go" ]; then echo "lint-changed: no changed Go files"; exit 0; fi; \
	packages=$$(printf '%s\n' "$$changed_go" | while IFS= read -r file; do dir=$${file%/*}; [ "$$dir" = "$$file" ] && dir=.; [ -d "$$dir" ] && printf './%s\n' "$$dir"; done | sort -u); \
	packages=$$(printf '%s\n' "$$packages" | while IFS= read -r pkg; do [ -n "$$(go list -e -f '{{.GoFiles}}{{.TestGoFiles}}{{.XTestGoFiles}}' "$$pkg" 2>/dev/null | tr -d '[] ')" ] && printf '%s\n' "$$pkg"; done); \
	if [ -z "$$packages" ]; then echo "lint-changed: no changed Go packages"; exit 0; fi; \
	echo "Running golangci-lint on changed packages: $$(printf '%s' "$$packages" | tr '\n' ' ')"; \
	go tool golangci-lint run --allow-parallel-runners --build-tags=integration,live,parity --new-from-rev="$$base" --whole-files $$packages

# Fast inner-loop gate (~90s). Build + vet + focused unit tests + QC smoke.
# Use between edits. `make check` is the pre-commit gate.
dev: build vet qc ## Fast inner loop: build, vet, and the QC smoke (about 90 s)

test: test-prereqs interface-deps parity-deps durable-interop-deps ## Grouped Go tests with locked interface tooling, fixtures, and safe concurrency
	@./automation/ci/test-grouped.sh

# The lane and pre-push gate. CI and make test still run every package.
test-warm: ## Build the Go, Rust SDK and Pi daemon fixture caches once so later test runs are warm (docs/testing/warm-caches.md)
	@./automation/ci/test-warm.sh

.PHONY: test-warm

test-changed: test-prereqs interface-deps parity-deps durable-interop-deps ## Test only packages the change affects (TEST_BASE, default @{upstream}); -race on edited packages; JSON timings in tmp/test-changed
	@./automation/ci/test-grouped.sh changed

.PHONY: test-changed

# Long real-PTY sessions run on PiG and Pi; see docs/testing/journeys.md.
JOURNEY_BASE ?= @{upstream}
journeys: parity-deps ## Run every PTY journey against PiG and Pi (CI); timings in tmp/journeys/timing.json
	go test -timeout 30m -tags parity ./test/journeys -run TestJourneys -count=1

journeys-changed: parity-deps ## Run only the PTY journeys whose Touches prefixes contain a file changed since JOURNEY_BASE (default @{upstream}); lane gate
	JOURNEY_CHANGED_BASE='$(JOURNEY_BASE)' JOURNEY_PARALLEL=$${JOURNEY_PARALLEL:-2} go test -timeout 30m -tags parity ./test/journeys -run TestJourneys -count=1

.PHONY: journeys journeys-changed test-aot-conformance

# Every maintained nested Go module must pass without workspace assistance.
# Fixture modules are included because they exercise the same clean-clone
# dependency boundaries as independently distributed modules.
test-sdk-go: ## Test the Go extension SDK module on its own
	@cd extensions/sdk && GOWORK=off go test $(GO_TEST_CI_COUNT) ./...

test-go-modules: test-sdk-go ## Test every maintained nested Go module with GOWORK=off
	@set -eu; \
	for module in \
		coding/cli/testdata/login-preview \
		coding/extension/host/subprocess/testdata/sdk-fixture \
		examples/extensions/go-factory \
		piglets/aot \
		piglets/porter/extensions/pig-porter \
		piglets/standard; do \
		echo "testing Go module: $$module"; \
		(cd "$$module" && GOWORK=off go test $(GO_TEST_CI_COUNT) ./...); \
	done

test-aot-conformance: ## Run the extension conformance suite on the compiled aot host (needs PIG_AOT_TEST_COMPILER, the Pi-Bolt 0.7.4 runtime executable)
	@piglets/aot/conformance.sh

# The Rust SDK's own unit tests. `test` builds the Rust conformance fixtures,
# which compiles the SDK library but not its test targets, so a test-only break
# such as a struct literal missing a newly added field compiles clean and ships.
# That is exactly how ReadyMsg.state reached main red.
test-sdk-rs: ## Run the Rust extension SDK's unit tests
	@cd extensions/sdk-rs && cargo test --quiet

# The Python SDK's pytest suite, in a cached venv holding the hash-locked dev dependencies. Regenerate the lock after
# changing the dev group in extensions/sdk-py/pyproject.toml with the command at the top of requirements-dev.txt.
test-sdk-py: ## Run the Python extension SDK's pytest suite with its hash-locked dev dependencies
	@PIG_CACHE_HOME="$(PIG_CACHE_HOME)" ./automation/ci/test-sdk-py.sh

.PHONY: test-sdk-py

# The SDK scenario table (test/extension-conformance/sdkscenarios) runs once per SDK through the real host, with the Go, Node, Python and Rust scenario drivers, and each SDK must produce the observation the table gives. A row in a scenario's Red map must still produce exactly its recorded Got, so the lane that fixes it deletes the entry and a regression inside a red row still fails. CI runs it in the test-conformance shard and in the Windows and macOS extension-conformance shards, because it is a test of ./test/extension-conformance; this target runs it alone, with the wiring checks that tie the table to the wire methods the code derives.
test-sdk-conformance: ## Run the SDK conformance scenario table against every extension SDK through the real host
	@go test ./test/extension-conformance -run '^TestSDKScenarios$$' $(GO_TEST_CI_COUNT)
	@go test ./test/wiring -run 'TestScenarioCoversNameRealWireMethods|TestEveryAuthObjectMethodIsExercisedByAScenario' $(GO_TEST_CI_COUNT)

test-mcp-conformance: ## Run the official MCP client conformance suite against PiG's MCP client (needs network the first time; see test/mcp-conformance/README.md)
	@go run ./test/mcp-conformance/run $(MCP_CONFORMANCE_ARGS)

test-sdk-ts: parity-deps ## Check the pinned TypeScript extension declarations and runtime helpers
	@cd extensions/sdk-ts && npm test

# Resolve the selected Rust toolchain before validators replace HOME so tool-manager shims do not select a different compiler.
examples-check: test-prereqs parity-bin ## Build and validate every example extension
	@set -eu; tmp=$$($(MKTEMP_DIR)); trap 'rm -rf "$$tmp"' EXIT; \
		rust_root=$$(rustc --print sysroot); \
		case "$${OSTYPE:-}" in msys*|cygwin*) rust_root=$$(cygpath -u "$$rust_root");; esac; \
		export PATH="$$rust_root/bin:$$PATH"; \
		cd examples/extensions/rust-factory && cargo build --release --quiet; \
		cd "$(CURDIR)"; \
		for extension in \
			examples/extensions/go-factory \
			examples/extensions/rust-factory \
			examples/extensions/python-factory \
			examples/extensions/node-factory; do \
			name=$$(basename "$$extension"); \
			echo "validating example extension: $$extension"; \
			./automation/ci/with-isolated-pig-home.sh "$(PARITY_PIG_BIN)" install --validate-only --json "$$extension" >"$$tmp/$$name.json"; \
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

# arm64 fuses float x*y+z into one rounding (FMA); JavaScript never does, so a Pi-derived score can differ by one ulp on arm64
# (macOS runners). The oracle tests that pin such numbers run here as a linux/arm64 binary under qemu user emulation.
# Install qemu-user (qemu-aarch64 or qemu-aarch64-static); the target fails when it is missing rather than passing without running.
FMA_ARM64_PACKAGES ?= ./tui
test-fma-arm64: ## Run the float-sensitive Pi oracle tests as linux/arm64 under qemu (fails when qemu-user is missing)
	@qemu=$$(command -v qemu-aarch64-static || command -v qemu-aarch64) || { echo "test-fma-arm64: qemu-aarch64 is required"; exit 1; }; \
	for pkg in $(FMA_ARM64_PACKAGES); do \
		(cd $$pkg && GOOS=linux GOARCH=arm64 go test -c -o /tmp/fma-arm64.test . && $$qemu /tmp/fma-arm64.test -test.run 'Fuzzy' -test.count=1) || exit 1; \
	done

test-stress: ## Stress the grouped Go test scheduler under higher contention
	@./automation/ci/test-grouped.sh stress

# Deterministic tmux-driven integration tier: real pig in a real pty, faux
# provider, no network. Carries the signal guards, which cover shutdown paths
# no unit test reaches (extension cleanup on SIGTERM, SIGINT aborting the
# operation rather than the session). Tests needing external extension sources
# skip themselves.
test-integration: ## Deterministic tmux tier: real pig, faux provider, no network
	@./automation/ci/integration-tests.sh fast

# The message pipeline against the pinned Pi with a large budget (cases per target and mode, PIPELINE_CASES). make test already runs it with the cheap-gate budget.
test-pipeline-fuzz: parity-deps ## Differential fuzz of the message pipeline against Pi with 1000 seeded cases per target and mode
	@PIG_PIPELINE_CASES=$${PIPELINE_CASES:-1000} go test ./test/pipeline-diff -timeout 30m

.PHONY: test-pipeline-fuzz

# Race + goroutineleak gate for the interactive TUI concurrency model. Locks in
# the single-main-loop ownership invariant: a future off-loop UI touch fails
# here instead of shipping a latent data race. See automation/ci/test-race.sh.
test-race: ## Race and goroutine-leak gate for the TUI concurrency model
	@./automation/ci/test-race.sh

# The nightly workflow runs these outside make check: whole-package -race on the concurrency owners, and every Fuzz target for PIG_FUZZ_TIME.
test-race-nightly: ## Whole-package -race and goroutine-leak gate (nightly)
	@./automation/ci/test-race-nightly.sh

fuzz-nightly: ## Run every Fuzz target for PIG_FUZZ_TIME, default 30s (nightly)
	@./automation/ci/fuzz-nightly.sh

.PHONY: test-race-nightly fuzz-nightly

# go-fix-clean enforces AGENTS.md rule #3 (Go 1.27 idiom compliance).
# `go fix -diff ./...` must produce zero output. If this target fails,
# run `go fix ./...`, manually validate any suggestions that would
# change observable behavior (e.g. type changes like
# `slices.Contains` returning bool vs a string flag), then commit.
go-fix-clean: ## Fail when go fix would change any file
	@# Judge the diff on stdout; stderr (tool warnings) passes through. go fix
	@# exits non-zero when it has a diff, so a failure without one is an error.
	@# third_party holds vendored upstream source kept byte-identical except for
	@# the edits its FORK.md lists, so go fix does not modernize it.
	@packages=$$(go list ./...) || exit 1; packages=$$(printf '%s\n' $$packages | grep -v '/third_party/'); \
	status=0; diff_output=$$(go fix -diff $$packages) || status=$$?; \
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
	@python3 -B -m unittest discover -s automation/ci -p test_go_pin.py
	@./automation/ci/check-compliance.py $(COMPLIANCE_ARGS)

# divergence-consistency enforces AGENTS.md rule #4. Every divergence
# numbered in docs/parity/DIVERGENCES.md must have a matching `// pig divergence (DN):`
# call-site comment in a .go file, and vice versa. Catches silent drift.
divergence-consistency: ## Match every docs/parity/DIVERGENCES.md entry to its call-site marker
	@./automation/ci/check-divergence-consistency.sh

divergence-quality: ## Validate divergence records as enforceable contracts
	@./automation/ci/check-divergence-quality.py

# divergence-guard fails on hidden divergences no docs/parity/DIVERGENCES.md entry
# records: invented limits and timeouts, dropped events, swallowed errors,
# success on an unknown stop reason. Current hits are ratcheted in
# automation/ci/divguard/baseline.toml, which may only shrink.
divergence-guard: ## Fail on unrecorded invented limits, dropped events and swallowed errors (ratchet)
	@go run ./automation/ci/divguard

# port-lint runs the porting anti-pattern analyzers (automation/ci/portlint): JS-to-Go
# semantic mismatches (map key order, nil versus empty JSON, UTF-16 units, regex
# folding, numbers, clocks), goroutine and request lifetime, error identity, and
# test isolation. Current findings are ratcheted in automation/ci/portlint/baseline.toml,
# which may only shrink. `go run ./automation/ci/portlint -report` prints counts and every finding.
port-lint: ## Fail on porting anti-patterns the baseline does not list (ratchet)
	@./automation/ci/cached-gate.sh port-lint -- go run ./automation/ci/portlint

# any-trap counts the decodes of JSON into any, map[string]any, []any and type parameters (automation/ci/anytrap):
# each builds a tree of boxed values. The counts in automation/ci/anytrap/baseline.toml may only shrink.
any-trap: ## Fail on JSON decodes into interface values the baseline does not list (ratchet)
	@./automation/ci/cached-gate.sh any-trap -- go run ./automation/ci/anytrap

# perf-lint finds known performance antipatterns (JSON round trips as clone, marshal-to-compare, regexp compiled in a function, message-history clones, env/stat and Sprintf in loops in the hot packages; automation/ci/perflint). A proven cold site carries //perf:ok <reason>; the rest are ratcheted in baseline.toml, which may only shrink.
perf-lint: ## Fail on performance antipatterns the baseline does not list (ratchet)
	@./automation/ci/cached-gate.sh perf-lint -- go run ./automation/ci/perflint

# race-coverage fails when a package that starts goroutines or timers and has tests is in no nightly -race group of automation/ci/race-packages.txt (and is not exempt there with a reason).
race-coverage: ## Fail on a concurrency package that no nightly -race group runs
	@python3 -B -m unittest discover -s automation/ci -p test_race_coverage.py
	@./automation/ci/race-coverage.sh

.PHONY: race-coverage

# alloc-baseline ratchets allocs/op of the benchmarks of the hot packages (automation/ci/allocbase). It runs them all at
# -benchtime=5x with GOMAXPROCS=1, which takes about an hour, so it belongs in a nightly job, not in `make check`.
alloc-baseline: ## Fail when a hot-path benchmark's allocs/op rises or falls from the committed baseline (slow; nightly)
	@go run ./automation/ci/allocbase

# docs-drift gates the shipped docs bundle against the code it describes: every
# documented command must exist, every listable command must be documented, and
# every page must be reachable from the index.
docs-drift: ## Fail when generated documentation no longer matches its source
	@python3 automation/gen/gen-knowledge-graph.py --check
	@# -count=1: public_claims_test.go runs check-public-claims.py, whose file reads the go test cache cannot see.
	@go test ./test/docs-drift/ -count=1

# bug-ledger needs the full git history (fetch-depth: 0 in CI); it reads the commits since the previous release, CHANGELOG.md, changelog.d and the divergence records.
bug-ledger: ## Regenerate the public bug fix ledger (docs/bug-ledger.json and docs/site/docs/bug-ledger.md) from the history, changelog and divergence records
	@go run ./automation/ci/bugledger

bug-ledger-refresh: ## Recompute every bug ledger entry from the history, as a release does (make bug-ledger keeps the committed entries whose fix is recorded)
	@go run ./automation/ci/bugledger -refresh

bug-ledger-check: ## Fail when a fix has no ledger entry, an entry names a missing test, or the committed ledger is stale
	@go run ./automation/ci/bugledger -check

# release-notes-draft prints draft release notes for NOTES_VERSION (default: the current PiG release) from CHANGELOG.md, changelog.d, the bug fix ledger and the divergence records. It writes docs/release/<version>-release-notes-draft.md only when that file is absent, so it never overwrites an owner's edits; remove the file to regenerate it.
NOTES_VERSION ?= $(shell sed -n 's/^const PigVersion = "\(.*\)"/\1/p' internal/coding/pigversion/pigversion.go)
release-notes-draft: ## Write the draft release notes for the current PiG release from the changelog, bug ledger and divergence records (never overwrites an existing draft)
	@out=docs/release/$(NOTES_VERSION)-release-notes-draft.md; \
	if [ -e "$$out" ]; then echo "$$out exists; remove it to regenerate the draft"; exit 1; fi; \
	go run ./automation/ci/bugledger -notes $(NOTES_VERSION) > "$$out.tmp" && mv "$$out.tmp" "$$out" && echo "wrote $$out"

npm-dist-test: ## Unit-test the npm package generator and launcher
	@python3 -m unittest automation/release/npm/test_pack_npm.py
	@node --test automation/release/npm/launcher.test.js

npm-dist-e2e: ## Cross-build, npm pack, install and run pig through the npm launcher
	@automation/release/npm/e2e-local.sh

standard-check: ## Verify PiG Standard requires and resolves only fused extensions
	@# -count=1: these tests build extensions in child go processes, whose source reads the go test cache cannot see.
	@go test ./coding/cli -run '^TestPiGStandardRequiresAndResolvesOnlyFusedExtensions$$' -count=1
	@go test ./coding/pigletbuild -run '^TestRunBuildRejectsRequiredFusedFallback$$' -count=1
	@go test ./coding/extension/host/subprocess -run '^TestHost_MixedFusedPackedAndIsolatedExtensions$$' -count=1
	@go build -buildvcs=false -o $(CHECK_PIG_BIN) ./cmd/pig
	@GOWORK=off ./automation/ci/with-isolated-pig-home.sh "$(CHECK_PIG_BIN)" install --validate-only --json piglets/standard/extensions/piglogin >/dev/null
	@GOWORK=off ./automation/ci/with-isolated-pig-home.sh "$(CHECK_PIG_BIN)" install --validate-only --json piglets/standard/extensions/pigrunner >/dev/null
	@GOWORK=off ./automation/ci/with-isolated-pig-home.sh "$(CHECK_PIG_BIN)" install --validate-only --json piglets/standard/extensions/angrypigs >/dev/null
	@GOWORK=off ./automation/ci/with-isolated-pig-home.sh "$(CHECK_PIG_BIN)" install --validate-only --json piglets/standard/extensions/bunruntime >/dev/null
	@GOWORK=off ./automation/ci/with-isolated-pig-home.sh "$(CHECK_PIG_BIN)" piglet validate piglets/standard/pig-standard.yaml >/dev/null
	@GOWORK=off ./automation/ci/with-isolated-pig-home.sh "$(CHECK_PIG_BIN)" piglet validate piglets/aot/pig-aot.yaml >/dev/null

##@ Gates

check-core: startup-proxies build vet lint test test-go-modules test-sdk-rs test-sdk-py test-sdk-ts typescript-extension-corpus examples-check test-race test-integration lint-scenarios port-map-drift coverage-drift standard-check go-fix-clean divergence-consistency divergence-quality divergence-guard port-lint any-trap perf-lint race-coverage check-contracts-fast source-hygiene test-provenance docs-drift bug-ledger-check perf-scoreboard-check module-size ## All deterministic build, lint, unit, race, and drift gates

check: module-publication check-core parity-fast ## Pre-commit gate: published dependencies, check-core and parity-fast
	@echo
	@echo "make check: all gates green."
	@echo
	@echo "Reminder: run 'make coverage' before signaling success to refresh"
	@echo "the AGENTS.md dashboard and test/parity/coverage.md. Do not hand-edit."

# verify = deterministic gates + declared durability parity + coverage.
# The recommended one-shot for end-of-loop verification: `parity` proves every
# declared pair and regenerates the dashboard after the core gates pass.
verify: check-core check-contracts parity ## End-of-loop gate: check-core, contracts, and full parity
	@echo
	@echo "make verify: dashboard refreshed deterministically from"
	@echo "docs/parity/PORT_MAP.md + test/parity/scenarios. Commit the diff if any."

qc: parity-bin ## Build a fresh pig, then run the smoke and focused parity QC
	@PIG_BIN="$(PARITY_PIG_BIN)" ./automation/ci/qc-smoke.sh

# Release gate. Tighter than `check`: parity-live must pass, coverage
# must not regress, no new entries in docs/parity/DIVERGENCES.md without justification.
release-check: parity-live parity-perf test-timing ## Release gate: live, perf and timing parity, no new divergences, no coverage loss
	@./automation/ci/check-divergence-delta.sh
	@python3 -B -m unittest discover -s automation/ci -p test_check_coverage_delta.py
	@./automation/ci/check-coverage-delta.sh

startup-proxies: ## Run deterministic startup allocation, cache, encoding, and artifact guards
	@automation/ci/with-isolated-pig-home.sh python3 automation/perf/startup_proxies.py
	@automation/ci/with-isolated-pig-home.sh python3 -m unittest discover -s automation/perf -p 'test_*.py'

.PHONY: startup-proxies

##@ Evals and performance

evals: pig ## Measure harness overhead against the mock model (HARNESSES=all for every installed harness)
	@$(PIGEVAL) overhead --harnesses $(HARNESSES) --runs $(RUNS) --pig bin/pig --out $(EVAL_RESULTS)
	@$(PIGEVAL) report $(EVAL_RESULTS) --out $(EVAL_RESULTS:.json=.md)

evals-requests: pig ## Save every request body and diff the first two HARNESSES (default: pig against pi)
	@$(PIGEVAL) requests --harnesses $(HARNESSES) --pig bin/pig --diff

evals-live: pig ## Run test/evals/tasks with a real model: make evals-live MODEL=provider/model
	@test -n "$(MODEL)" || { echo "evals-live: set MODEL, for example MODEL=anthropic/claude-sonnet-5" >&2; exit 2; }
	@$(PIGEVAL) live --harnesses $(HARNESSES) --pig bin/pig --model "$(MODEL)" --runs $(LIVE_RUNS) $(if $(EVAL_TASKS_DIR),--tasks-dir $(EVAL_TASKS_DIR)) --out tmp/evals/live.json
	@$(PIGEVAL) report tmp/evals/live.json --out tmp/evals/live.md

evals-mutate: ## Generate seeded bug-fix tasks from real Go files (MUTATIONS=30 SEED=1) into tmp/evals/mutation-tasks
	@$(PIGEVAL) mutate --count $(MUTATIONS) --seed $(SEED) --out tmp/evals/mutation-tasks

evals-publish: ## Copy a reviewed EVAL_RESULTS run to test/evals/results/latest.json and regenerate docs/site/docs/evals.md
	@test -f "$(EVAL_RESULTS)" || { echo "evals-publish: $(EVAL_RESULTS) not found; run make evals first" >&2; exit 2; }
	@cp "$(EVAL_RESULTS)" test/evals/results/latest.json
	@$(PIGEVAL) report test/evals/results/latest.json --out docs/site/docs/evals.md

evals-test: ## Unit-test pigeval, the eval harness
	@PYTHONPATH=$(CURDIR)/test/evals python3 -m unittest discover -s test/evals/tests -q

perf-check: ## Apply test/evals/budgets.toml (latency, memory, and request-size ceilings) to EVAL_RESULTS
	@$(PIGEVAL) check $(EVAL_RESULTS)

perf-scoreboard: ## Render docs/performance/scoreboard.md from automation/perf/scoreboard.toml, ratchet.toml and the last measurement
	@python3 automation/perf/scoreboard.py render

perf-scoreboard-check: ## Fail when the scoreboard registry names a missing test or ceiling, or docs/performance/scoreboard.md is stale
	@python3 automation/perf/scoreboard.py render --check

perf-scoreboard-measure: ## Run every count gate, record the counts in automation/perf/scoreboard-measured.json and render the scoreboard
	@automation/ci/with-isolated-pig-home.sh python3 automation/perf/scoreboard.py measure

PERF_RATCHET_BASE ?= @{upstream}
perf-ratchet-check: ## Fail when a ceiling in automation/perf/ratchet.toml rose or vanished since PERF_RATCHET_BASE without a Perf-Ratchet-Raise/Retire trailer
	@python3 automation/perf/scoreboard.py ratchet --base "$(PERF_RATCHET_BASE)"

.PHONY: perf-scoreboard perf-scoreboard-check perf-scoreboard-measure perf-ratchet-check

profile: pig ## Profile one prompt round trip (PROFILE=cpu,heap,allocs,block,mutex,trace) into tmp/profile
	@$(PIGEVAL) profile --pig bin/pig --kinds $(PROFILE) --out tmp/profile

pgo: pig ## Merge CPU profiles from repeated round trips into tmp/pgo/default.pgo for a profile-guided build
	@$(PIGEVAL) profile --pig bin/pig --kinds cpu --runs 20 --out tmp/pgo --merge tmp/pgo/default.pgo

slop: ## Measure erosion and clone verbosity (SlopCodeBench metrics) and list the heaviest functions
	@go run ./automation/ci/slopmetrics -top 15

slop-check: ## Fail when erosion or verbosity exceed the ratchet in test/evals/budgets.toml [slop]
	@go run ./automation/ci/slopmetrics -top 0 -max-verbosity $(SLOP_MAX_VERBOSITY) -max-erosion $(SLOP_MAX_EROSION) >/dev/null && echo "slop-check: within verbosity $(SLOP_MAX_VERBOSITY), erosion $(SLOP_MAX_EROSION)"

bench: ## Run Go benchmarks into tmp/bench/current.txt (BENCH=regex, BENCH_PKGS, BENCH_COUNT=6)
	@mkdir -p tmp/bench
	@go test -run '^$$' -bench '$(BENCH)' -benchmem -count $(BENCH_COUNT) $(BENCH_PKGS) > tmp/bench/current.txt; status=$$?; cat tmp/bench/current.txt; exit $$status

bench-base: ## Keep the last benchmark run as the base for bench-compare
	@cp tmp/bench/current.txt tmp/bench/base.txt

bench-compare: ## Compare the last benchmark run with the base by median; fails above a 5% regression
	@$(PIGEVAL) benchcmp tmp/bench/base.txt tmp/bench/current.txt --fail

##@ Benchmarks (bench/evals: pinned arms, no live key outside task-completion benches)

# Every arm is installed at the version and checksum in bench/evals/arms.lock into a throwaway directory and deleted afterwards.
# No-spend benches (overhead, reliability, longhistory, durable) run against a local mock endpoint with a dummy key and fail on any egress.
BENCH_PY := cd bench/evals && python3 -m pigbench
BENCH_ARMS ?= all
BENCH_SEED ?= 20261009
BENCH_OUT ?= $(CURDIR)/tmp/bench

bench-test: ## Unit tests for the benchmark tooling (stub harnesses with known timings, negative controls)
	@cd bench/evals && python3 -m unittest discover -s tests -t . -q
	@cd bench/local && python3 -m unittest discover -s tests -q
	@cd bench/compare && python3 -m unittest discover -s tests -q && python3 arms.py check

bench-lock-verify: ## Check arms.lock against its committed package-lock files (BENCH_ONLINE=1 also re-downloads each tarball)
	@$(BENCH_PY) lock verify $(if $(BENCH_ONLINE),--online)

bench-lock-bump: ## Re-pin one arm: make bench-lock-bump ARM=codex VERSION=0.163.0 (rewrites arms.lock and its package-lock file)
	@test -n "$(ARM)" -a -n "$(VERSION)" || { echo "bench-lock-bump: set ARM and VERSION" >&2; exit 2; }
	@$(BENCH_PY) lock bump $(ARM) $(VERSION)

bench-arms: ## Install every arm into a throwaway directory, check checksums and versions, delete it
	@$(BENCH_PY) arms --arms $(BENCH_ARMS)

bench-overhead: ## Harness overhead vs a scripted model: startup, print, PTY, RPC, 10 extensions, 1,000-turn session with compaction
	@$(BENCH_PY) overhead --arms $(BENCH_ARMS) --seed $(BENCH_SEED) --out $(BENCH_OUT)/overhead $(BENCH_OVERHEAD_ARGS)

bench-reliability: ## HTTP 429/500, disconnects, kill mid-tool-call, SIGTERM: pass or fail per fault per arm, scored against Pi
	@$(BENCH_PY) reliability --arms $(BENCH_ARMS) --seed $(BENCH_SEED) --out $(BENCH_OUT)/reliability $(BENCH_RELIABILITY_ARGS)

bench-startup: ## `--version` of every arm under hyperfine 2.0 (--shell=none): wall, CPU, peak resident memory; arms rotate each round
	@$(BENCH_PY) startup --arms $(BENCH_ARMS) --seed $(BENCH_SEED) --out $(BENCH_OUT)/startup $(BENCH_STARTUP_ARGS)

bench-aggregate: ## One report from several machines' raw.json files: make bench-aggregate DIRS="dirA dirB" (per-architecture tables and a cross-architecture summary)
	@test -n "$(DIRS)" || { echo "bench-aggregate: set DIRS" >&2; exit 2; }
	@$(BENCH_PY) aggregate $(abspath $(DIRS)) --out $(BENCH_OUT)/aggregate $(BENCH_AGGREGATE_ARGS)

bench-soak: ## Steady scripted turns for BENCH_SOAK_HOURS (default 24) on PiG and Pi at once; processes, threads, descriptors and memory sampled each minute and judged flat
	@$(BENCH_PY) soak --hours $(or $(BENCH_SOAK_HOURS),24) --seed $(BENCH_SEED) --out $(BENCH_OUT)/soak $(BENCH_SOAK_ARGS)

bench-local: ## Harness cost against a pinned local llama.cpp server and model (BENCH_LOCAL_ARGS; see bench/local/README.md); results are provisional
	@python3 bench/local/localrun.py --out $(BENCH_OUT)/local $(BENCH_LOCAL_ARGS)
	@python3 bench/local/report.py $(BENCH_OUT)/local/raw.json

bench-longhistory: ## Sessions of ~1M tokens, and ~3M across 3 compactions: open, resume, RSS, frames, keystrokes, resize, append
	@$(BENCH_PY) longhistory --arms $(BENCH_ARMS) --seed $(BENCH_SEED) --out $(BENCH_OUT)/longhistory $(BENCH_LONGHISTORY_ARGS)

bench-durable: durable-interop-deps ## The durable-bench workload (50/250/1000/3500 turns) on PiG Durable and pi-durable, with the CPU-idle gate
	@$(BENCH_PY) durable --seed $(BENCH_SEED) --out $(BENCH_OUT)/durable $(BENCH_DURABLE_ARGS)

bench-harbor: ## Harbor: write and validate job configs and check the Pi converter against PiG (needs HARBOR_PYTHON); runs no job
	@$(BENCH_PY) harbor-plan --out $(BENCH_OUT)/harbor/jobs $(BENCH_HARBOR_ARGS)
	@$(BENCH_PY) harbor-compat --arms pig-0.5 pig-0.4.1 pi-1.1.0 --seed $(BENCH_SEED) --out $(BENCH_OUT)/harbor

bench-report: ## Re-render report.md and summary.csv from a raw.json: make bench-report DIR=tmp/bench/overhead
	@test -n "$(DIR)" || { echo "bench-report: set DIR" >&2; exit 2; }
	@$(BENCH_PY) report $(abspath $(DIR))

##@ Docs

knowledge-graph: ## Regenerate the knowledge graph page, JSON-LD, and Mermaid from docs/knowledge-graph/pig.graph.json
	@python3 automation/gen/gen-knowledge-graph.py

# port-lens renders the static port health report from the repository's own ledgers (automation/portlens/README.md). PORT_LENS_SERVE=127.0.0.1:8650 also serves it; PORT_LENS_TOTALS_LOG=<file> adds a ledger totals log to the trend.
PORT_LENS_OUT ?= build/portlens
port-lens: ## Render the static port report (overview, port map, divergences, bug ledger, evals) into PORT_LENS_OUT (build/portlens); open index.html
	@python3 test/parity/interface-closure/breakdown/proofclass.py build/ledger-proof-classes >/dev/null || echo "port-lens: proof classes not refreshed; the report shows proof status as not read"
	@go run ./automation/portlens -out "$(PORT_LENS_OUT)" $(if $(PORT_LENS_TOTALS_LOG),-totals-log "$(PORT_LENS_TOTALS_LOG)") $(if $(PORT_LENS_SERVE),-serve "$(PORT_LENS_SERVE)")

##@ Upstream and catalogs

upstream-mirror: parity-deps ## Mirror the pinned Pi source into .upstream/current
	@./automation/gen/mirror-upstream.sh

pin-show: ## Show the effective upstream Pi pin and whether PI_UPSTREAM_REF overrides it
	@$(PIN_TOOL) show

pin-sync: ## Regenerate every committed file derived from .upstream/pin (pigversion.go, SDK and CI oracle pins, badges, ledger version fields)
	@$(PIN_TOOL) sync

pin-check: ## Test the pin tooling and fail when a file derived from .upstream/pin differs from its derivation
	@python3 -B -m unittest discover -s automation/pin -p 'test_*.py'
	@$(PIN_TOOL) sync --check

pin-ledgers: ## Generate the effective pin's per-pin ledgers (async-contracts, behavior-contracts, known-gaps, interface delta, custom-factory draft) from PIN_FROM=<tag|commit> (default: the committed pin)
	@$(PIN_TOOL) ledgers $(if $(PIN_FROM),--from "$(PIN_FROM)")

pin-bump: ## Move the pin to PI_UPSTREAM_REF=<tag|commit>, regenerate what derives from it, then mirror, install the oracle and generate the ledgers
	@test -n "$(PI_UPSTREAM_REF)" || { echo "pin-bump: set PI_UPSTREAM_REF=<tag|commit>" >&2; exit 2; }
	@old=$$(env -u PI_UPSTREAM_REF $(PIN_TOOL) get ref); env -u PI_UPSTREAM_REF $(PIN_TOOL) bump "$(PI_UPSTREAM_REF)"; \
		$(MAKE) --no-print-directory PI_UPSTREAM_REF= upstream-mirror interface-proposals test-inventory-generate && \
		$(MAKE) --no-print-directory PI_UPSTREAM_REF= PIN_FROM="$$old" pin-ledgers && \
		$(MAKE) --no-print-directory PI_UPSTREAM_REF= vendor-pi-dist model-catalogs && \
		./automation/gen/regenerate-observation-oracles.sh && \
		./automation/gen/refresh-pi-test-sources.sh

vendor-pi-dist: parity-deps ## Regenerate the vendored Pi runtime (runtime-node/shims) from the installed oracle of the pinned Pi
	@./automation/gen/vendor-pi-dist.sh

generate-schemas: ## Regenerate schemas/*.schema.json, the JSON Schemas of models.json, settings.json, keybindings.json and themes (Pi scripts/generate-schemas.ts)
	@go run ./cmd/generate-schemas

model-catalogs: parity-deps ## Regenerate model catalogs from the pinned published Pi package
	@./automation/gen/generate-model-catalogs.sh

##@ Dependencies

interface-deps: ## Install the locked TypeScript compiler used by parity inventories
	@python3 automation/ci/npm-locked.py test/parity/interface-extractor

durable-interop-deps: ## Install the Pi Durable packages the cross-runtime tests run on Node: the locked release of a tag pin, a build of the commit of a commit pin
	@if [ "$$($(PIN_TOOL) get kind)" = tag ]; then python3 automation/ci/npm-locked.py durable/interop; else $(PIN_TOOL) install-oracle; fi

durable-core-host-deps: durable-interop-deps ## Install the locked npm tree of the Durable core's Cloudflare host, with Pi's packages from the pinned build (the release itself for a tag pin)
	@python3 automation/ci/npm-locked.py durable/core/host/cf
	@$(PIN_TOOL) link-durable-host durable/core/host/cf

durable-core-host-check: durable-contract-setup durable-core-host-deps ## Typecheck and test the Cloudflare host and its pi-durable facade against the probe core built with TinyGo and Go
	@durable/core/host/cf/check.sh

durable-core-budget: durable-core-host-deps ## Build the core and host bundle with TinyGo and Go and check their size budgets (durable/core/budget/budget.json) and the TinyGo heap policy (fixed step, ceiling trap)
	@cd durable/core/budget && node measure.mjs --check && node allocs.mjs --check && node --test growheap.test.mjs allocs.test.mjs

durable-contract-setup: ## Check out the Pi reference the Durable core conformance corpus is captured from
	@durable/contract/tools/setup.sh

durable-contract-test: durable-contract-setup ## Unit, pipeline and matrix tests of the Durable conformance tooling (negative controls included)
	@go test ./durable/core/contracttest/...
	@cd durable/contract && node cli/contract.mjs fixtures 50 >/dev/null && node --test --test-timeout=600000 test/*.test.mjs

durable-contract-trace: durable-contract-setup ## Compare a candidate's call/commit interleaving with Pi's golden traces (IMPL=native|wasm-go|wasm-tinygo); red until the core schedules like Pi
	@cd durable/contract && node cli/trace.mjs check --impl $${IMPL:-native} && node cli/trace.mjs check-u --impl $${IMPL:-native}

durable-counts: durable-contract-setup ## Check the Durable core's count budgets (rows, crossings, heap, memory, allocations, instructions) against durable/contract/counts.json; builds the cores first; needs VALGRIND=PREFIX/bin/valgrind from durable/contract/tools/build-valgrind.sh
	@make -s -C durable/core build-tinygo build-go && sh durable/core/budget/build-native.sh >/dev/null && node durable/contract/cli/counts.mjs check

durable-bar: ## Judge the Durable 0.5.0 memory bar (no live-set growth per turn, heap independent of conversation length) over durable/contract/counts.json; fails today by design, and no entry may be raised to pass
	@node durable/contract/cli/counts.mjs bar --from durable/contract/counts.json

durable-flat: ## Long-run memory proof of the Durable 0.5.0 bar: record 300 turns, replay natively, fail unless the live set stops growing (about 3.5 min; TURNS=N)
	@sh durable/core/contracttest/flat.sh

durable-counts-update: durable-contract-setup ## Rewrite durable/contract/counts.json from this build (commit it with the change that moved the counts); needs VALGRIND as durable-counts does
	@make -s -C durable/core build-tinygo build-go && sh durable/core/budget/build-native.sh >/dev/null && node durable/contract/cli/counts.mjs update

durable-contract: durable-contract-setup ## Run the Durable core conformance gate (CONTRACT sections 5-7) against the TinyGo, Go and native builds; per-lane pass/fail
	@cd durable/contract && node cli/contract.mjs all $(DURABLE_CONTRACT_ARGS)

parity-deps: interface-deps ## Build the Pi comparator from source at the pin (PIG_PARITY_PI_PUBLISHED=1 installs the published release of a tag pin) and the TypeScript compiler its scenarios import
	@$(PIN_TOOL) install-oracle
	@python3 automation/ci/npm-locked.py internal/typebox/testdata/npm
	@python3 automation/ci/npm-locked.py ai/testdata/sdk
	@test -x "$(PIG_PARITY_PI_BIN)" || { echo "exact Pi comparator not found: $(PIG_PARITY_PI_BIN)" >&2; exit 1; }
	@test -d "$(PI_PACKAGE_ROOT)" || { echo "exact Pi package not found: $(PI_PACKAGE_ROOT)" >&2; exit 1; }
	@$(PIN_TOOL) check-oracle

.PHONY: bug-ledger-refresh port-lens pin-show pin-sync pin-check pin-bump pin-ledgers vendor-pi-dist bench-test bench-lock-verify bench-lock-bump bench-arms bench-overhead bench-reliability bench-longhistory bench-startup bench-aggregate bench-soak bench-local bench-durable bench-harbor bench-report durable-interop-deps durable-contract durable-contract-setup durable-contract-test node-runtime bug-ledger bug-ledger-check release-notes-draft npm-dist-test npm-dist-e2e compliance evals-mutate slop slop-check # Deterministic tmux-driven integration tier # Release gate. Tighter than `check` # The recommended one-shot for end-of-loop verification # docs-drift gates the shipped docs bundle against the code it describes # install # numbered in docs/parity/DIVERGENCES.md must have a matching `// pig divergence (DN) # the single-main-loop ownership invariant bench bench-base bench-compare build build-env-daemons check check-core clean dev divergence-guard divergence-quality doctor port-lint any-trap perf-lint evals evals-live evals-publish evals-requests evals-test examples-check go-fix-clean help help-parity interface-deps knowledge-graph lint lint-changed model-catalogs parity-deps perf-check pgo pig profile qc setup standard-check test test-fixtures test-go-modules test-mcp-conformance test-prereqs test-sdk-conformance test-sdk-go test-sdk-rs test-sdk-ts test-stress upstream-mirror vet durable-counts durable-bar durable-flat durable-counts-update durable-contract-trace durable-core-host-deps durable-core-host-check durable-core-budget

include automation/make/parity.mk
include automation/make/ci.mk
