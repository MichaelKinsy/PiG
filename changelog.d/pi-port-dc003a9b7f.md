### Changed

- The default GitHub Copilot model is `gpt-6.1-sol`, which replaces `gpt-5.4`, retired by Copilot. Carried forward from Pi e5fe9777.

### Fixed

- Automatic retry no longer treats digits inside a request id or another number (for example `req_011CU503xyz` or `5242880 bytes`) as a retryable HTTP status code: 429, 500, 502, 503, 504, 520 and 524 count only as standalone numbers. Carried forward from Pi 6e491cfa (earendil-works/pi#11057). <!-- fix: dc003a9b7f -->
- Automatic retry now triggers for `stream disconnected before completion` errors that a proxy wrapping the Codex CLI relays. Carried forward from Pi 29057ece (earendil-works/pi#11057). <!-- fix: dc003a9b7f -->
- Sign in with ChatGPT: a ChatGPT Business workspace usage-limit error (`subscription_sharing_workplace_individual_usage_limit_exceeded`) is not retried, and its message tells the user to ask their workspace admin to increase the limit. Carried forward from Pi cedefc53. <!-- fix: dc003a9b7f -->
