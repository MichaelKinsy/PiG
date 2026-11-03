### Fixed

- Resuming a very long session no longer reads and decodes every entry of the branch to restore the tool loadout. A 365 MB session peaked at 2 GB of memory at startup; it now peaks at about 130 MB, as the 46 MB one does, and Pi's startup peaks at about 640 MB for the same file. <!-- fix: ed01bf62b4, 836d58c4c8 -->
- An entry read from a long session's file no longer keeps the whole shared read (up to 8 MiB) alive: each entry owns its record, so a context built from scattered records retains what it holds and not the gaps between them. <!-- fix: c80895bd4f -->
- Switching to a long session with `/resume`, `/fork` or `/tree` no longer leaves memory the replaced session used: the heap is returned to the OS once the new transcript is painted, as it is at start. A 375 MB session resumed from inside PiG held 2.2 GB until exit; it now holds about 100 MB. <!-- fix: 7fae0d4832 -->
