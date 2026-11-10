### Fixed

- An Anthropic OAuth request (a Claude subscription) names Claude Code 2.1.322 in its `user-agent` instead of 2.1.280, which Anthropic refuses for newer models with `claude_code_version_too_old` ("Claude Code 2.1.280 does not support this model"). Pi 1.5.0 sends 2.1.280; PiG follows Pi main a05e3a0 (D235). A configured `User-Agent` header still replaces it. <!-- fix: 836c96bad -->
