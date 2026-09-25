<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Repository quality standard and initial-publication audit

## Purpose

This document defines the editorial, code-comment, structure, and maintenance
quality required for PiG's initial public repository. Use it when reviewing any
source, documentation, test, workflow, generated input, or maintainer tool.

The review protects contributor time. A technically correct project can still
lose trust when its repository contains fluent but ungrounded prose, obsolete
plans, duplicate authorities, decorative comments, or automation that hides
failures. Reviewers must be able to distinguish current contracts from history
without reconstructing the project from Git archaeology.

This standard applies to human-written and machine-assisted changes. It does not
assume that text is poor because a machine produced it. It rejects text and code
that do not carry useful, verifiable meaning.

## Release context

The project owner reports the following human decisions as complete. None of
them is recorded in a public, citable place yet, so public text must not cite
them (see "Public project writing" in `AGENTS.md`):

- HPE approved the project as a community open-source project.
- HPE approved the MIT license and copyright form.
- The applicable patent review is complete.
- The project uses Developer Certificate of Origin 1.1 sign-off.

The following external release actions remain separate gates:

- make the repository public;
- upload the final source-bound software bill of materials to VTN+;
- initiate and record the STROSS review;
- record final release authorization; and
- publish release artifacts.

Local scans and repository checks prepare evidence for those gates. They do not
replace the required HPE records. VTN+ and STROSS are blocked until the public
repository exists.

## Audit scope

Inspect all authored text that a contributor, user, reviewer, scanner, or coding
agent can encounter:

- Markdown documentation;
- Go comments and package documentation;
- shell, Python, JavaScript, TypeScript, YAML, and Makefile comments;
- command help, errors, warnings, and status messages;
- website and terminal user-interface text;
- extension and JSON schema descriptions;
- Piglet descriptions and Skills;
- test names and test comments;
- maintainer prompts;
- issue and pull request templates; and
- checked-in fixtures that contain authored prose.

Classify these files before changing their text:

- generated inventories and reports;
- exact upstream quotations;
- expected-output fixtures and golden files;
- protocol literals;
- third-party or legal notices;
- vendored files; and
- terminal snapshots.

Do not rewrite byte-exact evidence for style. Fix the generator when generated
text is wrong.

## Writing standard

Write precise, direct technical English.

Apply these rules:

1. State one instruction or main proposition per sentence.
2. Use active voice when the actor matters.
3. Use present tense for current behavior.
4. Use `must` for a requirement, `should` for a recommendation, and `may` for
   permission.
5. Do not use `can` when you mean permission.
6. Use one term for one concept. Do not vary names for style.
7. Define an acronym at its first use unless it is a code identifier.
8. Keep code identifiers, command names, field names, and protocol values exact.
9. Put the condition before the action when the condition limits the action.
10. Prefer a short complete sentence to a dense noun phrase.
11. Keep necessary technical detail. Remove ceremonial explanation.
12. State limits, ownership, failure behavior, and evidence explicitly.

A short sentence is not automatically clear. Preserve the grammar and technical
precision needed to prevent ambiguity.

## Em-dash policy

Authored PiG prose and comments use no em dashes.

Replace an em dash with:

- a period for a separate statement;
- a colon before a definition or list;
- parentheses for a short qualification;
- a comma when the clauses remain grammatically correct; or
- a hyphen inside a compound word.

An em dash may remain only in:

- exact upstream text;
- generated evidence;
- third-party or legal text;
- protocol fixtures; or
- byte-exact terminal and output fixtures.

Every remaining occurrence needs one of those classifications. Do not run a
blind replacement over generated files or test oracles.

## Prohibited writing patterns

Remove these patterns when they add no technical information.

### Empty openings

Examples include:

- `It is important to note that`;
- `It is worth mentioning that`;
- `At its core`;
- `This document aims to`;
- `Let us explore`; and
- `As we can see`.

Start with the fact or instruction.

### Unsupported praise

Do not use claims such as:

- world-class;
- enterprise-ready;
- seamless;
- robust;
- powerful;
- comprehensive;
- cutting-edge;
- best-in-class;
- revolutionary;
- sophisticated;
- future-proof;
- battle-tested; or
- complete compatibility.

