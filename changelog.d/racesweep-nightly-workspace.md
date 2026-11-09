### Fixed

- CI: the nightly `-race` run no longer fails on the nested modules that only build inside a Go workspace (`piglets/porter/extensions/pig-porter`, `apps/p2`, `apps/p2desktop`): their entries in `automation/ci/race-packages.txt` carry `workspace`, and `test-race-nightly.sh` runs them without `GOWORK=off`. `race-coverage.sh` accepts only an empty or `workspace` third column on a group entry. <!-- fix: race-nightly-workspace -->
