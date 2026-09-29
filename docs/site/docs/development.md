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
- npm 12.1.0;
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

After editing `coding/extension/host/subprocess/runtime-node/`, run `go generate ./coding/extension/host/subprocess` before a direct `go build`. Commit both `runtime-node.zip` and `runtime_node_digest_generated.go` with the source changes. The archive contains the same runtime files; its generated content digest keys the immutable runtime cache. `make pig`, `make build`, `make install`, and `make parity-bin` regenerate it automatically. `TestNodeRuntimeArchiveMatchesSources` rejects added, removed, or changed source files that are missing from the archive.

## Release version

Change the PiG release version with `make set-version VERSION=x.y.z`.
Use `SET_VERSION_ARGS=--dry-run` to inspect the diff without writing files.
The command updates the release pin, Standard development version, and changelog heading, then runs the version consistency tests.
It preserves Go module requirements and checksums so `main` continues to resolve published dependencies between releases.
Use `SET_VERSION_ARGS=--release-modules` only on an unpublished release branch to prepare matching nested-module pins and checksums.
Publish the nested SDK tag before merging those requirements to `main`.
Run `make module-publication` to check the required public tags and downloaded checksums without workspace or SDK-cache assistance.
Go factory builds retain required versions and checksums for locally replaced workspace modules (D20).
It leaves Unreleased entries in place unless you pass `SET_VERSION_ARGS=--move-unreleased`.
See [`docs/project/RELEASING.md`](https://github.com/MichaelKinsy/PiG/blob/main/docs/project/RELEASING.md) for candidate renaming and publication rules.

## Mirror upstream

PiG compares against an exact Pi release:

```bash
make upstream-mirror
```

This target installs the lockfile-pinned Pi comparator. It retrieves the pinned source and verifies its commit identity.

## Model catalog generation

`cmd/gen-models -src <published models.generated.js> -out <file>` translates the pinned published Pi catalog into Go. This is the complete-catalog regeneration path used by `automation/gen/generate-model-catalogs.sh`.

The command also exposes the ported models.dev Fireworks and Qwen Token Plan generation stages. Use `--json-only --json-output <directory>` to inspect their JSON catalogs without changing the committed Go catalog. Add `--strict` to reject an Individual model list that loses a required tool-capable model before publishing output. Other upstream network-provider stages and TypeScript publication are not implemented by this mode. Without `--json-only`, this mode writes the selected stages to `-out`; use a separate output file, not the complete committed catalog.

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

### Live provider tests

Live provider tests skip when their required environment variables are unset or empty. Use the normal Pi provider key names, such as `OPENAI_API_KEY` and `GEMINI_API_KEY`. A supplied credential that fails authentication fails the test; it does not skip.

```bash
go test -tags live ./ai ./coding -count=1 -v
```

Read the [live-test secrets inventory](https://github.com/MichaelKinsy/PiG/blob/main/docs/testing/live-secrets.md) for every credential, the Codex OAuth-token exception, optional cloud configuration, and integration-test commands. Use an isolated home directory in CI. A credential-free run proves skip behavior, not live-provider acceptance.

The optional `Live providers` workflow runs nightly on `main` and uses the protected `live-providers` GitHub environment. It does not run on PRs or participate in required checks. Dispatch it with `gh workflow run live-providers.yml -f packages=./ai/...`. Add `-f run='<Go test regex>'` to select tests. The job summary reports which providers' live tests ran or skipped.

## Parity workflow

For a behavior change:

1. read the corresponding source in `.upstream/current/`;
2. identify the observable contract;
3. reproduce the behavior in Pi and PiG;
4. add or identify a regression test;
5. fix the lowest shared source;
6. record an intentional difference in `docs/parity/DIVERGENCES.md` only when required;
7. run the focused and cross-family scenarios;
8. run the complete gate.

Fix root causes instead of hiding failures with timeouts, retries, sleeps, skips, or weaker comparisons.

### Faithful, general implementations

Follow the repository `AGENTS.md` rule of the same name: keep Pi's shared data-driven paths, cite Pi's own provider-specific branches, and fix sibling defects together. Test applicable OAuth, API-key, custom-base-URL OpenAI-compatible, and no-default-model shapes; Copilot and `test-faux` alone are not enough.

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

See the repository `.github/CONTRIBUTING.md`, `AGENTS.md`, and `RELEASING.md` files.
