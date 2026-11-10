### Fixed

- Windows: installing a plugin from npm extracts the packed tarball with a relative archive name, so the `tar` of Git for Windows (which reads `C:\...` as a file on host `C`) no longer fails with "Cannot connect to C: resolve failed". Pi passes the absolute path and fails there (D616, draft pending owner approval). <!-- fix: 5043738f102ccc23820ceefcc241c2495b6a5e8f -->
