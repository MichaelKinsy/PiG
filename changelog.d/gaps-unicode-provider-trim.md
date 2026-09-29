### Fixed

- Match Pi's ECMAScript whitespace rules when OpenAI Completions, Mistral and Google requests drop blank assistant text and thinking: BOM-only blocks are dropped and NEL-only blocks are sent. Mistral tool results trim with the same rules, and Mistral sends a later whitespace-only system update as Pi does.
