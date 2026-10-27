### Fixed

- Streaming a tool call's arguments allocates less per delta. The incremental parser no longer records changes nobody reads, and a streamed message's published copy keeps the parser's validated snapshot instead of copying it again (an invalid byte in an argument string is still rewritten as before). A 250 KB `write` call streams about 13% faster with about 22% fewer allocations; the print-mode allocation ceilings per delta fall from 20 and 35 to the measured 18.5 and 18.3 (faux provider and OpenAI Completions). <!-- fix: 81e1a45f0b -->
