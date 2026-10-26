# PiG CI images

PiG owns the source and publication workflow for its CI images.

The images use pinned neutral base-image digests and complete APK package locks. Every npm archive has a resolved URL and an integrity digest. The Go image fails when Grype finds a high or critical vulnerability with an available fix. The parity image also locks its upstream Pi dependency tree. Its Go, Node.js, Rust, tmux, and terminal database identities match the current parity baseline. Safe patch updates remove the known fixed curl and Python findings. Its vulnerability report is informational because changing an oracle dependency can change observed behavior; updates require a parity rebaseline rather than an automatic package substitution.

The parity image removes npm's bundled copies of the dependencies listed in the npm runtime overrides and resolves the pinned patched copies from `/opt/pig/npm/node_modules`. Keep the removal list in `automation/images/ci-parity/Dockerfile` aligned with the overrides in `automation/images/npm-runtime/package.json`. Neither lockfile carries an entry for npm's bundled `http-cache-semantics`, `ip-address` or `postcss-selector-parser`, because npm's tarball bundles vulnerable copies that a lockfile cannot replace. The npm runtime lockfile still records the other bundled copies that the image removes. `automation/ci/install-npm-toolchain.sh` applies the same removal to the hosted-runner npm in `automation/ci/npm-toolchain`: it removes the bundled copy of every dependency that `package.json` pins besides npm. An upgrade of npm that bundles a patched copy lets the direct pin and the removal go. The source-lock audit reports the bundled metadata. The image scan verifies the runnable result.

Build an image locally:

```bash
./automation/ci/build-ci-image.sh go
./automation/ci/build-ci-image.sh parity
```

The script tags each image with the source commit. It validates the installed tool versions after the build.

The workflow downloads each pinned build base as an OCI layout through the configured HTTPS proxy with a checksum-pinned `crane` binary. It builds with a job-scoped BuildKit instance and removes that instance and its cache after loading the result into the host daemon for scanning. Scanner images are loaded separately. This keeps pull credentials out of pull-request jobs, avoids reliance on daemon proxy configuration, and does not leave persistent build cache on a shared runner.

The `CI images` workflow separates what pull requests verify from what changes with the outside world.

Pull requests and pushes to `main` that touch the image inputs run `verify`. It pulls the image each `automation/images/<image>/published.env` pins by digest, checks that image's tool versions with `automation/ci/build-ci-image.sh` (`VERIFY_IMAGE_REF`), generates its SBOM, lints the Dockerfile offline with `docker buildx build --check`, and checks the npm locks. `published.env` also records the SHA-256 of the inputs the image was built from (`automation/ci/ci-image-inputs.sh`); when a pull request changes an input, the pinned image is older than the tree, the version checks wait for the refresh, and the job says so. `verify` reads no package index and no vulnerability database, so a package that Alpine or Wolfi drops, or an advisory published tomorrow, cannot turn a pull request red. A tree without a `published.env` (before the first publication) runs the static checks only and says so in a notice.

The refresh is the only place the package index and the vulnerability database are read. It runs weekly (Wednesday) and on `workflow_dispatch`:

1. `RESOLVE_APK_LOCK=1 automation/ci/build-ci-image.sh <image>` builds the Dockerfile's `apk-resolved` stage: the current index's versions of the locked packages, with their dependencies, written to `packages.lock`.
2. The normal build installs that exact lock and fails unless the installed set equals it, then checks tool versions, generates the SBOM, and scans (a high or critical finding with a fix fails the Go image).
3. `propose-lock` pushes a branch `ci-images/lock-<date>-<run>` with one GitHub-signed commit holding the locks that changed, and tries to open a pull request. The repository setting "Allow GitHub Actions to create and approve pull requests" is off by default; then the log names the branch and its compare URL.

Publication stays a dispatch from `main` with `publish` enabled, behind the `release` environment's approval. The workflow publishes commit-addressed images to GHCR, refuses to replace an existing tag, and records the pushed digest in the workflow artifacts. `propose-pin` then pushes a branch `ci-images/pin-<date>-<run>` that writes each `published.env` (digest and input hash), the same way. Merge that pull request to move pull requests to the new image. Local builds still work: `./automation/ci/build-ci-image.sh <image>` installs the committed lock and fails if the index has moved on, which is the signal to run the refresh.

Every base the workflows start from is pinned by digest: the Dockerfiles' `GO_IMAGE`, `GO_BUILDER` and `WOLFI_BASE`, and the BuildKit, Syft and Grype images that `automation/ci/seed-ci-image-bases.sh` pulls (the `crane` that pulls them is pinned by checksum). `test/ci-images` fails on an unpinned reference. The main verification workflow does not depend on these images.
