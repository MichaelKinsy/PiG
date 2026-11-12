### Changed

- The memory a start leaves free is returned to the operating system at the first idle collection, half a second after the session goes idle, and no longer by a forced collection right after the first frame, which took the core the first prompt needs. A session that is never idle keeps the memory until its next collection.
