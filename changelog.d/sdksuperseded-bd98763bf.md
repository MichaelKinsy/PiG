### Changed

- The root module zip that `go install github.com/MichaelKinsy/PiG/cmd/pig@<version>` downloads is about 460 KB smaller: it no longer holds the superseded SDK bundle copies `extensions/sdk-lisp/sdk.tgz` and the `.gz` twin directories of `extensions/sdk-py` and `extensions/sdk-rs` (463 KB), which the single `sdk.zst` frame replaced and no bundle reads. No SDK, bundle, staged copy or binary changes.
