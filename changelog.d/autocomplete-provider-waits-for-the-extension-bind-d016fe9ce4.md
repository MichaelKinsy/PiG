### Fixed

- Text typed before the extensions are bound (for example while an extension's `session_start` handler is still running) no longer opens the slash-command or `@` file menu, and Tab no longer completes a path there; the editor gets its completion provider when the bind ends, as in Pi. This has been so since 0.2.0. <!-- fix: d016fe9ce4 --> <!-- cause: the editor was given its autocomplete provider when it was built, without checking that Pi installs it only after bindExtensions returns, so input read before the bind finished reached completion code Pi has not armed yet -->
