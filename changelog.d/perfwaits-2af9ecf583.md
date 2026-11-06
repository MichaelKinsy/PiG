### Fixed

- `RpcClient.Start` in the Go SDK returns as soon as the agent process exits, instead of after the fixed 100 ms settle window: a child that fails at once (a bad flag, a missing file) is reported in about 5 ms, with the same error text and with its stderr already collected. A child that is still running at the end of the window is returned as before. <!-- fix: 2af9ecf583 -->
