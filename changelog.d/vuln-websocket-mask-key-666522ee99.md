### Security

- Updated `github.com/gorilla/websocket` past GO-2026-6278 (GHSA-w67g-5rqw-f597) so PiG's WebSocket client for OpenAI Codex Responses (used by default when `transport` is unset or `auto`, `websocket`, or `websocket-cached`, unless `sse` is selected) generates client frame mask keys with `crypto/rand` instead of `math/rand`; PiG's WebSocket servers do not mask frames.
