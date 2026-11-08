### Changed

- The `pig` binary and an embedder's program are about 16 KB smaller: the Go SDK's lookup of the member order of a decoded JSON object sorts its entries with one small routine instead of a copy of the standard library's sort compiled for that entry type. The order of the entries, and so every answer the lookup gives, is the same.
