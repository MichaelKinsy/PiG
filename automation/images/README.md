# PiG CI images

PiG owns the source and publication workflow for its CI images.

The images use pinned neutral base-image digests and complete APK package locks. Every npm archive has a resolved URL and an integrity digest. The Go image fails when Grype finds a high or critical vulnerability with an available fix. The parity image also locks its upstream Pi dependency tree. Its Go, Node.js, Rust, tmux, and terminal database identities match the current parity baseline. Safe patch updates remove the known fixed curl and Python findings. Its vulnerability report is informational because changing an oracle dependency can change observed behavior; updates require a parity rebaseline rather than an automatic package substitution.

The parity image removes npm's bundled copies of the dependencies listed in the npm runtime overrides and resolves the pinned patched copies from `/opt/pig/npm/node_modules`. Keep the removal list in `automation/images/ci-parity/Dockerfile` aligned with the overrides in `automation/images/npm-runtime/package.json`. The source-lock audit reports the bundled metadata. The image scan verifies the runnable result.

Build an image locally:

```bash
./automation/ci/build-ci-image.sh go
./automation/ci/build-ci-image.sh parity
```

The script tags each image with the source commit. It validates the installed tool versions after the build.

The workflow downloads each pinned build base as an OCI layout through the configured HTTPS proxy with a checksum-pinned `crane` binary. It builds with a job-scoped BuildKit instance and removes that instance and its cache after loading the result into the host daemon for scanning. Scanner images are loaded separately. This keeps pull credentials out of pull-request jobs, avoids reliance on daemon proxy configuration, and does not leave persistent build cache on a shared runner.

The `CI images` workflow builds both images when these files change. It generates an SPDX SBOM and a fixed-vulnerability report for each image.

A maintainer can run the workflow manually from `main` with `publish` enabled.
The workflow publishes commit-addressed images to GHCR, refuses to replace an
existing tag, and records the pushed digest in the workflow artifacts. Published
images are optional reproducibility artifacts. The main verification workflow
does not depend on them.
