### Fixed

- A Codex WebSocket stream whose caller cancels its context right after reading the result no longer closes the connection the provider keeps for the session, so the session's next request reuses it (one connection for sequential requests, as Pi) instead of failing on a closed connection and dialling a new one. <!-- fix: 801d08c8ba --> <!-- cause: the abort-close registration of a WebSocket stream outlived its terminal frame, so a cancel after the result raced the release that keeps the connection and closed it -->
