### Changed

- The default model of the `openai` provider is `gpt-6.1-sol` (it was `gpt-5.5`), as for `openai-codex` and `github-copilot`: it is the model selected after `/login` when no model is set, the startup fallback, and the default for `--provider openai`.
