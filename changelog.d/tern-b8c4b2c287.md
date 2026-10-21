### Fixed

- A tool's `prepareLoadout` hook now runs again before every later turn of a run, as in Pi, so the descriptions it sets and the declarations it hides follow the state the hook reads. The hooks ran when a run started and when the loadout changed, so a hook that read settings or extension state kept the first turn's answer for the rest of the run. <!-- fix: b8c4b2c287 -->
