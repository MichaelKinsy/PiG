# Piglet Binaries

A Piglet Binary is a target-native PiG executable for one fixed Piglet composition. It delivers an agent application directly without creating another harness implementation.

A Piglet Binary is a carrier for a Piglet. It does not introduce another configuration schema.

## Build a Binary

Validate the Piglet first:

```bash
pig piglet validate ./agents/reviewer.yaml
```

Build the executable:

```bash
pig piglet build ./agents/reviewer.yaml \
  --format binary \
  --out ./pig-reviewer
```

Run it directly:

```bash
./pig-reviewer
```

The build records the Piglet, its Resource closure, its component plan, the target, and verification information.

### Build progress

PiG reports build progress on stderr (D18). A terminal shows a spinner, the current phase, elapsed time, and dim step text. CI and pipes receive one plain line per phase. The final summary gives the binary path, byte size, and total time after verification and record publication.

Add `--verbose` to stream toolchain output with member labels:

```bash
pig piglet build ./agents/reviewer.yaml --format binary --out ./pig-reviewer --verbose
```

Packed members share one compiler invocation and label. Fused Go members compile with the final binary. Module resolution, compilation, and linking can occur inside one compiler invocation, so PiG reports that operation as one phase rather than estimating progress. A failed build names its phase, shows the diagnostic tail, and prints a hint. With `--json`, progress stays on stderr and stdout remains one JSON result.

The source-checkout command `pig build [--verbose]` uses the same progress display. It builds Stock PiG, not a Piglet Binary.

## Sign a Binary

Create an Ed25519 author key once. Keep the private file secret and publish the `.pub` file:

```bash
pig piglet keygen ./reviewer-signing.key
```

Sign during the native build:

```bash
pig piglet build ./agents/reviewer.yaml \
  --format binary \
  --out ./pig-reviewer \
  --sign-key ./reviewer-signing.key
```

The signature covers every executable byte and a DSSE manifest that names the Piglet, target, PiG version, resolution record, component plan, components, and embedded files. The Binary embeds the public half of its author key and checks the signature before command dispatch. An altered signature, manifest, Piglet, record, component, or executable byte makes the Binary refuse to run.

Check a file without running it:

```bash
pig verify ./pig-reviewer
pig piglet verify ./pig-reviewer
```

`pig verify` reports a valid Piglet signature as `ok` and an unsigned file as `n/a`; because it also verifies arbitrary archives and data files, it applies the required-signature policy only after a Piglet signature block identifies the target. `pig piglet verify` is Piglet-specific and succeeds only when the file carries a valid signature. Both commands reject a signed file whose key is revoked, and Piglet Binary startup enforces the required-signature policy for unsigned Binaries.

Trust and policy are explicit local actions:

```bash
pig piglet trust add ./reviewer-signing.key.pub
pig piglet trust list
pig piglet trust revoke ed25519:<key-id>
pig piglet trust require on
```

A valid signature by the embedded author key proves that the file has not changed since that key signed it. It does not prove who controls the key. Add a reviewed public key to the trust store to attach local identity, and enable `require on` when every Piglet Binary must be signed by a trusted, non-revoked key.

## Publish to GitHub Releases

Set `release.version` in the Piglet. Then publish signed Binaries as one GitHub Release named `v<release.version>`:

```bash
pig piglet publish ./agents/reviewer.yaml \
  --to github \
  --repo acme/reviewer \
  --sign-key ./reviewer-signing.key
```

Without `--yes`, publish is a dry run. It validates the Piglet, the signing key, and every target, checks that the release does not exist yet, and prints the assets it would upload. It builds and uploads nothing. Add `--yes` to build, sign, and upload these assets:

- one signed Binary per target, named `pig-<name>-<os>-<arch>` (with `.exe` for Windows);
- `SHA256SUMS`, which lists the SHA-256 of each Binary;
- `piglet-release.json`, the release index, signed with the same key.

Targets come from `--targets os/arch,...`, else `build.targets`, else the host target. The native builder builds only the host target, and a container builder does not sign, so one machine can usually publish only its own target. When a target has no ready signing builder, publish lists each missing target with every builder's reason and uploads nothing. Each build also writes the managed build record that `pig piglet build` writes.

