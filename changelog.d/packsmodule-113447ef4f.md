### Changed

- `third_party/pi-packs`, the vendored npm packs of the pinned Pi build, is a data-only directory with its own `go.mod`, so the root module zip that `go install` downloads is 132.8 MB again instead of 138.2 MB (5.4 MB of tarballs that no Go package embeds or imports). The packs stay in the repository; the `file:` dependencies, `automation/pin` and `internal/pioracle` read them by path.
