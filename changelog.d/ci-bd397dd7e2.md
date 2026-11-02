### Changed

- CI: `automation/gen/pending-test-batches.py` and `automation/gen/test-ported-baseline.py` no longer default to the policy and mapping ledgers of a removed Pi version. Both read the committed Pi pin through `automation/pin/pinlib.py`.
- CI: the live-providers workflow no longer names Rust 1.97.1 and npm 12.1.0 itself. It reads both from `ci.yml`, as the nightly workflow does.
- `make compliance` checks the npm pin (`NPM_VERSION` in `ci.yml`) against `automation/ci/npm-toolchain/package.json`, `automation/images/npm-runtime/package.json`, the CI parity image and the documented requirement, and checks the Go pin against `AGENTS.md`.
- Tests fail on a version literal in `automation/`, the top-level `Makefile` or a workflow that is neither derived from its source nor recorded with a reason, and on an `UpstreamReviewedVersion` that is after the pin or has no ledgers.
