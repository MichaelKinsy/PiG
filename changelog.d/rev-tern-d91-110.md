### Added

- A Piglet can name a Go frontend member under `slots.frontend` (D91). `pig piglet build` fuses it into the Piglet Binary, and the member then draws the interactive mode in place of the ANSI renderer from a retained tree of nodes (`extensions/sdk/frontend`). Stock PiG fuses no member and paints as before. See "Frontend members" in `docs/site/docs/piglet-binaries.md`.

### Fixed

- A frontend member's main region now matches the transcript after an extension's custom entry is placed before the streaming message and a later child of the transcript changes in the same frame. `Container.InsertBefore` did not report the children it kept, so the frame spliced a stale range: the session kept the removed entry and missed the inserted one. An insert alone also walked the whole transcript instead of the inserted entry.
