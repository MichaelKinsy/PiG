### Fixed

- The Kimi Code, Meta and xAI device logins judge the verification URI the way Pi does, and Meta and xAI open the URL Pi opens. A `https:host` or backslash form is read as a URL; a port above 65535, an out-of-range numeric host, or a space in the host is refused. The opened URL is normalized as `new URL` does (lowercase scheme and host, default port and empty userinfo dropped, an empty path becomes `/`) instead of being echoed as received.
