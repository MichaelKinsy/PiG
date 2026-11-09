### Fixed

- Every strict and packed Go cell of the extension conformance suite (the theme, child-session, markdown, exec-abort and other rows) failed to build with `go: updates to go.mod needed` since the root module moved to an SDK pseudo-version: 23 in-tree fixture, example and bench modules still required the PiG and SDK modules at v0.7.0. They are pinned to the root's requirement, and `make module-pins`, part of `ci-bookkeeping`, now fails when one drifts. <!-- fix: 142baa38fb -->
