### Fixed

- A pi-env connection whose daemon exits (or fails) before `hello` now adds the end of the daemon's stderr (the last 20 lines or 2000 characters, terminal escapes and control characters removed) to the start error, for example `pi-env exited with code 3 before it was ready` followed by `remote stderr:` and the profile's message. stderr is decoded as UTF-8 across reads, and a start that fails because the daemon exited waits for its pipes to end so the tail is complete; a ready session still fails as soon as the daemon exits. <!-- fix: 7163ab34f14e7ab9291f4ea80b4cb682d9246281 -->
