### Added

- The Go, Rust and Python SDKs export pi-ai's `createProvider`: a provider built from parts (an `api` for every chat model or one per `model.api`, `fetchModels`, `filterModels`, image and classifier implementations) that behaves as Pi's, including the stream error a model whose api has no implementation gets and the transactional model refresh. Register it with `RegisterNativeProvider`.
