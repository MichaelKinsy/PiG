<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Parity suite

The parity suite compares PiG with the exact Pi release pinned in
`coding/pigversion/pigversion.go`. Canonical scenarios are TOML files under
`parity/scenarios/<family>/` and run through `parity/runner`.

The coverage report counts observable assertions from paired scenarios and reviewed Go unit tests. Importing, registering, or starting code does not prove behavior. Unit evidence in `parity/unit-evidence/*.json` names an exact upstream path, the owning Go package, executable test names, the upstream source or test reference, and the compiling mutation that made those assertions fail. The generator rejects unknown upstream paths and missing tests. Review must establish the source correspondence and mutation proof; declaration alone is not proof. Unit coverage is shown separately from paired scenarios and does not imply exhaustive parity or a fresh test run.

## Layout

```text
parity/
├── behavior-contracts.toml
├── closure/                 deterministic evidence graph and reports
├── correspondence/          source-to-target correspondence compiler
├── interfaces/              generated inventories and reviewed mappings
├── porter/                  deterministic Pig Porter adapter contract
├── runner/                  canonical scenario runner
├── scenarios/               behavior-family TOML scenarios and fixtures
└── cmd/                     generators, linters, and verification commands
```

## Maintained commands

Use Make targets from the repository root:

```bash
make parity-fast
make parity
make parity-family FAMILY=compaction
make parity-driver DRIVER=cli-mode,print-mode,rpc-mode
make parity-durable
make parity-perf
make coverage
make lint-scenarios
make schedule-report
```

`make parity-fast` runs one PiG/Pi pair for each hermetic scenario. `make
parity` uses each scenario's declared durability. `make parity-perf` runs the
performance scenarios serially because parallel CPU contention invalidates
runtime comparisons.

## Scheduling and isolation

The Makefile derives process and group limits from the current host. Do not
hardcode a parallelism value in a scenario or maintainer procedure.

Default scheduler groups follow the driver:

| Driver | Default group |
|---|---|
| `interactive-tmux` | `tmux` or `tmux-ext` |
| `headless-terminal` | `ht` |
| `rpc-mode` | `rpc` |
| `print-mode`, `cli-mode` | `process` or `process-ext` |
| `serial` tag | `exclusive` |

Use an explicit `group` only when the default cannot isolate the behavior. Add a
`# group-rationale:` comment next to every override.

The runner:

- copies declared PiG and Pi homes into separate temporary roots;
- gives every tmux scenario a unique server and session;
- resolves writable scenario paths through `{{TEMP}}`;
- runs every binary in its own copy of a working directory under the canonical temporary root: the scenario's fixture `cwd`, or `parity/testdata/default-cwd`. Before creating a cwd copy, the runner resolves temporary-root symlinks and rejects roots inside the checkout or with ancestor context files (`AGENTS.override.md`, `AGENTS.md`, `AGENTS.MD`, `CLAUDE.md`, or `CLAUDE.MD`). Set `TMPDIR` to an existing clean directory if validation fails. The default fixture's one `AGENTS.md` is then the only project context file either binary loads. Copies are named `parity-snap-cwd-<10 digits>`, so pig and pi paths have equal length, and the runner masks the digits before comparing output. No binary runs inside the checkout, except a `cli-mode` fixture `cwd` without `snapshot_cwd`. `tmux.git_branch` makes the copy an empty git repository on that branch for branch-display scenarios;
- points Pi's `PI_PACKAGE_DIR` at a fixed-length link to its package, so the
  docs paths in Pi's system prompt do not grow with the checkout path;
- records "do not trust" for the checkout root in every snapshotted agent
  dir's `trust.json`, unless the fixture already decides it, so the tracked
  `.agents/skills` never opens the project-trust prompt. A `cli-mode` fixture
  project that exercises project trust sets `snapshot_cwd = true` to run from
  a copy outside the checkout; and
- removes ambient proxy variables from hermetic runs.

Set `TMPDIR` to a short clean root for footer scenarios that use `capture_start = "parity-snap-cwd-"`. The terminal drivers require the complete canonical cwd and configured git branch to fit `tmux.width`, without relying on HOME abbreviation. They reject an unsupported path before launching either binary, so startup resource listings cannot substitute for a truncated footer anchor. The complete two-row escaped footer comparison remains unchanged.

