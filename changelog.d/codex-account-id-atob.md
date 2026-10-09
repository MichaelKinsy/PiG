### Fixed

- The OpenAI Codex login reads the account id from the access token the way Pi does: the token payload is decoded as standard base64 with Pi's padding and whitespace rules (a payload using the URL-safe `-` or `_` characters is not accepted), and the decoded bytes are read as Latin-1 characters, so a non-ASCII account id comes out as Pi's text.
