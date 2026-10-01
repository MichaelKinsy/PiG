<!--
SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
SPDX-License-Identifier: MIT
-->

# PiG 0.4.0 release-day checklist

Status: a procedure for the release lead and the owner, derived from `docs/project/RELEASING.md`, `.github/workflows/release-candidate.yml` and `.github/workflows/npm-publish.yml`, and rehearsed in `docs/plan/release-040-dryrun.md`. It is not release approval. Run each command as written; every `gh` command uses the public repository. `X` is the Pi pin, 0.99.2.

Roles: **Lead** builds the release branch and pull request. **Owner** holds release authority: approves the `release` environment, publishes the draft, and confirms hosting and native checks.

Invariants: `main` is squash-only; the release commit lives on `release/v0.4.0`, not on `main`; never move or delete a published tag; never replace an artifact of a released tag; change the version only with `make set-version`; commit with `git commit --signoff -S`; stage named files only.

## A. Blockers to clear first (Lead, Owner)

Do not start section B until each line is closed or has a written owner decision.

1. Pin move finished (the Pi 0.99.2 port lanes `port-992-core` and `port-992-mcp` are merged here): `UpstreamVersion` is 0.99.2, `test/parity/interfaces/test-mapping-v0.99.2.json` exists, the version-sensitive docs and ledgers pass `make docs-drift` and `make compliance`. Verify each Pi 0.99.2 bullet of `docs/plan/release-040-NOTES.md` with the table in `docs/plan/release-040-dryrun.md` ("Pi 0.99.2 items") on the release commit and delete the bullets that fail.
2. Merge public `main` again before the content PR: `5e78a4e99` (PR #109, fullscreen Kitty keys and theme colors, #105 and #108) landed after the 0.3.1 merge here. Decide whether PR #110 (#106, Windows cell publication retry) ships in 0.4.0. The 0.3.1 fixes themselves (#103, identity, #104, `--exclude-tools`, npm self-update) are already in this line; their changelog fragments `fix-rpc-exclude-tools.md` and `fix-update-channels.md` must be deleted, not folded.
3. MCP in sessions is wired (`builtin:mcp` in every mode). Keep the notes' MCP section; verify it once more on the release commit with `pig -e builtin:mcp`, RPC `get_commands`, and the real-binary MCP tests in `cmd/pig`.
4. Upstream test-porting gate: done. The owner approved the designed-out cases on 2026-10-01 and the policy, mapping and upstream-sync rows record them; `make test-porting-release`, `make known-gaps`, `make known-gaps-drift` and `make upstream-delta` pass. The one remaining partial file is the approved `deferred-0.3.x` row for `2860-replaced-session-context.test.ts`.
5. Gate fixes from the dry run: done. `go fix`, lint, `correspondence-check` and `sdk-surface-drift` pass.
6. CI parity image: the bump of `automation/images/ci-parity/{Dockerfile,metadata.env,package.json,package-lock.json}` to Pi 0.99.2 must be in the content that lands on `main`. `make compliance` fails (check `pins`) until it is, and the parity image carries the Pi oracle, so the Pi comparison runs against Pi 0.99.2 only once the image is published. The image is published by step B1a, after the content is on `main` and before the release run (section C). Do not work around the failing check.
7. Docs in the same change as the features. Done: MCP, codemode and virtual-model pages (site and `pig docs`), with a test that every page the system prompt cites ships. The `CHANGELOG.md` entries for codemode and tool search, the system theme and header, and Sign in with ChatGPT are in `[Unreleased]`. Still open: built-in extensions page, "migration to 0.4.0" sections in `extensions/sdk/README.md`, `extensions/sdk-rs/README.md`, `extensions/sdk-py/README.md` and `docs/site/docs/extensions.md`.
8. Changelog fragments: done on the prep branch. The legacy fragments are folded into `[Unreleased]` (the lead decided), the 0.3.1 fragments are deleted, and `changelog.d` is empty. Keep it empty; new fragments from later merges must be folded the same way before the release commit.
9. Owner confirms, before the workflow runs:
   - the `release` GitHub environment exists with required reviewers and allows the release branch;
   - `PIG_UPDATE_SIGNING_KEY` is set on it and its public key is in `automation/release/update-trust.pem` (if unsure, run the sign rehearsal in the dry-run report; the workflow fails before it tags anything when the key is wrong);
   - all seven npm packages list `MichaelKinsy/PiG` and `npm-publish.yml` as a trusted publisher (they published 0.3.0 this way);
   - the private hosting repository serves `install.sh`, `install.ps1` and `/api/installer/releases`, and its mirror job can run after publication.
10. Native verification plan: macOS arm64 and Windows amd64 are covered by the workflow's `native-smoke`; decide what to say about linux/arm64, darwin/amd64 and windows/arm64, which are built and published without a native smoke (`RELEASING.md` forbids listing an unverified target as supported).
11. Full gates on the release commit: `make check-core`, `make check-contracts`, `make parity-fast`, `make parity`, `make parity-perf`, `make release-check`. `make check` is red on `module-publication` until section D creates the nested tag; that is expected.

### A.12 `UpstreamReviewedVersion` (`coding/upstream.go`)

Answer to the porter's question. It stays at `0.84.0` for 0.4.0, exactly as for 0.3.0. Do not change it in the release commit or the pin move.

What the rules say:

- `docs/project/RELEASING.md` does not mention the constant, and no release gate requires it to equal `UpstreamVersion`. The only rule is its doc comment: it is "the most recent pin whose parity ledgers carry reviewed dispositions", and "advance it only after those ledgers have no pending rows". `docs/project/compliance.md` adds that the Makefile derives the leap ledgers from it.
- Precedent: the 0.3.0 release commit (`f144ac89d`) has pin 0.87.1 and `UpstreamReviewedVersion = "0.84.0"`, unchanged since the 0.2.0 commit. The 0.84.0 to 0.87.1 semantic delta manifest was 6685 rows, all `pending`.
- No ledger from a later pin is fully reviewed today: `delta-v0.84.0-v0.99.1.json` has 8731 rows, all `pending`; `upstream-sync/v0.99.1.toml` has 216 files, all `pending` (the 0.87.1 one closed with 331 deferred, 46 designed-out and 23 ported). So 0.84.0 is still the honest value.

What the constant drives at release (the pin move must regenerate these for 0.99.2, or `make check-contracts` fails):

- `make interface-delta` (in `ci-contracts`, non-strict) needs `test/parity/interfaces/delta-v0.84.0-v0.99.2.json`, generated with `go run ./test/parity/cmd/interfacedelta -generate`, plus `upstream-v0.99.2.json` and `cli-v0.99.2.json`. Non-strict accepts `pending` rows, but the row set must match the generated delta exactly.
- `make behavior-input-mapping-proposal` carries `behavior-input-mapping-v0.84.0.json` forward into `behavior-input-mapping-v0.99.2.json`.
- `test/parity/closure` reads `delta-v<reviewed>-v<pin>.json`.
- `check-public-claims.py` accepts the reviewed version on a line about review; every other mention of an old Pi version needs a `version-records.toml` entry.
- `make upstream-delta` needs `test/parity/upstream-sync/v0.99.2.toml` (from 0.99.1 to 0.99.2). It is part of `foundation-check`, not of `make check` or the release workflow.

What advancing it to 0.99.2 would require (not planned for 0.4.0): `interface-delta-strict` passes (no `pending` or `partial` semantic rows in the delta, every non-pending row with evidence and rationale, 8.7 thousand rows plus the 0.99.2 additions); the behavior-input mapping has no pending or partial rows (`behavior-contracts-strict`); `upstream-sync` manifests for 0.99.1 (216 pending) and 0.99.2 have no pending rows (`upstream-delta`); `interface-mapping-strict`, `test-inventory-strict` and `coverage-strict` pass for v0.99.2. That is the `foundation-check` set. Only then change the constant, regenerate with `make generate`, and let the delta and carry-forward file names move to the new base.

Release consequence: the Owner must not describe the ledgers as reviewed to 0.99.2. The release text claims only what the notes claim (behavior checked by parity scenarios), and the known-gaps document keeps the pending counts.

## B. Content on `main`, then the release branch (Lead)

Lesson from 0.3.1: `main` accepts squash merges only, and two gates pin the order. The `source` job of `release-candidate.yml` requires the release commit's `go.mod` to require `extensions/sdk` at the new version, and `automation/ci/check-module-publication.py` refuses an unpublished SDK pin on `main`. The release commit is therefore not on `main`. It is a branch `release/v0.4.0` made of `main` plus one signed commit from `make set-version`.

1. **Land the 0.4.0 content on `main` first**, through squash-merged pull requests, with every nested pin still at the published `v0.3.1` (the pin public main now carries): the porter line (squash of the Pi 0.99.2 work), the folded `CHANGELOG.md` [0.4.0] content, `changelog.d` removal, regenerated files, docs and SDK migration sections (section A items 1 to 8 closed). `make check` must be green on `main` (`module-publication` passes because the pin is published). Do not run `make set-version` on this content; the version stays 0.3.0 on `main` until the pin PR.

   Put the folded 0.4.0 entries under `## [Unreleased]` in `CHANGELOG.md` on `main`; the release commit's `--move-unreleased` moves them under the dated `[0.4.0]` heading. Run `make parity-deps && make generate` on that content and commit the result with it, because the release branch may not carry generated-file changes.

1a. **Publish the 0.99.2 CI parity image** (Lead or Owner, whoever can dispatch workflows). `ci-images.yml` publishes only from `main`, so this step comes after the content of step 1 is on `main` and before the release branch is dispatched in section C:

   ```bash
   gh workflow run ci-images.yml --repo MichaelKinsy/PiG --ref main -f publish=true
   gh run watch --repo MichaelKinsy/PiG            # pick the ci-images run; every job must succeed
   grep IMAGE_TAG automation/images/ci-parity/metadata.env   # the tag must name Pi 0.99.2
   ```

   Then confirm on `main` that `make compliance` passes (its `pins` check compares the Dockerfile, the image tag and the oracle packages with the pin). Section C assumes this image exists.

2. **Create the release branch and its one commit** from the up-to-date `main`:

   ```bash
   git fetch public && git switch -c release/v0.4.0 public/main
   git status --short                                    # must be empty
   make set-version VERSION=0.4.0 SET_VERSION_ARGS='--move-unreleased --release-modules --dry-run'   # review the plan
   git add -N <any new module file>                      # the hash covers tracked files only
   make set-version VERSION=0.4.0 SET_VERSION_ARGS='--move-unreleased --release-modules'
   make set-version VERSION=0.4.0 SET_VERSION_ARGS='--release-modules --dry-run' | wc -c   # must print 0
   automation/release/module-tags.sh 0.4.0 go.mod        # prints v0.4.0 and extensions/sdk/v0.4.0
   git diff --stat HEAD && git status --short            # only set-version output
   git commit --signoff -S -m "Release 0.4.0"            # one signed commit, Verified on GitHub
   git push public release/v0.4.0
   ```

   The commit contains only what `set-version` writes: `pigversion.go`, the standard Piglet version, the `[0.4.0]` heading, and the nested `go.mod`/`go.sum` pins and hashes. Do not hand-edit anything else on this branch; a content fix goes to `main` first and the branch is rebuilt from the new `main`. Never reuse the branch after a nested tag exists.

3. **Gates on the release branch.** `make module-publication` fails here by design until section D creates the nested tag. Run `make check-core check-contracts parity-fast` and `automation/ci/check-public-claims.py`. Open the pin pull request later (step D8), not now.

Copy the final notes (HTML comments and pending blocks removed) to `release-notes-0.4.0.md` outside the repository for step D5; hand the same text to the hosting repository if the site publishes a news page.

## C. Start the release workflow (Owner)

Dispatch on the release branch, never on `main`:

```bash
gh workflow run release-candidate.yml --repo MichaelKinsy/PiG --ref release/v0.4.0 -f version=0.4.0
gh run watch --repo MichaelKinsy/PiG            # pick the release-candidate run
```

Jobs: `source`, then `binary` for six targets, `native-smoke` for linux/amd64, darwin/arm64 and windows/amd64, then `publish`. `source` refuses a stale version, a `replace` or `exclude` in `go.mod`, a nested requirement at another version, or a non-empty `set-version --release-modules --dry-run`. A failure before `publish` is safe to fix: rebuild the branch from `main` (step B2) and rerun.

## D. Publication (Owner)

1. When `publish` waits for the `release` environment, review the evidence: each `binary` job's SBOM, inventory validation and Grype report, all three `native-smoke` jobs, and the `source` job. Approve.
2. `publish` assembles the release, combines `SHA256SUMS`, signs `update.json` with every key in `PIG_UPDATE_SIGNING_KEY` and checks each against `update-trust.pem`, creates `extensions/sdk/v0.4.0` as an annotated tag on the release commit (an existing tag must already name that commit), runs `automation/ci/check-module-publication.py`, and creates the draft release `v0.4.0` on that commit. After the nested tag exists the commit is frozen: a change needs version 0.4.1.
3. Confirm the nested tag: `make module-publication` on the release branch now passes.
4. Verify the draft:

   ```bash
   gh release view v0.4.0 --repo MichaelKinsy/PiG --json isDraft,targetCommitish,assets --jq '.isDraft,.targetCommitish,(.assets|length)'
   d=$(mktemp -d) && gh release download v0.4.0 --repo MichaelKinsy/PiG --dir "$d" && cd "$d"
   sha256sum -c SHA256SUMS && sha256sum -c EVIDENCE-SHA256SUMS
   ls pig-0.4.0-*.sigstore.json
   gh attestation verify pig-0.4.0-linux-amd64.tar.gz --repo MichaelKinsy/PiG --bundle pig-0.4.0-linux-amd64.tar.gz.sigstore.json
   python3 - <<'PY'
   import base64, json
   m = json.load(open("update.json")); print(m["version"], m["packageName"], sorted(m["binaries"]))
   open("sig.bin", "wb").write(base64.b64decode(open("update.json.sig").read().strip().split(",")[0]))
   PY
   # one signature must verify against a trusted key (extract each key of update-trust.pem in turn):
   openssl pkeyutl -verify -pubin -inkey <one-key.pem> -rawin -in update.json -sigfile sig.bin
   ```

   Expect: `version 0.4.0`, `packageName @pi-in-go/pig`, four binaries (darwin and linux, amd64 and arm64), seven archives in `SHA256SUMS`, every `sha256sum -c` line `OK`.
5. Replace the draft body (the workflow used `--generate-notes`): `gh release edit v0.4.0 --repo MichaelKinsy/PiG --notes-file release-notes-0.4.0.md`.
6. Publish: `gh release edit v0.4.0 --repo MichaelKinsy/PiG --draft=false --latest` (or the UI's Publish button). This creates tag `v0.4.0` on the release commit and starts `npm-publish.yml`.
7. Watch `npm-publish.yml`: it resolves the tag, refuses a draft, verifies the archives, packs, and publishes the six platform packages then the launcher with provenance. A partial failure is finished by `gh workflow run npm-publish.yml --repo MichaelKinsy/PiG -f tag=v0.4.0` (versions already on npm are skipped). Confirm:

   ```bash
   npm view @pi-in-go/pig@0.4.0 version dist-tags
   for p in darwin-arm64 darwin-x64 linux-arm64 linux-x64 win32-arm64 win32-x64; do npm view @pi-in-go/pig-$p@0.4.0 version; done
   ```
8. **Only now open and squash-merge the pin pull request** (`release/v0.4.0` into `main`). `main` then carries the version, the heading and the SDK pin at the published `v0.4.0`, so `check-module-publication` passes there. The tags `v0.4.0` and `extensions/sdk/v0.4.0` name the branch commit, which is not on `main`'s history (squash); that is expected and matches 0.3.1.

If a step fails after D2: do not delete or move `extensions/sdk/v0.4.0`. Fix forward as 0.4.1. A draft that is wrong before D6 can be deleted with `gh release delete v0.4.0 --repo MichaelKinsy/PiG` and recreated by re-running the workflow on the same commit; npm versions cannot be replaced, only deprecated.

## E. Post-release verification (Owner, Lead)

1. External Go install with empty caches (RELEASING.md):

   ```bash
   GOBIN="$(mktemp -d)" GOMODCACHE="$(mktemp -d)" GOFLAGS= GOWORK=off go install github.com/MichaelKinsy/PiG/cmd/pig@v0.4.0
   go version -m "$GOBIN/pig" | head -5 && "$GOBIN/pig" version
   GOFLAGS= GOWORK=off go list -m github.com/MichaelKinsy/PiG@v0.4.0
   GOFLAGS= GOWORK=off go list -m github.com/MichaelKinsy/PiG/extensions/sdk@v0.4.0
   curl -fsS https://pkg.go.dev/github.com/MichaelKinsy/PiG@v0.4.0 -o /dev/null   # request the page; repeat for .../extensions/sdk
   ```
2. Latest-release plumbing: `curl -fsSL https://github.com/MichaelKinsy/PiG/releases/latest/download/update.json | python3 -m json.tool | head`; `curl -fsS https://pi-in-go.dev/api/installer/releases | head -c 400`.
3. Fresh installs on each supported platform, in a temporary install directory:
   - macOS and Linux: `curl -fsSL https://pi-in-go.dev/install.sh | PIG_INSTALL_DIR="$(mktemp -d)" PIG_HOME="$(mktemp -d)" sh`, then `"$PIG_INSTALL_DIR/pig" --version` prints `0.4.0+X`.
   - Windows (PowerShell): `irm https://pi-in-go.dev/install.ps1 | iex`, then `pig --version`.
   - npm: `npm install -g @pi-in-go/pig@0.4.0`, then `pig --version`.
4. Update paths from 0.3.0 (the post-release update check). Use machines or fresh homes that have never updated.
   - **macOS** (arm64 and, if supported, amd64): `PIG_VERSION=0.3.0 PIG_INSTALL_DIR=$HOME/pig030 PIG_HOME=$HOME/pig030-home sh install.sh`, then `PIG_HOME=$HOME/pig030-home $HOME/pig030/pig update`. Expect `Updating pig 0.3.0 → 0.4.0... Updated to pig 0.4.0.` and `pig --version` `0.4.0+X`. Confirm the first start after the update rebuilds extensions.
   - **Windows, standalone `pig.exe`:** `pig update` must tell the user it does not replace a running `pig.exe` (D39); run the PowerShell installer and confirm `pig --version`.
   - **Windows, npm:** install the previous release with `npm install -g @pi-in-go/pig@0.3.0` (or `@0.3.1` if it shipped), then run `pig update`. From 0.3.1 on, expect `npm install -g @pi-in-go/pig@0.4.0`, the launcher owning the install, and `pig --version` `0.4.0+X`. 0.3.0's own `pig update` installs the wrong package (`pig`), so from 0.3.0 run `npm update -g @pi-in-go/pig` and confirm the same result.
   - **Linux:** as macOS.
5. Report the results to the Lead with the exact versions, platforms and outputs. Do not claim a platform that was not run; update the notes' Verification paragraph and the release body (`gh release edit v0.4.0 --notes-file …`) only with observed results.
6. Housekeeping: comment on and close the issues the release fixes (#101, and #103 and #104 when their fixes shipped); leave #105, #106, #87 and #92 open with a pointer to the notes' Known issues; thank the credited handles in the notes; publish the site news page through the hosting repository; update `docs/project/RELEASING.md` examples to the next version.
