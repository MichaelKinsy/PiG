### Fixed

- `AZURE_OPENAI_DEPLOYMENT_NAME_MAP` is read as Pi reads it: a later entry for the same model wins, text after a second `=` in an entry is ignored, a leading byte order mark is trimmed, and a model mapped to an empty name uses the model ID.
- A custom model in `models.json` under the `azure` provider (or any built-in provider whose models have no base URL) without a `baseUrl` now reports `"baseUrl" is required when defining custom models.` and keeps the built-in models, as Pi does, instead of being dropped without a message.
