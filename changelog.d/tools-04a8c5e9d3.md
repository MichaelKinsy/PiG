### Fixed

- The `find` tool no longer reports the result limit when exactly `limit` files match, and `limit: 0` in `find` and `ls` no longer returns every result or an empty listing; a limit below 1 means 1 and a fractional limit is floored. A custom `FindOperations.glob` receives `limit + 1` in the Go, Python, Rust and Lisp SDKs, and a glob that ignores the limit has its results cut to it with the notice (D253). <!-- fix: 04a8c5e9d3 -->
