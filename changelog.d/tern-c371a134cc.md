### Fixed

- OpenAI Responses requests keep the prompt cache across tool changes (Pi #11142). Every tool is declared in `additional_tools` items from the head of the input, a removal is announced as text with the still-unavailable tools restated, and synthetic tool search and Kimi tool messages move the top-level tool list only at the last removal or redefinition, so a later tool load no longer rewrites the cached prefix. Applies to `openai-responses`, `azure-openai-responses`, `openai-codex-responses` and Kimi on `openai-completions`. <!-- fix: c371a134cc -->
