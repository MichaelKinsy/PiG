---
name: pig-porter
description: Run one bounded upstream-first Pi-to-PiG porting or verification task.
---

<!--
SPDX-License-Identifier: MIT
-->

# Pig Porter

Work against the exact Pi build in `.upstream/pin` (the
pin `coding/upstream.go` re-exports). Use one mode and one bounded scope:

- `inventory <family>` inspects the current frontier without changing behavior.
- `family <family>` ports one behavior family.
- `tighten <family>` strengthens existing evidence and fixes drift it exposes.
- `verify <family>` runs the family acceptance ladder without changing behavior.
- `closure <upstream-file> [part i/n]` closes the interface gap rows whose top-level symbol
  `<upstream-file>` declares (for example `closure packages/durable/src/harness/types.ts`); a file with
  more than 25 rows is split into parts, and a part task closes only its slice of the rows.
- `delta <package> [part i/n]` reviews the pending `upstream-sync` files of one package
  (for example `delta codemode`); a package with more than 8 pending files is split into parts, and a
  part task reviews only its slice of the files.
- `upgrade <tag>` plans an upstream-version change. It remains read-only until
  the user approves the plan.

Reject a missing mode, family, file, package, or upgrade tag. Combine families only when the
upstream contract and production call path require it. State the coupling before
editing.

## Closure and delta modes

These modes drive the work list of a pin move. The pipeline (`automation/pin/follow.py`) writes one task per
upstream file (`closure`) or package (`delta`). Run them headless: never stop to ask. When a step needs a
decision this skill reserves for a human (a divergence, a designed-out or deferred classification, a public
wire or API change), record the row as `BLOCKED <row> <reason>` in the report and continue with the next row.

Port every row from the Pi source, never from another lane's branch. If a branch or worktree outside your own already ports a
surface, record the row as `BLOCKED <row> already ported on <branch>` and do not copy its files (`git show`, `git checkout <ref> --`,
`cp` from another worktree): that work is in review elsewhere and a copy conflicts with it when it merges.

A closure or delta task for a followed commit is output for the pin-move branch of that commit (a later pin than the
committed one). It edits ledgers, PORT_MAP rows and citations that name files absent at the committed pin, so it is
correct only against the followed pin's gates (`PI_UPSTREAM_REF`); the maintainer carries it on the pin-move branch
and never merges it to a branch that still pins the earlier Pi.

`closure <upstream-file>`:

1. List the rows: `python3 automation/pin/follow.py "$(python3 automation/pin/pin.py get commit)" --closure <upstream-file>` (a part task adds `--part i/n`, and closes only the rows that list prints).
   Each line is the interface ID, the detector's reason, and the Go symbol it bound (empty when it found none).
2. Read the complete upstream declaration in `.upstream/current` and the bound Go code.
3. Close each row by its reason, and only from evidence:
   - `child-gap`: close the member rows it names; the parent closes with them.
   - `undecidable`: the detector cannot compare the two shapes. When Go matches Pi and a passing test proves it,
     add a reviewed entry to `test/parity/interface-closure/autobind/reviewed-types.json` (the Go symbol, and a
     reason that cites the Pi file and line and the test that proves it). Otherwise treat the row as a mismatch.
   - `no-go-symbol`: find the Go symbol (add a rename to `autobind/renames.json`), or port the behavior test-first
     against the pinned Pi executable. A surface Pig does not ship is `BLOCKED`.
   - `type-mismatch`, `member-missing`, `signature-mismatch`: Go differs from Pi. Fix Go test-first; do not edit a
     reviewed entry to make the detector agree.
   - `not-exercised`: add the asserting test and prove it with a mutation that compiles and fails.
   Read every test you cite. The detector lists tests that mention a symbol, not tests that assert the member you are
   closing: the evidence for a property must be a test that exercises that property (a `signal` property needs a test that
   cancels it), and the evidence for a changed behavior is a test that fails under a mutation you applied.
4. Rederive: `make ledger-update` twice, then `make ledger-check`. The file's row count
   must fall and no other file's rows may grow.
5. Report rows closed, rows left, `BLOCKED` rows with reasons, and the commands run.

`delta <package> [part i/n]`:

