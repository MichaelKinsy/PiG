### Fixed

- Startup no longer compiles 177 package-level regular expressions before `main`, materializes the catalog with a throwaway model per entry, or fingerprints staged SDKs when the extension cache has nothing to prune. First-start SDK staging swaps one complete tree instead of renaming each file. On a four-CPU host the no-extension startup measured against 0.2.0 moved from 1.13 warm and 1.29 cold to 1.01 warm and 1.06 cold.
