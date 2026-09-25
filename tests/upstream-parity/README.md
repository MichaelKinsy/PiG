<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Upstream parity tripwires

This package detects extension API and runtime-contract drift against the exact
Pi release pinned in `coding/pigversion/pigversion.go` and materialized under
`.upstream/current`.

These tests complement the compiler-derived inventories under
`parity/interfaces/`. They are narrow regression tripwires. They do not prove
that a declaration has a production caller or behavioral parity scenario.

## Contents

| Area | Purpose |
|---|---|
| parser and registry tests | compare extension events, methods, result types, and JSON fields |
| dispatch and context tests | check host dispatch and context surface correspondence |
| mirror tests | bind the local mirror to the pinned release and commit |
| divergence tests | validate divergence records and call-site markers |
| layout tests | protect intentional public package boundaries |
| script tests | verify maintained scheduling and policy commands |

## Run the tests

```bash
go test ./tests/upstream-parity -count=1
```

`make test` and `make check` include this package.

## Respond to drift

1. Read the changed declaration and its callers under `.upstream/current`.
2. Probe observable Pi behavior before changing PiG.
3. Port the behavior and add caller-level evidence.
4. Add a divergence only when the observable difference is intentional and
   approved under `AGENTS.md`.
5. Regenerate the affected inventories through their Make targets.
6. Run the focused tests, the owning parity family, and `make check`.

If the parser smoke test fails after an upstream change, update its fixture and
parser only after confirming the new TypeScript syntax. The TypeScript compiler
inventory remains the completeness authority.