1. List the files: `python3 automation/pin/follow.py "$(python3 automation/pin/pin.py get commit)" --delta <package>` (a part task adds `--part i/n`, and reviews only the files that list prints).
2. For each file, diff the previous and current mirrors under `.upstream/` and read the changed callers and tests.
3. Port what changed, test-first against the pinned Pi executable, through the family workflow. Then set the file's
   row in `test/parity/upstream-sync/v<version>.toml` to `ported`, `designed-out`, or `divergence` with `evidence`
   (tests that fail on a mutant) and a `rationale`. A file whose change is not observable (comments, build files,
   docs) is `designed-out` with that rationale. Leave `pending` any file you could not finish and say why.
4. Run `make upstream-delta` and report.

## Faithful, general implementations

Apply `AGENTS.md`'s faithful, general implementation rule before proposing a patch or accepting evidence. Port Pi's design, not just its output for the case in front of you. Pi drives provider, model, auth, and tool behavior from shared data (the model catalog, `defaultModelPerProvider`, provider auth metadata, API kinds) and a small number of shared functions. PiG must do the same:

- **One shared path per Pi function.** When Pi has one function for every provider (for example `completeProviderAuthentication`, `resolveCliModel`, `resolveProviderAuth`), PiG has one too. Do not add a provider-specific copy, and delete existing copies when you touch them.
- **No hard-coded provider or model choices.** A provider ID, model ID, API kind, base URL, or credential variable name belongs in generated catalog data or the provider's own implementation, not in a branch of shared code. If Pi itself special-cases a provider, cite the Pi file:line next to the branch.
- **Test across provider shapes.** A shared path is not verified by one provider. Cover at least an OAuth provider, an API-key provider, an OpenAI-compatible provider with a custom base URL, and a provider with no default model, as the path allows. GitHub Copilot and the `test-faux` provider alone are never enough; most past sessions used only those, which is how provider-specific bugs shipped.
- **Fix the class, not the instance.** When you find a special case that diverges from Pi, look for its siblings and fix them together. Report out-of-scope siblings and coordinate ownership before expanding the task.
- **Root causes only.** Do not weaken tests, raise a timeout, add a retry or sleep, skip a test, or normalize a comparison to make a failure go away. Retries and timeouts are for faults outside our control, and they must be surfaced, not hidden.

Review provider/model literal lint-guard findings under `AGENTS.md`. Each allowlisted exception must cite the Pi source that makes it provider-specific. If the guard is unavailable, perform the same review manually and report that limit. Lint cannot replace provider-shape evidence.

Refuse to report a port complete if it introduces a provider-specific branch without a Pi citation or verifies a shared path with a single provider. Record missing citations and missing provider-shape evidence as blockers. Explain an inapplicable shape from the upstream contract, not unavailable credentials or fixtures. This refusal applies to review and verification tasks as well as source-changing tasks.

## Preflight

1. Find the nearest ancestor that contains `AGENTS.md` and
   `coding/upstream.go`. Use it as the repository root.
2. Read `AGENTS.md` and `docs/typescript-to-go-porting.md`.
3. Confirm that `coding.UpstreamVersion`, `.upstream/current`, and the locked Pi
   executable identify the same release.
4. Run the relevant Make target. Do not replace a maintained target with a
   bespoke command.
5. Identify the upstream files, semantic interface IDs, async contracts,
   production PiG paths, parity scenarios, and divergences in scope.

For a family task, run:

```bash
make family-gaps FAMILY=<family>
go run ./test/parity/cmd/behaviorcheck -family <family>
```

Generated ledgers identify review obligations. They do not prove behavior.

## Family workflow

1. Read the complete upstream implementation, callers, tests, documentation,
   public declarations, and relevant asynchronous behavior.
2. List observable states and boundaries. Include omitted, undefined, and null
   state; ordering; string and terminal widths; errors; cancellation; empty and
   singleton states; first and last transitions; and persistence or wire effects.
3. Probe the exact upstream executable first. Record the command, arguments,
   environment, inputs, output, exit status, ordering, and side effects that form
   the contract.
4. Add or identify a PiG test or parity scenario derived from that contract.
   Observe the failure or prove the test with a deliberate mutation when PiG
   already matches.
5. Apply the smallest source fix that preserves Pi behavior. Stop for approval
   before adding a divergence, changing a public wire or API, or classifying a
   surface as deferred or designed out.
6. Re-probe the production path and affected cross-family callers. Use the
   strongest stable comparator. Do not normalize real drift.
