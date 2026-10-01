<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Parity suite

The parity suite compares PiG with the exact Pi release pinned in
`internal/coding/pigversion/pigversion.go`. Canonical scenarios are TOML files under
`test/parity/scenarios/<family>/` and run through `test/parity/runner`.

The coverage report counts observable assertions from paired scenarios and reviewed Go unit tests. Its orphan-cover section orders entries by scenario name and then upstream path so repeated stdout, file, and committed reports retain identical bytes. Importing, registering, or starting code does not prove behavior. Unit evidence in `test/parity/unit-evidence/*.json` names an exact upstream path, the owning Go package, executable test names, the upstream source or test reference, and the compiling mutation that made those assertions fail. The generator rejects unknown upstream paths and missing tests. Review must establish the source correspondence and mutation proof; declaration alone is not proof. Unit coverage is shown separately from paired scenarios and does not imply exhaustive parity or a fresh test run.

## Layout

```text
test/parity/
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
make normalization-inventory
make schedule-report
```

`make parity-fast` runs one PiG/Pi pair for each hermetic scenario. `make
parity` uses each scenario's declared durability. `make parity-perf` runs the
performance scenarios serially because parallel CPU contention invalidates
runtime comparisons.

Every parity target fails when the run compares no scenario. The runner fails, rather than skips, when it cannot find the pinned Pi or the PiG binary under test, and each target reads the results file through `require-parity-ran`. Set `PIG_PARITY_ALLOW_ZERO=1` to accept a run that compares nothing, for example on a host that has no Pi install. A version mismatch of the comparator has its own opt-out, `PIG_PARITY_ALLOW_VERSION_SKEW=1`.

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
- gives every tmux run a unique server and session, ignores host tmux configuration, and sets `extended-keys=on` and, on tmux 3.5+ (which added the option), `extended-keys-format=csi-u` before launching either binary;
- resolves writable scenario paths through `{{TEMP}}`;
- runs every binary in its own copy of a working directory under the canonical temporary root: the scenario's fixture `cwd`, or `test/parity/testdata/default-cwd`. Before creating a cwd copy, the runner resolves temporary-root symlinks and rejects roots inside the checkout or with ancestor context files (`AGENTS.override.md`, `AGENTS.md`, `AGENTS.MD`, `CLAUDE.md`, or `CLAUDE.MD`). Set `TMPDIR` to an existing clean directory if validation fails. The default fixture's one `AGENTS.md` is then the only project context file either binary loads. Copies are named `parity-snap-cwd-<10 digits>`, so pig and pi paths have equal length, and the runner masks the digits before comparing output. No binary runs inside the checkout, except a `cli-mode` fixture `cwd` without `snapshot_cwd`. `tmux.git_branch` makes the copy an empty git repository on that branch for branch-display scenarios;
- points Pi's `PI_PACKAGE_DIR` at a fixed-length link to its package, so the
  docs paths in Pi's system prompt do not grow with the checkout path;
- records "do not trust" for the checkout root in every snapshotted agent
  dir's `trust.json`, unless the fixture already decides it, so the tracked
  `.agents/skills` never opens the project-trust prompt. A `cli-mode` fixture
  project that exercises project trust sets `snapshot_cwd = true` to run from
  a copy outside the checkout; and
- waits for the pane processes and their observed descendants to exit after each tmux scenario, and fails the scenario if any remain running; this check never sends a stronger signal to hide a shutdown failure;
- removes only the tmux keeper it started; a helper process that inherits the run ID does not remove its parent's keeper;
- removes ambient proxy variables from hermetic runs;
- removes host terminal, WSL, remote-session (SSH_CONNECTION, SSH_CLIENT, SSH_TTY, MOSH_CONNECTION; the other SSH_* variables, such as SSH_AUTH_SOCK, are not read by Pi or PiG and stay inherited), Herdr, multiplexer (ZELLIJ, STY), COLORFGBG, FORCE_COLOR, TF_BUILD, AGENT_NAME, CI and TEAMCITY_VERSION signals from every driver's child environment, matching Windows variable names case-insensitively, so a scenario that needs one sets it in `[env]`, which is applied after the scrub. The headless-terminal driver also removes TMUX and TMUX_PANE. TERM, TERM_PROGRAM and TMUX are inherited by cli, print, rpc and extension-host runs, and DISPLAY, WAYLAND_DISPLAY and XDG_SESSION_TYPE are inherited by the cli, print, rpc, extension-host and tmux drivers because the X11 clipboard tests need them. The headless-terminal driver passes ht only the cleared keys, the fixture and scenario environment and PATH, because ht replaces the child environment, so a headless-terminal scenario that needs one of these sets it in `[env]`. It launches the binary through the absolute path of `env`, because ht resolves the command in the daemon's PATH before it applies `--env`.

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

