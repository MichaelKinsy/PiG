### Changed

- CI: the Linux conformance jobs run the extension conformance suite in fourteen shards (seven per job), packed by recorded duration, where eight shards ran 417 to 598 s of go test's 600 s package timeout on CI and a slower runner timed one out. `automation/ci/test-shard-timings.txt` records every top-level test's duration and a fitted cost model; `automation/ci/shard-balance.py pack` writes the shard pins from it and `check` (run by `test/ci-images`) fails when the pins drift from the timings or a shard's predicted time is over 60% of the timeout.
