### Fixed

- Preserve fresh regular lock files during upgrade recovery and use the normal contention timeout or cancellation. Stale empty v0.2.0 sidecars still recover safely across auth, settings, trust and model-cache stores, with old-writer and Windows file-identity checks retained.
