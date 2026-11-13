### Changed

- CI: the chord surface tests (`make ci-chord-tui`, `make ci-chord-web`) run in their own job, `Chord surfaces`, instead of as the last two steps of `Node extensions`, as Pi split its chord tests out (Pi #12114). The job sets up Go and current Node only and `CI result` requires it; `test/ci-images/chord_job_test.go` fails when a chord target runs in no job or in two, when the required check does not depend on its job, or when its recipe no longer fails on a missing Node or browser.
