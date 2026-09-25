<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Contributing to PiG

Read [`AGENTS.md`](AGENTS.md), [`docs/project/CONTEXT.md`](docs/project/CONTEXT.md), and the
[repository quality standard](docs/project/repository-quality.md) before
changing source.

## Contribution principles

- Preserve observable Pi behavior unless an approved numbered divergence applies.
- Keep Stock PiG product-neutral.
- Put product behavior in an extension or explicitly selected Piglet.
- Add the smallest change that satisfies the stated contract.
- Derive tests from upstream behavior or another documented requirement.
- Do not weaken parity checks, comparators, test counts, or security gates.
- Update licensing and provenance records when source or generated data changes.

## Developer Certificate of Origin

This project uses the [Developer Certificate of Origin 1.1](https://developercertificate.org/).

Add a `Signed-off-by` line to each commit:

```bash
git commit --signoff
```

The sign-off certifies that you have the right to submit the contribution under the project license. It is not a copyright assignment or a cryptographic signature.

PiG does not require a Contributor License Agreement.

## Agent-assisted contributions

Agent assistance is allowed. The contributor remains responsible for every
claim, change, test, and dependency in the submission.

Before submitting agent-assisted work:

- read and understand the changed code;
- reproduce the reported problem yourself;
- confirm that the tests can detect the behavior they claim;
- remove generated speculation, unrelated changes, and private data; and
- be prepared to explain the design and its interaction with the rest of PiG.

Do not submit unattended, high-volume, or unreviewed generated issues or pull
requests. Maintainers review technical evidence, not the apparent fluency of the
submission.

## Development workflow

If you need help preparing a development host, load the repository setup Skill:

```bash
pig --skill ./.agents/skills/setup-pig
```

The Skill checks only the tools required for the selected task and asks before
installing software.

1. Search existing issues and pull requests.
2. Open or link an issue when the change needs design agreement or coordination.
3. Use a focused topic branch.
4. Read the pinned upstream source and tests before changing parity-bound behavior.
5. Add or identify a test that can fail on the behavior under review.
6. Implement the smallest correct change.
7. Run the relevant local tests early.
8. Run `make check` before requesting review.
9. Sign off every commit.
10. Describe the problem, change, verification, and any divergence in the pull request.

A documentation-only or mechanical change can explain why it does not need an issue.

## Pull request requirements

A pull request must:

- have a clear and bounded purpose;
- pass required checks;
- contain no credentials or private data;
- include tests or explain why the existing tests prove the change;
- update `DIVERGENCES.md` for an approved observable difference;
- update extension conformance coverage for an SDK contract change;
- update SBOM, notice, or provenance inputs when dependencies or copied material change; and
- resolve review findings before merge.

Do not edit generated files by hand. Use the repository generator named by the file or gate.

## Upstream collaboration

Pi is the reference implementation. Keep PiG-specific discussion respectful and factual. Do not imply endorsement by Pi or its maintainers. A contribution to Pi follows Pi's own contribution policy and uses a separately approved contribution route.

## Conduct and security

Follow [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md). Report vulnerabilities through the private route in [`SECURITY.md`](SECURITY.md).