To publish several targets, build each target on a matching host, collect the Binaries in one directory, and publish them without building:

```bash
pig piglet publish ./agents/reviewer.yaml --to github --repo acme/reviewer \
  --sign-key ./reviewer-signing.key --artifacts ./dist --yes
```

Publish identifies each file in `--artifacts` by its signed manifest. Every file must be a Binary of this Piglet source and `release.version`, signed by `--sign-key`. The directory must hold exactly one Binary for each target, and every Binary must come from the same PiG version. Before the upload, PiG checks each staged asset against the signed index with the checks that `pig piglet pull` applies.

PiG runs the GitHub CLI (`gh release view` and `gh release create`) with your existing `gh` authentication and never handles a GitHub token. It refuses a release that already exists, because a published release is immutable. A SemVer prerelease version, such as `2.0.0-rc.1`, publishes as a GitHub prerelease. `--commit <sha>` creates a missing release tag at that commit. The index records the Piglet source as `git:github.com/<owner>/<repo>@<commit or tag>` unless `--source-ref` names another npm or Git source. Publication contacts GitHub only through `gh`, and it never contacts pi.dev or a PiG service.

The reusable workflow [`docs/examples/piglet-release.yml`](https://github.com/MichaelKinsy/PiG/blob/main/docs/examples/piglet-release.yml) builds each target on a native GitHub runner, attests the build provenance of each Binary, and publishes the release with `--artifacts`. Copy it to `.github/workflows/piglet-release.yml` in the Piglet's repository. Its header shows the workflow that calls it.

## Pull a published Binary

A publisher can place signed per-target Binaries and a signed `piglet-release.json` index on GitHub Releases or another HTTPS host. Pull the Binary for the current target:

```bash
pig piglet pull https://github.com/acme/reviewer/releases/download/v1.2.3/piglet-release.json
```

Select another target or use a GitHub release reference explicitly:

```bash
pig piglet pull github:acme/reviewer@1.2.3 --target linux/amd64
```

PiG verifies the release-index DSSE signature, the complete asset size and SHA-256, the Binary signature trailer, and the Piglet, release, target, and PiG identities before it installs any file. The first successful pull pins the signer key for that Piglet. A later signer change is refused unless you name the new key exactly with `--accept-signer ed25519:<key-id>`. Local revocation and `trust require on` apply to pulled releases.

Pulled Binaries live under `~/.pig/artifacts/piglets/`, and their signed receipts live under `~/.pig/receipts/piglets/`. `pig piglet list` and `show` report them as Binary facets. Pull requests only the supplied index and the asset URL signed into that index; it performs no automatic catalog or PiG service request.

### Sigstore keyless provenance

An Ed25519 Piglet signature is offline artifact integrity and local trust policy. Sigstore keyless provenance is separate evidence that a named CI workflow built an artifact. A release workflow can publish GitHub build provenance, and the recipient can verify it explicitly:

```bash
pig verify --provenance \
  --repo acme/reviewer \
  --signer-workflow acme/reviewer/.github/workflows/piglet-release.yml \
  ./pig-reviewer
```

This command delegates to `gh attestation verify`, which checks the Sigstore bundle, certificate identity, and transparency-log inclusion. It requires the GitHub CLI and may contact GitHub's attestation service. PiG never performs this network check at startup. Startup signature verification remains offline.

## What the Binary contains

A Piglet Binary always contains:

- the Stock PiG execution engine;
- one fixed Piglet;
- its resolution and component plan;
- every extension or native component marked as Binary materialized;
- verification data required at startup.

A Binary can still require external items when its plan declares them:

- an external MCP service;
- a secret value;
- a configuration file;
- an interpreted runtime;
- a component supplied by a release or required agent environment.

Do not describe a Binary as self-contained unless its record proves that none of these requirements remain.

## Realization and materialization

Each executable component records two independent facts.

| Axis | Values | Question answered |
|---|---|---|
| Realization | fused, subprocess, external | How does the component execute? |
| Materialization | binary, release, `agentEnv`, external | Where do its bytes and runtime come from? |

A subprocess component does not create a different Binary type. It remains part of the same Piglet application and component plan.

## Fused Go extensions

A compatible Go factory can be fused into the Binary. PiG compiles the extension into the executable and starts it through the same registration contract used by subprocess extensions.

Advantages:

- one target-native executable;
- no target-side Go compiler;
- no extension process startup;
- no serialization cost for process placement;
- exact extension source included in the build closure.

Costs:

- the Binary must be rebuilt when the extension changes;
- the artifact is target specific;
- the extension shares the PiG process failure boundary;
- fused code requires stricter review;
- only compatible Go factories can fuse.

Fused behavior must match source subprocess behavior. Fusion is a delivery optimization, not a second extension API.

## Subprocess extensions

A normal Piglet Binary can start exact subprocess components when its plan permits them.

Advantages:

- process failure isolation;
- support for Go, Rust, Python, Node, and native programs;
- independent heartbeat and cancellation handling;
- easier source development and reload.

Costs:

- process and IPC overhead;
- explicit runtime requirements;
- more lifecycle and transport handling;
- the Binary might not contain every required runtime.

A prebuilt native subprocess component can avoid a target-side compiler. An interpreted component still needs its recorded runtime.

## External components

An external realization connects to a separately managed service. The Binary records the requirement but does not embed or operate that service.

Use an external realization for network services that have their own lifecycle, scaling, credentials, or deployment owner.

## Require all extensions to fuse

A Piglet can reject subprocess fallback:

```yaml
build:
  extensionRealization: fused
```

The build then requires every selected extension to be a fuse-compatible Go factory. If one extension resolves to a subprocess, the build fails and names that extension and the reason.

PiG Standard uses this requirement. Every extension added to PiG Standard must:

- use the public Go extension factory contract;
- work in source mode through the normal subprocess host;
- work as a fused component;
- pass the same behavior and lifecycle tests in both modes.

## Startup verification

A Piglet Binary verifies its embedded Piglet, component closure, and optional Ed25519 signature before command dispatch. A signed Binary refuses to run when any executable or signature byte changes. An unsigned Binary reports unsigned and runs unless the local trust policy requires every Piglet Binary to carry a trusted signature.

Startup does not silently:

- install a Package;
- download a missing component;
- substitute a same-named executable from `PATH`;
- log in to a provider;
- weaken an environment requirement;
- replace a failed fused extension with a subprocess.

A missing or tampered requirement causes a clear failure.

## Failure and recovery

Fusion and subprocess execution have different physical failure boundaries, but they use the same extension behavior contract.

For subprocess components, PiG provides bounded heartbeat, cancellation, crash detection, quarantine, reload, and replacement behavior. A failed replacement does not remove the last working extension set.

PiG does not automatically replay an interrupted tool or command. A replay could duplicate file changes, messages, or network actions. The caller decides whether an operation is safe to retry.

A fused extension cannot lose a process socket because it runs inside PiG, but it can still panic, block, or misuse resources. Fused extensions therefore require lifecycle, shutdown, and leak verification before release.

See [Extensions](/docs/latest/extensions) for the complete tradeoff and recovery model.

## Portability

Build one Binary per target:

```yaml
build:
  targets:
    - linux/amd64
    - darwin/arm64
    - windows/amd64
```

Do not claim target support until the artifact passes native verification on that target.

A Binary built on one target does not make an external service, secret, or interpreter portable. The record keeps these requirements visible.

## Source mode versus Binary mode

| Need | Use |
|---|---|
| Edit and reload extension source | Host Piglet source mode |
| Inspect a composition before release | Host Piglet source mode |
| Hand off one executable | Piglet Binary |
| Require one native all-fused application | Piglet with `extensionRealization: fused` |
| Carry a complete operating environment | Piglet Image when that producer is available |

## Related documentation

- [Piglets](/docs/latest/piglets)
- [Extensions](/docs/latest/extensions)
- [Derivative harnesses](/docs/latest/derivative-harnesses)
- [Packages](/docs/latest/packages)
