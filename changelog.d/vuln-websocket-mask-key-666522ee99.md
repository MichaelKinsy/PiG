### Security

- PiG's Codex WebSocket transport sent frames masked from a predictable generator (GO-2026-6278, medium); the WebSocket library is moved to the upstream commit that fixes it. Affects 0.7.0 and earlier since 0.2.0.
