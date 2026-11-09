### Changed

- The release binary is 49,926 B smaller: the full highlight.js grammar snapshot (`grammars_all.json`, 1 MB of JSON) is stored as one zstd frame (208,419 B) instead of a gzip stream (258,345 B), and decodes to the same text. Highlighting a language outside the eager set for the first time takes 15.0 ms instead of 20.4 ms, and the first auto-detection 23.4 ms instead of 26 to 29 ms, with 147 fewer allocations in the first case; the index of the snapshot's nodes is allocated once instead of grown by appending.
