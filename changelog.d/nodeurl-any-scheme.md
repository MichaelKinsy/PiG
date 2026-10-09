### Fixed

- Where Pi calls `new URL(value)`, Pig now parses a `ws:`, `wss:`, `ftp:` or `file:` URL, and a bracketed IPv6 host of any other scheme, instead of refusing it. This affects package and repository sources (`coding/source`), the OAuth paste and GitHub Copilot enterprise-domain inputs.
