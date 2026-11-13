### Changed

- `extensions/sdk-py/pig_sdk` and `extensions/sdk-rs/src` are directories with their own `go.mod`, as `extensions/sdk-lisp/src` is, so the root module zip that `go install` downloads is 128.5 MB instead of 132.3 MB (3.8 MB of Python and Rust SDK sources that no Go package embeds or imports). The files stay in the repository; PiG still carries both SDKs in their embedded packs, which are unchanged, and Python, cargo, tests, generators and drift gates read the directories by path.
