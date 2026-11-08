### Fixed

- The pi-env daemon on macOS: a small reply (an `lstat`, a ping) could come after megabytes of queued command output, more than a client buffers, where Pi's daemon answers between output frames. The writer now flushes after every frame as Pi's does instead of holding up to 1 MiB, and the daemon waits on its inherited stdin and stdout through the runtime poller, so a request that arrives while the runtime is busy with the output no longer waits up to 10 ms for a worker. <!-- fix: 7fc9ec365 -->
