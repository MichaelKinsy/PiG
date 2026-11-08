### Fixed

- Interactive mode is ready to type about 10 ms sooner when the recorded "what's new" version differs from the running version without a newer release to show (a downgrade, or a settings file that carries a later version). The startup check inflated the whole bundled changelog (about 1 MB) before drawing the first frame; it now decodes the changelog only as far as the releases newer than the recorded version extend, which is a few kilobytes here. What's New, the recorded version and the install report are unchanged.
