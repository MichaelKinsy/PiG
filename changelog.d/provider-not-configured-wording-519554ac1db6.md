### Fixed

- The error for a provider with no credentials now reads `Provider is not configured: <id>` as in Pi, where it read `provider is not configured: <id>`, so a failed `/compact` or prompt after `auth.json` was emptied shows Pi's sentence. <!-- fix: 519554ac1db6 --> <!-- cause: the messages were written in Go error-string style (lowercase) instead of copied from Pi's ModelsError text, and their tests pinned the lowercase copy -->
