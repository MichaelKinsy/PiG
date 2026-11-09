### Fixed

- Chord's plugin watcher kept no watch on a directory of the app's base code that was removed and made again while an event of one of its ancestors was handled, on a file system that gives the new directory the inode and birth time of the old one (ext4): an edit in it never marked `base`, so the app did not restart its hosts. A watch the watcher cannot put back now leaves no record of the directory's identity, so the directory is watched again when it returns. <!-- fix: 336d0dc96f -->
