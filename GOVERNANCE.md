<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Governance

PiG is an independent open-source project. This document defines project decisions and maintainer responsibilities.

## Project philosophy

PiG exists to extend the Pi ecosystem, not to fragment it. Pi is the reference implementation.

PiG aims to:

- preserve observable compatibility with the pinned Pi release;
- minimize unnecessary divergence;
- follow Pi's design principles where they apply to Go;
- document every intentional user-visible or interoperability difference;
- keep Stock PiG product-neutral; and
- share generally useful findings with the broader ecosystem when the contribution route permits it.

A Go implementation detail is not a reason to change observable behavior. A product integration belongs in an extension or explicitly selected Piglet unless it establishes a generic public contract.

## Roles

Contributors propose changes through issues and pull requests.

Maintainers:

- set project direction and scope;
- review and merge changes;
- enforce contribution and conduct policies;
- maintain compatibility with Pi;
- coordinate security reports and fixes;
- approve release candidates; and
- preserve licensing, provenance, and release evidence.

[`MAINTAINERS.md`](MAINTAINERS.md) identifies current maintainers and contact routes.

## Core committee

The core committee is PiG's creator and its maintainers, as listed in [`MAINTAINERS.md`](MAINTAINERS.md). The committee sets project direction and scope. Each committee member has the maintainer responsibilities in this document. [`MAINTAINERS.md`](MAINTAINERS.md) names the release authority and the security-response owner.

## Decisions

Use a pull request for a source, policy, governance, or release-process change. Record the decision and rationale in the pull request. A maintainer must approve the change before merge.

PiG follows observable Pi behavior by default. Record an intentional user-visible or interoperability difference in [`DIVERGENCES.md`](DIVERGENCES.md). Apply the evidence requirements in [`AGENTS.md`](AGENTS.md).

A governance change requires explicit approval from an active maintainer.

## Reviews and merges

A pull request must:

1. satisfy [`CONTRIBUTING.md`](CONTRIBUTING.md);
2. pass required checks;
3. receive the required maintainer or code-owner approval; and
4. resolve all review conversations.

An author does not self-approve when another qualified maintainer is available. A sole-maintainer merge still requires protected checks and a documented review of security, compatibility, licensing, and release impact.

## Releases

Only a maintainer with release authority can approve a release candidate. Follow [`docs/project/RELEASING.md`](docs/project/RELEASING.md). External legal, security, or publication approvals remain separate from repository governance and must be complete before publication when they apply.

## Security

The security-response owner coordinates private vulnerability reports. Follow [`SECURITY.md`](SECURITY.md). Do not disclose an unresolved vulnerability in a public issue or discussion.

## Maintainer changes

An active maintainer approves the addition or removal of a maintainer through a governance pull request. The pull request records the person's agreement and assigned responsibilities.

An inactive maintainer can ask to step down. The remaining maintainers must assign code review, release, and security responsibilities before removing access.
