### Changed

- CI: the nightly fuzz job's timeout is 90 minutes: the tree has 111 fuzz targets, each taking 45 s (30 s of fuzzing and 15 s of build and corpus work), and the job's checkout, Go setup and first build take 5 minutes, which is 111 x 45 s + 5 min = 88 minutes. `test/ci-images/nightly_fuzz_budget_test.go` fails when targets are added past what the timeout covers.
