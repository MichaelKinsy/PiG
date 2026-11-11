### Fixed

- A session swap (`/new`, `/resume`, `/fork`, `Session.ReplaceInner`) no longer races with `/new`, a fork or a clone that reads whether the session has a file, and a routed request stores a virtual model's router state on the session it read the state from, not on one a swap installed meanwhile. <!-- fix: 4ed1d7213 -->