Replace praise with a bounded behavior, measured result, or linked evidence.

### Hollow contrast

Remove constructions such as:

- `not only X, but also Y`;
- `more than just X`;
- `not merely X`;
- `This is not about X. It is about Y.`; and
- `X is not a feature. It is a philosophy.`

State the required point directly.

### Repeated assurance

Review repeated statements that begin with:

- `This ensures`;
- `This guarantees`;
- `This provides confidence`;
- `This makes it easy`; and
- `This allows users to`.

State the mechanism and observable result. Do not infer confidence from the
existence of a check.

### Vague qualification

Remove or define words such as:

- generally;
- typically;
- usually;
- potentially;
- where possible;
- as appropriate;
- in many cases; and
- in most situations.

Keep a qualifier only when it changes the contract.

### Repetition and artificial rhythm

Remove:

- conclusions that repeat the introduction;
- repeated three-item slogans;
- repeated sentence openings;
- dramatic sentence fragments;
- decorative metaphors;
- unnecessary rhetorical questions;
- excessive bold emphasis;
- promotional headings; and
- repeated use of `clear`, `simple`, `easy`, or `obvious`.

Do not personify software. Describe what the code reads, writes, validates,
selects, or rejects.

## Code-comment standard

Comments are exceptional. Prefer code that states its own intent through:

- precise names;
- concrete types;
- small functions;
- guard clauses;
- explicit ownership;
- typed state;
- named constants;
- direct error propagation; and
- tests derived from an external contract.

Before retaining a comment, ask whether a better name, type, function boundary,
or control-flow change can express the same information. Refactor the code when
that change improves the code independently of comment removal.

Do not create an abstraction only to remove a comment. A short comment is better
than a generic helper, hidden control flow, or a misleading type hierarchy.
Comment rarity is an outcome, not a numeric target.

### Keep a comment when it records

- an externally visible contract;
- a non-obvious invariant;
- cancellation, concurrency, or resource ownership;
- a security boundary;
- an upstream correspondence that affects implementation;
- why an apparently simpler implementation is incorrect;
- a deliberate and measured tradeoff; or
- a numbered divergence.

### Remove or rewrite a comment when it

- repeats the following code;
- narrates a loop, branch, assignment, getter, or setter;
- uses the file name as a decorative heading;
- records implementation history;
- names an old phase, milestone, or workstream;
- refers to a deleted plan or issue;
- promises that a feature will land later;
- describes a temporary bridge without a current contract;
- says `this test proves` when the assertion already states the result;
- excuses an incomplete implementation;
- preserves dead code for possible future use; or
- uses banner characters only to divide a file.

Remove labels such as:

```text
Phase 2
ε.4
Ω.2
β.12
kaizen
future row
when this lands
scheduled to be replaced
newly wired
for future use
```

Use Git history or a ratified design record for history.

### Test comments

Keep a test comment only when it identifies:

- the external or upstream contract;
- a surprising regression input;
- an ordering or concurrency condition that assertions do not reveal;
- why a mutation must fail; or
- fixture provenance.

A test name should state behavior. A test body should make its setup and oracle
clear without a narrative transcript.

### Test value

Bind every test to a contract outside the implementation: an upstream Pi
behavior, an API or wire shape, a user flow, or a stated requirement. A test
shaped from the code as written certifies that code, including its defects, and
charges suite time on every run. Prefer a black-box parity scenario over a unit
test that restates the port. Keep a unit test when it isolates a boundary or an
edge case that a parity scenario reaches only with heavy setup.

Remove a test when it:

- asserts a literal that the production code already fixes;
- cannot fail after you change the code it covers;
- duplicates a parity scenario that already proves the same behavior; or
- pins a private implementation detail that has no external contract.

Do not remove a test to make a check pass. Before you remove a test, show where
the behavior stays proven: a parity scenario, a stronger test, or the fact that
the removed test could not fail. Treat coverage as a floor that flags unexercised
code, not a target to raise.

