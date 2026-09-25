<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Security Policy

## Supported versions

No PiG version is supported until the first public release.

After that release, the latest released version receives security fixes. The `main` branch is development code. Older releases can require an upgrade to receive a fix.

## Security boundary

PiG runs with the operating-system permissions of the user who starts it. PiG does not sandbox model output, shell commands, tools, extensions, skills, hooks, project instructions, or local configuration.

Treat the user account, writable files, environment, shell configuration, PiG configuration, and trusted project files as one local trust boundary. Use a container, virtual machine, or another operating-system boundary when you need isolation.

A report must show how PiG grants access or crosses a security boundary. Expected behavior from already trusted local input is not a vulnerability by itself.

## Report a vulnerability

Do not report an undisclosed vulnerability in a public issue, pull request, or discussion.

Use GitHub private vulnerability reporting when it is enabled for this repository. If that route is unavailable, email Michael Kinsy at `mrkinsy5@gmail.com`.

Include:

- the affected PiG version or commit;
- the operating system and installation method;
- the vulnerability type and expected impact;
- reproduction steps or a proof of concept;
- known mitigations; and
- any disclosure deadline.

Do not include live credentials, customer data, or unrelated confidential information.

## In scope

Security reports can cover:

- PiG source and distributed artifacts;
- privilege or trust-boundary crossings caused by PiG;
- unsafe archive, update, package, or extension handling;
- credential disclosure caused by PiG;
- exploitable parser, protocol, or network behavior; and
- vulnerable shipped dependencies with evidence of applicability.

## Out of scope

The following are out of scope unless the report demonstrates a separate PiG boundary failure:

- prompt injection from content the user chose to trust;
- behavior of an untrusted extension, skill, hook, tool, or project instruction that the user chose to load;
- local execution with the permissions of the user who started PiG;
- modification of files that the attacker could already write;
- malicious model output;
- intentionally weakened configuration;
- public exposure of a local PiG process; and
- denial-of-service claims that require trusted local input and do not cross a resource boundary.

## Response and disclosure

The security-response owner assesses each report and communicates the next action when enough information is available. PiG does not promise a fixed response or remediation time.

Coordinate public disclosure with the security-response owner. A fix can require an advisory, a patched release, dependency updates, new SBOMs, and updated provenance evidence.
