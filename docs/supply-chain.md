<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# Supply-chain evidence

PiG uses a scanner-agnostic evidence process. A scanner result is a starting point. The release inventory and SPDX SBOM are the reviewed records.

## Required process

For each release candidate:

1. Build the candidate from the protected release commit.
2. Inventory the source and every distributed artifact.
3. Generate SPDX 2.3 JSON and CycloneDX JSON SBOMs.
4. Reconcile the inventory with language manifests, lock files, embedded assets, generated data, and archive contents.
5. Verify package names, versions, suppliers, licenses, checksums, and dependency relationships.
6. Review vulnerability findings for applicability and reachability.
7. Fix each applicable finding or record its reviewed disposition.
8. Submit the validated inventory and SBOMs through the required review processes.
9. Retain the inputs, reports, decisions, signatures, and provenance with the release record.

A successful scanner exit does not complete these steps.

## Inventory sources

Reviewers compare scanner output with these authoritative inputs:

- Go modules in every `go.mod` and `go.sum` pair;
- npm packages in every `package.json` and lock file;
- Python project metadata and resolved distributions;
- Rust crates in every `Cargo.toml` and `Cargo.lock` pair;
- embedded JavaScript, fonts, images, schemas, and generated tables;
- copied or translated upstream source identified by `NOTICE`, SPDX metadata, and `PORT_MAP.md`;
- files included in each source or binary archive;
- operating-system packages in each published OCI image.

The review removes false positives, adds components a scanner missed, and resolves `NOASSERTION` fields. It does not suppress an unresolved finding to make a gate pass.

## Artifact-specific SBOMs

Generate a separate inventory for each distributed artifact:

- source archive;
- each operating-system and architecture binary archive;
- each published OCI image;
- each published Piglet Binary or Piglet Image;
- the documentation site when it contains vendored runtime assets.

Do not substitute a source-directory scan for an artifact SBOM. A source inventory can contain test and build dependencies that are absent from a binary. A binary or image can contain toolchain and operating-system components that are absent from the source tree.

## Required release evidence

Each release artifact record includes:

- source commit and tag;
- workflow identity;
- artifact filename and SHA-256;
- SPDX 2.3 JSON SBOM and its SHA-256;
- CycloneDX JSON SBOM and its SHA-256 when required by the receiving process;
- license inventory and review disposition;
- vulnerability reports and reviewed dispositions;
- provenance attestation;
- artifact signature;
- scanner names and versions;
- vulnerability database identities and timestamps.

The release workflow publishes machine-readable evidence as release artifacts. The repository keeps the workflow and validation rules. It does not commit generated reports that become stale on the next source change.

## Tool coverage

The project can use more than one tool for independent evidence:

- Syft or Code Insight for component inventory and SBOM generation;
- Grype, OSV-Scanner, `govulncheck`, `npm audit`, and `pip-audit` for vulnerability evidence;
- Code Insight, FOSSology, `go-licenses`, and REUSE for license evidence;
- Gitleaks and TruffleHog for secret detection;
- CodeQL, `staticcheck`, `golangci-lint`, Ruff, Bandit, and ShellCheck for source analysis.

No tool name is part of the compliance claim. The claim is that the final inventory is complete, accurate, reviewed, and bound to the exact artifact.

## Review record

A reviewer records for each finding:

- the component and artifact;
- the source that confirms the component identity;
- the detected license or vulnerability;
- whether the component is shipped, build-only, test-only, or absent;
- reachability or applicability evidence;
- the remediation or accepted disposition;
- the reviewer and review date.

Release approval remains separate from technical evidence generation.
