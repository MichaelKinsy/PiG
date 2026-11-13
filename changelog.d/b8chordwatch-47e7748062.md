### Fixed

- On macOS and the BSDs, a chord app's watch of its base code puts back the watch of a directory that was moved away and back (a base directory, or one holding a recorded file). The system's file watcher (kqueue) held the directory again for its own rename alone, so nothing made in it was reported and `base` never changed; the watcher now adds that directory again when an event names it, as Pi's FSEvents watch of a path follows the path. <!-- fix: 47e7748062 -->
