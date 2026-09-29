### Fixed

- Remove unpaired UTF-16 surrogates from system prompts, system updates, user text and assistant text and thinking in Anthropic Messages, OpenAI Completions and Google requests, as Pi does. PiG previously sent each unit as three U+FFFD characters. OpenAI Completions reasoning fields stay unchanged, as in Pi.
