### Fixed

- Idle and typing frames in a long interactive session no longer redraw from the header down: a frame costs the same at any transcript length. Extension headers and footers still update every frame.
- Markdown with many inline `$` signs renders in linear time, as in Pi.
