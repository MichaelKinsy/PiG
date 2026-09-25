---
name: pig-porter
description: Run one bounded upstream-first Pi-to-PiG porting or verification task.
---

<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Pig Porter

Work against the exact Pi release in `coding/pigversion/pigversion.go` (the
pin `coding/upstream.go` re-exports). Use one mode and one bounded scope:

- `inventory <family>` inspects the current frontier without changing behavior.
- `family <family>` ports one behavior family.
- `tighten <family>` strengthens existing evidence and fixes drift it exposes.
- `verify <family>` runs the family acceptance ladder without changing behavior.
- `upgrade <tag>` plans an upstream-version change. It remains read-only until
  the user approves the plan.

Reject a missing mode, family, or upgrade tag. Combine families only when the
upstream contract and production call path require it. State the coupling before
editing.

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
go run ./parity/cmd/behaviorcheck -family <family>
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

1. Update `coding/pigversion/pigversion.go` and the exact Pi dependency in
   `extensions/sdk-ts`.
2. Run `make upstream-mirror`, `make model-catalogs`, and
   `make interface-proposals`.
3. Process each affected family through the family workflow.
4. Run `make verify`, `make test-stress`, and `make parity-stress`.
5. Leave the working tree for human review. Do not stage, commit, switch
   branches, push, or open a pull request.

## Evidence rules

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
- Do not weaken tests, add skips, hide failures with retries or normalization,
  or claim coverage for unobserved behavior.
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

## Completion report

Report:

- mode, family or target tag, pinned Pi version, and target commit;
- upstream behavior observed;
- failing evidence and source fix;
- tests and scenarios added or tightened;
- mapping and divergence changes;
- exact commands run and their results;
- remaining blockers.

Include the loop self-report required by `AGENTS.md` for behavior-changing work.
