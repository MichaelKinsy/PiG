# PiG Piglets

`standard/` contains the explicit PiG Standard Piglet and its selected product
Resources. Bare Stock PiG does not import or activate them.

`porter/pig-porter.yaml` is the authored Piglet for local Pi-to-PiG parity
work. It is a focused workbench for interactive family loops and bounded
headless tasks.

## Interactive use

Build the current Pig binary, then start the Piglet from the same project cwd
you want Pig to use. From this repository root:

```bash
make porter
```

The Piglet selects a thin `pig-porter` extension that calls the same Go
closure engine as the direct process adapter. The extension exposes the
`pig_porter` tool and `/pig-porter` command. The procedure lives in
`porter/skills/pig-porter/SKILL.md`.

```text
/skill:pig-porter inventory project-trust
```

A Session transcript remains Piglet-independent. To continue an existing
conversation under the porter Piglet, exit Pig and resume it through the same
runner:

```bash
# Run this from the same cwd where the conversation was created.
make porter PORTER_ARGS=--continue
# or select a transcript explicitly:
make porter PORTER_ARGS=--resume
```

Then invoke `/skill:pig-porter` with the bounded family or audit task.

## Headless use

Use the same runner and skill expansion rather than a second automation path:

```bash
make porter-task TASK="verify model"
```

The deterministic engine contract is also callable without a session:

```bash
printf '%s\n' '{"operation":"status","root":".","database":"tmp/closure/graph.db"}' \
  | go run ./parity/cmd/porter
```

The same contract covers correspondence inventory and planning, read-only work
packets and prompts, validated bundle submission, evidence-request and
mutation-request inspection, mutation readiness, and graph-derived report projections. `inventory`, `plan`, and
`work-packet` require the exact `upstreamVersion` and `targetCommit`;
`report` projects the imported `port-map`, `coverage-row`, `semantic-mapping`,
`semantic-delta`, `behavior-input-mapping`, `async-contract`, `upstream-sync`,
`behavior-contract`, and `format-version` datasets. It also derives
`family-coverage`, `scenario-quality`, `divergence`, and `foundation`
dashboards. Every row exposes separate
proven/waived/provisional/open/contradicted fields. `submit-bundle` accepts
repository-local `packetPath` and `bundlePath` values and publishes only
content-addressed submissions.

Mutation-required tests use `mutant`, `mutation-request`, executor-imported
`mutation-run`, and loader-derived `mutation-attestation` records.
`execute-mutation` first proves the bound test passes, then applies one exact
source edit in a disposable checkout of the clean candidate and records the
structured failing test plus its patch and streams. `mutation-readiness`
reports missing-request, missing-run, stale-run, surviving-run, or ready state
without reading temporary parity output.

The extension is a standalone Go module so Pig can build it with `GOWORK=off`.
Its relative replacements intentionally bind this repository's parity engine;
the extension is not a separately portable Package. Run the Piglet from this
source tree or prebuild its Go cell in a Piglet Binary for direct handoff.

Headless tasks must name one mode and scope. Supported modes are:

- `inventory <family>`: refresh the family frontier without changing behavior;
- `family <family>`: probe, red-test, port, review, and close one behavior family;
- `tighten <family>`: strengthen existing proof without optimistic breadth;
- `verify <family>`: run the family acceptance ladder and report blockers; and
- `upgrade <tag>`: produce a read-only upstream upgrade plan, then wait for
  approval before changing the pinned source.

The porter procedure in `porter/skills/pig-porter/SKILL.md` is the single
workflow authority.

## Local acceptance

```bash
make porter-smoke
PIG_PORTER_HEADLESS_TASK="inventory model" make porter-smoke
```

Local acceptance validates Piglet loading, the file-backed Skill, the exact Pi
oracle and mirror, deterministic inventory and mapping checks, mapping
isolation, extension loading, and headless Skill activation through the faux
provider.
