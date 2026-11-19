### Fixed

- The plugins watcher's directory identity (record D540, now retired) is inode and ctime where Node gives the ctime as the birth time, as Pi's: on Linux and Android when statx is refused, and always on DragonFly, Solaris, illumos and AIX; it was the inode alone, so a directory made again that got the same inode number was not seen as replaced there, and a chmod was not seen as a change. <!-- fix: b2aac7a52c --> <!-- cause: the divergence record took Go's lack of a birth time for lack of any value, but libuv fills the birth time from the ctime where the kernel gives none. -->
