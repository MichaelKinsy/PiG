### Fixed

- Resuming a very long session no longer reads and decodes every entry of the branch to restore the tool loadout. A 365 MB session peaked at 2 GB of memory at startup; it now peaks at about 130 MB, as the 46 MB one does, and Pi's startup peaks at about 640 MB for the same file.
