### Changed

- The release binary is 544,768 B smaller: the embedded Node runtime (4,740,074 B) is stored as zstd frames written by the same pinned zstd command at its highest level that writes every other embedded frame, 4,195,160 B, and unpacks to the same files. The first start of a Node extension in a fresh cache takes the same time as before (268.6 ms against 276.0 ms median over ten paired runs of a cold start, ready included), with the same memory.