Driver-specific fields are defined in `test/parity/runner/schema.go`. Use an existing
scenario in the same family as the closest example.

`tmux.capture_history = true` captures retained scrollback together with the viewport. It creates an application pane after setting the private session's history limit to tmux's maximum; it does not change terminal dimensions or shared server defaults. History storage grows with emitted output and is released when the owned session is killed. This is terminal state, not a raw byte-stream comparator. Require an opening capture anchor and a source-derived completeness assertion when the whole document is the contract, so lost history cannot become a passing tail-only comparison. Use an unsubmitted editor marker with `wait_visible_contains` to observe that the terminal consumed a long command's output before capturing it.

RPC lifecycle scenarios set `terminate = true` to send SIGTERM after their final output barrier while stdin remains open. The driver waits for process exit instead of sending EOF. Use `exit_code = 143` for Pi's SIGTERM shutdown contract. RPC captures stdout without trimming and retains stderr separately. Set `stderr_equal = true` to compare the complete diagnostic stream.

RPC steps search only complete new output after the command is sent. `wait_event = '{"type":"response","id":"request-id"}'` matches those fields together in one parsed record and ignores object-member ordering. Use a unique request ID when a response is the barrier. `wait_contains` also searches only new complete lines; it is synchronization, not an acceptance comparator. Tmux `wait_contains` requires every requested marker to be new after the step starts. Use `wait_visible_contains` for retained visible state or startup state instead. `wait_stable_milliseconds = N` waits, after the step's other waits, until the visible pane has not changed for N ms. Use it as the completion barrier for an animation that emits no completion output; choose N longer than the animation's longest idle gap between frames of motion, and cite the source that bounds that gap.

Tmux steps that send Escape before literal text use a visible state barrier between those inputs. Without a barrier, the terminal decoder can combine `ESC` and the first character into one Alt-modified key. Wait for the actual state transition, such as the cleared tree query, rather than delaying input by an arbitrary duration.

`changelog_fixture = "testdata/changelog.md"` supplies one pinned document to both interactive binaries. The runner builds the current PiG CLI with a Go embed overlay before terminal timing starts. It gives Pi a private package view through Pi's `PI_PACKAGE_DIR` override. Only `CHANGELOG.md` differs from the normal package. Neither the checkout nor the installed Pi package is modified. Use this field for changelog rendering assertions instead of depending on either product's growing release notes.

`openai_fixture = true` starts a harness-owned local HTTP/SSE endpoint and writes the same `strict-wire/strict` model definition into each isolated agent directory. It exercises real provider, Session, tool, and mode code without an extension provider or the mirrored faux provider. `READ` requests the read tool, a tool followup echoes its actual content, and `HTTP_ERROR` returns a deterministic HTTP 400. The test owns endpoint cleanup.

`provider_fixture = "<api>"` serves the same fixture for another provider API: `anthropic-messages`, `google-generative-ai`, `mistral-conversations`, or `openai-completions`. It excludes `openai_fixture`, which remains the spelling for `openai-completions`. The runner writes the `strict-wire/strict` model with that `api` and the base URL the provider SDK expects (`/v1beta` for Google, the server root for Anthropic and Mistral, `/v1` for OpenAI). Each API uses its own request reader and SSE shape, and every reply is one `Content-Length` write so headers and the whole body arrive together. `READ`, tool-result echo and `HTTP_ERROR` behave as for the OpenAI fixture. Google generates tool-call IDs from the clock, so its scenarios alias the `read_<ms>_<counter>` IDs with an `id` alias.

The `cli-mode` driver connects both stdout and stderr to one temporary regular file. Node writes to files synchronously, so Pi's immediate `process.exit()` cannot discard queued pipe output from large one-shot listings. The driver reads the file after process exit and removes it with the run's temporary directory. This preserves complete combined-output bytes without changing the oracle, filtering rows, or normalizing output. Print and RPC drivers keep their existing transports.

RPC scenarios can set `rpc.artifact_path` to capture a command-owned file after process shutdown.
Use an RPC step that waits for a post-write notification before closing stdin.
A handled-command prompt acknowledgement also follows a command error, so it does not prove that the artifact write succeeded.

## Paired fixture inputs

