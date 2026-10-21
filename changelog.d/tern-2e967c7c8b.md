### Fixed

- A run that an extension's `sendMessage` with `triggerTurn` starts now carries the session's system prompt. The first run of a session reached the model with no system prompt, as did any later run that a custom message started after the instructions changed. Pi records the run's prompt sections for a custom starting message as for a prompt. <!-- fix: 2e967c7c8b -->
