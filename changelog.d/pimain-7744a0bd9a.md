### Fixed

- `--model <provider>/<id>` now resolves when the provider id itself contains a `/`, such as a URL-derived id like `llama-server=http://host:10800`. Before, the reference was split at its first `/`, so the prefix never named a provider and resolution ended in "not found". The longest matching provider id wins. <!-- fix: 7744a0bd9a -->
