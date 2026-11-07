### Changed

- The `pig` binary is about 1.4 MB smaller (1.5 MB on Linux amd64 and Windows): `pig extension upgrade`, the startup offer for an extension written for an older SDK and `extensionsAutoUpgrade` no longer carry Go's type checker. They build `extensions/sdk/upgrade/driver` from the staged SDK with the Go toolchain that builds the extension, and run it. The plan, the report and the error text stay the same. The first analysis after a Go or SDK update compiles that program (a few seconds on an empty Go build cache); each later analysis takes about 0.1 s longer than before.
