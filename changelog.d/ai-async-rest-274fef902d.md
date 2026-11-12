### Fixed

- ai: an images API provider now refuses a model of another api with `Mismatched api: <model api> expected <api>` before it runs, as Pi's images registry does; GitHub Copilot and OpenAI Codex logins report `Login cancelled` when the login is cancelled while a prompt or request is in flight, and GitHub Copilot login errors are no longer prefixed.
