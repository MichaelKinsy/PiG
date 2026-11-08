### Fixed

- A telemetry span of a failed provider request kept a 500-character cut of the provider's error message that still pointed into the whole message, so a 4 MB error body stayed in memory for as long as the recorded span did. The cut is copied now. <!-- fix: b50a5abb5c565c5700b67230fc31deed6b5c6c34 -->

### Added

- The docs have a Models Configuration page that lists every field of `models.json` with its type, default and description, generated from the same schema trees as `schemas/models.schema.json` (`make generate-schemas`).
