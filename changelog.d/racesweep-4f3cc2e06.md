### Fixed

- `Session.ID`, `Session.Path`, `Session.CWD` and `Session.Inner` no longer race with a session swap (`/new`, `/resume`, `/fork`, `Session.ReplaceInner`): a host call that reads the session id while the swap runs sees the old or the new session, and the Go race detector no longer reports the read. <!-- fix: 4f3cc2e06 -->