Provider-wire probes must call equivalent API layers with equivalent options. Anthropic's native `stream` preserves `maxTokens` and defaults an enabled thinking budget to 1024 tokens. Its `streamSimple` converts a reasoning level into a budget and adjusts the output cap. The Anthropic wire probe uses native thinking options on both sides; substituting a simple reasoning level changes the request rather than testing the same input.

The wire capture servers implement HTTP/1.1. Both probes set `AWS_BEDROCK_FORCE_HTTP1=1` so Pi's Bedrock SDK uses the server's protocol instead of its HTTP/2 default. A probe that captures no request fails with the provider's terminal error; it must not fabricate an endpoint path.

## Assertions

| Assertion | Contract |
|---|---|
| `exit_code` | Both processes exit with the same required code. |
| `pig_exit_code`, `pi_exit_code` | Each process exits with its separately required code. |
| `both_contain`, `both_not_contain` | Both outputs include or exclude each value. |
| `pig_contains`, `pig_not_contain` | Only PiG output is checked. |
| `pi_contains`, `pi_not_contain` | Only Pi output is checked. |
| `both_match_regex` | Every expression matches both outputs. |
| `json_output_equal` | Parse and compare complete JSONL objects, preserving field presence, null, scalar types, array/record order, string whitespace and exact numeric values. Equivalent number spelling and object-member order are not differences. Reject malformed records and duplicate keys. No text normalizer runs. |
| `stderr_equal` | Compare the entire separately captured stderr stream. CLI and print use separate regular files when this assertion is selected. Unsupported capture fails rather than comparing absent streams as empty. |
| `artifact_equal` | Compare the entire driver artifact without ANSI or whitespace normalization. |
| `output_equal` | Outputs are byte-identical except individually declared and justified replacements. RPC `canonical_json = true` first parses each JSONL record and sorts object keys; raw stdout is retained separately. |
| `escaped_output_equal` | Escaped tmux captures are byte-identical. |
| `output_layout_equal` | Terminal layout is equal under the layout comparator. |
| `output_normalized_equal` | Outputs are equal after the declared normalization. |
| `artifact_normalized_equal` | Driver output files are equal after normalization. |
| `runtime_ratio_max` | Serial median PiG runtime stays within the declared Pi ratio. |
| `changelog_headers_complete` | Compare every rendered release header, in source-reversed order, against each binary's independent bundled changelog input. Require complete tmux history. D22 permits the product documents to differ; no header is aliased or discarded. This does not compare body text or styling between different documents. |
| `both_reach_ready` | Both interactive processes reach their ready boundary. |

Use the strongest stable comparator:

1. `escaped_output_equal` for exact TUI output;
2. `output_equal` for exact process or protocol output;
3. `output_layout_equal` when terminal placement is the contract;
4. `output_normalized_equal` only for identified unstable fields; and
5. substring assertions only when equality is not a valid contract.

Every `normalize_replace` needs its own `reason` in the rule and a scenario rationale comment. Missing reasons fail loading and scenario lint. Exact comparators do not use the ANSI/whitespace normalizer. Every weak-quality tag, skip, serial tag, or group override also needs a specific rationale. Do not normalize a real PiG/Pi difference.

Full-record comparisons use `[[assert.json_aliases]]` only for explicit scalar identities. Each rule declares `paths`, `kind`, and `reason`. Paths are JSON pointers with `*` for one segment and `**` for zero or more segments. `kind = "id"` requires a shared `group` and maintains a bijection across IDs and references. An optional `pattern` restricts which string values qualify. `kind = "timestamp"` retains timestamp presence, JSON type and valid numeric/ISO syntax, but does not certify timestamp precision. `kind = "duration"` retains a measured elapsed time's presence and Pi's 0.1 s number spelling (a non-negative decimal with at most one fractional digit, no exponent), but not its value; use it for a host-load-dependent duration such as bash's `wall_time_seconds`, which Pi computes as `Math.round(ms / 100) / 10` (`bash.ts:392`). Every full-record JSON scenario that runs bash declares it; `TestJSONScenariosAliasMeasuredDurations` discovers them from the faux provider's bash prompts. `kind = "path"` substitutes only exact isolated runtime roots, preserving suffixes and surrounding text. Its optional `roots` selects specific roots; `readme` and `examples` require explicit selection, so existing directory aliases do not gain new substitutions. `readme`, `docs`, and `examples` bind the exact D2/D22 documentation destinations. `kind = "literal"` declares exact nonempty `pig` and `pi` spellings at the selected JSON paths. Multiple literals and paths compose as typed tokens in one pass over the original string, not cascading replacements. Every unmatched byte, alias occurrence, and its order remain compared. `kind = "session_file"` validates Pi's ISO-millisecond/UUID `.jsonl` filename, compares its full directory with the selected path roots, and binds the embedded UUID to the shared identity `group` (the same group as `sessionId`). No rule deletes a field or record. Keep raw-format scenarios for identity spelling and timestamp precision.

