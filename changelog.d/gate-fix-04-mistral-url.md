### Fixed

- Resolve Mistral request URLs as Pi does: `v1/chat/completions` is resolved beneath the normalized base path with WHATWG host, port and path rules, so a custom base URL ending in `/v1` is no longer rewritten. Invalid base URLs fail before any request, empty error bodies report the HTTP status text, and whitespace handling follows JavaScript `trim`.
- Compare models by ID and declared provider, as Pi's `modelsAreEqual` does, even when a model has no attached backend.
