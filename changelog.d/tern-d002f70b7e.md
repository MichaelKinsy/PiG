### Changed

- `--mode rpc` runs on one Go scheduler thread (GOMAXPROCS 1) when the environment does not set `GOMAXPROCS`, instead of two: an rpc turn costs 11-16% less CPU. Parallel tool calls and session loading still run at the full CPU width, and `GOMAXPROCS` still overrides the default. Output is unchanged.
