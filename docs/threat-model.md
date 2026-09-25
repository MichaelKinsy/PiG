<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Threat model

## Scope

This model covers Stock PiG, its local configuration and caches, its extension host, Package and Piglet handling, self-update, and published release artifacts.

Product extensions and deployment platforms have separate threat models. Their credentials, APIs, policy, and infrastructure are outside Stock PiG.

## Assets

PiG can access assets available to the user who starts it:

- source code and workspace files;
- credentials in supported local stores and environment variables;
- model prompts and responses;
- session history;
- Package, extension, skill, hook, and Piglet source;
- downloaded or built artifacts; and
- update and release metadata.

## Trust boundaries

### User process boundary

PiG runs with the user's operating-system permissions. PiG does not isolate model output or built-in tools from the user account.

### Workspace and configuration boundary

Files in the workspace, `PIG_HOME`, `PIG_CODING_AGENT_DIR`, shell environment, and loaded context can influence PiG. Treat writable content in these locations as trusted input unless the process runs inside a stronger sandbox.

### Extension boundary

Extensions, skills, hooks, MCP servers, and Packages can execute code or influence model behavior. Installation and project discovery require explicit source trust. Subprocess hosting provides lifecycle and protocol isolation. It is not an operating-system sandbox.

### Network boundary

Model providers, package sources, catalogs, and update endpoints are external systems. TLS validation is enabled by default. Credentials must not appear in URLs, logs, command-line arguments, release records, or SBOMs.

### Release boundary

Release artifacts cross from protected build infrastructure to users. Checksums, signatures, provenance, and artifact-specific SBOMs bind each published file to the reviewed source commit and workflow.

## Threats and controls

| Threat | Control |
|---|---|
| Untrusted project instructions cause unsafe actions | Trust prompt before loading project resources; document that prompt injection is inside the selected trust boundary |
| Malicious extension executes with user privileges | Require explicit trust; use subprocess lifecycle controls; recommend an OS sandbox for stronger isolation |
| Package archive escapes its destination | Validate lexical and resolved containment; reject traversal and unsafe links |
| Dependency or build input changes without review | Pin manifests, lock files, toolchains, actions, and image bases; run dependency review |
| Vulnerable dependency ships | Run ecosystem scanners; review reachability; bind dispositions to the exact artifact |
| Secret enters source or release output | Run Gitleaks and TruffleHog; inspect generated evidence; prohibit credentials in configuration examples |
| Update replaces the wrong executable or accepts incomplete data | Prove one installation owner; verify size and checksum; perform atomic replacement; do not fall through after a tier starts |
| Published artifact differs from reviewed source | Build in a protected workflow; emit checksums, SBOMs, signatures, and provenance |
| Extension process outlives its owner | Bind process and goroutine lifetime to a context; drain shutdown; report errors |
| TUI extension blocks input or mutates UI off-loop | Keep blocking IPC off the input/render loop; marshal mutations through the owned UI path |
| SBOM omits or misidentifies a component | Reconcile scanner output with manifests, locks, embedded assets, generated data, and archive contents |

## Security assumptions

- The operating system and user account are trusted.
- The user reviews a source before granting project or extension trust.
- A model response is untrusted input.
- External services can fail, delay, or return malformed data.
- A checksum without an authenticated release identity is insufficient.
- A scanner finding is not a final security or licensing decision.

## Review triggers

Review this model when a change adds or changes:

- an executable input source;
- credential storage or authentication;
- network transport;
- Package, extension, or Piglet installation;
- archive extraction;
- self-update;
- sandbox or process isolation;
- release publication;
- telemetry; or
- a new distributed artifact type.

Report vulnerabilities through [`SECURITY.md`](../SECURITY.md).
