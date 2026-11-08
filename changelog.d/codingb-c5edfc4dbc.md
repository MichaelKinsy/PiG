### Fixed

- The read tool streams a text file instead of loading it whole: only the lines it can show stay in memory, so a one-line read of a very large file no longer costs the file's size, and an abort stops the read. <!-- fix: c5edfc4dbc -->
