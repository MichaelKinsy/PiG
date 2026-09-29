### Fixed

- Apply a Go extension's `context` handler result when its `messages` value is a typed list such as `[]map[string]any`, as Pi applies any returned array. A `messages` value that is not a list is now that handler's error in every SDK instead of a silently unchanged context.
