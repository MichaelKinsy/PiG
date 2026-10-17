# The upstream Pi pin

PiG targets one Pi build. `.upstream/pin` names it, and every other statement of the pin is read from it or derived from it. The pin is a Pi release tag (now `v1.2.0`) or a pinned commit of Pi's main branch, which is reported as the package version followed by `+` and the first eight characters of the commit (`Pi <package_version>+<commit[:8]>`).

## The pin file

`.upstream/pin` is the one committed setting. It is tracked even though the rest of `.upstream/` is the ignored source mirror.

| Key | Meaning |
| --- | --- |
| `ref` | What the pin was bumped to: a release tag, or the full commit of a commit pin. |
| `commit` | The exact 40-digit commit. |
| `package_version` | What Pi's `package.json` files and `pi --version` report (`1.1.0`). |
| `version` | The display and ledger version: the package version for a tag, `<package_version>+<commit[:8]>` for a commit. Ledgers and inventories are named by it (`mapping-v1.1.0+42a3497d.json`), and so is the mirror (`.upstream/v1.1.0+42a3497d`). |

In Go, `coding.UpstreamVersion` is the display version, `coding.UpstreamPackageVersion` the package version, `coding.UpstreamCommit` the commit and `coding.UpstreamRef` the ref. A parity comparison against `pi --version`, an npm dependency or a `package.json` uses the package version. A file name, ledger field or the coverage label uses the display version. [D63](DIVERGENCES.md#d63---version-prints-pigs-composite-version) states the composite `--version`.

## Reading and overriding the pin

Every consumer reads the pin through `automation/pin/pin.py` (`pin.py get <key>`, `pin.py env`, `pin.py make-vars`) or through the generated `pigversion.go`. `make pin-show` prints the effective pin.

`PI_UPSTREAM_REF=<tag|commit|branch>` overrides the pin for one run without editing a file:

```bash
make pin-show PI_UPSTREAM_REF=v1.1.0
make parity-deps upstream-mirror PI_UPSTREAM_REF=42a3497d   # the oracle and source mirror of that pin
make test PI_UPSTREAM_REF=42a3497d
eval "$(PI_UPSTREAM_REF=42a3497d python3 automation/pin/pin.py env)" && go test ./coding/...
```

An override resolves a tag through `git ls-remote`, a commit through the GitHub API and its tarball, and remembers the result under `~/.cache/checkouts/github.com/earendil-works/pi/resolved/`. A run that overrides the pin builds Go with a `-overlay` that replaces `pigversion.go` (the overlay lives under `$TMPDIR/pig-pin-overlay-*`), so no tracked file changes. Make and the env command export `PIG_UPSTREAM_*` for child processes.

`make parity-deps` installs the Pi oracle of the effective pin into `extensions/sdk-ts/node_modules` (a link into a cache) and points `.upstream/current` at the pin's mirror when that mirror exists:

The oracle is always **Pi built from source at the pin**, for a release tag and a commit alike (`automation/pin/oracle.py`). The source is the exact commit tarball from GitHub (`codeload`), whose root names the commit and is checked against the pin, so no `.git` is needed. The build runs `npm ci` with Pi's own lock, builds with Pi's scripts, `npm pack`s the workspace packages `pi-coding-agent` depends on, and runs `npm ci` of the pin's frozen dependency closure with those packs in place of the registry packages. The closure is a `package.json`/`package-lock.json` pair in `automation/pin/frozen/<commit>.closure.json.gz`: a release's is the lock of the published install (`pin.py freeze-closure --from-published-lock extensions/sdk-ts/package-lock.json`), so the third-party tree is the one `npm install @earendil-works/pi-coding-agent@<version>` gave and the install is laid out as that package's (`install-strategy=shallow`) is; a commit's is derived once from its packs with `npm install --package-lock-only --before=<commit date>` (`pin.py freeze-closure`) and committed, so it is the tree a published install of that commit would have had. Each workspace pack entry in the closure carries the sha512 integrity of its tarball (a release's is the published package's; a commit's is the digest of the packs the freeze built), so `npm ci` rejects a source build that is not byte-for-byte the frozen one. A commit with no frozen closure is derived on its first build and frozen there; a release with none is an error. Pi's own `install-lock` is not used: it comes from the monorepo's workspace resolution and lays packages out differently (for example `openai` inside `pi-ai`), which the tests and the vendored runtime do not read. `check-oracle` compares the installed tree with the closure, package by package. It is cached under `$PIG_PI_BUILD_STORE` (default `~/.cache/pig-dev/pi-builds`) per commit and frozen inputs (the model data and closure digests), and carries `.pig-pi-oracle.json` (`commit`, `source: "build"`, the digest of the model data). Each build goes into a fresh directory that is published by an atomic ref write and never rewritten or removed afterwards, so worktrees with different frozen files, a forced rebuild (`install-oracle --force`, which publishes another directory) and a parity test reading an oracle never disturb one another; concurrent builders of one identity wait on a lock and build once. A failed build is an error: nothing falls back to another Pi, the stamp is written only after the whole build, and `make parity-deps` exits non-zero.

Pi regenerates `packages/ai/src/providers/data/*.json` from live models.dev while it builds, so two builds of one commit differ. Every pin therefore freezes that directory in `automation/pin/frozen/<commit>.provider-data.tgz` (a reproducible archive with a recorded digest), unpacked in place of the hydrate step. A release tag's snapshot is the data its published package shipped (`pin.py freeze-oracle --from <pi-ai dist/providers/data>`); a release tag with no snapshot fails the build, naming the command. A commit's snapshot is taken from its first build: the build hydrates once and freezes that data itself. `check-oracle` and the parity runner reject an oracle built with other data, and an oracle that is not the source build of the pinned commit.

The published npm package is an explicit opt-in: `PIG_PARITY_PI_PUBLISHED=1 make parity-deps` installs the locked release of a tag pin (`automation/ci/npm-locked.py`), and the runner then accepts an unstamped oracle (or the exact mise-installed release when `PIG_PARITY_PI_BIN` is unset). A commit pin has no published package, so the opt-in does not apply to it. For v1.1.0 the source-built tree is the published one: `diff -r` of the two `node_modules` trees differs only in npm's bookkeeping files (`.package-lock.json`, the dependency-ready marker), and every package is the same name and version at the same path.

## Following Pi main (pin-follow)

`automation/pin/follow.py <commit>` prepares everything a pin move needs, without moving the pin. `automation/pin/watch.py` runs it for each new head of Pi's main branch (`git ls-remote`, so no API token; cron every 5 minutes under `flock -n`).

| Step | Command | Result |
| --- | --- | --- |
| oracle | `pin.py install-oracle` | the source build; the first build of a commit freezes its provider data and dependency closure in `automation/pin/frozen/` |
| mirror | `make upstream-mirror` | `.upstream/v<version>` |
| inventories | `make interface-proposals`, `make test-inventory-generate` | extracted per package (`PIG_INTERFACE_EXTRACT_INCREMENTAL=1`): a package keeps its earlier extraction while the files its TypeScript program reads (its own and its reachable workspace packages' code, declarations and manifests, and the third-party files the last extraction read) are unchanged |
| ledgers | `pin.py ledgers` | pending mapping carried from the committed pin, gap baseline (`make ledger-update` twice), deltas |
| gaps | `make ledger-check` | the gap list |
| tasks | | one `closure <upstream-file>` task per file with gap rows and one `delta <package>` task per package with pending upstream-sync files, in `tmp/pin-follow/<version>/tasks.tsv` |

Each step is recorded in `tmp/pin-follow/<version>/state.json`; a rerun resumes after the last passing step, and `--until <step>` stops after one. A failed step appends a FINDING line to the ready log; a finished follow appends a READY line and writes `automation/pin/records/<version>.json` (step times, latency from the commit time, frozen artifacts). The ready log is `--ready-log`, else `PIG_PIN_FOLLOW_READY_LOG`, else `tmp/pin-follow/<version>/ready.log`; set `PIG_PIN_FOLLOW_READY_LOG` in the cron entry to post to a shared log. The commit time is the modification time `git archive` gives the source tarball. The targets are the gap list 30 minutes and Porter output 2 hours after the upstream commit. `--run-porter` runs one headless `make porter-task` per task, serially in one tree; `automation/porter/run-porter-tasks.sh <tasks.tsv> <log-dir> <worktree>` runs a task list and commits each task's output (every created file included; a timed-out or non-building task is committed as `PARTIAL`); run the groups of different packages in separate worktrees to run them side by side, because the Porter edits the shared ledgers.

The follower never changes `.upstream/pin`, never merges, and the reviewers and merge train gate its output as any other.

## Moving the pin

```bash
make pin-bump PI_UPSTREAM_REF=<tag|commit>
```

writes `.upstream/pin`, then regenerates everything derived from it (`make pin-sync`): `pigversion.go`, the `@earendil-works` versions in `extensions/sdk-ts`, `automation/images/ci-parity`, `durable/interop` and `bench/k8s/image/pi-durable` (with their lock files when a version changed), the README badge and sentences, `docs/site/docs/index.md`, the version fields of `known-gaps.toml`, `behavior-contracts.toml` and `async-contracts.toml`, and the credential-expiry probe. It then mirrors the source, installs the oracle and generates the unreviewed inventories (`make interface-proposals test-inventory-generate`). `make pin-check` (a prerequisite of `make port-map-drift`, so `make check` runs it) fails when a derived file differs from its derivation.

`make pin-ledgers` (run by `make pin-bump`, or by hand under `PI_UPSTREAM_REF=<ref>`; `PIN_FROM=<ref>` names the earlier pin, default the committed one) generates the new pin's three single-file ledgers into `test/parity/pin-ledgers/v<version>/` and the interface delta manifest and custom-factory draft: `async-contracts.toml` keeps the earlier pin's reviewed row for every source file that is byte-identical in the two mirrors and is pending for the rest (`asynccheck -generate -previous`), and `behavior-contracts.toml` and `known-gaps.toml` are copied with their version field set (a contract whose source changed fails its drift gate until the batch that ports it reviews it). It skips a file that already exists; `--force` regenerates.

The three single-file ledgers above are archived per pin under `test/parity/pin-ledgers/v<version>/` when a bump leaves a pin, and restored when a bump returns to it, so moving the pin back reproduces the earlier pin's ledgers. Every ledger named by version (`mapping-v<version>.json`, `upstream-sync/v<version>.toml`, and the other inventories) stays in the repository, so an earlier pin keeps its own set. `PI_UPSTREAM_REF` alone does not swap the three single-file ledgers: a gate that reads them reports the version mismatch until the pin is bumped.

A release leap or commit move then follows the upgrade workflow in `piglets/porter/skills/pig-porter/SKILL.md`: reviewed mappings, ported behavior, `make generate`.

## Not yet derived

The CI parity image (`automation/images/ci-parity/Dockerfile`) still installs the published npm package and sets `PIG_PARITY_PI_PUBLISHED=1` to say so; for a commit pin its build fails on purpose until the image builds the oracle from source. The native CI jobs build from source (`make parity-deps`, `pin.py install-oracle`). The vendored Pi runtime of the Node extension host (`coding/extension/host/subprocess/runtime-node`, `make node-runtime`, `automation/gen/vendor-pi-dist.sh`) is regenerated with the port, not by the bump.
