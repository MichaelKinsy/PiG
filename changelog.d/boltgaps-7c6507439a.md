### Fixed

- A `write` tool call that streams a large file keeps the highlight of the whole file warm in steps of 4 KB that each cost what they add, so the 99th percentile of the gap between frames while a 200 KB or 400 KB file streams in is 17 ms where it was 18-20 ms, and the CPU is not above. The highlighter keeps the UTF-16 units of the text it has seen from one step to the next and converts only the text a step adds; the lines the card draws are those of a run over the whole file, as before. <!-- fix: 7c6507439a0773eeeaea7ade7bb4562b02ff6ab2 -->
