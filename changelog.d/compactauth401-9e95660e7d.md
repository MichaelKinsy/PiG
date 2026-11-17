### Fixed

- A compaction, branch summary or bug report summary no longer fails with "Summarization failed: 401 Unauthorized" when the short-lived token it started with (a GitHub Copilot API token lives about 30 minutes) expires before its last request: after a retry's backoff, or on a later chunk. A Session started from the command line resolved its credentials once, when the summary began, and sent that token with every request of it. Each request now resolves its own credentials when it is sent, as a turn does and as Pi's sessions do. <!-- fix: 9e95660e7d6185fff8d31df8ebd61a491cee7812 -->
