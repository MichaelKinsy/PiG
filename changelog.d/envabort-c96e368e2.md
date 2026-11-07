### Fixed

- Stop and timeouts end remote calls at once while the host is down: a request cancelled while the connection starts ends with `aborted` instead of waiting for the start, a sent request settles as `aborted` when the daemon does not answer its cancel within a second, `RemoteExecutionEnv.Exec` enforces its timeout locally, file operations stop waiting for the connection when cancelled, and `ssh` uses `ConnectTimeout=15`. Follows Pi main c96e368e2 ahead of the pin (D304).
