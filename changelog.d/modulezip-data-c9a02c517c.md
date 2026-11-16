### Changed

- Twenty-five more data-only directories (the recorded model catalog source, the test shard timing tables, the recorded oracle data and the fixtures of many tests) have their own `go.mod`, so the root module zip that `go install` downloads is 132.0 MB instead of 134.2 MB. No Go package embeds or imports them; the files stay in the repository and the generators, tests and drift gates read them by path.
