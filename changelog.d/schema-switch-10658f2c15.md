### Changed

- Node extensions import `@earendil-works/pi-ai` through the extension view, as in Pi: pi-ai's compat entry plus TypeBox's `Type` and a TypeBox-built `StringEnum`. pi-ai's own entry no longer re-exports `Type`, so the shim adds it explicitly; extensions that import `Type`, `Static` or `TSchema` from `@earendil-works/pi-ai` keep working.
