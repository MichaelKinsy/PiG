### Changed

- The embedded `CHANGELOG.md` is stored as a zstd frame (318 KB, 38 KB smaller than the gzip it replaces). The startup What's New check and `/changelog` decode it in about 2.8 ms instead of 9.7 ms and allocate 1.2 MB instead of 3.3 MB.
