### Added
- `PIG_PIGLET_GITHUB_URL` points `pig piglet pull github:...`, `pig piglet publish --to npm --binaries` and `pig piglet update` at a loopback release server for testing. It requires `PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP=1` and refuses any non-loopback host. Unset, nothing changes.
