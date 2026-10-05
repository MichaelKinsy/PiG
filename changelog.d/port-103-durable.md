### Added

- The durable execution environment follows Pi 1.0.3. File systems open bounded binary readers (`OpenBinaryReader`, with `NoFollow` to refuse a final-component symlink and a one-pass `ScanLines`) and paged directory readers (`OpenDirReader`), and report changes to files and directories with `Watch`. `NodeExecutionEnv` watches natively and falls back to snapshot polling on Windows and on network and FUSE file systems.
- `Exec` takes a string or a `[]string` argv, which runs a program without a shell, and every output chunk names its stream. An environment may omit shell output outside the tail a tool keeps (`ShellExecOptions.Window`) and report the omission; the bash tool passes the window along.
- `settings.progress` sets how often generation partials and running tool output are committed (defaults stay 100 ms).
- `durabletest.RegisterEnvConformance` checks a custom `ExecutionEnv`, as `RegisterStorageConformance` checks storage.

### Changed

- The durable `read` tool reads only the file header, one line scan and the lines it shows, instead of loading the whole file. Its results are unchanged.

### Fixed

- `FlushFile` on a directory fails with `is_directory` on Windows, as on POSIX.
- Tail-retained tool output no longer depends on when progress commits happened: a snapshot compacted the stored output to the kept window, which could move where a later window's first line started.
