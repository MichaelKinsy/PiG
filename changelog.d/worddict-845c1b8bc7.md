### Changed

- The embedded CJK, Thai, Lao, Khmer and Burmese word dictionaries take 2.2 MB less in the `pig` binary (4.96 MB to 2.78 MB), and word movement in those scripts is faster, not slower: the first Chinese or Japanese word-boundary lookup in a process takes about 0.1 ms instead of 0.23 ms, and ordinary lines segment about three times faster. The dictionaries are stored as shared-prefix tries that are read in place, so nothing is decompressed, allocated or kept in memory, and the segmentation is unchanged.
