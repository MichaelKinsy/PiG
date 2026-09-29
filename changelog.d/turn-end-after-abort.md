### Fixed

- Deliver `turn_end` to subprocess extensions for the turns of an aborted run, as Pi does. The boundary was dispatched with the aborted run's context, so every subprocess `turn_end` handler call after an abort was dropped.