`test/parity/normalization-inventory.json` lists each replacement and alias with its owning scenario and justification, each plain-text comparator and crop with its limited assertion surface, and the source hashes of the shared transformations and reviewed fixture projections. `make lint-scenarios` checks the generated inventory through `normalization-inventory-check`. Run `make normalization-inventory` after reviewing a changed rule or mechanism. Inventory presence records review scope; it does not turn a crop, faux followup or diagnostic summary into complete wire proof. Provider-wire probes compare complete captured HTTP bodies alongside their legacy summaries. Real-provider RPC/JSON scenarios compare the complete mode event stream.

## Coverage contract

`covers` contains exact paths from `docs/parity/PORT_MAP.md`. Each path must participate in
the behavior asserted by the scenario. The generated report distinguishes:

- behavioral verification;
- boot-only, registration-only, and smoke-only evidence;
- deferred evidence; and
- untested rows.

Weak evidence remains visible but does not count as behavioral verification.
Run `make coverage` after a successful parity run. Do not edit
`test/parity/coverage.md`, the README badge, or the generated `AGENTS.md` block by
hand.

## Approved 0.3.x test-porting gaps

The owner decision of 2026-09-28 permits explicit release-gate deferrals for unfinished upstream test acceptance. It does not mark tests ported or make their assertions pass. The decision and review findings are recorded in [the known-gap ledger](../../docs/findings/0.3.0-known-gaps.md).

Keep the mapping disposition `partial` or `pending` and retain its evidence, upstream hash and missing-case rationale. Keep the policy row's `hot-path` tag. Add `deferred-0.3.x` only after owner review. Start its policy rationale with `0.3.x known gap: ` and describe the unfinished acceptance. Retain the production-path rationale. Include `SCRUTINIZED:approved`, the approving owner and decision date, and `FOLLOWUP-<area>;`. End with `Missing cases: test/parity/interfaces/test-mapping-v<version>.json#<exact upstream test path>` so the gate reads the missing cases from that row rather than a second stale copy. The follow-up placeholder refers to the area group in the 0.3.1 tracking draft until the lead files it.

`make test-porting-release` prints every approved row, its unchanged disposition, the complete missing-case rationale and the known-gap count. An unlisted hot path, an invalid approval, a missing review, source-hash drift or a decreased committed ported count still fails. A deferral on a closed or untagged row fails as stale. Remove the deferral and restore the production-path rationale when reviewed closure lands. `make test-inventory-strict` still rejects every partial/pending mapping. No unit-test runner or parity comparator consumes the deferral tag.

After you close, defer or retag a row, run `make known-gaps` and commit `docs/parity/KNOWN-GAPS-0.3.x.md`. `make known-gaps-drift` (part of `make check` and `ci-contracts`) fails when the generated block is stale. A branch that changes the mapping or the release policy must regenerate the block. When a merge conflicts inside the block markers, take either side and run `make known-gaps`; never merge the block by hand.

At release freeze, rerun the gate against the exact frozen source with the reviewed policy change applied. Reconcile the row list and remaining confirmed review findings against that commit. Retain the raw results. A passing policy gate proves accounting, not parity or authorization to publish a release.

## Design out individual upstream cases

When owner review designs out some cases of an upstream test file and the other cases are ported, keep the mapping disposition `ported` or `partial` and list the designed-out case ids in `designedOutCases`. Copy each id exactly from `test/parity/interfaces/upstream-tests-v<version>.json`. The rationale names the owner decision, each designed-out case line and the upstream source that makes the case unreachable in PiG. `make test-inventory` rejects an unknown or duplicate id, an id shared by two upstream case sites (the schema cannot say which site is exempt), a missing rationale, a list that covers every case, and the field on any other disposition. Record the same ids in `designedOutCases` on the file's row in `test/parity/interfaces/test-porting-policy-v<version>.json` and put `SCRUTINIZED:approved`, the approving owner and the decision date in that row's rationale. `make test-porting-release` rejects a mapping list that differs from the approved policy list, so neither file can add or drop a designed-out case alone. The remaining evidence is still resolved. Do not classify such a file `designed-out`: a `designed-out` file keeps no ported cases.

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
