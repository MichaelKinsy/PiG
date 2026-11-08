### Fixed

- `/mcp login` and `pig mcp login` no longer stay on "Connecting..." after a successful browser sign-in while the browser keeps an unused connection to the OAuth callback port: the callback server drops connections that have not sent a request when it closes. <!-- fix: 62b28a24c6 -->
