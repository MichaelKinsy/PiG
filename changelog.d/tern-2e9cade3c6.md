### Fixed

- A Durable Session whose commit was cancelled while it waited for the Session line now closes instead of hanging in `Close`.
