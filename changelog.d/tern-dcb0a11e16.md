### Fixed

- A slow toolchain version check no longer blocks other starts without limit. Go, Rust and Python extension builds probe `go version`, `rustc --version` and the like; the probe is now ended after two minutes, a start that finds another start probing the same tool waits at most five minutes before probing for itself, and two projects with different `go.mod` or `rust-toolchain.toml` selectors each keep their own cached version instead of probing again on every alternate start. <!-- fix: dcb0a11e16 -->
