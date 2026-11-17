### Added

- A Go program that embeds PiG can read part of a long session branch. `SessionManager.BranchRefs` returns the entries `GetBranch` returns, in the same order, as small values with no entry body read, and `SessionManager.ReadEntries(ctx, ids)` reads only the entries it is given, in one batch, and returns an error and no entries for an unknown id, a record that cannot be read back (`coding.RecordChangedError`) or a done context. (issue #451, D655)
