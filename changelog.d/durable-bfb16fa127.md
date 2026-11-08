### Added

- Durable's `SqliteStorage` commits a call of many batches in a few statements, as Pi main now does (`storage/sqlite/commit.ts`): the check state of all batches is read up front, each batch is checked in memory, and the accepted batches are written per table in multi-row statements. A batch with a document copy writes the batches before it first, so a call of many forks costs a few statements per fork. The Durable core's tick model (`hopcost`) counts the new awaits, and the contract corpus and tick tables are regenerated at Pi main c23b9ec1. <!-- fix: fabf916f -->
- A Durable conversation can carry its own compaction settings, including `modelOverrides`, that win over the Harness's for that conversation (`AgentChange.Compaction`, Pi `922c3ff2b`). The overrides keep the order they were set in, in the stored `pi.agent` state and when read back, as the JavaScript object Pi stores does; `durable.CompactionSettings.ModelOverrides` is now a `durable.CompactionOverrides` (`NewCompactionOverrides`, `Get`, `Set`, `All`) instead of a Go map. <!-- fix: 922c3ff2b -->
- Durable `Tx.Submission` reads a submission inside a change, and stream options reach the model call. <!-- fix: 9ca15920 -->

### Changed

- A Durable commit of a single entry on a local SQLite store costs about twice what it did, because the statements are now Pi's (`INSERT … SELECT json_extract(…) FROM json_each(?)`, one call for any number of batches): 65 µs to 142 µs for one entry, while a call of 100 entries allocates 35% less. The statements are kept identical to Pi's, so the cost is SQLite's to execute them.
