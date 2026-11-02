### Added

- `pi.exec` takes `maxBuffer` (Go `ExecOptions.MaxBuffer`, Rust `max_buffer`, Python `max_buffer`, Lisp `:max-buffer`) and its result carries `truncated`, in every extension SDK. Output past the cap (default 256 MiB) ends the command with `truncated` and `killed` set and a nonzero code, and a command that ignores SIGTERM after a timeout, abort or overflow is killed five seconds later (Pi c466a359).
- An extension's `SourceInfo` and package resource metadata carry `changelogPath` through the SDKs and the session graph (Pi b4b72a35), and a component's optional `capturesViewportInput` and the `getSelectPageIndex` helper are available in every SDK (Pi 3bae8527).
