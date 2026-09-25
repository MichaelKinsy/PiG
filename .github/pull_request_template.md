<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

Tracking: <issue URL or “No issue: <reason>”>

## Problem and outcome

Describe the user-visible problem and the required result. State what remains unchanged.

## Upstream and divergence

- Pinned upstream Pi version reviewed:
- Upstream source files or behavior probes reviewed:
- Observable contract exercised:
- PiG behavior:
  - [ ] Matches upstream Pi.
  - [ ] Uses an existing numbered divergence: `D<number>`.
  - [ ] Introduces a reviewed numbered divergence: `D<number>`.
  - [ ] Is an additive feature outside parity-bound core.
  - [ ] Not applicable. Explain why:

Do not use a weaker parity comparator, skip, normalization, or coverage claim to hide a behavior difference.

## Changes

List the concrete source, contract, documentation, dependency, and generated-file changes.

## Verification

- Regression or contract test that fails without the change:
- Invalid, empty, boundary, and error cases exercised:
- Relevant parity scenarios and comparator strength:
- Commands and observable results:

Complete each applicable gate or explain why it does not apply:

- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `make lint`
- [ ] `make lint-scenarios`
- [ ] `reuse lint`
- [ ] `make check`
- [ ] Relevant declared-run parity scenarios
- [ ] Cross-platform build or native test
- [ ] Documentation and generated-file drift checks

## Security, licensing, and provenance

- [ ] No credential, customer data, private configuration, or internal-only endpoint is included.
- [ ] New or changed dependencies have reviewed licenses and vulnerability results.
- [ ] Copied, ported, generated, or vendored material has its source and license recorded.
- [ ] SPDX declarations, `NOTICE`, third-party notices, and SBOM inputs are updated where required.
- [ ] Workflow permissions remain least-privilege, and untrusted code cannot access release credentials.

List any dependency, license, provenance, secret-scan, SBOM, or supply-chain impact:

## Release and compatibility impact

State any effect on public APIs, persisted formats, supported platforms, installation, updates, release artifacts, changelog, or documentation. State “None” only after checking these surfaces.

## Risk and rollback

Describe what can fail and how to return to the previous state without losing user data or release evidence.

## Contributor declaration

- [ ] I understand and reviewed any agent-assisted material in this submission.
- [ ] Every non-merge commit includes a Developer Certificate of Origin `Signed-off-by` line.
- [ ] This contribution is submitted under the repository license and preserves third-party terms.
