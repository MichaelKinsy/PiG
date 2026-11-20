### Changed

- Models with an unknown context window (`0`) no longer attempt automatic threshold compaction in the shared compaction check (either after an assistant reply or before a new run using the previous reply); explicit compaction, overflow compaction, and models with a known positive window are unchanged.
