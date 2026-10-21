### Changed

- The bug fix ledger (`docs/bug-ledger.json` and `docs/site/docs/bug-ledger.md`) is listed by entry ID instead of by date and no longer carries a total, and `make bug-ledger` changes only the entries a branch adds or removes, so two pull requests that each add a fix merge without a conflict and a merge of `main` needs no regeneration. `make bug-ledger-refresh` recomputes every entry from the history, as a release does, and `go run ./automation/ci/bugledger -summary` prints the counts the page used to show.
