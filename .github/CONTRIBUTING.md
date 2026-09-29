<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Contributing to PiG

Read [`AGENTS.md`](AGENTS.md), [`docs/project/CONTEXT.md`](../docs/project/CONTEXT.md), and the
[repository quality standard](../docs/project/repository-quality.md) before
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

## Signed commits

Every commit requires a signature that GitHub marks **Verified**. DCO sign-off is separate and is also required. A `Signed-off-by` line does not satisfy the signature requirement.

To sign with an existing SSH key, configure Git in your checkout:

```bash
git config gpg.format ssh
git config user.signingkey /path/to/key.pub
git config commit.gpgsign true
```

Add the public key to GitHub under **Settings → SSH and GPG keys → New SSH key**. Select **Signing Key** as the key type, even if you already added the same key for authentication. See [GitHub's SSH signing setup](https://docs.github.com/en/authentication/managing-commit-signature-verification/telling-git-about-your-signing-key#telling-git-about-your-ssh-key).

Create commits with `git commit --signoff`. Git signs them automatically with the configuration above. Confirm that GitHub shows **Verified** after you push.

To sign an existing unsigned tip commit without changing its message:

```bash
git commit --amend -S --no-edit
```

To sign every commit on a linear topic branch (one with no merge commits), replace `<base>` with its base commit:

```bash
git rebase --exec 'git commit --amend -S --no-edit' <base>
```

A default rebase drops merge commits, including any conflict resolution they contain, so rebase onto the current `main` first to make the branch linear. These commands rewrite commit IDs. Coordinate with a maintainer before replacing commits already pushed. Keep each DCO sign-off in the commit message.

If you cannot set up signing, say so in the pull request. A maintainer can land your change in a maintainer-signed commit that credits you with a `Co-authored-by:` trailer, which GitHub shows as Verified. Re-signing a commit that keeps you as its author is not enough if your account uses vigilant mode, because GitHub then marks it only Partially verified.

## Agent-assisted contributions

Agent assistance is allowed. The contributor remains responsible for every
claim, change, test, and dependency in the submission.

Before submitting agent-assisted work:

- read and understand the changed code;
- reproduce the reported problem yourself;
- confirm that the tests can detect the behavior they claim;
- check agent output against [Faithful, general implementations](AGENTS.md#faithful-general-implementations);
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
6. Implement the smallest correct change through Pi's shared data-driven paths, without uncited provider/model special cases.
7. Run the relevant local tests early across applicable provider shapes, not only Copilot or `test-faux`.
8. If you change exported Go API, CLI flags, settings, parity scenarios, or docs mirrors, run `make generate` and commit the result. CI's drift gates compare these files.
9. Run `make check` before requesting review.
10. Sign off and cryptographically sign every commit. Confirm that GitHub marks each signature **Verified**.
11. Describe the problem, change, verification, and any divergence in the pull request.

A documentation-only or mechanical change can explain why it does not need an issue.

Maintainers squash-merge pull requests, which keeps you as the commit author, so you appear on GitHub's contributors page. When a maintainer lands your change inside a larger commit, the commit carries a `Co-authored-by:` trailer for you.

## Pull request requirements

A pull request must:

- have a clear and bounded purpose;
- pass required checks;
- include both a DCO `Signed-off-by` line and a GitHub-verified signature on every commit;
- contain no credentials or private data;
- include tests or explain why the existing tests prove the change;
- update `docs/parity/DIVERGENCES.md` for an approved observable difference;
- update extension conformance coverage for an SDK contract change;
- update SBOM, notice, or provenance inputs when dependencies or copied material change; and
- resolve review findings before merge.

Do not edit generated files by hand. Use the repository generator named by the file or gate.

## Upstream collaboration

Pi is the reference implementation. Keep PiG-specific discussion respectful and factual. Do not imply endorsement by Pi or its maintainers. A contribution to Pi follows Pi's own contribution policy and uses a separately approved contribution route.

## Conduct and security

Follow [`.github/CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md). Report vulnerabilities through the private route in [`.github/SECURITY.md`](SECURITY.md).