## Documentation quality

Give each document one primary purpose:

- tutorial;
- how-to guide;
- reference; or
- explanation.

Do not mix a long tutorial into a reference page. Do not put design opinion into
a command table. Do not make a how-to guide teach unrelated fundamentals.

Review every document for:

- stale paths;
- removed commands;
- old package names;
- incorrect source-of-truth claims;
- unsupported platform claims;
- unavailable releases or downloads;
- conflicting protocol-version statements;
- completed work marked as planned;
- private product paths;
- duplicate facts;
- speculative roadmap content; and
- claims of endorsement, certification, sponsorship, or release status.

Public user documentation lives in `docs/site/docs/`; the website renders it
from there. The concise offline guide
lives in `internal/pigdocs/content/`. Do not maintain identical long passages in
both locations. Generate shared facts or keep the offline guide deliberately
shorter.

## Code and public API quality

Every exported symbol must be one of:

- a supported public API;
- an explicitly unstable API;
- a required Pi compatibility surface; or
- a value used by another maintained module.

Review exported types for:

- `any` aliases that conceal a known contract;
- placeholders and future-facing names;
- duplicate public and internal representations;
- stale aliases;
- unnecessary interfaces;
- constructors that return an interface without a substitution boundary;
- missing ownership and error semantics; and
- comments that promise a future type.

Do not tighten an opaque type without tracing upstream declarations, production
callers, SDKs, wire serialization, and conformance tests. A change from `any` to
a concrete type can break source compatibility.

## Automation standard

The Makefile is the maintained command interface. Documentation and contributor
instructions use Make targets instead of direct script paths when a target
exists.

A script remains only when it has one current purpose and moving its body into
the Makefile would reduce readability. Such a script is an implementation
detail behind a Make target.

Reject automation that:

- ignores a test, parity, lint, or vulnerability failure;
- uses broad process matching;
- kills tmux servers or sessions it does not own;
- uses `git reset --hard`, `git clean`, `git stash`, or destructive checkout;
- creates branches, commits, pushes, or pull requests without direct approval;
- hardcodes a model for a general workflow;
- writes nondeterministic data into a claimed deterministic artifact;
- keeps one-time migration code after migration; or
- duplicates another maintained path.

Every maintained nested Go module must pass with `GOWORK=off`. Fixture modules
must also build in clean-clone verification. The root module requires each
nested PiG module it imports at the release version and carries no `replace`
directive, so `go install github.com/MichaelKinsy/PiG/cmd/pig@vX.Y.Z` works; a
checkout builds it through `go.work`, and `tests/gomodule` keeps its `go.sum`
pinned to the published nested module hashes.

## Pig Porter standard

Pig Porter has one procedure authority:

```text
piglets/porter/skills/pig-porter/SKILL.md
```

Its maintained entry points are:

```bash
make porter
make porter-task TASK="inventory <family>"
make porter-campaign MODE=verify FAMILIES="<family> ..."
make porter-check
make porter-smoke
```

The extension adapts typed requests. Deterministic Go packages calculate facts
and evidence. The Skill defines the agent procedure. Campaign workers remain
read-only. Source-changing work remains serialized. A maintainer owns every Git
operation.

## Setup Skill

Provide one project setup Skill at:

```text
.agents/skills/setup-pig/SKILL.md
```

The Skill must first identify the requested task:

1. run an existing PiG binary;
2. build PiG;
3. author an extension;
4. build a Piglet Binary;
5. run the complete parity suite; or
6. build the documentation site.

It then checks only the required tools. It prefers an existing version manager
and asks before installing or replacing software. It points to the canonical
development guide and Make target. It does not duplicate an installer.

## Repository layout

Keep the established Go package roots:

```text
agent/
ai/
coding/
tui/
cmd/
internal/
```

Do not introduce `pkg/`, a TypeScript-style `packages/` tree, or decorative
enterprise layers. Move a package only when ownership, dependency direction, or
a supported API requires the move.

Keep project governance and community files at the repository root for the
initial publication. They are required by the approved repository runbook and
must remain easy to find.

