### Fixed

- A tool that an extension activates with `setActiveTools` now appears in the system prompt's tool list of the next run that a `triggerTurn` message starts. The request already declared the tool, but the prompt left its snippet out, so the model was not told about it. <!-- fix: 4ac1e5dc92 -->
