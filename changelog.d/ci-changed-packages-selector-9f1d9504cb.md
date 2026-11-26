### Fixed

- `automation/ci/changed-packages.py` no longer pulls in unrelated packages from Go comments or non-fixture 3-segment directories (such as `docs/site/docs` or `automation/ci/preflight`), cutting light-PR `test-fast` selection on docs/preflight diffs from 186 fast-parallel packages to 11. <!-- fix: 9f1d9504cb --> <!-- cause: referencing() scanned raw Go source including comments, matched any 3-segment parent directory as a fixture tree, and attributed go list test-variant dependency sources to the importing test package -->