Group maintainer documentation only when the move improves navigation. Perform
documentation moves separately from behavior changes.

## Badges and reports

Keep the Core CI badge and generated parity-coverage badge. Do not add badges
for a service, release, certification, score, or compliance state that does not
exist.

Keep stable, reviewable source evidence in the repository. Publish volatile
scanner, timing, benchmark, and vulnerability output as workflow summaries or
artifacts. Do not commit continuously changing reports.

## Parallel tmux work

A coordinator may divide a large audit into non-overlapping tmux sessions.
Partition by ownership, not by arbitrary file count. Suitable areas include:

- public and offline documentation;
- production code comments and public APIs;
- tests and fixtures;
- Makefile and maintainer automation;
- website copy; and
- parity ledgers and generated-evidence inputs.

Before starting a session, record:

- the exact files or directories it owns;
- whether it is read-only or may edit;
- the acceptance checks it must run;
- files it must not touch; and
- the coordinator session that receives its report.

Use a unique tmux session name. Never use `tmux kill-server`. A worker may stop
only the session and processes that it created.

Workers must not edit overlapping files. When a worker finds a required change
outside its assignment, it must stop before editing that file and report the
cross-area impact to the coordinator.

Each worker reports:

```text
Scope:
Files read:
Files changed:
Contracts checked:
Commands run and results:
Cross-area impacts:
Unresolved findings:
```

The coordinator reviews every diff, resolves cross-area impacts, and runs the
shared gates. Worker reports are findings, not merge evidence. Workers do not
stage, commit, push, rebase, reset, clean, or change repository visibility.

## Initial-publication cleanup map

### Keep

```text
agent/
ai/
coding/
tui/
cmd/
internal/
extensions/
piglets/
parity/
tests/
examples/
automation/
```

### Maintain through Make

```text
automation/porter/run-pig-porter.sh
automation/porter/run-pig-porter-campaign.sh
automation/porter/verify-pig-porter-local.sh
automation/gen/mirror-upstream.sh
automation/gen/generate-model-catalogs.sh
automation/dev/stage-clipboard-png.sh
```

These scripts remain implementation details or focused manual fixture helpers.
Document each from its owning Make target or parity guide.

### Remove obsolete automation

```text
scripts/pig-upgrade.sh
scripts/pig-sync-upstream.sh
scripts/pig-self-upgrade.sh
scripts/pig-steer.sh
scripts/build-port-spec.sh
scripts/migrate-scenarios-to-tokens.sh
scripts/dogfood/
scripts/dogfood-prompts/
scripts/ci/stages/pig-examples-validate.sh
scripts/generate-interface-proposals.sh
scripts/validate-interface-proposal.sh
scripts/parity-coverage.sh
```

### Correct known stale material

```text
parity/README.md
tests/upstream-parity/README.md
PROVENANCE.md
docs/extension-authoring.md
docs/additive-features.md
PORT_MAP.md
internal/pigdocs/content/concepts.md
internal/pigdocs/content/install.md
coding/services.go
coding/extension/opaque_types.go
internal/codingagent/output_guard.go
internal/codingagent/interactive_turn.go
internal/codingagent/interactive_commands.go
internal/codingagent/event_bridge.go
tui/keybinding_hints.go
ai/bedrock.go
```

### Review before public API commitment

```text
coding/extension/opaque_types.go
coding/extension/host/
coding/packagecontent/
coding/secretresolver/
coding/source/
tui/parity/
tui/termsim/
tui/widthx/
```

## Completion criteria

The initial-publication cleanup is complete when:

1. obsolete automation is absent;
2. Make exposes every maintained workflow;
3. Pig Porter has one procedure authority;
4. every maintained module passes independently;
5. authored prose contains no unclassified em dash;
6. production comments contain no phase or roadmap residue;
7. stale paths and contradictory claims are absent;
8. public opaque types are resolved or explicitly accepted as release blockers;
9. documentation links and drift checks pass;
10. the website builds without unexpected remote requests;
11. the frozen source tree passes the required local security and provenance
    checks; and
12. a maintainer reviews the complete diff before any commit or push.
