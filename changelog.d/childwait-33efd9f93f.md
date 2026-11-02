### Fixed

- `pi.exec` with a `maxBuffer` could keep a child's stderr bytes ahead of its stdout bytes under heavy CPU load, though the child wrote stdout first and Node hands the two pipes to the cap in that order (about one run in two thousand with the host oversubscribed four times over). The reader of stderr decided whether to wait for stdout from two answers taken one after the other, and a stdout reader that took its bytes between them was seen by neither; it now asks the pipe first and the reader second. <!-- fix: 33efd9f93f -->
