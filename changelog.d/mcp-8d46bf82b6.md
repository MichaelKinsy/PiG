### Fixed

- A sign-in wait that ends with its context no longer leaves its state pending, so a retry with the same state works, and a wait begun after the callback server closed fails at once. <!-- fix: 8d46bf82b6 -->
- `mcp-auth.json` is replaced through a temporary file and a rename, so a crash cannot leave a torn credential file, and the PKCE verifier is dropped once its code exchange succeeded. <!-- fix: 8d46bf82b6 -->
- Tool search and codemode resolve a local `$ref` in one pass over the schema instead of parsing it once per path segment. <!-- fix: 8d46bf82b6 -->
