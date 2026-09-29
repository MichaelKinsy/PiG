### Fixed

- Keep lone UTF-16 surrogates in session names, labels, custom entries, custom messages, compaction and branch summaries, and model changes when PiG writes or reads a session file, as Pi's `JSON.stringify` and `JSON.parse` do. RPC `get_state`, `get_tree` and `get_messages` no longer report them as U+FFFD.
