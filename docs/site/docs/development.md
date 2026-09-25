# Development

PiG is a parity-bound Go implementation of upstream Pi. Read the repository instructions before changing behavior.

## Requirements

If PiG is already available, use the repository Skill for guided setup:

```bash
pig --skill ./.agents/skills/setup-pig
```

The Skill checks only the tools required for the selected task. It asks before
installing software.

The qualified verification toolchain is:

- Go 1.27.1;
- macOS 13 or later for native macOS builds;
- Git;
- Node.js 24.19.0;
- npm 12.0.2;
- Python 3.12;
- Rust and Cargo 1.97.1;
- tmux 3.7 on Unix.

Use the versions pinned by the repository and CI image metadata.

## Build

```bash
go build -o bin/pig ./cmd/pig
./bin/pig --version
```

Do not use `go install ./cmd/pig` in a development checkout. Keep test and candidate binaries under the repository `bin/` directory or another explicit temporary path.

## Mirror upstream

PiG compares against an exact Pi release:

```bash
make upstream-mirror
```

This target installs the lockfile-pinned Pi comparator. It retrieves the pinned source and verifies its commit identity.

## Primary checks

Run the deterministic main gate:

```bash
make check
```

Run declared scenario durability and regenerate coverage at the end of a parity loop:

```bash
make verify
```

Other focused commands include:

```bash
make build
make vet
make lint
make test
make lint-scenarios
make parity-fast
make parity
make coverage
```

A green gate is necessary but does not prove that a change is faithful. Compare observable behavior with upstream Pi and fix or document every difference.

## Parity workflow

For a behavior change:

1. read the corresponding source in `.upstream/current/`;
2. identify the observable contract;
3. reproduce the behavior in Pi and PiG;
4. add or identify a regression test;
5. fix the lowest shared source;
6. record an intentional difference in `DIVERGENCES.md` only when required;
7. run the focused and cross-family scenarios;
8. run the complete gate.

Do not weaken a comparator or skip a scenario to make a defect green.

## Extensions

Create an extension with the running PiG SDK staging path:

```bash
pig extension init ./my-extension --lang go
pig install ./my-extension --validate-only --json
```

A new extension API capability must reach the Go, Rust, Python, and Node bridges where applicable. Add cross-SDK conformance that can detect a missing or fabricated implementation.

See [Extensions](/docs/latest/extensions).

## Piglet applications

Define product or workflow behavior in an ordinary Piglet and Resource set. Do not add derivative-harness behavior to Stock PiG.

```bash
pig piglet validate ./my-agent.yaml
pig --piglet ./my-agent.yaml
```

See [Build a derivative harness](/docs/latest/derivative-harnesses).

## Documentation website

User documentation is authored as Markdown under `docs/site/docs/`, and `docs/site/docs/docs.json` classifies and orders every page. The website at https://pi-in-go.dev is built from these files by a separate, private hosting repository, as pi.dev is for Pi. `make docs-drift` checks the page manifest here; the installer the site serves is `docs/site/public/install.sh`.

The embedded agent reference remains under `internal/pigdocs/content/` and ships in the executable.

## Contribution requirements

Contributions use Developer Certificate of Origin 1.1 sign-off. Preserve upstream and third-party attribution. Do not claim endorsement, sponsorship, certification, or release status without recorded approval.

See the repository `CONTRIBUTING.md`, `AGENTS.md`, and `RELEASING.md` files.
