### Fixed

- Prompts submitted after `/scoped-models` refreshed the model catalogs and Ctrl+P switched the model no longer fail with "No model selected". The scope update installed catalog descriptors that carry no backend; it now installs the Session's runtime-bound models, as Pi does. <!-- fix: d81ace2962 -->
