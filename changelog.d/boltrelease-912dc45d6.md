### Fixed

- A `write` tool call that streams a large file no longer stalls a frame while the highlighting of the whole file is kept warm: that work (20-30 ms a step for a large file) now runs on a worker the card owns instead of inside the frame it falls in, and ends with the call, the card or the session. The lines the card draws are unchanged. <!-- fix: e650ce6f5bfb271063b4350b23a83e2c42f117ad -->
