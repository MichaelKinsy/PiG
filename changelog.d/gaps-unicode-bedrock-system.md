### Fixed

- Fold later system messages into the leading Bedrock system prompt, as Pi does, even when a model's compat settings claim mid-conversation system message support. Bedrock requests no longer carry system updates as user turns or drop them by Unicode whitespace rules.
