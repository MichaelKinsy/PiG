### Fixed

- `/fork` and `/clone` of a very long session no longer decode and rewrite every record of the branch in memory. Forking a 427 MB session peaked at 4.8-5.1 GB and now peaks at about 280 MB (settling at about 190 MB); Pi peaks at about 1.2 GB for the same file. The new file holds the same bytes as before and as Pi writes. <!-- fix: 761f5a208c -->
