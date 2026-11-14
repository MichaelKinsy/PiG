### Fixed

- The error for a model whose provider is not known now reads `Unknown provider: <id>` as in Pi, where it read `unknown provider: <id>`. <!-- fix: a1b09915ffbb --> <!-- cause: the message was written in Go error-string style (lowercase) instead of copied from Pi's ModelsError text, and no test pinned it -->