7. Run the smallest relevant tests first. Use `make parity-family
   FAMILY=<family>` for family acceptance. Use `go test -count=3` when the
   change affects concurrency, process ownership, Session ownership, or the
   parity harness.
8. Update generated inventories and reviewed mappings only from evidence
   produced by this workflow.
9. Review the complete diff against upstream and the requested scope.

## Upstream upgrade workflow

An `upgrade <tag>` invocation first produces a read-only plan.

1. Require an explicit release tag. Do not select `latest` silently.
2. Require a clean, passing source baseline before changing the pin.
3. Resolve the target tag to an exact upstream commit.
4. Compare every tracked upstream package between the current and target
   releases. Group work by observable behavior and owner family, not by file
   count.
5. Identify files changed again later in a multi-release range. Prefer the final
   surviving shape unless an intermediate observable is required.
6. Report changed files, affected families, required fixtures, public API
   changes, likely divergences, and the proposed verification order.
7. Stop and request approval before changing source or generated evidence.

After approval:

1. Run `make pin-bump PI_UPSTREAM_REF=<tag|commit>`, which writes `.upstream/pin` and regenerates `internal/coding/pigversion/pigversion.go` and the exact Pi dependency in
   `extensions/sdk-ts`.
2. Run `make upstream-mirror`, `make model-catalogs`, and
   `make interface-proposals`.
3. Process each affected family through the family workflow.
4. Run `make verify`, `make test-stress`, and `make parity-stress`.
5. Leave the working tree for human review. Do not stage, commit, switch
   branches, push, or open a pull request.

## Evidence rules

- [ ] Check [Faithful, general implementations](#faithful-general-implementations) against the branch citations and provider-shape assertions.
- Prefer hermetic unit proof, then local protocol proof, then terminal parity.
  Use live credentials only when the contract requires an external provider.
- Registration, startup, and declaration presence do not prove dispatch,
  payload conversion, cancellation, persistence, selection, or rendering.
- Missing fixtures and runner stimuli are implementation work, not a reason to
  defer a contract.
- Treat old divergences, TODOs, phase labels, compatibility paths, and test-only
  public surfaces as unverified until current evidence supports them.
- Do not edit `.upstream/current` directly.
- Do not hand-edit generated inventories.
- Do not claim coverage for unobserved behavior.
- Do not run blocking extension IPC or unbounded work on the TUI loop.
- Own every goroutine, process, file descriptor, temporary directory, and tmux
  session. Bound cancellation and shutdown.

## Parallel campaign boundary

Only `inventory` and `verify` may run concurrently. Use:

```bash
make porter-campaign MODE=<inventory|verify> FAMILIES="<family> ..."
```

Campaign workers use immutable disposable snapshots. Their reports are
proposals. A coordinator must re-probe every accepted finding in the canonical
workspace.

Do not run `family`, `tighten`, or `upgrade` concurrently. Do not modify source,
tests, evidence, generated inventories, or Git state from a campaign worker.
Never inspect, signal, or remove another worker's processes, tmux sessions,
temporary files, homes, snapshots, or reports.

## Session context at scale

Sessions reach hundreds of megabytes and tens of thousands of entries, and the host waits on `session_start` and per-turn handlers before the UI moves on. Treat the session as large by default when porting any extension that reads session data.

Prefer the context branch (what the model will see, trimmed at the last compaction) over the full history. Read the whole history only when the feature truly needs it, and never do so inside `session_start` or a per-turn handler. Defer such work to a background goroutine or task and paint from retained state first.

Compute incrementally: keep running totals and update them from each new entry instead of rescanning the branch. Stream or peek (read only the fields you need, skip entries you do not care about) rather than materializing every entry. Before shipping, measure the handler on a large synthetic session (hundreds of MB, tens of thousands of entries, many compactions) and check both latency and peak memory.

## Completion report

Report:

- mode, family or target tag, pinned Pi version, and target commit;
- upstream behavior observed;
- failing evidence and source fix;
- tests and scenarios added or tightened;
- [ ] shared-path Pi citations, provider-shape results, and contract-based reasons for inapplicable shapes;
- mapping and divergence changes;
- exact commands run and their results;
- remaining blockers.

Include the loop self-report required by `AGENTS.md` for behavior-changing work.
