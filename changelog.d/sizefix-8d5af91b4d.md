### Upgrading

- Go API `coding/extension/host/runtimecell`: a failed Go build is classified as SDK drift (the "written for an older SDK" message and `BuildFailure.Drift`) through `runtimecell.SetDriftClassifier`, which the `pig` command installs. `BuildFailure.Drift` is a list of `runtimecell.Drift`, with the fields and JSON of `upgrade.Drift`. A program that embeds the extension host without the `pig` command and starts Go extensions reports a failed build with the compiler's own message, as it does for an error that is not an SDK change; one that wants the drift calls `SetDriftClassifier` from an init function.

### Changed

- A program that embeds PiG and starts extensions no longer links `go/types` and the SDK upgrade rules: the host reached them through `extensions/sdk/upgrade` only to classify a failed extension build, and the `pig` command now installs that classification (`examples/headless` on linux/amd64: 40,190,112 to 39,809,184 bytes). The `pig` command links them as before.
- The Python and Rust SDK bundles in the `pig` binary carry their unicode property tables gzipped (`_tables.py.gz`, `tables.rs.gz`, an eighth of the 375 KB of text) and staging writes the files back: the stock binary is 323,584 bytes smaller (69,705,888 on linux/amd64) and the staged SDKs hold the same files as before.
