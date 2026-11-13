### Fixed

- A `write` tool call that streams a large file no longer stalls a frame while the highlighting of the whole file is kept warm: that work (20-30 ms a step for a large file) now runs on a worker the card owns instead of inside the frame it falls in, and ends with the call, the card or the session. The lines the card draws are unchanged. <!-- fix: 912dc45d6dcc155ca8d4b634988fa0c87abd9e2a -->
