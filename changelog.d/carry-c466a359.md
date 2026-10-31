### Added

- `pi.exec()` accepts a `maxBuffer` option that limits stdout and stderr combined, and its result has a `truncated` field that is true when output exceeded the limit, in the Go (`ExecOptions.MaxBuffer`, `ExecResult.Truncated`), Rust (`max_buffer`, `truncated`), Python (`exec(max_buffer=)`, `ExecResult.truncated`), Lisp (`:max-buffer`, `exec-result-truncated`) and Node SDKs. `extensions/sdk-ts` declares both for TypeScript extensions. A `maxBuffer` that is not an integer from 0 to the runtime string limit rejects the call before the command runs.

### Fixed

- `pi.exec()` no longer buffers a command's output without limit, which exhausted memory when a command printed too much. The default cap is 256 MiB, counted over stdout and stderr together; past the cap the command is terminated, a character the cap cuts is dropped whole, and the result has `truncated: true`, `killed: true` and a nonzero code. Carried forward from Pi c466a359 (earendil-works/pi#11360), past the 1.4.0+b3a29f11 pin. <!-- fix: c466a359 -->
- `pi.exec()` no longer waits forever for a command that ignores SIGTERM after a timeout, an abort or the output cap: the host sends SIGKILL 5 seconds later if the command has not exited, as Pi intended. Carried forward from Pi c466a359 (earendil-works/pi#11360), past the 1.4.0+b3a29f11 pin. <!-- fix: c466a359 -->
