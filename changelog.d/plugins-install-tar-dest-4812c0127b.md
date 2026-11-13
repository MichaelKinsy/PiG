### Fixed

- Windows: installing a plugin from npm also names the extraction directory relative to the pack directory (`-C ../<dir>`), so the `tar` of Git for Windows, which resolves a `-C C:\...` name against its working directory, no longer fails with "Cannot open: No such file or directory". Pi passes the absolute directory (D616 now names the archive and the destination). <!-- fix: 4812c0127bc6c2049a5c417ebc419edc355ea7bb -->
