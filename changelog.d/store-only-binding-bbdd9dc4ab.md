### Changed

- Selecting a store-only model (loaded from `models-store.json` and absent from the bundled catalog and `models.json`) via `BuildModel` or `--model` now binds that model's own composed entry from `ModelRuntime().GetModel(...)` instead of falling back to the provider's default model or an empty entry. Bundled models and `models.json` definitions are unchanged.
