### Fixed

- The Lisp SDK reads a Windows path as one: `with-file-mutation-queue` no longer sends the host the working directory followed by a drive-lettered path, which shared no queue with the host or another SDK, and `agent-session-runtime-import-from-jsonl` finds a session file given by its Windows path instead of looking for `/D:\...\D:\...`. <!-- fix: 7c936f9107 -->
