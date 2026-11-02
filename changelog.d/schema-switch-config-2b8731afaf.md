### Changed

- The generated `schemas/models.schema.json` lists the `required` properties of every object of the pi-ai compat and model schemas (`ModelCost` and its tiers, `ProviderCompat`, the routing and fallback-model objects) after its `properties`, as Pi's pi-schema-built schemas do. Editors and validators read the same schema; only the key order of the file changes.

### Fixed

- A `models.json` number that is not an integer and also below the minimum of an integer field (for example `inputLimits.images.resize.maxBytes: 0.5`) is now reported with both errors, `must be integer` and `must be >= 1`, as Pi's TypeBox validation lists them. Pig listed only `must be integer`. <!-- fix: 2b8731afaf6a9136dd0b24aac9c9c162753fed90 -->
