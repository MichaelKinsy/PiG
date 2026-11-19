### Changed

- An interactive start holds about 96 KB less live heap at the first frame (2.112 MB to 2.016 MB in the process gate, which runs pig in a directory whose AGENTS.md is 62 KB), and builds the system prompt once instead of twice. The three search, paste and tool-pattern caches no longer allocate their maps before the first expression is stored (26 KB), and the system prompt text and its sections now share one copy of the prompt instead of holding two (69 KB with a 62 KB project context file). The prompt text, its sections and the cache limits are unchanged.
