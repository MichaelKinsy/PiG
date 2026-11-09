### Fixed

- A process that reads every built-in model record, as an extension host does when it attaches, no longer keeps the whole catalog's expanded JSON (about 1 MB) on the heap afterwards: the expanded record chunks are now reclaimable once nothing reads them. <!-- fix: a966fc9a7c -->
