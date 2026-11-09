### Changed

- The root module zip that `go install github.com/MichaelKinsy/PiG/cmd/pig@<version>` downloads is 463 KB smaller: `extensions/sdk-lisp/sdk.tgz` and the `.gz` twin directories of `extensions/sdk-py` and `extensions/sdk-rs`, formats the single `sdk.zst` frame replaced and no bundle reads, are gone again after a merge had brought them back. No SDK, bundle, staged copy or binary changes.
