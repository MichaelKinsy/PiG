### Fixed

- A Node extension's editor component whose `handleInput` (or another call the host makes) throws is now reported as `editor handleInput failed: <message>`, naming the call as the Go, Rust, Python and Lisp SDKs do, instead of `editor failed: <message>`. <!-- fix: d22ad24247 -->
