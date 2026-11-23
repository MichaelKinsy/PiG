### Added

- Interactive mode now shows a transcript notice when OpenAI drops replayed reasoning items from a request (such as `prefix_mismatch` after compaction on GPT-6.2) when `showCacheMissNotices` is enabled and more items are dropped than in the previous response; both Anthropic and OpenAI drop notices now list the reasons, and the OpenAI Responses, Azure OpenAI Responses, and OpenAI Codex providers record non-`turn_boundary` removals as `openai_input_transformations` assistant message diagnostics.
