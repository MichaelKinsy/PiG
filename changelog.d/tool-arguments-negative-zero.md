### Fixed

- A streamed tool argument of `-0` is held as `0`, so the serialized arguments write `0` as Pi's `JSON.stringify` does instead of `-0`.
