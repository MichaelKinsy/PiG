### Changed

- The release binary is 49,926 B smaller: the full highlight.js grammar snapshot is stored as one zstd frame (208,419 B) instead of a gzip stream (258,345 B) and decodes to the same text. The first highlight of a language outside the eager set is faster (15.0 ms instead of 20.4 ms).
