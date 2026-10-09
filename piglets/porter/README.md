<!--
SPDX-License-Identifier: MIT
-->

# Pig Porter

Pig Porter is the repository workbench for maintaining behavioral parity with
the Pi release pinned in `internal/coding/pigversion/pigversion.go`.

It combines:

- the deterministic contracts under `test/parity/closure`,
  `test/parity/correspondence`, and `test/parity/porter`;
- the `pig_porter` extension in `extensions/pig-porter`;
- the procedure in `skills/pig-porter/SKILL.md`;
- one interactive launcher and one read-only campaign launcher.

Follow [Faithful, general implementations](skills/pig-porter/SKILL.md#faithful-general-implementations). Porter must refuse completion for uncited provider-specific branches or single-provider proof of shared paths.

Use Make targets from the repository root:

```bash
make porter
make porter PORTER_ARGS=--continue
make porter-task TASK="inventory project-trust"
make porter-campaign MODE=verify FAMILIES="project-trust rpc"
make porter-check
make porter-smoke
```

`porter-campaign` accepts only `inventory` and `verify`. It runs against
read-only disposable snapshots. Porting and proof-tightening tasks stay
serialized in the canonical working tree.

Pig Porter never owns Git commits, pushes, pull requests, tags, releases, or
repository visibility. A maintainer performs those actions after reviewing the
working tree and verification evidence.
