### Fixed

- Group-commit publication could publish the next part of a storage call before a wait continuation of the previous part ran when the wait was entering `SessionImpl.Park`. `Park` called `enter(continuationOf(ctx))` before inserting the new `*Parked` entry into `session.parks`, allowing `deliverLoop` to go idle and the next part's `Hop` to proceed before `awaitReadied` could see the wait. `Park` now inserts into `session.parks` and releases `parkMu` before calling `enter`. <!-- fix: b5a660ac5c63b895c52e0cd66791e5582c5755d0 -->
