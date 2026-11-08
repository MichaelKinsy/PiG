### Fixed

- A `number` or `integer` schema no longer accepts NaN or an infinity, with or without bounds, and an error names it (`expected number, got NaN`), as TypeBox does. Follows Pi main 55a5c6914 ahead of the pin (D303).
