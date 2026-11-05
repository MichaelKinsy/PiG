### Fixed

- `pig mcp list` sends the token of the provider named by a server's `auth.provider` (Radius, for example), refreshed when expired, so a signed-in provider's server no longer shows as needing sign-in. The hint then reads `/login <provider> in pig`, `--json` carries `authProvider`, and `pig mcp login` and `pig mcp logout` point to `/login` and `/logout` instead of failing with "does not use OAuth". <!-- fix: c807fb7058 -->
