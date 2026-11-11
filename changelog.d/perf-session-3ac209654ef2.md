### Changed

- Resuming a long session holds far less memory and a turn no longer does work proportional to the session's length: a loaded session keeps about 79 bytes per JSONL line (it was 106), its ids live in one arena instead of one string each, and the active branch and the context messages are cached and extended on append, so a turn allocates the same at turn 50 as at turn 3,500. Session files and every observable session, tree, branch and compaction result are unchanged.