Never use a literal shared `/tmp` path or `pre_clear_paths`. Never write into a
checked-in fixture root.

## Scenario structure

A scenario names one observable, its owner family, its driver, exact upstream
paths, and assertions:

```toml
name = "startup-banner"
description = "PiG and Pi reach the interactive prompt"
covers = [
  "packages/coding-agent/src/main.ts",
]
tags = ["fast", "hermetic", "tui"]
driver = "interactive-tmux"

[tmux]
width = 100
height = 35
ready_pattern_pig = "(auto)"
ready_pattern_pi = "pi v"
ready_timeout_seconds = 30

[assert]
both_reach_ready = true
runs = 3
```

Supported drivers are:

- `interactive-tmux`;
- `headless-terminal`;
- `print-mode`;
- `cli-mode`;
- `rpc-mode`; and
- `extension-host`.

Driver-specific fields are defined in `parity/runner/schema.go`. Use an existing
scenario in the same family as the closest example.

The `cli-mode` driver connects both stdout and stderr to one temporary regular file. Node writes to files synchronously, so Pi's immediate `process.exit()` cannot discard queued pipe output from large one-shot listings. The driver reads the file after process exit and removes it with the run's temporary directory. This preserves complete combined-output bytes without changing the oracle, filtering rows, or normalizing output. Print and RPC drivers keep their existing transports.

## Assertions

| Assertion | Contract |
|---|---|
| `exit_code` | Both processes exit with the same required code. |
| `pig_exit_code`, `pi_exit_code` | Each process exits with its separately required code. |
| `both_contain`, `both_not_contain` | Both outputs include or exclude each value. |
| `pig_contains`, `pig_not_contain` | Only PiG output is checked. |
| `pi_contains`, `pi_not_contain` | Only Pi output is checked. |
| `both_match_regex` | Every expression matches both outputs. |
| `output_equal` | Outputs are byte-identical. |
| `escaped_output_equal` | Escaped tmux captures are byte-identical. |
| `output_layout_equal` | Terminal layout is equal under the layout comparator. |
| `output_normalized_equal` | Outputs are equal after the declared normalization. |
| `artifact_normalized_equal` | Driver output files are equal after normalization. |
| `runtime_ratio_max` | Serial median PiG runtime stays within the declared Pi ratio. |
| `both_reach_ready` | Both interactive processes reach their ready boundary. |

Use the strongest stable comparator:

1. `escaped_output_equal` for exact TUI output;
2. `output_equal` for exact process or protocol output;
3. `output_layout_equal` when terminal placement is the contract;
4. `output_normalized_equal` only for identified unstable fields; and
5. substring assertions only when equality is not a valid contract.

Every `normalize_replace`, weak-quality tag, skip, serial tag, or group override
needs a specific rationale. Do not normalize a real PiG/Pi difference.

## Coverage contract

`covers` contains exact paths from `PORT_MAP.md`. Each path must participate in
the behavior asserted by the scenario. The generated report distinguishes:

- behavioral verification;
- boot-only, registration-only, and smoke-only evidence;
- deferred evidence; and
- untested rows.

Weak evidence remains visible but does not count as behavioral verification.
Run `make coverage` after a successful parity run. Do not edit
`parity/coverage.md`, the README badge, or the generated `AGENTS.md` block by
hand.

## Add a scenario

Probe Pi first, then scaffold the scenario:

```bash
make parity-new \
  FAMILY=model-resolver-selector \
  NAME=14-example \
  DRIVER=interactive-tmux \
  COVERS='packages/coding-agent/src/example.ts'
```

Then:

1. record the upstream command, input, environment, and observable in comments;
2. add the strongest stable assertion;
3. run `make parity-family FAMILY=<family>`;
4. fix PiG or record an approved numbered divergence;
5. run `make lint-scenarios`; and
6. regenerate coverage only after the scenario passes.

Keep one canonical parity check per observable. Delete a superseded duplicate in
the same change after confirming that the TOML scenario preserves its evidence.
