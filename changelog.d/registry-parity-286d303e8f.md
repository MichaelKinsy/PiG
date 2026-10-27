### Fixed

- A native Provider object an extension registers (`registerNativeProvider`, `pi.registerProvider(provider)`) whose models declare no `baseUrl` now reaches the model catalog, as in Pi: the host composes the Provider object untouched, so its models keep the base URL they declare (or none) and its name, models and streams work. Before, such a provider never composed (no models, its name replaced by its id, every stream an error), and a provider-level `baseUrl` was copied onto its models, which Pi does not do (it is only the context of `refreshModels`). <!-- fix: 286d303e8f -->
